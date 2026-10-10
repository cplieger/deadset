package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/cplieger/deadset/internal/report"
	"pgregory.net/rapid"
)

// The pools a drawn report takes its values from, small enough that two
// reports often hold the same record and two records often tie on a key
// component, which is where the order and the deduplication decide anything.
var (
	drawnAnalyzers = []string{"deadset-go", "deadset-ts", "example-go", "other-ts"}
	drawnPaths     = []string{"a.go", "internal/b.go", "internal/b.ts", "web/a.ts"}
	drawnNames     = []string{"a", "b", "Event"}
	drawnMessages  = []string{"unexported function has no reference in the target", "dead"}
	drawnSeverity  = []report.Severity{report.SeverityAllow, report.SeverityWarn, report.SeverityDeny}
	drawnEdges     = []string{"A", "B", "C"}

	// drawnEdgeStates draws dead twice as often as live or absent, so pairs
	// of dead answers, which only promotion decides, are common.
	drawnEdgeStates = []report.State{report.StateLive, report.StateDead, report.StateDead, report.StateAbsent}
	drawnSides      = []report.Side{report.SideProvides, report.SideUsedBy}
	drawnFixtures   = []string{"private-member-unread", "unused-exported-consumer"}
	drawnRules      = []report.TestFileRule{{Rule: "go-test-suffix", Matched: 3}, {Rule: "go-test-suffix", Matched: 4}, {Rule: "ts-test-suffix", Matched: 1}}
	drawnConsumers  = []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: "../cli"}, {ID: "example.com/web", Role: "consumer", Path: "../web"}}
	drawnAbsent     = []report.UnavailableConsumer{
		{ID: "example.com/tool", Role: "consumer", Reason: "it declares no path"},
		{ID: "example.com/tool", Role: "consumer", Reason: "the configuration excludes it"},
	}
	drawnCgo     = []string{"a_cgo.go", "b_cgo.go"}
	drawnConfigs = []report.Configuration{platform("linux-amd64"), platform("linux-amd64-integration", "integration"), platform("darwin-arm64")}
	drawnDropped = []report.ConfigurationNotBuilt{
		{Configuration: platform("windows-amd64"), Error: "cgo is not enabled"},
		{Configuration: platform("windows-amd64"), Error: "no toolchain for windows"},
	}
)

// drawFinding draws one finding of a component the named analyzer minted.
func drawFinding(t *rapid.T, analyzer, label string) report.Finding {
	path := rapid.SampledFrom(drawnPaths).Draw(t, label+": the path")
	f := finding(analyzer+"/c-"+strconv.Itoa(rapid.IntRange(1, 3).Draw(t, label+": the component")),
		path, rapid.IntRange(1, 3).Draw(t, label+": the line"), rapid.SampledFrom(drawnNames).Draw(t, label+": the name"))
	f.Position.Column = rapid.IntRange(1, 2).Draw(t, label+": the column")
	f.Message = rapid.SampledFrom(drawnMessages).Draw(t, label+": the message")
	f.Severity = rapid.SampledFrom(drawnSeverity).Draw(t, label+": the severity")
	f.Component.Root = rapid.Bool().Draw(t, label+": the subject roots its component")
	f.Component.DeletableLines = rapid.IntRange(0, 9).Draw(t, label+": the deletable lines")
	return f
}

