package merge

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// The stale-edge finding's code and the name, fixability and default severity
// the issue-kind vocabulary gives it, and the target-relative path of the
// document that declares edges, where a document-level finding sits.
const (
	staleEdgeCode       = "DS1705"
	staleEdgeKind       = "stale-cross-language-edge"
	staleEdgeFixability = report.FixabilityNone
	staleEdgeSeverity   = report.SeverityDeny
	edgesDocument       = "deadset-edges.json"
)

// staleEdges is one stale-edge finding per edge, in bytewise order of the edge
// identifiers, for every edge with a side that holds evaluations and whose
// every evaluation is absent. merger prefixes each finding's component, which
// counts the emitted findings from 1.
func staleEdges(index edgeIndex, merger string) []report.Finding {
	var emitted []report.Finding
	for _, edge := range slices.Sorted(maps.Keys(index)) {
		var held, stale []report.EdgeSide
		for _, side := range []report.Side{report.SideProvides, report.SideUsedBy} {
			evaluations := index[edge][side]
			if len(evaluations) == 0 {
				continue
			}
			evaluated := report.EdgeSide{Side: side, Symbol: evaluations[0].Symbol, State: strongestOf(evaluations)}
			held = append(held, evaluated)
			if evaluated.State == report.StateAbsent {
				stale = append(stale, evaluated)
			}
		}
		if len(stale) > 0 {
			emitted = append(emitted, staleEdge(edge, held, stale, merger+"/c-"+strconv.Itoa(len(emitted)+1)))
		}
	}
	return emitted
}

// strongestOf is the strongest state among one side's evaluations, each read
// as the state it reports.
func strongestOf(evaluations []*report.EdgeEvaluation) report.State {
	strongest := evaluations[0].State
	for _, e := range evaluations[1:] {
		if strength[e.State] < strength[strongest] {
			strongest = e.State
		}
	}
	return strongest
}

// staleEdge is the finding for one stale edge: about the first stale side's
// symbol, at the start of the edges document, rooting a one-member component
// with no deletable line, and naming every evaluated side.
func staleEdge(edge string, held, stale []report.EdgeSide, component string) report.Finding {
	message := "no analyzer enumerates the symbol either side of the edge names"
	if len(stale) == 1 {
		message = "no analyzer enumerates the symbol the " + string(stale[0].Side) + " side of the edge names"
	}
	language, _, _ := strings.Cut(stale[0].Symbol, "://")
	return report.Finding{
		Code:              staleEdgeCode,
		Kind:              staleEdgeKind,
		Language:          language,
		Position:          report.Position{Path: edgesDocument, Line: 1, Column: 1, EndLine: 1},
		Symbol:            report.Symbol{Ref: stale[0].Symbol, Kind: "edge", Name: edge, SizeLines: 1},
		ReachabilityClass: report.ClassCertain,
		Confidence:        report.ClassCertain,
		Component:         report.Component{ID: component, Root: true, SymbolCount: 1},
		RetainedBy:        []string{},
		Configurations:    []string{},
		ConsumersLoaded:   []string{},
		Fixability:        staleEdgeFixability,
		Severity:          staleEdgeSeverity,
		Message:           message,
		Details:           report.Details{Edge: edge, Sides: held},
	}
}
