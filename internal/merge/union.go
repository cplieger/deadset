package merge

import "github.com/cplieger/deadset/internal/report"

// records is the merge's working set: every record of every input, each
// carrying the name of the analyzer whose report held it, with nothing
// deduplicated.
type records struct {
	findings []report.Finding
	stale    []report.StaleSuppression
	gaps     []report.DeclaredGap

	// evaluations is every edge evaluation of every input, a dead one with
	// the pending finding it carries, until resolution leaves the live and
	// absent ones the merged report holds. A carried evaluation's finding is
	// the input's own, so nothing writes through it.
	evaluations []report.EdgeEvaluation
}

// union carries every finding, stale suppression, declared gap and edge
// evaluation of every input into one working set, stamping each record with
// its input's analyzer name. The records are copies, so the inputs stay as
// they were.
func union(inputs []Input) *records {
	carried := &records{
		findings:    []report.Finding{},
		stale:       []report.StaleSuppression{},
		gaps:        []report.DeclaredGap{},
		evaluations: []report.EdgeEvaluation{},
	}
	for i := range inputs {
		r := inputs[i].Report
		name := r.Analyzer.Name
		carried.findings = carry(carried.findings, r.Findings, func(f *report.Finding) { f.Analyzer = name })
		carried.stale = carry(carried.stale, r.StaleSuppressions, func(s *report.StaleSuppression) { s.Analyzer = name })
		carried.gaps = carry(carried.gaps, r.DeclaredGaps, func(g *report.DeclaredGap) { g.Analyzer = name })
		carried.evaluations = carry(carried.evaluations, r.EdgeEvaluations, func(e *report.EdgeEvaluation) { e.Analyzer = name })
	}
	return carried
}

// carry appends a copy of every record of from to into, stamping each copy.
func carry[T any](into, from []T, stamp func(*T)) []T {
	start := len(into)
	into = append(into, from...)
	for i := range into[start:] {
		stamp(&into[start+i])
	}
	return into
}