// drawReport draws one analyzer's report: a few records of every kind, its
// edge evaluations in the states given, the envelope members a merge unions,
// some worded two ways under one id, and the counts it sums.
func drawReport(t *rapid.T, analyzer string, states []report.State) *report.Report {
	language := analyzer[len(analyzer)-2:]
	r := analyzerReport(analyzer, language)
	r.Analyzer.Version = rapid.SampledFrom([]string{"1.0.0", "1.1.0"}).Draw(t, analyzer+": the version")
	for i := range rapid.IntRange(0, 4).Draw(t, analyzer+": the findings") {
		r.Findings = append(r.Findings, drawFinding(t, analyzer, analyzer+" finding "+strconv.Itoa(i)))
	}
	if len(r.Findings) > 0 && rapid.Bool().Draw(t, analyzer+": a finding repeats") {
		r.Findings = append(r.Findings, r.Findings[rapid.IntRange(0, len(r.Findings)-1).Draw(t, analyzer+": the repeated finding")])
	}
	for i := range rapid.IntRange(0, 2).Draw(t, analyzer+": the stale suppressions") {
		label := analyzer + " stale suppression " + strconv.Itoa(i)
		r.StaleSuppressions = append(r.StaleSuppressions, stale("deadset-ignore.json",
			rapid.IntRange(1, 3).Draw(t, label+": the line"),
			"go://example.com/app#"+rapid.SampledFrom(drawnNames).Draw(t, label+": the symbol")))
	}
	for i := range rapid.IntRange(0, 2).Draw(t, analyzer+": the declared gaps") {
		r.DeclaredGaps = append(r.DeclaredGaps, gap(rapid.SampledFrom(drawnFixtures).Draw(t, analyzer+" gap "+strconv.Itoa(i))))
	}
	for i := range rapid.IntRange(0, 2).Draw(t, analyzer+": the edge evaluations") {
		label := analyzer + " evaluation " + strconv.Itoa(i)
		r.EdgeEvaluations = append(r.EdgeEvaluations, evaluation(
			"wire/"+rapid.SampledFrom(drawnNames).Draw(t, label+": the edge"),
			rapid.SampledFrom(drawnSides).Draw(t, label+": the side"),
			rapid.SampledFrom(states).Draw(t, label+": the state"),
		))
	}
	r.Configurations = rapid.SliceOfNDistinct(rapid.SampledFrom(drawnConfigs), 1, 2,
		func(c report.Configuration) string { return c.ID }).Draw(t, analyzer+": the matrix")
	r.ConfigurationsNotBuilt = rapid.SliceOfN(rapid.SampledFrom(drawnDropped), 0, 1).Draw(t, analyzer+": the dropped configurations")
	r.Consumers.Unavailable = rapid.SliceOfN(rapid.SampledFrom(drawnAbsent), 0, 1).Draw(t, analyzer+": the unavailable consumers")
	r.TestFileRules = rapid.SliceOfNDistinct(rapid.SampledFrom(drawnRules), 0, 2,
		func(rule report.TestFileRule) string { return rule.Rule }).Draw(t, analyzer+": the test file rules")
	r.Consumers.Loaded = rapid.SliceOfNDistinct(rapid.SampledFrom(drawnConsumers), 0, 2,
		func(c report.LoadedConsumer) string { return c.ID }).Draw(t, analyzer+": the loaded consumers")
	r.ExcludedByCgo = rapid.SliceOfNDistinct(rapid.SampledFrom(drawnCgo), 0, 2,
		func(path string) string { return path }).Draw(t, analyzer+": the cgo files")
	r.Totals.SuppressionsInEffect = rapid.IntRange(0, 3).Draw(t, analyzer+": the suppressions in effect")
	r.Totals.ReasonsRecorded = rapid.IntRange(0, 3).Draw(t, analyzer+": the reasons recorded")
	return r
}

// TestMergePreservesEveryRecordWhateverTheInputOrder is property
// dead-code-suite/P26. It draws report sets whose edge evaluations are all
// live, so no record awaits resolution, and merges each in two orders: the
// merged bytes are the same, and the merged arrays hold every finding, stale
// suppression, declared gap and edge evaluation of every input, each stamped
// with its analyzer, as many times as the inputs hold it.
func TestMergePreservesEveryRecordWhateverTheInputOrder(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		analyzers := rapid.SliceOfNDistinct(rapid.SampledFrom(drawnAnalyzers), 1, len(drawnAnalyzers),
			func(name string) string { return name }).Draw(t, "the analyzers")
		reports := make([]*report.Report, len(analyzers))
		for i, analyzer := range analyzers {
			reports[i] = drawReport(t, analyzer, []report.State{report.StateLive})
		}
		shuffled := rapid.Permutation(reports).Draw(t, "the order the merge reads them in")
		before := encodeRecord(t, reports)

		merged, err := Merge(inputs(reports...), accepted, &caller)
		if err != nil {
			t.Fatalf("Merge(the drawn reports) = %v, want a report", err)
		}
		reordered, err := Merge(inputs(shuffled...), accepted, &caller)
		if err != nil {
			t.Fatalf("Merge(the drawn reports reordered) = %v, want a report", err)
		}
		var first, second bytes.Buffer
		if err := report.Encode(&first, merged); err != nil {
			t.Fatalf("report.Encode(the merged report) = %v", err)
		}
		if err := report.Encode(&second, reordered); err != nil {
			t.Fatalf("report.Encode(the reordered merged report) = %v", err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatalf("Merge(reordered) =\n%s\nwant the bytes of Merge(the drawn order)\n%s", second.Bytes(), first.Bytes())
		}
		if after := encodeRecord(t, reports); after != before {
			t.Fatalf("the inputs after two merges =\n%s\nwant them unchanged\n%s", after, before)
		}

		want := map[string]int{}
		for _, r := range reports {
			for _, f := range r.Findings {
				f.Analyzer = r.Analyzer.Name
				want["finding "+encodeRecord(t, f)]++
			}
			for _, s := range r.StaleSuppressions {
				s.Analyzer = r.Analyzer.Name
				want["stale "+encodeRecord(t, s)]++
			}
			for _, g := range r.DeclaredGaps {
				g.Analyzer = r.Analyzer.Name
				want["gap "+encodeRecord(t, g)]++
			}
			for _, e := range r.EdgeEvaluations {
				e.Analyzer = r.Analyzer.Name
				want["evaluation "+encodeRecord(t, e)]++
			}
		}
		got := map[string]int{}
		for _, f := range merged.Findings {
			got["finding "+encodeRecord(t, f)]++
		}
		for _, s := range merged.StaleSuppressions {
			got["stale "+encodeRecord(t, s)]++
		}
		for _, g := range merged.DeclaredGaps {
			got["gap "+encodeRecord(t, g)]++
		}
		for _, e := range merged.EdgeEvaluations {
			got["evaluation "+encodeRecord(t, e)]++
		}
		if !maps.Equal(got, want) {
			t.Fatalf("Merge() records =\n%v\nwant every input record, stamped, as often as the inputs hold it\n%v",
				slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
		}
	})
}

