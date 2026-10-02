package merge

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/deadset/internal/report"
)

// Of two pending findings no report can resolve, the refusal names the one
// first in canonical order of the findings, whatever order the edges sort in.
func TestMergeNamesTheFirstUnresolvedPendingFindingInCanonicalOrder(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/A", report.SideProvides, finding("deadset-go/c-1", "z.go", 1, "Z")),
		pending("wire/B", report.SideProvides, finding("deadset-go/c-2", "a.go", 1, "A")),
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/C", report.SideUsedBy, report.StateLive)}

	merged, err := Merge(inputs(typescript, golang), accepted, &caller)
	refused, ok := errors.AsType[*UnresolvedError](err)
	if merged != nil || !ok || !errors.Is(err, ErrUnresolvedEdge) {
		t.Fatalf("Merge(two unresolved pending findings) = %v, %v, want no report and an *UnresolvedError", merged, err)
	}
	want := UnresolvedError{
		Edge: "wire/B", Side: report.SideProvides, Symbol: "go://example.com/app#A", Analyzer: "deadset-go",
		Searched: []string{"deadset-go", "deadset-ts"},
	}
	if refused.Edge != want.Edge || refused.Side != want.Side || refused.Symbol != want.Symbol ||
		refused.Analyzer != want.Analyzer || !slices.Equal(refused.Searched, want.Searched) {
		t.Errorf("Merge(two unresolved pending findings) = %+v, want %+v", refused, want)
	}
}

// Where two analyzers evaluate one side, one live answer keeps the pair live
// and drops both pending findings, and one dead answer with no live one
// promotes both.
func TestMergeReadsTheStrongestAnswerAcrossAnalyzersOfOneSide(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		other report.EdgeEvaluation
		want  []string
	}{
		{name: "live_beside_dead", other: evaluation("wire/Event", report.SideUsedBy, report.StateLive), want: []string{}},
		{
			name:  "absent_beside_dead",
			other: evaluation("wire/Event", report.SideUsedBy, report.StateAbsent),
			want:  []string{"other-ts:Client", "deadset-go:Event", "deadset-go:helper"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			golang := analyzerReport("deadset-go", "go")
			golang.Findings = []report.Finding{finding("deadset-go/c-1", "wire.go", 9, "helper")}
			golang.EdgeEvaluations = []report.EdgeEvaluation{
				pending("wire/Event", report.SideProvides, finding("deadset-go/c-1", "wire.go", 1, "Event")),
			}
			typescript := analyzerReport("deadset-ts", "ts")
			typescript.EdgeEvaluations = []report.EdgeEvaluation{tc.other}
			other := analyzerReport("other-ts", "ts")
			other.EdgeEvaluations = []report.EdgeEvaluation{
				pending("wire/Event", report.SideUsedBy, finding("other-ts/c-1", "web/wire.ts", 1, "Client")),
			}

			merged, _ := mustMerge(t, inputs(golang, typescript, other))
			if got := findingNames(merged); !slices.Equal(got, tc.want) {
				t.Errorf("Merge(dead provides, %s and dead used_by) findings = %q, want %q", tc.other.State, got, tc.want)
			}
		})
	}
}

// Promoted findings joined across two edges take the identifier of the
// component the canonical visit reaches first, with every joined component's
// counts summed, whichever order the analyzers' reports arrive in.
func TestMergeJoinsPairedComponentsUnderTheFirstReachedIdentifier(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/Second", report.SideProvides, finding("deadset-go/c-2", "a.go", 1, "Second")),
		pending("wire/First", report.SideProvides, finding("deadset-go/c-1", "b.go", 1, "First")),
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/First", report.SideUsedBy, finding("deadset-ts/c-1", "c.ts", 1, "First")),
		pending("wire/Second", report.SideUsedBy, finding("deadset-ts/c-1", "d.ts", 1, "Second")),
	}

	merged, forward := mustMerge(t, inputs(golang, typescript))
	for i := range merged.Findings {
		f := &merged.Findings[i]
		if f.Component.ID != "deadset-go/c-2" || f.Component.SymbolCount != 3 || f.Component.DeletableLines != 15 {
			t.Errorf("Merge() finding %s:%s component = %+v, want deadset-go/c-2 with 3 symbols and 15 lines",
				f.Analyzer, f.Symbol.Name, f.Component)
		}
	}
	if len(merged.Findings) != 4 {
		t.Errorf("Merge() findings = %q, want the four promoted findings", findingNames(merged))
	}
	if _, backward := mustMerge(t, inputs(typescript, golang)); !bytes.Equal(forward, backward) {
		t.Errorf("Merge(typescript, golang) =\n%s\nwant the bytes of Merge(golang, typescript)\n%s", backward, forward)
	}
}
