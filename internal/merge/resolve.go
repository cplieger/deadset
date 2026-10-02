package merge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// ErrUnresolvedEdge is a pending finding whose edge no input report evaluates
// on any other side, so nothing in the merge can resolve it.
var ErrUnresolvedEdge = errors.New("a pending finding's edge has no evaluation of its other side")

// UnresolvedError is the first pending finding, in canonical order of the
// findings, whose edge no input report evaluates on another side. It wraps
// [ErrUnresolvedEdge].
type UnresolvedError struct {
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
func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("merge: %s holds a pending finding on the %s side of edge %s (%s), and no report of %s evaluates another side of that edge",
		e.Analyzer, e.Side, e.Edge, e.Symbol, strings.Join(e.Searched, ", "))
}

// Unwrap is [ErrUnresolvedEdge].
func (e *UnresolvedError) Unwrap() error { return ErrUnresolvedEdge }

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
// whose edge no other side evaluates ends the merge with an
// [*UnresolvedError]; the components rules 2 and 4 drop, run to a fixpoint,
// leave the findings; every other pending finding is promoted and its
// component joins the components paired with it; one stale-edge finding is
// emitted per edge with a wholly absent side. The working set then holds no
// dead evaluation. merger is the merging product's name, which prefixes the
// component of every finding resolve emits.
func (r *records) resolve(inputs []Input, merger string) error {
	sortTotal(r.evaluations, compareEvaluations)
	index := indexEvaluations(r.evaluations)
	dead := deadEvaluations(r.evaluations)
	for _, e := range dead {
		if _, evaluated := index.strongestOther(e, nil); !evaluated {
			return unresolved(e, inputs)
		}
	}
	dropped := dropComponents(dead, index)

	kept := r.findings[:0:0]
	for i := range r.findings {
		if !dropped[componentOfFinding(&r.findings[i])] {
			kept = append(kept, r.findings[i])
		}
	}
	var promoted []promotion
	for _, e := range dead {
		if dropped[componentOfEvaluation(e)] {
			continue
		}
		finding := *e.Finding
		finding.Analyzer = e.Analyzer
		promoted = append(promoted, promotion{finding: finding, edge: e.Edge, side: e.Side})
		kept = append(kept, finding)
	}
	unite(kept, promoted)
	kept = append(kept, staleEdges(index, merger)...)
	r.findings = kept

	live := r.evaluations[:0:0]
	for i := range r.evaluations {
		if r.evaluations[i].State != report.StateDead {
			live = append(live, r.evaluations[i])
		}
	}
	r.evaluations = live
	return nil
}

func unresolved(e *report.EdgeEvaluation, inputs []Input) *UnresolvedError {
	searched := make([]string, len(inputs))
	for i := range inputs {
		searched[i] = inputs[i].Report.Analyzer.Name
	}
	return &UnresolvedError{Edge: e.Edge, Side: e.Side, Symbol: e.Symbol, Analyzer: e.Analyzer, Searched: searched}
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
	sortTotal(dead, func(a, b **report.EdgeEvaluation) int {
		first, second := *(*a).Finding, *(*b).Finding
		first.Analyzer, second.Analyzer = (*a).Analyzer, (*b).Analyzer
		return compareFindings(&first, &second)
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

// promotion is one pending finding resolution promoted, stamped with its
// analyzer, with the edge side whose evaluation carried it.
type promotion struct {
	edge    string
	side    report.Side
	finding report.Finding
}

// unite joins the component of each promoted finding with the component of
// every promoted finding on another side of its edge, visiting the promotions
// in canonical order. A joined component takes the identifier of the
// component the visit reached first and the sums of the joined components'
// symbol counts and deletable lines, and every finding of a joined component
// is rewritten to it; each keeps its own root flag.
func unite(findings []report.Finding, promoted []promotion) {
	var u unionFind
	for i := range promoted {
		p := &promoted[i]
		u.reach(&p.finding)
		for j := range promoted {
			q := &promoted[j]
			if q.edge == p.edge && q.side != p.side {
				u.reach(&q.finding)
				u.join(componentOfFinding(&p.finding), componentOfFinding(&q.finding))
			}
		}
	}
	joined := u.components()
	for i := range findings {
		if c, ok := joined[componentOfFinding(&findings[i])]; ok {
			findings[i].Component.ID = c.ID
			findings[i].Component.SymbolCount = c.SymbolCount
			findings[i].Component.DeletableLines = c.DeletableLines
		}
	}
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
