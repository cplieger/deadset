package invoke_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/deadset/internal/invoke"
)

// refusals is every *invoke.Error the error Run returned joins, in order.
func refusals(t *testing.T, err error) []*invoke.Error {
	t.Helper()

	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("Run() = %v, want an error joining the refusals", err)
	}
	var found []*invoke.Error
	for _, one := range joined.Unwrap() {
		refused, ok := errors.AsType[*invoke.Error](one)
		if !ok {
			t.Fatalf("Run() joins %v, want only *invoke.Error values", one)
		}
		found = append(found, refused)
	}
	return found
}

// TestRunReturnsEveryReportInRequestOrder pins that a run whose analyzers all
// leave a report returns each one at its request's position.
func TestRunReturnsEveryReportInRequestOrder(t *testing.T) {
	t.Parallel()

	requests := []invoke.Request{
		request(t, "deadset-ts", fakeAnalyzer(t, 0, published(t, tsReport)).command),
		request(t, "deadset-go", fakeAnalyzer(t, 1, published(t, oneReport)).command),
	}
	reports, err := invoke.Run(t.Context(), requests)
	if err != nil {
		t.Fatalf("Run(two analyzers that each write a report) = %v, want both reports", err)
	}
	var got []string
	for _, r := range reports {
		got = append(got, r.Analyzer.Name)
	}
	if want := []string{"deadset-ts", "deadset-go"}; !slices.Equal(got, want) {
		t.Errorf("Run() returned the reports of %q, want %q, the order of the requests", got, want)
	}
}

// TestRunPresentsNoReportBesideAFailure pins that one analyzer exiting 3 leaves
// the run with no report at all: the other analyzer's report, read and valid,
// is not returned, and the refusal names the analyzer that failed alone.
func TestRunPresentsNoReportBesideAFailure(t *testing.T) {
	t.Parallel()

	requests := []invoke.Request{
		request(t, "deadset-go", fakeAnalyzer(t, 1, published(t, oneReport)).command),
		request(t, "deadset-ts", fakeAnalyzer(t, 3, nil).command),
	}
	reports, err := invoke.Run(t.Context(), requests)
	if reports != nil {
		t.Errorf("Run(one analyzer exiting 3 beside one writing a report) = %d reports, want none", len(reports))
	}
	found := refusals(t, err)
	if len(found) != 1 || found[0].Analyzer != "deadset-ts" || found[0].Exit != 3 {
		t.Errorf("Run() = %v, want one refusal, of deadset-ts exiting 3", err)
	}
}

// TestRunJoinsEveryFailureInRequestOrder pins that a run in which every
// analyzer fails names each failure, so the operator sees them all at once.
func TestRunJoinsEveryFailureInRequestOrder(t *testing.T) {
	t.Parallel()

	requests := []invoke.Request{
		request(t, "deadset-go", fakeAnalyzer(t, 2, nil).command),
		request(t, "deadset-ts", fakeAnalyzer(t, 0, truncated(t)).command),
	}
	reports, err := invoke.Run(t.Context(), requests)
	if reports != nil {
		t.Errorf("Run(two failing analyzers) = %d reports, want none", len(reports))
	}
	found := refusals(t, err)
	if len(found) != 2 || found[0].Analyzer != "deadset-go" || found[1].Analyzer != "deadset-ts" {
		t.Fatalf("Run() = %v, want the refusals of deadset-go and then deadset-ts", err)
	}
	if found[0].Exit != 2 || found[1].Exit != 0 {
		t.Errorf("Run() refusals name exits %d and %d, want 2 and 0", found[0].Exit, found[1].Exit)
	}
}
