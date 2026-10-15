package merge

import (
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// listing is one input's members of one component.
type listing struct {
	component componentKey
	members   []report.Positioned
}

// components is the merged report's listing, nil where no input lists
// components: one entry per component the ordered findings name, in the order
// of the first finding naming it, holding every member the inputs list for it
// and for each component the union joined into it, once each, ordered by
// path, line, column and reference, and a stale edge's subject alone.
func (r *records) components(findings []report.Finding) []report.ComponentListing {
	if !r.lists {
		return nil
	}
	members := map[string][]report.Positioned{}
	for i := range r.listings {
		id := r.listings[i].component.id
		if renamed, ok := r.renamed[r.listings[i].component]; ok {
			id = renamed
		}
		members[id] = append(members[id], r.listings[i].members...)
	}
	listed := []report.ComponentListing{}
	seen := map[string]bool{}
	for i := range findings {
		f := &findings[i]
		id := f.Component.ID
		held, ok := members[id]
		if !ok && f.Code == vocab.StaleCrossLanguageEdge {
			// The merger mints a stale edge's one-member component, which no input lists.
			held, ok = []report.Positioned{{Ref: f.Symbol.Ref, Name: f.Symbol.Name, Position: f.Position}}, true
		}
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		listed = append(listed, report.ComponentListing{ID: id, Members: report.Distinct(held, report.ComparePositioned)})
	}
	return listed
}
