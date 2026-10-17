package merge

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
)

// errUnresolvedEdge is a pending finding whose edge no input report evaluates
// on any other side, so nothing in the merge can resolve it.
var errUnresolvedEdge = errors.New("a pending finding's edge has no evaluation of its other side")

// unresolvedError is the first pending finding, in canonical order of the
// findings, whose edge no input report evaluates on another side. It wraps
// [errUnresolvedEdge].
type unresolvedError struct {
	Edge   string
	Side   report.Side
	Symbol string

	// Analyzer names the report that holds the pending finding.
	Analyzer string

	// Searched is the analyzer name of every input report, in merged_from's
	// order.
	Searched []string
}

// Error names the edge, the pending side and its symbol, the report holding
// the pending finding, and every report the merge searched.
func (e *unresolvedError) Error() string {
	return fmt.Sprintf("merge: %s holds a pending finding on the %s side of edge %s (%s), and no report of %s evaluates another side of that edge",
		e.Analyzer, e.Side, e.Edge, e.Symbol, strings.Join(e.Searched, ", "))
}

// Unwrap is [errUnresolvedEdge].
func (*unresolvedError) Unwrap() error { return errUnresolvedEdge }

// componentKey names a component within the input report that carried it: the
// analyzer of that report and the identifier the analyzer minted.
type componentKey struct {
	analyzer string
	id       string
}

func componentOfFinding(f *report.Finding) componentKey {
	return componentKey{analyzer: f.Analyzer, id: f.Component.ID}
}

func componentOfEvaluation(e *report.EdgeEvaluation) componentKey {
	return componentKey{analyzer: e.Analyzer, id: e.Finding.Component.ID}
}

// edgeIndex is every evaluation of the working set by edge and side, each list
// in the evaluations' canonical order.
type edgeIndex map[string]map[report.Side][]*report.EdgeEvaluation

func indexEvaluations(evaluations []report.EdgeEvaluation) edgeIndex {
	index := edgeIndex{}
	for i := range evaluations {
		e := &evaluations[i]
		if index[e.Edge] == nil {
			index[e.Edge] = map[report.Side][]*report.EdgeEvaluation{}
		}
		index[e.Edge][e.Side] = append(index[e.Edge][e.Side], e)
	}
	return index
}

// strength orders the states the way resolution reads them: one live answer
// outranks a dead one, and a dead one outranks an absent one.
var strength = map[report.State]int{report.StateLive: 0, report.StateDead: 1, report.StateAbsent: 2}

// strongestOther is the strongest state the evaluations on the other sides of
// e's edge read as, a dead evaluation of a dropped component reading as live.
// It reports false where no other side holds an evaluation.
func (index edgeIndex) strongestOther(e *report.EdgeEvaluation, dropped map[componentKey]bool) (report.State, bool) {
	var strongest report.State
	found := false
	for side, held := range index[e.Edge] {
		if side == e.Side {
			continue
		}
		for _, other := range held {
			state := other.State
			if state == report.StateDead && dropped[componentOfEvaluation(other)] {
				state = report.StateLive
			}
			if !found || strength[state] < strength[strongest] {
				strongest, found = state, true
			}
		}
	}
	return strongest, found
}