func encodeRecord(t *rapid.T, record any) string {
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(%+v) = %v", record, err)
	}
	return string(encoded)
}

// drawEdgeReport draws one analyzer's report over the edges of drawnEdges: on
// each edge it evaluates its language's side or not, in any state, a dead
// evaluation carrying a pending finding of one of two components, and a few
// carried findings of those components or a third. Every finding the report
// holds is distinct from every other, so a merged finding names the input
// finding it came from once its component is set aside.
func drawEdgeReport(t *rapid.T, analyzer string) *report.Report {
	language := analyzer[len(analyzer)-2:]
	side, path, store := report.SideProvides, "wire.go", "store.go"
	if language == "ts" {
		side, path, store = report.SideUsedBy, "web/wire.ts", "web/store.ts"
	}
	component := func(label string) string {
		return analyzer + "/c-" + strconv.Itoa(rapid.IntRange(1, 3).Draw(t, label))
	}
	r := analyzerReport(analyzer, language)
	for i, edge := range drawnEdges {
		label := analyzer + " edge " + edge
		if rapid.IntRange(0, 5).Draw(t, label+": evaluated unless 0") == 0 {
			continue
		}
		e := report.EdgeEvaluation{
			Edge: "wire/" + edge, Side: side, Symbol: language + "://example.com/app#" + edge,
			State: rapid.SampledFrom(drawnEdgeStates).Draw(t, label+": the state"),
		}
		if e.State == report.StateDead {
			f := finding(analyzer+"/c-"+strconv.Itoa(rapid.IntRange(1, 2).Draw(t, label+": the component")), path, i+1, edge)
			e.Finding = &f
		}
		r.EdgeEvaluations = append(r.EdgeEvaluations, e)
	}
	for i := range rapid.IntRange(0, 2).Draw(t, analyzer+": the carried findings") {
		r.Findings = append(r.Findings, finding(component(analyzer+" carried "+strconv.Itoa(i)+": the component"), store, i+1, "carried"))
	}
	return r
}

// withoutComponent is a finding as its analyzer reported it, stamped with
// that analyzer and with its component set aside, which is what a merge keeps
// of it.
func withoutComponent(t *rapid.T, f report.Finding, analyzer string) string {
	f.Analyzer = analyzer
	f.Component = report.Component{}
	return encodeRecord(t, f)
}

