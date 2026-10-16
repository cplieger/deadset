package source_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
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
	outcomes, err := source.Run(t.Context(), sources, make([]source.Files, 2))
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
	), make([]source.Files, 3))
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

// A setup failure is the failure code with a line the setup-failure prefix
// opens; ByName orders outcomes by analyzer.
func TestOutcomesSetupFailedAndByName(t *testing.T) {
	t.Parallel()

	line := []byte("deadset-go: loading\n" + vocab.SetupFailurePrefix + "missing-module: x\n")
	for _, c := range []struct {
		out  source.Outcome
		want bool
	}{
		{out: source.Outcome{Exit: vocab.ExitFailure, Printed: line}, want: true},
		{out: source.Outcome{Exit: vocab.ExitUsage, Printed: line}},
		{out: source.Outcome{Exit: vocab.ExitFailure, Printed: []byte("memory exhausted\n")}},
	} {
		if got := c.out.SetupFailed(); got != c.want {
			t.Errorf("(%+v).SetupFailed() = %t, want %t", c.out, got, c.want)
		}
	}
	ordered := source.ByName([]source.Outcome{{Analyzer: "deadset-ts"}, {Analyzer: "deadset-go"}})
	if ordered[0].Analyzer != "deadset-go" || ordered[1].Analyzer != "deadset-ts" {
		t.Errorf("ByName() = %s, %s, want deadset-go first", ordered[0].Analyzer, ordered[1].Analyzer)
	}
}
