// Command deadset is the front door of the deadset dead-code toolkit. It
// detects the languages in a repository, runs each language's analyzer as a
// separate process, resolves the cross-language edges between their reports,
// merges the reports into one and returns one exit code. It contains no
// language analysis of its own and never edits a source file.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/run"
	"github.com/cplieger/deadset/v2/internal/source"
)

// runner runs one parsed command line. A non-nil error is printed and gives
// the exit code; otherwise the code is the one returned.
type runner func(ctx context.Context, stdout, stderr io.Writer) (int, error)

// binders defines, per verb, the verb's own options on a flag set and returns
// what runs the command line it parsed. Its setting options are config's.
var binders = map[string]func(c *command, set *flag.FlagSet) runner{
	"analyze":      (*command).bindAnalyze,
	"print-config": (*command).bindPrintConfig,
	"version":      (*command).bindVersion,
}

// verbs is every verb of the orchestrator's row of cli.json, in its order.
var verbs = vocab.OrchestratorVerbs()

// command is this program's command line and how it opens a source.
type command struct {
	// open is the run's [run.Request.Open].
	open func(planned *plan.Source, dir string, diagnostics io.Writer) (source.Source, error)
}

func main() {
	os.Exit((&command{}).dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

// dispatch runs the command line in args and returns the process exit code.
// It writes the verb's output to stdout and diagnostics to stderr, and never
// exits the process itself, so a test drives it directly.
func (c *command) dispatch(args []string, stdout, stderr io.Writer) int {
	top := flag.NewFlagSet(run.Product, flag.ContinueOnError)
	top.SetOutput(stderr)
	top.Usage = func() { fmt.Fprint(stderr, usage()) }
	if err := top.Parse(args); err != nil {
		return vocab.ExitUsage
	}
	name := top.Arg(0)
	at := slices.IndexFunc(verbs, func(v vocab.Verb) bool { return v.Name == name })
	switch {
	case name == "":
		top.Usage()
		return vocab.ExitUsage
	case at < 0:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", run.Product, name)
		top.Usage()
		return vocab.ExitUsage
	}
	verb := &verbs[at]
	set, runVerb := c.flags(verb)
	set.SetOutput(stderr)
	set.Usage = func() { fmt.Fprint(stderr, verbUsage(verb)) }
	if err := set.Parse(top.Args()[1:]); err != nil {
		return vocab.ExitUsage
	}
	if set.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: %s takes no argument, got %q\n", run.Product, verb.Name, set.Arg(0))
		set.Usage()
		return vocab.ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := runVerb(ctx, stdout, stderr)
	if err != nil {
		return failed(stderr, err, set.Usage)
	}
	return code
}

// flags is the flag set of one verb, which defines exactly the options its
// row of cli.json lists. A binder that defines another set is a mistake in
// this command's own source, so it panics, failing every test that runs the
// verb.
func (c *command) flags(verb *vocab.Verb) (*flag.FlagSet, runner) {
	set := flag.NewFlagSet(run.Product+" "+verb.Name, flag.ContinueOnError)
	runVerb := binders[verb.Name](c, set)
	if slices.ContainsFunc(verb.Options, func(name string) bool { o, _ := vocab.OptionOf(name); return o.Setting != "" }) {
		config.RegisterFlags(set)
	}
	var defined []string
	set.VisitAll(func(f *flag.Flag) { defined = append(defined, f.Name) })
	if want := slices.Sorted(slices.Values(verb.Options)); !slices.Equal(defined, want) {
		panic(fmt.Sprintf("deadset %s: the verb defines the options %q, and cli.json lists %q", verb.Name, defined, want))
	}
	return set, runVerb
}

// usage is the usage text: every verb with its summary.
func usage() string {
	var text strings.Builder
	fmt.Fprintf(&text, "usage: %s <command> [options]\n\ncommands:\n", run.Product)
	for _, v := range verbs {
		fmt.Fprintf(&text, "  %-13s %s\n", v.Name, v.Summary)
	}
	return text.String()
}

// verbUsage is one verb's usage text: its command line, its summary and each
// option with what it does.
func verbUsage(verb *vocab.Verb) string {
	var text, line strings.Builder
	fmt.Fprintf(&line, "usage: %s %s", run.Product, verb.Name)
	width := 0
	spelled := make([]string, len(verb.Options))
	for i, name := range verb.Options {
		o, _ := vocab.OptionOf(name)
		spelled[i] = "--" + o.Name + "=" + strings.ToUpper(o.Value)
		fmt.Fprintf(&line, " [%s]", spelled[i])
		width = max(width, len(spelled[i]))
	}
	fmt.Fprintf(&text, "%s\n\n%s\n", line.String(), verb.Summary)
	if len(verb.Options) > 0 {
		text.WriteString("\noptions:\n")
	}
	for i, name := range verb.Options {
		o, _ := vocab.OptionOf(name)
		fmt.Fprintf(&text, "  %-*s  %s\n", width, spelled[i], o.Summary)
	}
	return text.String()
}

func init() {
	for name := range binders {
		if !slices.ContainsFunc(verbs, func(v vocab.Verb) bool { return v.Name == name }) {
			panic(fmt.Sprintf("deadset: the verb %s has a binder, and cli.json lists no such verb", name))
		}
	}
	for _, v := range verbs {
		if binders[v.Name] == nil {
			panic(fmt.Sprintf("deadset: cli.json lists the verb %s, and this build binds none", v.Name))
		}
	}
}
