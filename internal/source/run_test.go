package source_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/source"
)

// stub is a source whose outcome a test states; every stub of a run waits at
// started until all of them have begun producing.
type stub struct {
	started *sync.WaitGroup
	out     source.Outcome
}

func (s *stub) Name() string { return s.out.Analyzer }

func (*stub) Admit(context.Context, rundir.Entry, []string) error { return nil }

func (s *stub) Produce(context.Context, source.Files) source.Outcome {
	s.started.Done()
	s.started.Wait()
	return s.out
}

func stubs(outcomes ...source.Outcome) []source.Source {
	var started sync.WaitGroup
	started.Add(len(outcomes))
	held := make([]source.Source, len(outcomes))
	for i := range outcomes {
		held[i] = &stub{started: &started, out: outcomes[i]}
	}
	return held
}

// Every source produces at once, each outcome at its source's place; a run
// whose sources all leave a report keeps every one.
func TestRunProducesEverySourceAtOnce(t *testing.T) {
	t.Parallel()

	sources := stubs(source.Outcome{Analyzer: "deadset-ts", Report: &report.Report{}}, source.Outcome{Analyzer: "deadset-go", Report: &report.Report{}})
	outcomes, err := source.Run(t.Context(), sources, make([]source.Files, 2), false)
	if err != nil || outcomes[0].Analyzer != "deadset-ts" || outcomes[1].Analyzer != "deadset-go" || outcomes[0].Report == nil || outcomes[1].Report == nil {
		t.Errorf("Run() = %+v, %v, want both reports in the sources' order", outcomes, err)
	}
}

// One refusal leaves the run with no report at all, and the error joins every
// refusal in the sources' order.
func TestRunKeepsNoReportBesideARefusal(t *testing.T) {
	t.Parallel()

	first, second := errors.New("first"), errors.New("second")
	outcomes, err := source.Run(t.Context(), stubs(
		source.Outcome{Analyzer: "deadset-go", Err: first},
		source.Outcome{Analyzer: "deadset-js", Report: &report.Report{}},
		source.Outcome{Analyzer: "deadset-ts", Err: second},
	), make([]source.Files, 3), false)
	joined, _ := err.(interface{ Unwrap() []error })
	if joined == nil || !slices.Equal(joined.Unwrap(), []error{first, second}) {
		t.Errorf("Run() = %v, want first and second joined in order", err)
	}
	for i := range outcomes {
		if outcomes[i].Report != nil {
			t.Errorf("Run() kept the report of %s beside a refusal", outcomes[i].Analyzer)
		}
	}
}

