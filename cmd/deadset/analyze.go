package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
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
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
	"github.com/cplieger/deadset/internal/summary"
	"github.com/cplieger/deadset/internal/verdict"
)

func init() { register("analyze", runAnalyze) }

const analyzeUsage = "usage: deadset analyze [--target=DIR] [--central=FILE] [--run-dir=DIR] [--exit-code=on|off] " +
	"[--languages=LIST] [--min-confidence=CLASS] [--formats=LIST] [--fail-on=SEVERITY]"

// name is the analyzer name a merged report this command writes carries.
const name = "deadset"

// The two values --exit-code takes.
const (
	exitCodeOn  = "on"
	exitCodeOff = "off"
)

// renderings is every format this command renders, each mapped to what it
// writes to stdout. The json format writes nothing there: its rendering is
// the merged report in the run directory, which every run writes.
var renderings = map[config.Format]func(io.Writer, *report.Report, config.Severity) error{
	config.FormatText:   func(w io.Writer, r *report.Report, _ config.Severity) error { return summary.Text(w, r) },
	config.FormatJSON:   nil,
	config.FormatGitHub: summary.Annotations,
}

// analysis is one analyze invocation: what its flags asked for, and where it
// writes.
type analysis struct {
	stdout, stderr io.Writer

	// set is the parsed flag set, which the setting flags are read from.
	set *flag.FlagSet

	target, central, runDir string

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
	if refused := rendered(reporters.Formats); refused != nil {
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
	merged, err := analyzeAll(ctx, runs)
	if err != nil {
		return 0, err
	}
	present.Apply(merged, &present.Options{
		Severity:    resolved.Severity,
		Sort:        reporters.Sort,
		MaxFindings: reporters.MaxFindings,
	})
	if err := dir.WriteMerged(merged); err != nil {
		return 0, err
	}
	if err := a.print(merged, reporters); err != nil {
		return 0, err
	}
	return a.verdict(merged, reporters.FailOn), nil
}

// rendered refuses a format this command renders nothing for, before any
// analyzer runs.
func rendered(formats []config.Format) error {
	for _, format := range formats {
		if _, held := renderings[format]; !held {
			named := slices.Sorted(maps.Keys(renderings))
			return &config.Error{
				Key: "reporters.formats",
				Message: fmt.Sprintf("reporters.formats names %q, which this build does not render: it renders %s",
					format, strings.Join(spelled(named), ", ")),
			}
		}
	}
	return nil
}

// spelled is each format's name.
func spelled(formats []config.Format) []string {
	names := make([]string, len(formats))
	for i, format := range formats {
		names[i] = string(format)
	}
	return names
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
// and every refusal of one step is named.
func analyzeAll(ctx context.Context, runs []analyzerRun) (*report.Report, error) {
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
		return nil, errors.Join(refused...)
	}

	requests := make([]invoke.Request, len(runs))
	for i := range runs {
		requests[i] = runs[i].request
	}
	reports, err := invoke.Run(ctx, requests)
	if err != nil {
		return nil, err
	}
	inputs := make([]merge.Input, len(reports))
	for i := range reports {
		inputs[i] = merge.Input{Report: reports[i], Digest: digests[i]}
	}
	conformance, err := merge.Conformance()
	if err != nil {
		return nil, err
	}
	return merge.Merge(inputs, accepted, &merge.Caller{
		SchemaVersion:   report.SchemaVersion,
		ContractVersion: report.ContractVersion,
		Name:            name,
		Version:         version(),
		Conformance:     conformance,
	})
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

// print writes to stdout every rendering the formats name, in their order,
// then the summary, and the remediation when a finding fails the run.
func (a *analysis) print(r *report.Report, reporters *config.Reporters) error {
	var out strings.Builder
	for _, format := range reporters.Formats {
		if render := renderings[format]; render != nil {
			if err := render(&out, r, reporters.FailOn); err != nil {
				return err
			}
		}
	}
	if err := summary.Write(&out, r); err != nil {
		return err
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