// resolve decides every dead evaluation of the working set: a pending finding
// whose edge no other side evaluates ends the merge with the run failure
// unresolved-edge, wrapping an [*unresolvedError]; the components rules 2 and
// 4 drop, run to a fixpoint, leave the findings; every other pending finding
// is promoted and united; one stale-edge finding is emitted per edge with a
// wholly absent side. The working set then holds no dead evaluation. merger
// is the merging product's name, which prefixes the component of every
// finding resolve emits.
func (r *records) resolve(inputs []Input, merger string) error {
	report.SortTotal(r.evaluations, report.CompareEvaluations)
	index := indexEvaluations(r.evaluations)
	dead := deadEvaluations(r.evaluations)
	for _, e := range dead {
		if _, evaluated := index.strongestOther(e, nil); !evaluated {
			return failure.Wrap(vocab.RunUnresolvedEdge, unresolved(e, inputs))
		}
	}
	dropped := dropComponents(dead, index)

	kept := r.findings[:0:0]
	for i := range r.findings {
		if !dropped[componentOfFinding(&r.findings[i])] {
			kept = append(kept, r.findings[i])
		}
	}
	promoted := promote(dead, dropped)
	for i := range promoted {
		kept = append(kept, promoted[i].finding)
	}
	r.renamed = unite(kept, promoted)
	emitted, listings := staleEdges(index, merger)
	kept = append(kept, emitted...)
	r.findings = kept
	r.listings = append(r.listings, listings...)

	live := r.evaluations[:0:0]
	for i := range r.evaluations {
		if r.evaluations[i].State != report.StateDead {
			live = append(live, r.evaluations[i])
		}
	}
	r.evaluations = live
	return nil
}

func unresolved(e *report.EdgeEvaluation, inputs []Input) *unresolvedError {
	searched := make([]string, len(inputs))
	for i := range inputs {
		searched[i] = inputs[i].Report.Analyzer.Name
	}
	return &unresolvedError{Edge: e.Edge, Side: e.Side, Symbol: e.Symbol, Analyzer: e.Analyzer, Searched: searched}
}

// deadEvaluations is every dead evaluation, in canonical order of the finding
// each carries under its analyzer, two equal on the key by their encodings.
func deadEvaluations(evaluations []report.EdgeEvaluation) []*report.EdgeEvaluation {
	var dead []*report.EdgeEvaluation
	for i := range evaluations {
		if evaluations[i].State == report.StateDead {
			dead = append(dead, &evaluations[i])
		}
	}
	report.SortTotal(dead, func(a, b **report.EdgeEvaluation) int {
		first, second := *(*a).Finding, *(*b).Finding
		first.Analyzer, second.Analyzer = (*a).Analyzer, (*b).Analyzer
		return report.CompareFindings(&first, &second)
	})
	return dead
}

// dropComponents runs rules 2 and 4 to a fixpoint: the component of a dead
// evaluation whose other sides read as live or as wholly absent drops, and
// from then on every dead evaluation of that component reads as live on its
// own edge, until a pass drops nothing new.
func dropComponents(dead []*report.EdgeEvaluation, index edgeIndex) map[componentKey]bool {
	dropped := map[componentKey]bool{}
	for grew := true; grew; {
		grew = false
		for _, e := range dead {
			component := componentOfEvaluation(e)
			if dropped[component] {
				continue
			}
			if state, _ := index.strongestOther(e, dropped); state != report.StateDead {
				dropped[component] = true
				grew = true
			}
		}
	}
	return dropped
}

// promote is the finding of every dead evaluation whose component is not
// dropped, once per finding, in canonical order, with every edge side whose
// evaluation carried it.
func promote(dead []*report.EdgeEvaluation, dropped map[componentKey]bool) []promotion {
	var promoted []promotion
	for _, e := range dead {
		if dropped[componentOfEvaluation(e)] {
			continue
		}
		finding := *e.Finding
		finding.Analyzer = e.Analyzer
		at := carrier{edge: e.Edge, side: e.Side}
		// dead is in canonical order, so the evaluations carrying one finding are adjacent.
		if n := len(promoted); n > 0 && report.CompareFindings(&promoted[n-1].finding, &finding) == 0 {
			promoted[n-1].carriers = append(promoted[n-1].carriers, at)
			continue
		}
		promoted = append(promoted, promotion{finding: finding, carriers: []carrier{at}})
	}
	return promoted
}

// promotion is one pending finding resolution promoted, stamped with its
// analyzer, with every edge side whose evaluation carried it.
type promotion struct {
	finding  report.Finding
	carriers []carrier
}

type carrier struct {
	edge string
	side report.Side
}

// pairs reports whether p and q were carried on two sides of one edge.
func (p *promotion) pairs(q *promotion) bool {
	for _, a := range p.carriers {
		for _, b := range q.carriers {
			if a.edge == b.edge && a.side != b.side {
				return true
			}
		}
	}
	return false
}

