package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/detect"
	"github.com/cplieger/deadset/internal/invoke"
	"github.com/cplieger/deadset/internal/merge"
	"github.com/cplieger/deadset/internal/present"
	"github.com/cplieger/deadset/internal/providers"
	"github.com/cplieger/deadset/internal/render"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
	"github.com/cplieger/deadset/internal/summary"
	"github.com/cplieger/deadset/internal/verdict"
)

func init() { register("analyze", runAnalyze) }

const analyzeUsage = "usage: deadset analyze [--target=DIR] [--central=FILE] [--run-dir=DIR] [--template=FILE] " +
	"[--exit-code=on|off] [--languages=LIST] [--min-confidence=CLASS] [--formats=LIST] [--fail-on=SEVERITY]"

// name is the analyzer name a merged report this command writes carries.
const name = "deadset"

// templateFlag names the flag that names the template the template format
// renders.
const templateFlag = "template"

// The two values --exit-code takes.
const (
	exitCodeOn  = "on"
	exitCodeOff = "off"
)

// renderings is every format rendered on stdout, each mapped to what writes
// it there.
var renderings = map[config.Format]func(io.Writer, *report.Report, config.Severity) error{
	config.FormatText:   func(w io.Writer, r *report.Report, _ config.Severity) error { return summary.Text(w, r) },
	config.FormatGitHub: summary.Annotations,
}

// documents is every format rendered as a file beside the merged report in
// the run directory, each mapped to the suffix its file carries. Like the
// merged report, which is the json format's rendering, each is a file rather
// than a stream: a document a crash cut short on a stream is not one a reader
// can tell from a complete one, and an upload step names a file.
var documents = map[config.Format]string{
	config.FormatSARIF:    ".sarif",
	config.FormatTemplate: ".tmpl",
}

// analysis is one analyze invocation: what its flags asked for, and where it
// writes.
type analysis struct {
	stdout, stderr io.Writer

	// set is the parsed flag set, which the setting flags are read from.
	set *flag.FlagSet

	// template is the parsed template --template names, nil where it names
	// none.
	template *render.Template

	target, central, runDir, templatePath string

	exitCode verdict.Switch
}

// runAnalyze analyzes the target with every analyzer the provider list holds
// for a language in scope, merges their reports and returns the verdict of
// the merged report. It reads no argument.
func runAnalyze(args []string, stdout, stderr io.Writer) int {
	a := &analysis{stdout: stdout, stderr: stderr}
	a.set = flag.NewFlagSet("deadset analyze", flag.ContinueOnError)
	a.set.SetOutput(stderr)
	a.set.Usage = func() { fmt.Fprintln(stderr, analyzeUsage) }
	a.set.StringVar(&a.target, "target", ".", targetUsage)
	a.set.StringVar(&a.central, "central", "", centralUsage)
	a.set.StringVar(&a.runDir, "run-dir", "",
		"the run directory, which must not exist; empty makes one under the temporary directory")
	a.set.StringVar(&a.templatePath, templateFlag, "", "the file holding the template the template format renders")
	exitCode := a.set.String("exit-code", exitCodeOn,
		"whether the exit code carries the verdict of the run: "+exitCodeOn+" or "+exitCodeOff)
	config.RegisterFlags(a.set)
	if err := a.set.Parse(args); err != nil {
		return verdict.Usage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := verdict.Clean, a.invocation(*exitCode)
	if err == nil {
		code, err = a.run(ctx)
	}
	if err != nil {
		fmt.Fprintf(stderr, "deadset: %v\n", err)
		code = verdict.ForError(err)
		if code == verdict.Usage {
			a.set.Usage()
		}
	}
	return code
}

// invocation checks the half of the command line no document has a say in,
// before any document is read.
func (a *analysis) invocation(exitCode string) error {
	if a.set.NArg() != 0 {
		return &verdict.InvocationError{Err: fmt.Errorf("analyze takes no argument, got %q", a.set.Arg(0))}
	}
	switch exitCode {
	case exitCodeOn:
		a.exitCode = verdict.On
	case exitCodeOff:
		a.exitCode = verdict.Off
	default:
		return &verdict.InvocationError{
			Err:  fmt.Errorf("%q is not a value: the values are %s and %s", exitCode, exitCodeOn, exitCodeOff),
			Flag: "exit-code",
		}
	}
	return nil
}

// run resolves the configuration, chooses the analyzers, runs each into the
// run directory, merges their reports, writes and prints the merged report,
// and returns its verdict. An error is a run that produced no merged report.
func (a *analysis) run(ctx context.Context) (int, error) {
	resolved, root, err := resolveConfiguration(a.set, a.target, a.central)
	if err != nil {
		return 0, err
	}
	reporters := &resolved.Config.Reporters
	if refused := a.readTemplate(reporters.Formats); refused != nil {
		return 0, refused
	}
	inScope, err := detect.Languages(root, detect.Options{Languages: resolved.Config.Languages})
	if err != nil {
		return 0, err
	}
	analyzers, err := providers.Select(resolved.Config.Providers, inScope)
	if err != nil {
		return 0, err
	}
	dir, err := a.runDirectory()
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(a.stderr, "deadset: the run directory is %s\n", dir.Path())

	runs, err := prepare(dir, root, resolved, analyzers, inScope)
	if err != nil {
		return 0, err
	}
	for i := range runs {
		runs[i].request.Diagnostics = a.stderr
	}
	merged, inputs, err := analyzeAll(ctx, runs)
	if err != nil {
		return 0, err
	}
	present.Apply(merged, &present.Options{
		Severity:    resolved.Severity,
		Sort:        reporters.Sort,
		MaxFindings: reporters.MaxFindings,
	})
	if err := a.publish(dir, root, merged, inputs, reporters); err != nil {
		return 0, err
	}
	return a.verdict(merged, reporters.FailOn), nil
}

// readTemplate reads and parses the template --template names, before any
// analyzer runs. The template format with no template, and a template that
// does not parse, are invocations the run refuses.
func (a *analysis) readTemplate(formats []config.Format) error {
	if a.templatePath == "" {
		if slices.Contains(formats, config.FormatTemplate) {
			return &verdict.InvocationError{
				Err:  errors.New("the template format renders the template this flag names, and none was named"),
				Flag: templateFlag,
			}
		}
		return nil
	}
	text, err := os.ReadFile(a.templatePath)
	if err != nil {
		return &verdict.InvocationError{Err: err, Flag: templateFlag}
	}
	a.template, err = render.ParseTemplate(string(text))
	if err != nil {
		return &verdict.InvocationError{Err: err, Flag: templateFlag}
	}
	return nil
}

// runDirectory creates the run directory --run-dir names, or one of its own
// when it names none. A path that exists, or whose parent does not, is an
// invocation the run refuses.
func (a *analysis) runDirectory() (*rundir.Dir, error) {
	if a.runDir == "" {
		return rundir.CreateTemp()
	}
	dir, err := rundir.Create(a.runDir)
	if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrNotExist) {
		return nil, &verdict.InvocationError{Err: err, Flag: "run-dir"}
	}
	return dir, err
}