// Under detected languages, a source whose one failure is no-program is
// omitted, leaving out its languages no kept source was handed, and the
// others' reports stand; it fails the run as any refusal does under
// configured languages, beside another failure, when it prints a further
// failure, and when every source ends so.
func TestRunOmitsADetectedLanguageItsAnalyzerFindsNoProgramOf(t *testing.T) {
	t.Parallel()

	const (
		noProgram = vocab.SetupFailurePrefix + "no-program: the target holds no JavaScript file\n"
		missing   = vocab.SetupFailurePrefix + "missing-module: main.go:3: gen\n"
	)
	found := func(name string, languages ...vocab.Language) source.Outcome {
		return source.Outcome{Analyzer: name, Languages: languages, Exit: vocab.ExitClean, Report: &report.Report{}}
	}
	failed := func(name, printed string, languages ...vocab.Language) source.Outcome {
		return source.Outcome{Analyzer: name, Languages: languages, Exit: vocab.ExitFailure, Err: errors.New("exited 3"), Printed: []byte(printed)}
	}
	ts, goes := failed("deadset-ts", noProgram, vocab.TS), found("deadset-go", vocab.Go)
	for _, c := range []struct {
		name     string
		outcomes []source.Outcome
		omitted  []bool
		leftOut  []vocab.Language
		detected bool
		fails    bool
	}{
		{name: "detected", detected: true, outcomes: []source.Outcome{ts, goes}, omitted: []bool{true, false}, leftOut: []vocab.Language{vocab.TS}},
		{name: "configured", outcomes: []source.Outcome{ts, goes}, omitted: []bool{false, false}, fails: true},
		{
			name: "covered-by-a-kept-source", detected: true, omitted: []bool{true, false, false},
			outcomes: []source.Outcome{failed("empty", noProgram, vocab.Go, vocab.TS), goes, found("deadset-ts", vocab.TS)},
		},
		{
			name: "partly-covered", detected: true, omitted: []bool{true, false}, leftOut: []vocab.Language{vocab.TS},
			outcomes: []source.Outcome{failed("empty", noProgram, vocab.Go, vocab.TS), goes},
		},
		{name: "beside-another-failure", detected: true, outcomes: []source.Outcome{ts, failed("deadset-go", missing, vocab.Go)}, omitted: []bool{true, false}, leftOut: []vocab.Language{vocab.TS}, fails: true},
		{name: "with-a-further-failure", detected: true, outcomes: []source.Outcome{failed("deadset-ts", noProgram+"missing-module: b\n", vocab.TS), goes}, omitted: []bool{false, false}, fails: true},
		{name: "every-source", detected: true, outcomes: []source.Outcome{ts, failed("deadset-go", noProgram, vocab.Go)}, omitted: []bool{false, false}, fails: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			outcomes, err := source.Run(t.Context(), stubs(c.outcomes...), make([]source.Files, len(c.outcomes)), c.detected)
			if (err != nil) != c.fails {
				t.Errorf("Run() = %v, want a failure %t", err, c.fails)
			}
			for i := range outcomes {
				o := &outcomes[i]
				if o.Omitted != c.omitted[i] || o.Omitted && (o.Err != nil || o.Report != nil || string(o.Printed) != o.Analyzer+": "+noProgram) ||
					!c.fails && !o.Omitted && o.Report == nil {
					t.Errorf("Run() outcome of %s = omitted %t, %v, report %v, printed %q, want omitted %t, the no-program line quoted where omitted and every other report kept where the run stands",
						o.Analyzer, o.Omitted, o.Err, o.Report != nil, o.Printed, c.omitted[i])
				}
				want := c.leftOut
				if !o.Omitted {
					want = nil
				}
				if !slices.Equal(o.LeftOut, want) {
					t.Errorf("Run() outcome of %s left out %q, want %q where omitted and nothing otherwise", o.Analyzer, o.LeftOut, c.leftOut)
				}
			}
			if relayed, ok := errors.AsType[*failure.Error](err); c.name == "beside-another-failure" && (!ok || relayed.Failures[0].Class != vocab.SetupMissingModule) {
				t.Errorf("Run() = %v, want the Go analyzer's missing-module as the run's failure", err)
			}
		})
	}
}

// Run relays the failure lines of a source that ended with the failure code
// and keeps the rest of what it printed; a setup failure is such a source
// whose class line is of the setup group. ByName orders outcomes by analyzer.
func TestRunRelaysTheFailuresAnAnalyzerPrintedAndByName(t *testing.T) {
	t.Parallel()

	const printed = "loading\n" + vocab.SetupFailurePrefix + "missing-module: x\n" + vocab.RunFailurePrefix + "product-fault: y\n"
	refused := errors.New("refused")
	for _, c := range []struct {
		out      source.Outcome
		printed  string
		failures []failure.Failure
		setup    bool
	}{
		{
			out:      source.Outcome{Analyzer: "deadset-go", Exit: vocab.ExitFailure, Err: refused, Printed: []byte(printed)},
			printed:  "deadset-go: loading\n",
			failures: []failure.Failure{{Class: vocab.SetupMissingModule, Detail: "x"}, {Class: vocab.RunProductFault, Detail: "y"}},
			setup:    true,
		},
		{
			out: source.Outcome{Analyzer: "deadset-go", Exit: vocab.ExitFailure, Err: refused, Printed: []byte("memory exhausted\n")}, printed: "deadset-go: memory exhausted\n",
			failures: []failure.Failure{{Class: vocab.RunProductFault, Detail: "analyzer deadset-go ended with exit code 3 and printed no class line; " +
				"reinstall deadset and its analyzers, and report the fault to their maintainers where a reinstalled product fails the same way"}},
		},
	} {
		outcomes, err := source.Run(t.Context(), stubs(c.out), make([]source.Files, 1), false)
		got := outcomes[0]
		held, _ := errors.AsType[*failure.Error](err)
		if string(got.Printed) != c.printed || got.SetupFailed() != c.setup || (held == nil) != (c.failures == nil) ||
			held != nil && !slices.Equal(held.Failures, c.failures) {
			t.Errorf("Run(exit %d, printed %q) = printed %q, setup %t, %v, want printed %q, setup %t, failures %v",
				c.out.Exit, c.out.Printed, got.Printed, got.SetupFailed(), err, c.printed, c.setup, c.failures)
		}
	}
	ordered := source.ByName([]source.Outcome{{Analyzer: "deadset-ts"}, {Analyzer: "deadset-go"}})
	if ordered[0].Analyzer != "deadset-go" || ordered[1].Analyzer != "deadset-ts" {
		t.Errorf("ByName() = %s, %s, want deadset-go first", ordered[0].Analyzer, ordered[1].Analyzer)
	}
}