// unite joins the component of each promoted finding with that of every
// promoted finding on another side of one of its edges, visiting the
// promotions in canonical order. A joined component takes the identifier the
// visit reached first and the summed symbol counts and deletable lines; every
// finding of it is rewritten to it, keeps its root flag, and is capped at
// [lowestRoots] (confidence.29). It returns the identifier each reached
// component now carries.
func unite(findings []report.Finding, promoted []promotion) map[componentKey]string {
	var u unionFind
	for i := range promoted {
		p := &promoted[i]
		u.reach(&p.finding)
		for j := range promoted {
			if q := &promoted[j]; p.pairs(q) {
				u.reach(&q.finding)
				u.join(componentOfFinding(&p.finding), componentOfFinding(&q.finding))
			}
		}
	}
	joined := u.components()
	capped := lowestRoots(findings, joined)
	for i := range findings {
		f := &findings[i]
		c, ok := joined[componentOfFinding(f)]
		if !ok {
			continue
		}
		if lowest, held := capped[c.ID]; held && less(lowest, f.Confidence) {
			f.Confidence = lowest
		}
		f.Component.ID = c.ID
		f.Component.SymbolCount = c.SymbolCount
		f.Component.DeletableLines = c.DeletableLines
	}
	renamed := make(map[componentKey]string, len(joined))
	for key, c := range joined {
		renamed[key] = c.ID
	}
	return renamed
}

// lowestRoots is, per joined identifier, the lowest confidence among the
// joined component's root findings that are not test-only.
func lowestRoots(findings []report.Finding, joined map[componentKey]report.Component) map[string]vocab.Class {
	lowest := map[string]vocab.Class{}
	for i := range findings {
		f := &findings[i]
		c, ok := joined[componentOfFinding(f)]
		if !ok || !f.Component.Root || f.TestOnly {
			continue
		}
		if held, seen := lowest[c.ID]; !seen || less(f.Confidence, held) {
			lowest[c.ID] = f.Confidence
		}
	}
	return lowest
}

// less reports whether a is a less certain class than b.
func less(a, b vocab.Class) bool {
	classes := vocab.Classes()
	return slices.Index(classes, a) > slices.Index(classes, b)
}

// unionFind is the components the union visit reached, in the order it
// reached them, each with the counts the first finding reaching it carried.
type unionFind struct {
	parent  map[componentKey]componentKey
	reached map[componentKey]int
	counts  map[componentKey]report.Component
}

func (u *unionFind) reach(f *report.Finding) {
	key := componentOfFinding(f)
	if _, ok := u.reached[key]; ok {
		return
	}
	if u.parent == nil {
		u.parent, u.reached, u.counts = map[componentKey]componentKey{}, map[componentKey]int{}, map[componentKey]report.Component{}
	}
	u.parent[key] = key
	u.reached[key] = len(u.reached)
	u.counts[key] = f.Component
}

func (u *unionFind) find(key componentKey) componentKey {
	for u.parent[key] != key {
		key = u.parent[key]
	}
	return key
}

// join merges the sets of a and b under whichever root the visit reached
// first.
func (u *unionFind) join(a, b componentKey) {
	first, second := u.find(a), u.find(b)
	if first == second {
		return
	}
	if u.reached[second] < u.reached[first] {
		first, second = second, first
	}
	u.parent[second] = first
}

// components is the joined component every reached component belongs to: the
// root's identifier and the counts summed over the set.
func (u *unionFind) components() map[componentKey]report.Component {
	sums := map[componentKey]report.Component{}
	for key, counts := range u.counts {
		root := u.find(key)
		sum := sums[root]
		sum.ID = root.id
		sum.SymbolCount += counts.SymbolCount
		sum.DeletableLines += counts.DeletableLines
		sums[root] = sum
	}
	joined := make(map[componentKey]report.Component, len(u.counts))
	for key := range u.counts {
		joined[key] = sums[u.find(key)]
	}
	return joined
}
