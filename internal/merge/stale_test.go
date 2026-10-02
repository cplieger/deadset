package merge

import (
	"slices"
	"testing"

	"github.com/cplieger/deadset/internal/report"
)

// Each edge with a wholly absent side yields one stale-edge finding, numbered
// in the edges' bytewise order and naming the stale side, and an edge both
// sides of which some analyzer enumerates yields none. The findings sit in
// canonical order, which their symbols decide.
func TestMergeReportsEachStaleEdgeOnceInEdgeOrder(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		{Edge: "wire/b", Side: report.SideProvides, Symbol: "go://example.com/app#B", State: report.StateAbsent},
		{Edge: "wire/a", Side: report.SideProvides, Symbol: "go://example.com/app#A", State: report.StateLive},
		{Edge: "wire/c", Side: report.SideProvides, Symbol: "go://example.com/app#C", State: report.StateLive},
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.EdgeEvaluations = []report.EdgeEvaluation{
		{Edge: "wire/b", Side: report.SideUsedBy, Symbol: "ts://@example/app/b.ts#B", State: report.StateAbsent},
		{Edge: "wire/a", Side: report.SideUsedBy, Symbol: "ts://@example/app/a.ts#A", State: report.StateAbsent},
		{Edge: "wire/c", Side: report.SideUsedBy, Symbol: "ts://@example/app/c.ts#C", State: report.StateLive},
	}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	var got []string
	for i := range merged.Findings {
		f := &merged.Findings[i]
		got = append(got, f.Component.ID+" "+f.Details.Edge+" "+f.Language+" "+f.Symbol.Ref+": "+f.Message)
	}
	want := []string{
		"deadset/c-2 wire/b go go://example.com/app#B: no analyzer enumerates the symbol either side of the edge names",
		"deadset/c-1 wire/a ts ts://@example/app/a.ts#A: no analyzer enumerates the symbol the used_by side of the edge names",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Merge() stale edges = %q, want %q", got, want)
	}
}