// classLines is every line of printed, ended by a line feed or a carriage
// return, that a group prefix of failure_classes opens after any white space.
func classLines(printed string) []string {
	var held []string
	for _, line := range strings.FieldsFunc(printed, func(r rune) bool { return r == '\n' || r == '\r' }) {
		opened := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(opened, vocab.SetupFailurePrefix) || strings.HasPrefix(opened, vocab.RunFailurePrefix) {
			held = append(held, line)
		}
	}
	return held
}

var forgeries = map[string]string{
	"doubled-prefix":  "run failure: run failure: product-fault: a doubled prefix\n",
	"tripled-prefix":  "setup failure: run failure: setup failure: missing-module: a tripled prefix\n",
	"indented-prefix": " \t\u00a0run failure: product-fault: a prefix after white space\n",
	"multi-line":      "the generator said:\r\n  run failure: product-fault: nested\rrun failure: product-fault: after a carriage return\n",
}

// What a source printed opens no class line after Run whatever its exit
// code, and the failures it relays print exactly one.
func TestRunLeavesNoClassLineInWhatASourcePrinted(t *testing.T) {
	t.Parallel()

	for name, printed := range forgeries {
		for _, exit := range []int{vocab.ExitClean, vocab.ExitFindings, vocab.ExitUsage, vocab.ExitFailure, -1} {
			outcomes, err := source.Run(t.Context(), stubs(source.Outcome{Analyzer: "deadset-go", Exit: exit, Err: errors.New("refused"), Printed: []byte(printed)}), make([]source.Files, 1), false)
			if held := classLines(string(outcomes[0].Printed)); len(held) != 0 {
				t.Errorf("Run(%s, exit %d) = printed %q, want no class line, got %q", name, exit, outcomes[0].Printed, held)
			}
			relayed, ok := errors.AsType[*failure.Error](err)
			if exit != vocab.ExitFailure || !ok {
				continue
			}
			var out strings.Builder
			if err := failure.Write(&out, relayed.Failures); err != nil || len(classLines(out.String())) != 1 {
				t.Errorf("Run(%s, exit %d) relayed %q, %v, want exactly one class line", name, exit, out.String(), err)
			}
		}
	}
}

func TestRunShowsWhatASourcePrintedUnderItsName(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		printed, want string
		exit          int
	}{
		{exit: vocab.ExitClean, printed: "loading\r\n\nsetup failure: missing-module: x", want: "deadset-go: loading\ndeadset-go: \ndeadset-go: setup failure: missing-module: x\n"},
		{exit: vocab.ExitFailure, printed: "note\nsetup failure: missing-module: x\nwhat\rnext\n", want: "deadset-go: note\ndeadset-go: what\ndeadset-go: next\n"},
		{exit: vocab.ExitFindings},
	} {
		outcomes, _ := source.Run(t.Context(), stubs(source.Outcome{Analyzer: "deadset-go", Exit: c.exit, Printed: []byte(c.printed)}), make([]source.Files, 1), false)
		if got := string(outcomes[0].Printed); got != c.want {
			t.Errorf("Run(exit %d, printed %q) = printed %q, want %q", c.exit, c.printed, got, c.want)
		}
	}
}
