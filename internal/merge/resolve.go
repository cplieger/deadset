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
// unresolved-edge, wrapping an [*unresolvedError]; the components the one
// fixpoint drops leave the findings; every other pending finding is promoted
// and united; one stale-edge finding is emitted per edge with a wholly absent
// side. The caller's name prefixes the component of every finding resolve
// emits, and its minimum confidence withholds a schema finding below it.
func (r *records) resolve(inputs []Input, caller *Caller) error {
	report.SortTotal(r.evaluations, report.CompareEvaluations)
	index := indexEvaluations(r.evaluations)
	dead := deadEvaluations(r.evaluations)
	for _, e := range dead {
		if _, evaluated := index.strongestOther(e, nil); !evaluated {
			return failure.Wrap(vocab.RunUnresolvedEdge, unresolved(e, inputs))
		}
	}
	run := r.decide(dead, index, caller.MinConfidence)
	r.withheld = r.withheldBelow(dead, index, caller.MinConfidence, run)
	for i := range run.promoted {
		if len(run.promoted[i].records) > 0 {
			f := &run.promoted[i].finding
			r.listings = append(r.listings, listing{component: componentOfFinding(f), members: []report.Positioned{{Ref: f.Symbol.Ref, Name: f.Symbol.Name, Position: f.Position}}})
		}
	}
	r.renamed = run.renamed
	r.notes = run.pass.notes(inputs)
	emitted, listings := staleEdges(index, caller.Name)
	r.findings = run.kept
	r.findings = append(r.findings, emitted...)
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

// resolution is the dead evaluations and binding records decided under one
// minimum confidence.
type resolution struct {
	pass    *bindingPass
	renamed map[componentKey]string
	// kept is the carried findings of no dropped component, then the
	// promoted findings, rewritten by the union.
	kept     []report.Finding
	promoted []promotion
}

// decide runs the one fixpoint, the promotion and the component union over
// the working set, withholding below minimum. It modifies none of r.
func (r *records) decide(dead []*report.EdgeEvaluation, index edgeIndex, minimum vocab.Class) *resolution {
	pass := newBindingPass(slices.Clone(r.bindings), map[componentKey]bool{}, minimum)
	fixpoint(func() bool { return dropEdges(dead, index, pass.dropped) }, pass.drop)
	kept := r.findings[:0:0]
	for i := range r.findings {
		if !pass.dropped[componentOfFinding(&r.findings[i])] {
			kept = append(kept, r.findings[i])
		}
	}
	carriers := pass.carriers()
	blocked := pass.blocked(dead, carriers)
	promoted := promoteBindings(promote(dead, blocked), carriers, blocked)
	base := len(kept)
	for i := range promoted {
		kept = append(kept, promoted[i].finding)
	}
	renamed := unite(kept[base:], kept, promoted, pass, pass.unpromoted(kept[:base], dead, carriers, blocked))
	return &resolution{pass: pass, kept: kept, promoted: promoted, renamed: renamed}
}

// withheldBelow is binding rule 3's withheld count: for each confidence below
// minimum, from the highest, every finding a merge withholding below that
// confidence promotes and no merge at a higher one does, counted at it, so
// lowering the minimum to a confidence shows the findings counted there.
func (r *records) withheldBelow(dead []*report.EdgeEvaluation, index edgeIndex, minimum vocab.Class, run *resolution) report.Withheld {
	var counted report.Withheld
	if minimum == "" {
		return counted
	}
	shown := map[findingKey]bool{}
	for i := range run.promoted {
		shown[keyOf(&run.promoted[i].finding)] = true
	}
	classes := vocab.Classes()
	for _, level := range classes[slices.Index(classes, minimum)+1:] {
		lower := r.decide(dead, index, level)
		for i := range lower.promoted {
			if key := keyOf(&lower.promoted[i].finding); !shown[key] {
				shown[key] = true
				if level == vocab.Probable {
					counted.Probable++
				} else {
					counted.Possible++
				}
			}
		}
	}
	return counted
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

// fixpoint repeats the passes, each dropping components into one shared set,
// until a round in which none drops a new one, so a drop of one relation is
// read by the other: merge.md step 4's one fixpoint over the edge rules and
// the binding rules.
func fixpoint(passes ...func() bool) {
	for grew := true; grew; {
		grew = false
		for _, pass := range passes {
			grew = pass() || grew
		}
	}
}

// dropEdges is one pass of rules 2 and 4: the component of a dead evaluation
// whose other sides read as live or as wholly absent drops, a dead
// evaluation of a dropped component reading as live on its own edge. It
// reports whether it dropped one.
func dropEdges(dead []*report.EdgeEvaluation, index edgeIndex, dropped map[componentKey]bool) bool {
	grew := false
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
	return grew
}

// promote is the finding of every dead evaluation that no dropped component
// carries, blocked holding their keys, once per finding, in canonical order,
// with every edge side whose evaluation carried it.
func promote(dead []*report.EdgeEvaluation, blocked map[findingKey]bool) []promotion {
	var promoted []promotion
	for _, e := range dead {
		finding := *e.Finding
		finding.Analyzer = e.Analyzer
		if blocked[keyOf(&finding)] {
			continue
		}
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
// analyzer, with every edge side whose evaluation carried it and every
// binding record that carried it.
type promotion struct {
	finding  report.Finding
	carriers []carrier
	records  []bindingCarrier
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

// unionFind is the components the union visit reached, in the order it
// reached them, with each component's counts as its findings carry them.
type unionFind struct {
	parent  map[componentKey]componentKey
	reached map[componentKey]int
	counts  map[componentKey]report.Component
}

// unite is merge.md's component union over the promoted findings in
// canonical order, binding joins before edge joins: a joined component takes
// the identifier reached first and the summed counts, less the debits of each
// component, and every finding of it is rewritten by [rewrite]; a member
// folded into its container is no root. own[i] is promoted[i]'s finding as
// findings holds it. It returns the identifier each reached component now
// carries.
func unite(own, findings []report.Finding, promoted []promotion, pass *bindingPass, debits map[componentKey]report.Component) map[componentKey]string {
	u := unionFind{counts: map[componentKey]report.Component{}}
	for i := range findings {
		if key := componentOfFinding(&findings[i]); u.counts[key].ID == "" {
			u.counts[key] = findings[i].Component
		}
	}
	u.debit(debits)
	promotedKeys := map[componentKey]bool{}
	for i := range promoted {
		promotedKeys[componentOfFinding(&promoted[i].finding)] = true
	}
	for i := range promoted {
		p := &promoted[i]
		key := componentOfFinding(&p.finding)
		u.reach(key)
		if u.bindingJoins(key, p.records, promotedKeys, pass) {
			own[i].Component.Root = false
		}
		for j := range promoted {
			if q := &promoted[j]; p.pairs(q) {
				u.join(key, componentOfFinding(&q.finding))
			}
		}
	}
	joined := u.components()
	u.countOnce(joined, promoted)
	rewrite(findings, joined)
	renamed := make(map[componentKey]string, len(joined))
	for key, c := range joined {
		renamed[key] = c.ID
	}
	return renamed
}

// debit takes each component's debit from its counts.
func (u *unionFind) debit(debits map[componentKey]report.Component) {
	for key, debit := range debits {
		if c, ok := u.counts[key]; ok {
			c.SymbolCount -= debit.SymbolCount
			c.DeletableLines -= debit.DeletableLines
			u.counts[key] = c
		}
	}
}

// bindingJoins joins key with every component the binding records carrying
// its finding join it to, reporting whether one joins it into its container.
func (u *unionFind) bindingJoins(key componentKey, records []bindingCarrier, promoted map[componentKey]bool, pass *bindingPass) bool {
	member := false
	for j := range records {
		c := &records[j]
		if c.carried && c.record.component() != key {
			u.join(key, c.record.component())
		}
		others, folds := pass.joins(c, promoted)
		for _, other := range others {
			u.join(key, other)
		}
		member = member || folds
	}
	return member
}

// countOnce takes from a joined component's counts, for the findings several
// of the components it joined carry, one symbol per carrying component beyond
// the first, and each source line those findings span once per carrying
// component beyond the first that spans it: two records of one component
// count a finding once, and two findings on one line count it once.
func (u *unionFind) countOnce(joined map[componentKey]report.Component, promoted []promotion) {
	symbols, spans := u.shared(promoted)
	for root, byComponent := range spans {
		lines := recounted(byComponent)
		for key, c := range joined {
			if u.find(key) == root {
				c.SymbolCount -= symbols[root]
				c.DeletableLines -= lines
				joined[key] = c
			}
		}
	}
}

// shared is, per joined root, the symbols its carrying components count
// beyond the first, and the positions of the findings several of them carry,
// by carrying component.
func (u *unionFind) shared(promoted []promotion) (symbols map[componentKey]int, spans map[componentKey]map[componentKey][]report.Position) {
	symbols = map[componentKey]int{}
	spans = map[componentKey]map[componentKey][]report.Position{}
	for i := range promoted {
		records := promoted[i].records
		if len(records) == 0 || !records[0].carried {
			continue
		}
		components := map[componentKey]bool{}
		for j := range records {
			components[records[j].record.component()] = true
		}
		if len(components) < 2 {
			continue
		}
		root := u.find(componentOfFinding(&promoted[i].finding))
		symbols[root] += len(components) - 1
		if spans[root] == nil {
			spans[root] = map[componentKey][]report.Position{}
		}
		for component := range components {
			spans[root][component] = append(spans[root][component], promoted[i].finding.Position)
		}
	}
	return symbols, spans
}

// recounted is the lines the components count beyond the distinct lines
// their positions span together.
func recounted(byComponent map[componentKey][]report.Position) int {
	var all []report.Position
	lines := 0
	for _, positions := range byComponent {
		lines += report.DistinctLines(positions)
		all = append(all, positions...)
	}
	return lines - report.DistinctLines(all)
}

// rewrite gives every finding of a joined component its identifier and
// counts, its confidence capped at the lowest of its roots.
func rewrite(findings []report.Finding, joined map[componentKey]report.Component) {
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

func (u *unionFind) reach(key componentKey) {
	if _, ok := u.reached[key]; ok {
		return
	}
	if u.parent == nil {
		u.parent, u.reached = map[componentKey]componentKey{}, map[componentKey]int{}
	}
	u.parent[key] = key
	u.reached[key] = len(u.reached)
}

func (u *unionFind) find(key componentKey) componentKey {
	for u.parent[key] != key {
		key = u.parent[key]
	}
	return key
}

// join reaches b and merges the sets of a and b under whichever root the
// visit reached first.
func (u *unionFind) join(a, b componentKey) {
	u.reach(b)
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
	for key := range u.reached {
		counts := u.counts[key]
		root := u.find(key)
		sum := sums[root]
		sum.ID = root.id
		sum.SymbolCount += counts.SymbolCount
		sum.DeletableLines += counts.DeletableLines
		sums[root] = sum
	}
	joined := make(map[componentKey]report.Component, len(u.reached))
	for key := range u.reached {
		joined[key] = sums[u.find(key)]
	}
	return joined
}
