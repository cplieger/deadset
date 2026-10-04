package invoke_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// rendezvous is a fake analyzer named analyzer that prints its first line,
// marks its start in markers, waits for the start marker of the analyzer named
// other, prints its second line and writes written to its report. It exits
// with 3 when the other marker has not appeared within ten seconds, so a pair
// of them leaves two reports only when both ran at once.
func rendezvous(t *testing.T, markers, analyzer, other string, written []byte) fake {
	t.Helper()

	dir := t.TempDir()
	document := filepath.Join(dir, "written.json")
	if err := os.WriteFile(document, written, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", document, err)
	}
	return script(t, dir, strings.Join([]string{
		fmt.Sprintf(`echo '%s starts' >&2`, analyzer),
		fmt.Sprintf(`: > '%s'`, filepath.Join(markers, analyzer)),
		`waited=0`,
		fmt.Sprintf(`while [ ! -e '%s' ]; do`, filepath.Join(markers, other)),
		`	waited=$((waited + 1))`,
		`	if [ "$waited" -gt 1000 ]; then exit 3; fi`,
		`	sleep 0.01`,
		`done`,
		fmt.Sprintf(`echo '%s ends' >&2`, analyzer),
		fmt.Sprintf(`cp '%s' "$report"`, document),
		`exit 0`,
		``,
	}, "\n"))
}

// rendezvousPair is the requests of two rendezvous analyzers, deadset-ts
// first, each waiting for the other to start, printing to diagnostics.
func rendezvousPair(t *testing.T, diagnostics *bytes.Buffer) []invoke.Request {
	t.Helper()

	markers := t.TempDir()
	requests := []invoke.Request{
		request(t, "deadset-ts", rendezvous(t, markers, "deadset-ts", "deadset-go", published(t, tsReport)).command),
		request(t, "deadset-go", rendezvous(t, markers, "deadset-go", "deadset-ts", published(t, oneReport)).command),
	}
	for i := range requests {
		requests[i].Diagnostics = diagnostics
	}
	return requests
}

// TestRunStartsEveryAnalyzerBeforeAnyFinishes pins that the analyzers of a
// run run at once: each of the pair finishes only after the other has
// started, so a run that waited for one before starting the next leaves the
// first without its report.
func TestRunStartsEveryAnalyzerBeforeAnyFinishes(t *testing.T) {
	t.Parallel()

	reports, err := invoke.Run(t.Context(), rendezvousPair(t, &bytes.Buffer{}))
	if err != nil || len(reports) != 2 {
		t.Fatalf("Run(two analyzers that each wait for the other to start) = %d reports, %v, want both reports", len(reports), err)
	}
}

// TestRunWritesEachAnalyzersOutputWholeInNameOrder pins that what each
// analyzer prints reaches Diagnostics whole and ordered by analyzer name,
// whatever the request order: the pair prints its two lines around the other's
// start, so lines written as they were printed would interleave.
func TestRunWritesEachAnalyzersOutputWholeInNameOrder(t *testing.T) {
	t.Parallel()

	var diagnostics bytes.Buffer
	if _, err := invoke.Run(t.Context(), rendezvousPair(t, &diagnostics)); err != nil {
		t.Fatalf("Run(two analyzers that each wait for the other to start) = %v, want both reports", err)
	}
	const want = "deadset-go starts\ndeadset-go ends\ndeadset-ts starts\ndeadset-ts ends\n"
	if got := diagnostics.String(); got != want {
		t.Errorf("Run() wrote the diagnostics %q, want %q, each analyzer's whole and deadset-go's first", got, want)
	}
}
