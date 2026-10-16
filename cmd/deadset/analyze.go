package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset-spec/v8/render"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/run"
	"github.com/cplieger/deadset/v2/internal/scope"
	"github.com/cplieger/deadset/v2/internal/source"
)

// The options a refusal names more than once.
const (
	scopeOption    = "scope"
	templateOption = "template"
)

// analyzeFlags is what one analyze command line asked for.
type analyzeFlags struct {
	*command
	set                                          *flag.FlagSet
	target, scopePath, central, runDir, template string
	exitCode                                     onOff
}

// onOff is the value of --exit-code.
type onOff bool

func (o *onOff) String() string {
	if *o {
		return "on"
	}
	return "off"
}

func (o *onOff) Set(value string) error {
	switch value {
	case "on", "off":
		*o = value == "on"
		return nil
	}
	return fmt.Errorf("%q is not a value: the values are on and off", value)
}

// bindAnalyze binds the analyze verb, which analyzes the target with every
// analyzer the provider list holds for a language in scope, merges their
// reports and returns the verdict of the merged report.
func (c *command) bindAnalyze(set *flag.FlagSet) runner {
	a := &analyzeFlags{command: c, set: set, exitCode: true}
	set.StringVar(&a.target, "target", ".", "")
	set.StringVar(&a.scopePath, scopeOption, "", "")
	set.StringVar(&a.central, "central", "", "")
	set.StringVar(&a.runDir, "run-dir", "", "")
	set.StringVar(&a.template, templateOption, "", "")
	set.Var(&a.exitCode, "exit-code", "")
	return a.analyze
}

// analyze reads what the command line names, runs the analysis, prints what
// every analyzer printed, the renderings and the verdict, and returns the
// verdict's exit code.
func (a *analyzeFlags) analyze(ctx context.Context, stdout, stderr io.Writer) (int, error) {
	resolved, root, err := resolveConfiguration(a.set, a.target, a.central)
	if err != nil {
		return 0, err
	}
	reporters := &resolved.Config.Reporters
	template, err := a.readTemplate(reporters.Formats)
	if err != nil {
		return 0, err
	}
	declared, err := a.readScope(root)
	if err != nil {
		return 0, err
	}
	opts := &render.Options{Template: template, BySize: reporters.Sort == config.SortSize}
	result, err := run.Analyze(ctx, &run.Request{
		Resolved:    resolved,
		Scope:       declared,
		Root:        root,
		RunDir:      a.runDir,
		Render:      opts,
		Version:     version(),
		Diagnostics: stderr,
		Started:     func(dir string) { fmt.Fprintf(stderr, "%s: the run directory is %s\n", run.Product, dir) },
		Print:       func(outcomes []source.Outcome) error { return printOutcomes(stderr, outcomes) },
		Open:        a.open,
	})
	switch {
	case errors.Is(err, run.ErrRunDir):
		return 0, &usageError{err: err, option: "run-dir"}
	case isUnclaimed(err):
		return 0, &usageError{err: err, option: scopeOption}
	case err != nil:
		return 0, err
	}
	if err := printResult(stdout, result, opts, reporters); err != nil {
		return 0, err
	}
	return a.verdict(stderr, result.Merged, reporters.FailOn), nil
}

// isUnclaimed reports whether err holds a consumer no analyzer claims.
func isUnclaimed(err error) bool {
	_, unclaimed := errors.AsType[*plan.UnclaimedError](err)
	return unclaimed
}

// readTemplate reads and parses the template --template names, before any
// analyzer runs. The template format with no template, and a template that
// cannot be read or does not parse, are command lines the run refuses.
func (a *analyzeFlags) readTemplate(formats []vocab.Format) (*render.Template, error) {
	if a.template == "" {
		if slices.Contains(formats, vocab.FormatTemplate) {
			return nil, &usageError{err: errors.New("the template format renders the template this option names, and none was named"), option: templateOption}
		}
		return nil, nil
	}
	text, err := os.ReadFile(a.template)
	if err != nil {
		return nil, &usageError{err: err, option: templateOption}
	}
	parsed, err := render.ParseTemplate(string(text))
	if err != nil {
		return nil, &usageError{err: err, option: templateOption}
	}
	return parsed, nil
}

