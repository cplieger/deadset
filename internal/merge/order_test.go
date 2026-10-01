package merge

import (
	"bytes"
	"slices"
	"testing"

	"github.com/cplieger/deadset/internal/report"
)

func TestMergeInterleavesTheLanguagesByPath(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{
		finding("deadset-go/c-1", "web/server.go", 1, "handler"),
		finding("deadset-go/c-2", "internal/wire/wire.go", 1, "Event"),
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Findings = []report.Finding{
		finding("deadset-ts/c-1", "web/src/wire.ts", 1, "Event"),
		finding("deadset-ts/c-2", "internal/wire/client.ts", 1, "client"),
	}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	var paths []string
	for _, f := range merged.Findings {
		paths = append(paths, f.Position.Path)
	}
	want := []string{"internal/wire/client.ts", "internal/wire/wire.go", "web/server.go", "web/src/wire.ts"}
	if !slices.Equal(paths, want) {
		t.Errorf("Merge(a Go and a TypeScript report) finding paths = %q, want %q", paths, want)
	}
}

func TestMergeOrdersTwoIdenticalFindingsByTheirAnalyzer(t *testing.T) {
	t.Parallel()

	beta := analyzerReport("beta-go", "go")
	beta.Findings = []report.Finding{finding("deadset-go/c-1", "store.go", 1, "a")}
	alpha := analyzerReport("alpha-go", "go")
	alpha.Findings = []report.Finding{finding("deadset-go/c-1", "store.go", 1, "a")}

	merged, forward := mustMerge(t, inputs(beta, alpha))
	if got := findingNames(merged); !slices.Equal(got, []string{"alpha-go:a", "beta-go:a"}) {
		t.Errorf("Merge(one finding from two analyzers) = %q, want alpha-go's, then beta-go's", got)
	}
	if _, backward := mustMerge(t, inputs(alpha, beta)); !bytes.Equal(forward, backward) {
		t.Errorf("Merge(alpha, beta) =\n%s\nwant the bytes of Merge(beta, alpha)\n%s", backward, forward)
	}
}

// The analyzer name is a key component, so it decides before the members the
// record itself carries: alpha-go's finding precedes beta-go's although its
// message sorts later.
func TestMergeOrdersByTheAnalyzerBeforeTheRecordsOwnMembers(t *testing.T) {
	t.Parallel()

	beta := analyzerReport("beta-go", "go")
	early := finding("deadset-go/c-1", "store.go", 1, "a")
	early.Message = "a dead function"
	beta.Findings = []report.Finding{early}
	beta.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 1, "go://example.com/app#a")}
	beta.StaleSuppressions[0].Message = "a stale entry"
	alpha := analyzerReport("alpha-go", "go")
	late := finding("deadset-go/c-1", "store.go", 1, "a")
	late.Message = "z dead function"
	alpha.Findings = []report.Finding{late}
	alpha.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 1, "go://example.com/app#a")}
	alpha.StaleSuppressions[0].Message = "z stale entry"

	merged, _ := mustMerge(t, inputs(beta, alpha))
	if got := merged.Findings[0].Message; got != late.Message {
		t.Errorf("Merge() first finding's message = %q, want alpha-go's %q", got, late.Message)
	}
	if got := merged.StaleSuppressions[0].Analyzer; got != "alpha-go" {
		t.Errorf("Merge() first stale suppression's analyzer = %q, want alpha-go", got)
	}
}

