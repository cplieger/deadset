package merge

import (
	"bytes"
	"slices"
	"testing"

	"github.com/cplieger/deadset/v2/internal/report"
)

// member is the positioned symbol of the function finding names at path and line.
func member(path string, line int, name string) report.Positioned {
	f := finding("", path, line, name)
	return report.Positioned{Ref: f.Symbol.Ref, Name: name, Position: f.Position}
}

// listedRefs is every listed component with the references of its members, in order.
func listedRefs(merged *report.Report) []string {
	var listed []string
	for _, entry := range merged.Components {
		for _, one := range entry.Members {
			listed = append(listed, entry.ID+" "+one.Ref)
		}
	}
	return listed
}

// Two components a dead edge joins are listed once, under the identifier the
// union keeps, with the members of both in canonical order; a component the
// merged findings no longer name is not listed.
func TestMergeListsAJoinedComponentsMembersOnceUnderItsIdentifier(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{finding("deadset-go/c-1", "wire.go", 9, "helper")}
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/Event", report.SideProvides, finding("deadset-go/c-1", "wire.go", 1, "Event")),
	}
	golang.Components = []report.ComponentListing{
		{ID: "deadset-go/c-1", Members: []report.Positioned{member("wire.go", 1, "Event"), member("wire.go", 9, "helper")}},
		{ID: "deadset-go/c-7", Members: []report.Positioned{member("gone.go", 1, "Gone")}},
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/Event", report.SideUsedBy, finding("deadset-ts/c-1", "web/wire.ts", 1, "Client")),
	}
	typescript.Components = []report.ComponentListing{
		{ID: "deadset-ts/c-1", Members: []report.Positioned{member("web/wire.ts", 1, "Client")}},
	}

	for _, order := range [][]*report.Report{{golang, typescript}, {typescript, golang}} {
		merged, _ := mustMerge(t, inputs(order...))
		want := []string{
			"deadset-ts/c-1 ts://example.com/app#Client",
			"deadset-ts/c-1 go://example.com/app#Event",
			"deadset-ts/c-1 go://example.com/app#helper",
		}
		if got := listedRefs(merged); !slices.Equal(got, want) {
			t.Errorf("Merge(a joined component).components = %q, want %q", got, want)
		}
	}
}

// A merge of reports that list no component lists none either.
func TestMergeListsNoComponentWhereNoInputDoes(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{finding("deadset-go/c-1", "main.go", 3, "spare")}
	merged, written := mustMerge(t, inputs(golang))
	if merged.Components != nil || bytes.Contains(written, []byte(`"components"`)) {
		t.Errorf("Merge(no listing).components = %v, want the member absent", merged.Components)
	}
}

// A stale edge's component, which the merger mints and no input lists, is listed
// with the edge's subject alone where an input lists components.
func TestMergeListsAStaleEdgesComponentWithItsSubject(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		{Edge: "wire/b", Side: report.SideProvides, Symbol: "go://example.com/app#B", State: report.StateAbsent},
	}
	golang.Components = []report.ComponentListing{}

	merged, _ := mustMerge(t, inputs(golang))
	if got, want := listedRefs(merged), []string{"deadset/c-1 go://example.com/app#B"}; !slices.Equal(got, want) {
		t.Errorf("Merge(a stale edge).components = %q, want %q", got, want)
	}
}
