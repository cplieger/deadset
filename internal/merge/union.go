package merge

import (
	"slices"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// records is the merge's working set: every record of every input, each
// carrying the name of the analyzer whose report held it, with nothing
// deduplicated.
type records struct {
	// renamed is the identifier each component the union joined now carries.
	renamed map[componentKey]string

	findings []report.Finding
	stale    []report.StaleSuppression
	gaps     []report.DeclaredGap

	// evaluations is every edge evaluation of every input, a dead one with
	// the pending finding it carries, until resolution leaves the live and
	// absent ones the merged report holds. A carried evaluation's finding is
	// the input's own, so nothing writes through it.
	evaluations []report.EdgeEvaluation

	// listings is every component listing of every input, under the component
	// it names in that input, and of every stale-edge component the merge
	// mints; lists records whether any input lists components.
	listings []listing
	lists    bool
}

// union carries every finding, stale suppression, declared gap and edge
// evaluation of every input into one working set, stamping each record with
// its input's analyzer name, but no unmatched-root finding for a configured
// string an analyzer matched. The records are copies, so the inputs stay as
// they were.
func union(inputs []Input) *records {
	matched := matchedRoots(inputs)
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
		carried.lists = carried.lists || r.Components != nil
		for j := range r.Components {
			carried.listings = append(carried.listings, listing{
				component: componentKey{analyzer: name, id: r.Components[j].ID},
				members:   r.Components[j].Members,
			})
		}
	}
	carried.findings = slices.DeleteFunc(carried.findings, func(f report.Finding) bool {
		return f.Code == vocab.UnmatchedRoot && matched[f.Symbol.Ref]
	})
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

// matchedRoots is every configured string an input reports unmatched that an
// input reading the string does not: an analyzer matched it.
func matchedRoots(inputs []Input) map[string]bool {
	unmatched := make([]map[string]bool, len(inputs))
	for i := range inputs {
		unmatched[i] = unmatchedRoots(inputs[i].Report)
	}
	matched := map[string]bool{}
	for i := range unmatched {
		for root := range unmatched[i] {
			for k := range inputs {
				if vocab.ReadsRoot(inputs[k].Report.Analyzer.Languages, root) && !unmatched[k][root] {
					matched[root] = true
				}
			}
		}
	}
	return matched
}

// unmatchedRoots is every configured string r reports unmatched.
func unmatchedRoots(r *report.Report) map[string]bool {
	unmatched := map[string]bool{}
	for i := range r.Findings {
		if r.Findings[i].Code == vocab.UnmatchedRoot {
			unmatched[r.Findings[i].Symbol.Ref] = true
		}
	}
	return unmatched
}