// analyzerRun is one selected analyzer: the files the run directory keeps for
// it and the invocation that runs it.
type analyzerRun struct {
	entry   rundir.Entry
	request invoke.Request
}

// prepare writes the scope document and each analyzer's configuration into
// dir and returns one run per analyzer, in the order of analyzers. The scope's
// working directory and every analyzer's are the one root, so every path a
// report names is relative to the target.
func prepare(dir *rundir.Dir, root string, resolved *config.Resolved,
	analyzers []providers.Analyzer, inScope []string,
) ([]analyzerRun, error) {
	scope := rundir.Scope{Root: root, Target: rundir.Module{Path: "."}}
	if err := dir.WriteScope(&scope); err != nil {
		return nil, err
	}
	runs := make([]analyzerRun, 0, len(analyzers))
	for i := range analyzers {
		selected := &analyzers[i]
		entry, err := dir.Entry(selected.Entry.Name)
		if err != nil {
			return nil, err
		}
		claimed := slices.DeleteFunc(slices.Clone(selected.Entry.Languages),
			func(language string) bool { return !slices.Contains(inScope, language) })
		if err := entry.WriteConfig(resolved, claimed...); err != nil {
			return nil, err
		}
		runs = append(runs, analyzerRun{entry: entry, request: invoke.Request{
			Analyzer: selected.Entry.Name,
			Command:  selected.Executable,
			Dir:      scope.Root,
			Scope:    dir.Scope(),
			Config:   entry.Config(),
			Report:   entry.Report(),
		}})
	}
	return runs, nil
}

// analyzeAll runs the handshake with every analyzer and then every analysis,
// and merges the reports. No analysis runs unless every analyzer was admitted,
// no report merges unless each names the analyzer of its provider entry, and
// every refusal of one step is named. It returns the merged report and the
// analyzer member of every report it read.
func analyzeAll(ctx context.Context, runs []analyzerRun) (*report.Report, []report.Analyzer, error) {
	accepted := []string{report.SchemaVersion}
	digests := make([]string, len(runs))
	var refused []error
	for i := range runs {
		run := &runs[i]
		digest, err := artifactDigest(run.request.Command)
		if err == nil {
			_, err = invoke.Describe(ctx, &run.request, run.entry, accepted)
		}
		if err != nil {
			refused = append(refused, err)
		}
		digests[i] = digest
	}
	if len(refused) > 0 {
		return nil, nil, errors.Join(refused...)
	}

	requests := make([]invoke.Request, len(runs))
	for i := range runs {
		requests[i] = runs[i].request
	}
	reports, err := invoke.Run(ctx, requests)
	if err != nil {
		return nil, nil, err
	}
	inputs := make([]merge.Input, len(reports))
	analyzers := make([]report.Analyzer, len(reports))
	for i := range reports {
		if named := reports[i].Analyzer.Name; named != runs[i].request.Analyzer {
			refused = append(refused, fmt.Errorf("analyzer %s, report %s: the report names the analyzer %q, not its provider entry %q",
				runs[i].request.Analyzer, runs[i].request.Report, named, runs[i].request.Analyzer))
		}
		inputs[i] = merge.Input{Report: reports[i], Digest: digests[i]}
		analyzers[i] = reports[i].Analyzer
	}
	if len(refused) > 0 {
		return nil, nil, errors.Join(refused...)
	}
	conformance, err := merge.Conformance()
	if err != nil {
		return nil, nil, err
	}
	merged, err := merge.Merge(inputs, accepted, &merge.Caller{
		SchemaVersion:   report.SchemaVersion,
		ContractVersion: report.ContractVersion,
		Name:            name,
		Version:         version(),
		Conformance:     conformance,
	})
	return merged, analyzers, err
}