// readScope reads the scope document --scope names, whose target must be the
// target root; with no document the scope is the target alone. A document
// that cannot be read is a command line the run refuses, and one the scope
// schema refuses a failure of the run, as for every analyzer.
func (a *analyzeFlags) readScope(root string) (*scope.Document, error) {
	if a.scopePath == "" {
		return &scope.Document{Target: scope.Module{Path: root}}, nil
	}
	declared, err := scope.Read(a.scopePath)
	switch {
	case errors.Is(err, scope.ErrDocument):
		return nil, fmt.Errorf("--scope=%s: %w", a.scopePath, err)
	case err != nil:
		return nil, &usageError{err: err, option: scopeOption}
	case !sameDirectory(declared.Target.Path, root):
		return nil, &usageError{
			err:    fmt.Errorf("the document names the target %s, and the target of the run is %s", declared.Target.Path, root),
			option: scopeOption,
		}
	}
	return declared, nil
}

// sameDirectory reports whether two paths name one existing directory.
func sameDirectory(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil || !first.IsDir() {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

// printOutcomes writes what every analyzer printed, whole and in analyzer
// name order, each setup failure followed by the line telling how to analyze
// the target without that analyzer. It returns every failed write of an
// analyzer that refused nothing itself; an analyzer's own refusal stands for
// a failed write of its output.
func printOutcomes(stderr io.Writer, outcomes []source.Outcome) error {
	var failures []error
	for _, o := range source.ByName(outcomes) {
		shown := o.Printed
		if o.SetupFailed() {
			shown = append(slices.Clip(shown), workaround(outcomes, o.Analyzer)...)
		}
		if len(shown) == 0 {
			continue
		}
		if _, err := stderr.Write(shown); err != nil && o.Err == nil {
			failures = append(failures, fmt.Errorf("analyzer %s, exit %d: copy what it printed: %w", o.Analyzer, o.Exit, err))
		}
	}
	return errors.Join(failures...)
}

// workaround is the line telling how to analyze the target without the
// analyzer named failed: the configuration that hands the run only the
// languages of the analyzers that left a report, or those analyzers run
// alone. With no such analyzer it is empty.
func workaround(outcomes []source.Outcome, failed string) string {
	var languages, names []string
	for i := range outcomes {
		if outcomes[i].Err == nil {
			for _, language := range outcomes[i].Languages {
				languages = append(languages, strconv.Quote(string(language)))
			}
			names = append(names, outcomes[i].Analyzer)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	return fmt.Sprintf("%s: to analyze the target without %s, set analysis.languages: [%s] or run %s alone\n",
		run.Product, failed, strings.Join(slices.Compact(slices.Sorted(slices.Values(languages))), ", "), strings.Join(names, " and "))
}

// printResult writes to stdout every rendering the formats name that is a
// stream, in their order, then the summary, a line naming each rendering
// written beside the merged report, and the remediation when a finding fails
// the run.
func printResult(stdout io.Writer, result *run.Result, opts *render.Options, reporters *config.Reporters) error {
	var out strings.Builder
	r := result.Merged
	for _, format := range reporters.Formats {
		if run.Streamed(format) {
			if err := render.Render(&out, format, r, opts); err != nil {
				return err
			}
		}
	}
	if err := render.Summary(&out, r); err != nil {
		return err
	}
	for _, w := range result.Written {
		fmt.Fprintf(&out, "%s %s\n", w.Format, w.Path)
	}
	if render.Failing(&r.Totals.BySeverity, reporters.FailOn) {
		out.WriteString(remediation)
	}
	_, err := io.WriteString(stdout, out.String())
	return err
}

// remediation is what a run that a finding failed prints after its summary.
const remediation = "remediation: for each failing finding, delete the symbol, wire it up so the program uses it, " +
	"or record an adjudication with its reason in an inline directive or a deadset-ignore.json entry\n"

// verdict names on stderr what the merged report holds that a reader acts on
// whatever the exit code says, and returns the code the exit-code table gives
// it, or the clean code where the exit code is configured off.
func (a *analyzeFlags) verdict(stderr io.Writer, r *report.Report, failOn vocab.Severity) int {
	if n := r.Totals.Pending; n > 0 {
		fmt.Fprintf(stderr, "%s: %s\n", run.Product, counted(n, "pending finding", "pending findings"))
	}
	if n := r.Totals.StaleSuppressions; n > 0 {
		fmt.Fprintf(stderr, "%s: %s\n", run.Product, counted(n, "stale suppression", "stale suppressions"))
	}
	answer := render.Verdict(&r.Totals, failOn)
	if a.exitCode || answer == vocab.ExitClean {
		return answer
	}
	fmt.Fprintf(stderr, "%s: the exit code is configured off: the verdict of this run is %d\n", run.Product, answer)
	return vocab.ExitClean
}

// counted renders a count with the noun for it.
func counted(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}
