package policy_test

import (
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/policy"
)

// merged is the merged report a published merge case expects.
func merged(t *testing.T, name string) *report.Report {
	t.Helper()

	body, err := spec.Vectors.ReadFile("vectors/merge/" + name + "/expected.json")
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	r, err := report.Decode(body)
	if err != nil {
		t.Fatalf("Setup: Decode(%s) = %v", name, err)
	}
	return r
}

// configured is a severity map naming one code.
func configured(code string, severity vocab.Severity) func(string) (vocab.Severity, bool) {
	return func(asked string) (vocab.Severity, bool) {
		if asked == code {
			return severity, true
		}
		return "", false
	}
}

// codes is the code and analyzer of every finding, in order.
func codes(r *report.Report) []string {
	held := make([]string, len(r.Findings))
	for i := range r.Findings {
		held[i] = string(r.Findings[i].Code) + "/" + r.Findings[i].Analyzer
	}
	return held
}

// A severity the configuration sets for a code the merge emits replaces the
// merge's default and the counts follow; allow withholds the finding.
func TestApply_givesTheMergesOwnFindingsTheirConfiguredSeverity(t *testing.T) {
	t.Parallel()

	r := merged(t, "edge-absent-on-every-side")
	policy.Apply(r, &policy.Options{Severity: configured("DS9005", vocab.Warn)})
	if len(r.Findings) != 1 || r.Findings[0].Severity != vocab.Warn {
		t.Errorf("Apply(DS9005 at warn) findings = %+v, want the one DS9005 at warn", r.Findings)
	}
	if want := (report.BySeverity{Warn: 1}); r.Totals.BySeverity != want || r.Totals.Findings != 1 {
		t.Errorf("Apply(DS9005 at warn) totals = %+v, want 1 finding counted %+v", r.Totals, want)
	}

	r = merged(t, "edge-absent-on-every-side")
	policy.Apply(r, &policy.Options{Severity: configured("DS9005", vocab.Allow)})
	if len(r.Findings) != 0 || r.Findings == nil {
		t.Errorf("Apply(DS9005 at allow) findings = %#v, want an empty list", r.Findings)
	}
	if want := (report.Totals{}); r.Totals != want {
		t.Errorf("Apply(DS9005 at allow) totals = %+v, want every count zero", r.Totals)
	}
}

// A finding an input report carried keeps the severity its analyzer gave it,
// whatever the configuration sets for its code: the analyzer applied the
// configuration it was handed.
func TestApply_leavesAnAnalyzersFindingAsTheAnalyzerWroteIt(t *testing.T) {
	t.Parallel()

	r := merged(t, "two-reports-no-edges")
	want := r.Totals
	policy.Apply(r, &policy.Options{Severity: configured("DS1001", vocab.Allow)})
	if got := codes(r); !slices.Equal(got, []string{"DS1001/deadset-go", "DS1002/deadset-go", "DS1003/deadset-ts", "DS1001/deadset-ts"}) {
		t.Errorf("Apply(DS1001 at allow over analyzer findings) findings = %q, want all four kept", got)
	}
	if r.Totals != want {
		t.Errorf("Apply(DS1001 at allow over analyzer findings) totals = %+v, want %+v unchanged", r.Totals, want)
	}
}

// The merged report stays in canonical order whatever order the renderings
// take: reporters.sort orders renderings only.
func TestApply_keepsTheCanonicalOrderUnderEitherSort(t *testing.T) {
	t.Parallel()

	for _, bySize := range []bool{true, false} {
		r := merged(t, "two-reports-no-edges")
		before := codes(r)
		policy.Apply(r, &policy.Options{BySize: bySize})
		if got := codes(r); !slices.Equal(got, before) {
			t.Errorf("Apply(by size %t) = %q, want the canonical order %q", bySize, got, before)
		}
	}
}

// A maximum keeps the first findings of the order and counts the rest as
// omitted, and every other count keeps describing the whole set; zero keeps
// every finding.
func TestApply_boundsTheFindingsAndCountsWhatItOmits(t *testing.T) {
	t.Parallel()

	r := merged(t, "two-reports-no-edges")
	want := r.Totals
	want.Omitted = 2
	policy.Apply(r, &policy.Options{BySize: true, MaxFindings: 2})
	if got := codes(r); !slices.Equal(got, []string{"DS1001/deadset-go", "DS1002/deadset-go"}) {
		t.Errorf("Apply(max 2 by size) findings = %q, want the two largest", got)
	}
	if r.Totals != want {
		t.Errorf("Apply(max 2 by size) totals = %+v, want %+v", r.Totals, want)
	}

	r = merged(t, "two-reports-no-edges")
	policy.Apply(r, &policy.Options{})
	if len(r.Findings) != 4 || r.Totals.Omitted != 0 {
		t.Errorf("Apply(no maximum) kept %d findings and omitted %d, want 4 and 0", len(r.Findings), r.Totals.Omitted)
	}
}