// artifactDigest is the digest of the analyzer artifact at path, spelled as a
// merged report's merged_from names it.
func artifactDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("digest the analyzer %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest the analyzer %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// publish writes the merged report r and every rendering of it the formats
// name that is a file beside it into dir, then prints the rest.
func (a *analysis) publish(dir *rundir.Dir, root string, r *report.Report,
	inputs []report.Analyzer, reporters *config.Reporters,
) error {
	if err := dir.WriteMerged(r); err != nil {
		return err
	}
	written, err := a.writeDocuments(dir, root, r, inputs, reporters.Formats)
	if err != nil {
		return err
	}
	return a.print(r, reporters, written)
}

// writeDocuments writes into dir every rendering the formats name that is a
// file beside the merged report, in their order, and returns each file's line
// of the output: the format and the path. The SARIF rendering hashes the
// source lines its results name, which it reads below the target root, the
// directory every path of the report is relative to.
func (a *analysis) writeDocuments(dir *rundir.Dir, root string, r *report.Report,
	inputs []report.Analyzer, formats []config.Format,
) ([]string, error) {
	var written []string
	for _, format := range formats {
		suffix, held := documents[format]
		if !held {
			continue
		}
		var document bytes.Buffer
		var err error
		if format == config.FormatSARIF {
			err = sarifOf(&document, root, r, inputs)
		} else {
			err = a.template.Render(&document, r)
		}
		if err != nil {
			return nil, err
		}
		if err := dir.WriteRendering(suffix, document.Bytes()); err != nil {
			return nil, err
		}
		written = append(written, string(format)+" "+dir.Rendering(suffix))
	}
	return written, nil
}

// sarifOf writes the SARIF rendering of r, reading every source file inside
// root and nowhere else.
func sarifOf(w io.Writer, root string, r *report.Report, inputs []report.Analyzer) error {
	files, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = files.Close() }()
	return render.SARIF(w, r, &render.Sources{
		Read:      func(path string) ([]byte, error) { return files.ReadFile(filepath.FromSlash(path)) },
		Analyzers: inputs,
	})
}

// print writes to stdout every rendering the formats name, in their order,
// then the summary, a line naming each rendering written beside the merged
// report, and the remediation when a finding fails the run.
func (a *analysis) print(r *report.Report, reporters *config.Reporters, written []string) error {
	var out strings.Builder
	for _, format := range reporters.Formats {
		if write := renderings[format]; write != nil {
			if err := write(&out, r, reporters.FailOn); err != nil {
				return err
			}
		}
	}
	if err := summary.Write(&out, r); err != nil {
		return err
	}
	for _, line := range written {
		fmt.Fprintln(&out, line)
	}
	if verdict.Failing(&r.Totals.BySeverity, reporters.FailOn) {
		if err := summary.Remediation(&out); err != nil {
			return err
		}
	}
	_, err := io.WriteString(a.stdout, out.String())
	return err
}

// verdict names on stderr what the merged report holds that a reader acts on
// whatever the exit code says, and returns the code the exit-code table and
// the switch give it.
func (a *analysis) verdict(r *report.Report, failOn config.Severity) int {
	if n := r.Totals.Pending; n > 0 {
		fmt.Fprintf(a.stderr, "deadset: %s\n", counted(n, "pending finding", "pending findings"))
	}
	if n := r.Totals.StaleSuppressions; n > 0 {
		fmt.Fprintf(a.stderr, "deadset: %s\n", counted(n, "stale suppression", "stale suppressions"))
	}
	code := verdict.Code(r, failOn, a.exitCode)
	if answer := verdict.Code(r, failOn, verdict.On); answer != code {
		fmt.Fprintf(a.stderr, "deadset: the exit code is configured off: the verdict of this run is %d\n", answer)
	}
	return code
}

// counted renders a count with the noun for it.
func counted(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}
