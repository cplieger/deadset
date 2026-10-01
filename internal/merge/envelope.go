package merge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// envelope is the merged report: the ordered working set inside the envelope
// members derived from the inputs, with self and each input's digest as the
// caller's facts. Admission has passed, so every input names one target.
func envelope(inputs []Input, accepted []string, self *report.Analyzer, carried *records) *report.Report {
	var held gathered
	for i := range inputs {
		held.add(&inputs[i])
	}
	analyzer := *self
	analyzer.Languages = distinct(held.languages, compareStrings)
	analyzer.SchemaVersionsAccepted = slices.Clone(accepted)
	sortTotal(held.mergedFrom, compareInputReports)
	consumers := report.Consumers{
		Loaded:      distinct(held.loaded, func(a, b *report.LoadedConsumer) int { return strings.Compare(a.ID, b.ID) }),
		Unavailable: distinct(held.unavailable, func(a, b *report.UnavailableConsumer) int { return strings.Compare(a.ID, b.ID) }),
	}
	consumers.Declared = len(consumers.Loaded) + len(consumers.Unavailable)
	return &report.Report{
		SchemaVersion:   report.SchemaVersion,
		ContractVersion: report.ContractVersion,
		Analyzer:        analyzer,
		MergedFrom:      held.mergedFrom,
		Target:          inputs[0].Report.Target,
		Configurations: distinct(held.configurations,
			func(a, b *report.Configuration) int { return strings.Compare(a.ID, b.ID) }),
		ConfigurationsNotBuilt: distinct(held.notBuilt,
			func(a, b *report.ConfigurationNotBuilt) int { return strings.Compare(a.ID, b.ID) }),
		Consumers:         consumers,
		Findings:          carried.findings,
		EdgeEvaluations:   carried.evaluations,
		StaleSuppressions: carried.stale,
		DeclaredGaps:      carried.gaps,
		ExcludedByCgo:     distinct(held.excludedByCgo, compareStrings),
		TestFileRules: distinct(held.testFileRules,
			func(a, b *report.TestFileRule) int { return strings.Compare(a.Rule, b.Rule) }),
		Totals: totals(carried, &held),
	}
}

// gathered is every envelope member of every input that the merged envelope
// unions, and the counts it sums.
type gathered struct {
	languages      []string
	mergedFrom     []report.InputReport
	configurations []report.Configuration
	notBuilt       []report.ConfigurationNotBuilt
	loaded         []report.LoadedConsumer
	unavailable    []report.UnavailableConsumer
	excludedByCgo  []string
	testFileRules  []report.TestFileRule

	inEffect          int
	reasons           int
	omitted           int
	omittedBySeverity report.BySeverity
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
	if r.Totals.Omitted > 0 {
		g.omitted += r.Totals.Omitted
		listed := severities(r.Findings)
		g.omittedBySeverity.Allow += max(r.Totals.BySeverity.Allow-listed.Allow, 0)
		g.omittedBySeverity.Warn += max(r.Totals.BySeverity.Warn-listed.Warn, 0)
		g.omittedBySeverity.Deny += max(r.Totals.BySeverity.Deny-listed.Deny, 0)
	}
}

// totals are the merged report's counts, recomputed over the merged arrays
// except the suppression counts and the findings an input's cap omitted, which
// no merged array holds and which sum over the inputs. An omitted finding keeps
// its severity, so a capped input cannot hide a failing finding from the
// verdict.
func totals(carried *records, held *gathered) report.Totals {
	counted := report.Totals{
		Findings:             len(carried.findings) + held.omitted,
		BySeverity:           severities(carried.findings),
		DeletableLines:       deletableLines(carried.findings),
		SuppressionsInEffect: held.inEffect,
		ReasonsRecorded:      held.reasons,
		StaleSuppressions:    len(carried.stale),
		Omitted:              held.omitted,
	}
	counted.BySeverity.Allow += held.omittedBySeverity.Allow
	counted.BySeverity.Warn += held.omittedBySeverity.Warn
	counted.BySeverity.Deny += held.omittedBySeverity.Deny
	for i := range carried.evaluations {
		if carried.evaluations[i].State == report.StateDead {
			counted.Pending++
		}
	}
	return counted
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

// deletableLines sums the deletable lines of every component a finding roots,
// counting a component two findings root once. The component a finding names
// first in canonical order supplies the count.
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

func compareInputReports(a, b *report.InputReport) int {
	return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Version, b.Version))
}
