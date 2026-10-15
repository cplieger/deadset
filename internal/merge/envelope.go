package merge

import (
	"maps"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// envelope is the merged report: the working set inside the envelope members
// derived from the inputs, in merged_from's order, with the caller's facts and
// each input's digest, in canonical order, its counts recomputed over the
// merged arrays but those of records no merged array holds, which sum over
// the inputs. No input omitted a finding and resolution left none pending. Admission has passed, so every input names
// one target, one analyzer name, and one entry under each id across each pair
// of id-keyed arrays.
func envelope(ordered []Input, accepted []string, caller *Caller, carried *records) *report.Report {
	var held gathered
	for i := range ordered {
		held.add(&ordered[i])
	}
	consumers := report.Consumers{
		Loaded:      firstPerID(held.loaded, func(c *report.LoadedConsumer) string { return c.ID }),
		Unavailable: firstPerID(held.unavailable, func(c *report.UnavailableConsumer) string { return c.ID }),
	}
	consumers.Declared = len(consumers.Loaded) + len(consumers.Unavailable)
	merged := &report.Report{
		SchemaVersion:   caller.SchemaVersion,
		ContractVersion: caller.ContractVersion,
		Analyzer: report.Analyzer{
			Name:                   caller.Name,
			Version:                caller.Version,
			Languages:              held.languages,
			SchemaVersionsAccepted: slices.Clone(accepted),
			Conformance:            caller.Conformance,
		},
		MergedFrom:             held.mergedFrom,
		Target:                 mergedTarget(ordered),
		Configurations:         firstPerID(held.configurations, func(c *report.Configuration) string { return c.ID }),
		ConfigurationsNotBuilt: firstPerID(held.notBuilt, func(c *report.ConfigurationNotBuilt) string { return c.ID }),
		Consumers:              consumers,
		Findings:               carried.findings,
		EdgeEvaluations:        carried.evaluations,
		StaleSuppressions:      carried.stale,
		DeclaredGaps:           carried.gaps,
		ExcludedByCgo:          held.excludedByCgo,
		TestFileRules:          held.testFileRules,
		TypeErrorSkips:         held.typeErrorSkips,
		Notes:                  held.notes,
		UnansweredQuestions:    held.unanswered,
		ConventionsApplied:     held.conventions,
	}
	report.Order(merged)
	merged.Components = carried.components(merged.Findings)
	report.Recount(merged)
	merged.Totals.SuppressionsInEffect = held.inEffect
	merged.Totals.ReasonsRecorded = held.reasons
	merged.Totals.Withheld = held.withheld
	return merged
}

// mergedTarget is the root and the identity every input names, and the kind
// the inputs give every language they cover: that kind where it is one kind
// for all of them, and mixed with each language's kind otherwise. Admission
// refused two inputs giving one language two kinds.
func mergedTarget(ordered []Input) report.Target {
	claimed := map[vocab.Language]report.TargetKind{}
	for i := range ordered {
		r := ordered[i].Report
		for _, language := range r.Analyzer.Languages {
			claimed[language] = r.Target.KindOf(language)
		}
	}
	first := ordered[0].Report.Target
	target := report.Target{Root: first.Root, Identity: first.Identity}
	if kinds := slices.Compact(slices.Sorted(maps.Values(claimed))); len(kinds) == 1 {
		target.Kind = kinds[0]
		return target
	}
	target.Kind = report.TargetKindMixed
	target.LanguageKinds = claimed
	return target
}

// gathered is every envelope member of every input that the merged envelope
// unions, in merged_from's order, and the counts it sums.
type gathered struct {
	languages      []vocab.Language
	mergedFrom     []report.InputReport
	configurations []report.Configuration
	notBuilt       []report.ConfigurationNotBuilt
	loaded         []report.LoadedConsumer
	unavailable    []report.UnavailableConsumer
	excludedByCgo  []string
	testFileRules  []report.TestFileRule
	typeErrorSkips []report.TypeErrorSkip
	notes          []report.Note
	unanswered     []report.UnansweredQuestion
	conventions    []report.ConventionApplied

	inEffect int
	reasons  int
	withheld report.Withheld
}

func (g *gathered) add(in *Input) {
	r := in.Report
	g.languages = append(g.languages, r.Analyzer.Languages...)
	g.mergedFrom = append(g.mergedFrom, report.InputReport{
		Name:      r.Analyzer.Name,
		Version:   r.Analyzer.Version,
		Digest:    in.Digest,
		Languages: slices.Clone(r.Analyzer.Languages),
		Totals:    r.Totals,
	})
	g.configurations = append(g.configurations, r.Configurations...)
	g.notBuilt = append(g.notBuilt, r.ConfigurationsNotBuilt...)
	g.loaded = append(g.loaded, r.Consumers.Loaded...)
	g.unavailable = append(g.unavailable, r.Consumers.Unavailable...)
	g.excludedByCgo = append(g.excludedByCgo, r.ExcludedByCgo...)
	g.testFileRules = append(g.testFileRules, r.TestFileRules...)
	g.typeErrorSkips = append(g.typeErrorSkips, r.TypeErrorSkips...)
	g.notes = append(g.notes, r.Notes...)
	g.unanswered = append(g.unanswered, r.UnansweredQuestions...)
	g.conventions = append(g.conventions, r.ConventionsApplied...)
	g.inEffect += r.Totals.SuppressionsInEffect
	g.reasons += r.Totals.ReasonsRecorded
	g.withheld.Probable += r.Totals.Withheld.Probable
	g.withheld.Possible += r.Totals.Withheld.Possible
}

// firstPerID is one entry per id, ordered by id: of the entries under one id,
// the one entries holds first. entries is in merged_from's order, so an entry
// two reports word differently is the first report's.
func firstPerID[T any](entries []T, id func(*T) string) []T {
	kept := make([]T, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for i := range entries {
		if key := id(&entries[i]); !seen[key] {
			seen[key] = true
			kept = append(kept, entries[i])
		}
	}
	slices.SortStableFunc(kept, func(a, b T) int { return strings.Compare(id(&a), id(&b)) })
	return kept
}