// TestMergeResolvesEveryPendingFindingByItsPairsState is property
// dead-code-suite/P27. Over report sets whose edge sides one, two or no
// analyzers evaluate in any state, merged in two orders: an unpaired pending
// finding refuses both alike; otherwise the bytes agree, no pending finding
// remains, a promoted finding's other side answers dead, promoted too, and
// never live, a promoted pair shares one component, a component is reported
// whole or not at all, and each edge with a wholly absent side is stale once.
func TestMergeResolvesEveryPendingFindingByItsPairsState(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		analyzers := append([]string{"deadset-go", "deadset-ts"}, rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"example-go", "other-ts"}), 0, 2,
			func(name string) string { return name }).Draw(t, "the second analyzers of a language")...)
		reports := make([]*report.Report, len(analyzers))
		for i, analyzer := range analyzers {
			reports[i] = drawEdgeReport(t, analyzer)
		}
		shuffled := rapid.Permutation(reports).Draw(t, "the order the merge reads them in")
		before := encodeRecord(t, reports)

		type answer struct {
			finding string
			side    report.Side
			state   report.State
		}
		answers := map[string][]answer{}
		unresolvable := false
		for _, r := range reports {
			for _, e := range r.EdgeEvaluations {
				a := answer{side: e.Side, state: e.State}
				if e.Finding != nil {
					a.finding = withoutComponent(t, *e.Finding, r.Analyzer.Name)
				}
				answers[e.Edge] = append(answers[e.Edge], a)
			}
		}
		for _, r := range reports {
			for _, e := range r.EdgeEvaluations {
				paired := slices.ContainsFunc(answers[e.Edge], func(a answer) bool { return a.side != e.Side })
				unresolvable = unresolvable || (e.State == report.StateDead && !paired)
			}
		}

		merged, err := Merge(inputs(reports...), accepted, &caller)
		reordered, reorderedErr := Merge(inputs(shuffled...), accepted, &caller)
		if unresolvable {
			if !errors.Is(err, errUnresolvedEdge) || reorderedErr == nil || reorderedErr.Error() != err.Error() {
				t.Fatalf("Merge(an unresolvable pending finding) = %v, and reordered %v, want one *unresolvedError in both orders", err, reorderedErr)
			}
			return
		}
		if err != nil || reorderedErr != nil {
			t.Fatalf("Merge(the drawn reports) = %v, and reordered %v, want a report", err, reorderedErr)
		}
		var first, second bytes.Buffer
		if err := report.Encode(&first, merged); err != nil {
			t.Fatalf("report.Encode(the merged report) = %v", err)
		}
		if err := report.Encode(&second, reordered); err != nil {
			t.Fatalf("report.Encode(the reordered merged report) = %v", err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatalf("Merge(reordered) =\n%s\nwant the bytes of Merge(the drawn order)\n%s", second.Bytes(), first.Bytes())
		}
		if after := encodeRecord(t, reports); after != before {
			t.Fatalf("the inputs after two merges =\n%s\nwant them unchanged\n%s", after, before)
		}
		for _, e := range merged.EdgeEvaluations {
			if e.State == report.StateDead || e.Finding != nil {
				t.Fatalf("Merge() edge evaluation %+v, want no pending finding in the merged report", e)
			}
		}
		if merged.Totals.Pending != 0 {
			t.Fatalf("Merge() totals pending = %d, want 0", merged.Totals.Pending)
		}

		kept := map[string]string{}
		stale := 0
		for _, f := range merged.Findings {
			if f.Code == staleEdgeCode {
				stale++
				continue
			}
			kept[withoutComponent(t, f, f.Analyzer)] = f.Component.ID
		}
		for edge, held := range answers {
			for _, a := range held {
				component, promoted := kept[a.finding]
				if a.state != report.StateDead || !promoted {
					continue
				}
				pairedDead := false
				for _, other := range held {
					if other.side == a.side {
						continue
					}
					if other.state == report.StateLive {
						t.Fatalf("Merge() promoted %s on %s, whose other side answers live", a.finding, edge)
					}
					if partner, ok := kept[other.finding]; ok && other.state == report.StateDead {
						pairedDead = true
						if partner != component {
							t.Fatalf("Merge() promoted the pair on %s into components %s and %s, want one", edge, component, partner)
						}
					}
				}
				if !pairedDead {
					t.Fatalf("Merge() promoted %s on %s with no promoted dead answer on another side", a.finding, edge)
				}
			}
		}

		members := map[string][]string{}
		for _, r := range reports {
			for _, f := range r.Findings {
				members[r.Analyzer.Name+" "+f.Component.ID] = append(members[r.Analyzer.Name+" "+f.Component.ID], withoutComponent(t, f, r.Analyzer.Name))
			}
			for _, e := range r.EdgeEvaluations {
				if e.Finding != nil {
					members[r.Analyzer.Name+" "+e.Finding.Component.ID] = append(members[r.Analyzer.Name+" "+e.Finding.Component.ID], withoutComponent(t, *e.Finding, r.Analyzer.Name))
				}
			}
		}
		for component, held := range members {
			present := 0
			for _, f := range held {
				if _, ok := kept[f]; ok {
					present++
				}
			}
			if present != 0 && present != len(held) {
				t.Fatalf("Merge() reports %d of the %d findings of component %s, want all or none", present, len(held), component)
			}
		}

		wantStale := 0
		for _, held := range answers {
			for _, side := range drawnSides {
				evaluated := slices.ContainsFunc(held, func(a answer) bool { return a.side == side })
				if evaluated && !slices.ContainsFunc(held, func(a answer) bool { return a.side == side && a.state != report.StateAbsent }) {
					wantStale++
					break
				}
			}
		}
		if stale != wantStale {
			t.Fatalf("Merge() emitted %d stale-edge findings, want one per edge with a wholly absent side, %d", stale, wantStale)
		}
	})
}