// TestMergeOrdersFindingsByEachKeyComponentInTurn pins that every component of
// the canonical key decides before the next one: each case's first finding
// precedes its second although the second wins on every later component.
func TestMergeOrdersFindingsByEachKeyComponentInTurn(t *testing.T) {
	t.Parallel()

	base := finding("deadset-go/c-1", "b.go", 5, "m")
	cases := map[string]struct{ first, second func(*report.Finding) }{
		"path before line": {
			first:  func(f *report.Finding) { f.Position.Path, f.Position.Line = "a.go", 9 },
			second: func(f *report.Finding) { f.Position.Path, f.Position.Line = "b.go", 1 },
		},
		"line before column": {
			first:  func(f *report.Finding) { f.Position.Line, f.Position.Column = 2, 9 },
			second: func(f *report.Finding) { f.Position.Line, f.Position.Column = 10, 1 },
		},
		"column before code": {
			first:  func(f *report.Finding) { f.Position.Column, f.Code, f.Kind = 2, "DS1002", "unused-unexported" },
			second: func(f *report.Finding) { f.Position.Column, f.Code, f.Kind = 10, "DS1001", "unused-exported" },
		},
		"code before symbol": {
			first: func(f *report.Finding) {
				f.Code, f.Kind, f.Symbol.Ref = "DS1001", "unused-exported", "go://example.com/app#z"
			},
			second: func(f *report.Finding) {
				f.Code, f.Kind, f.Symbol.Ref = "DS1002", "unused-unexported", "go://example.com/app#a"
			},
		},
		"symbol before message": {
			first:  func(f *report.Finding) { f.Symbol.Ref, f.Message = "go://example.com/app#a", "z" },
			second: func(f *report.Finding) { f.Symbol.Ref, f.Message = "go://example.com/app#b", "a" },
		},
		"equal keys by encoding": {
			first:  func(f *report.Finding) { f.Message = "a dead function" },
			second: func(f *report.Finding) { f.Message = "b dead function" },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first, second := base, base
			c.first(&first)
			c.second(&second)
			golang := analyzerReport("deadset-go", "go")
			golang.Findings = []report.Finding{second, first}

			merged, _ := mustMerge(t, inputs(golang))
			if got := merged.Findings[0]; got.Position != first.Position || got.Code != first.Code ||
				got.Symbol.Ref != first.Symbol.Ref || got.Message != first.Message {
				t.Errorf("Merge() first finding = %+v, want %+v", got, first)
			}
		})
	}
}

func TestMergeOrdersStaleSuppressionsByTheirSiteThenTheirSymbol(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.StaleSuppressions = []report.StaleSuppression{
		stale("main.go", 3, "go://example.com/app#a"),
		stale("deadset-ignore.json", 9, "go://example.com/app#b"),
		stale("deadset-ignore.json", 4, "go://example.com/app#z"),
		stale("deadset-ignore.json", 4, "go://example.com/app#c"),
	}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#c")}

	merged, _ := mustMerge(t, inputs(typescript, golang))
	var got []string
	for _, s := range merged.StaleSuppressions {
		got = append(got, s.Position.Path+":"+s.Symbol+":"+s.Analyzer)
	}
	want := []string{
		"deadset-ignore.json:go://example.com/app#c:deadset-go",
		"deadset-ignore.json:go://example.com/app#c:deadset-ts",
		"deadset-ignore.json:go://example.com/app#z:deadset-go",
		"deadset-ignore.json:go://example.com/app#b:deadset-go",
		"main.go:go://example.com/app#a:deadset-go",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Merge() stale suppressions = %q, want %q", got, want)
	}
}

// A declared gap carries no path, code or symbol, so the analyzer name decides
// before anything the gap itself holds.
func TestMergeOrdersDeclaredGapsByAnalyzerThenEncoding(t *testing.T) {
	t.Parallel()

	zeta := analyzerReport("zeta-ts", "ts")
	zeta.DeclaredGaps = []report.DeclaredGap{gap("b-fixture"), gap("a-fixture")}
	alpha := analyzerReport("alpha-go", "go")
	alpha.DeclaredGaps = []report.DeclaredGap{gap("z-fixture")}

	merged, _ := mustMerge(t, inputs(zeta, alpha))
	var got []string
	for _, g := range merged.DeclaredGaps {
		got = append(got, g.Analyzer+":"+g.Fixture)
	}
	if want := []string{"alpha-go:z-fixture", "zeta-ts:a-fixture", "zeta-ts:b-fixture"}; !slices.Equal(got, want) {
		t.Errorf("Merge() declared gaps = %q, want %q", got, want)
	}
}

func TestMergeOrdersEdgeEvaluationsByEdgeSideAndAnalyzer(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{
		evaluation("wire/Z", report.SideProvides, report.StateLive),
		evaluation("wire/A", report.SideUsedBy, report.StateAbsent),
		evaluation("wire/A", report.SideProvides, report.StateLive),
	}
	other := analyzerReport("alpha-go", "go")
	other.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/A", report.SideUsedBy, report.StateLive)}

	merged, _ := mustMerge(t, inputs(golang, other))
	var got []string
	for _, e := range merged.EdgeEvaluations {
		got = append(got, e.Edge+":"+string(e.Side)+":"+e.Analyzer)
	}
	want := []string{
		"wire/A:provides:deadset-go",
		"wire/A:used_by:alpha-go",
		"wire/A:used_by:deadset-go",
		"wire/Z:provides:deadset-go",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Merge() edge evaluations = %q, want %q", got, want)
	}
}
