package merge

import (
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// envelope is the merged report: the ordered working set inside the envelope
// members derived from the inputs, in merged_from's order, with the caller's
// facts and each input's digest. Admission has passed, so every input names
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
	return &report.Report{
		SchemaVersion:   caller.SchemaVersion,
		ContractVersion: caller.ContractVersion,
		Analyzer: report.Analyzer{
			Name:                   caller.Name,
			Version:                caller.Version,
			Languages:              distinct(held.languages, compareStrings),
			SchemaVersionsAccepted: slices.Clone(accepted),
			Conformance:            caller.Conformance,
		},
		MergedFrom:             held.mergedFrom,
		Target:                 ordered[0].Report.Target,
		Configurations:         firstPerID(held.configurations, func(c *report.Configuration) string { return c.ID }),
		ConfigurationsNotBuilt: firstPerID(held.notBuilt, func(c *report.ConfigurationNotBuilt) string { return c.ID }),
		Consumers:              consumers,
		Findings:               carried.findings,
		EdgeEvaluations:        carried.evaluations,
		StaleSuppressions:      carried.stale,
		DeclaredGaps:           carried.gaps,
		ExcludedByCgo:          distinct(held.excludedByCgo, compareStrings),
		TestFileRules: distinct(held.testFileRules,
			func(a, b *report.TestFileRule) int { return strings.Compare(a.Rule, b.Rule) }),
		Totals: totals(carried, &held),
	}
}

// gathered is every envelope member of every input that the merged envelope
// unions, in merged_from's order, and the counts it sums.
type gathered struct {
	languages      []string
	mergedFrom     []report.InputReport
	configurations []report.Configuration
	notBuilt       []report.ConfigurationNotBuilt
	loaded         []report.LoadedConsumer
	unavailable    []report.UnavailableConsumer
	excludedByCgo  []string
	testFileRules  []report.TestFileRule

	inEffect int
	reasons  int
}

func (g *gathered) add(in *Input) {
	r := in.Report
	g.languages = append(g.languages, r.Analyzer.Languages...)
	g.mergedFrom = append(g.mergedFrom, report.InputReport{
		Name:    r.Analyzer.Name,
		Version: r.Analyzer.Version,
		Digest:  in.Digest,
	})
	g.configurations = append(g.configurations, r.Configurations...)
	g.notBuilt = append(g.notBuilt, r.ConfigurationsNotBuilt...)
	g.loaded = append(g.loaded, r.Consumers.Loaded...)
	g.unavailable = append(g.unavailable, r.Consumers.Unavailable...)
	g.excludedByCgo = append(g.excludedByCgo, r.ExcludedByCgo...)
	g.testFileRules = append(g.testFileRules, r.TestFileRules...)
	g.inEffect += r.Totals.SuppressionsInEffect
	g.reasons += r.Totals.ReasonsRecorded
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

// totals are the merged report's counts, recomputed over the merged arrays
// except the suppression counts, which no merged array holds and which sum
// over the inputs. No input omitted a finding and resolution left no pending
// one, so the merged report omits none and holds none pending.
func totals(carried *records, held *gathered) report.Totals {
	return report.Totals{
		Findings:             len(carried.findings),
		BySeverity:           severities(carried.findings),
		DeletableLines:       deletableLines(carried.findings),
		SuppressionsInEffect: held.inEffect,
		ReasonsRecorded:      held.reasons,
		StaleSuppressions:    len(carried.stale),
	}
}

func severities(findings []report.Finding) report.BySeverity {
	var counted report.BySeverity
	for i := range findings {
		switch findings[i].Severity {
		case report.SeverityAllow:
			counted.Allow++
		case report.SeverityWarn:
			counted.Warn++
		case report.SeverityDeny:
			counted.Deny++
		}
	}
	return counted
}

// deletableLines sums the deletable lines of every component a root finding
// names, counting a component two root findings name once. The component a
// root finding names first in canonical order supplies the count.
func deletableLines(findings []report.Finding) int {
	counted := map[string]bool{}
	lines := 0
	for i := range findings {
		component := &findings[i].Component
		if !component.Root || counted[component.ID] {
			continue
		}
		counted[component.ID] = true
		lines += component.DeletableLines
	}
	return lines
}

func compareStrings(a, b *string) int { return strings.Compare(*a, *b) }
