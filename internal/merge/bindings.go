package merge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// The notes the merge writes from the facts, merge.md step 6.
const (
	unboundSchemaMessage      = "no analyzed language binds a declaration of this schema file; proto.plugins names the generators that do"
	bindingUnsupportedMessage = "the generator of this file is outside every supported version or option set, so no declaration of it binds"
)

// binding is one binding record of the working set, with the analyzer whose
// facts document carried it.
type binding struct {
	*facts.Binding
	analyzer string
	// library is whether the record's language is a library under its
	// report's target kind.
	library bool
	// index is the record's place in the working set, the order every list
	// of records is read in.
	index int
}

func (b *binding) component() componentKey {
	return componentKey{analyzer: b.analyzer, id: b.Finding.Component.ID}
}

// sameSchemaFile is merge.md's file rule: equal, or one the other preceded by
// whole path segments.
func sameSchemaFile(a, b string) bool {
	return a == b || strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// implementerKey names an implementer record by its analyzer and symbol, as a
// via entry names one.
type implementerKey struct{ analyzer, symbol string }

// bindingPass is merge.md step 4's "Bindings.": the working set's binding
// records, indexed once, read against the components the one fixpoint has
// dropped.
type bindingPass struct {
	dropped map[componentKey]bool
	// bySchema is the consumer and schema records under the schema they
	// name, users the schema records under each reference their uses names,
	// and containers under each reference their members names.
	bySchema, users, containers map[string][]*binding
	implementers                map[implementerKey]*binding

	all []binding
	// deadSchemas and deadImplementers are the dead records of each role.
	deadSchemas, deadImplementers []*binding
	fallbacks                     []*binding
}

// newBindingPass indexes the records and withholds, as dropped from the first
// pass on, every dead schema record whose finding binding rule 3 would
// promote below minimum.
func newBindingPass(all []binding, dropped map[componentKey]bool, minimum vocab.Class) *bindingPass {
	p := &bindingPass{
		dropped: dropped, all: all, implementers: map[implementerKey]*binding{},
		bySchema: map[string][]*binding{}, users: map[string][]*binding{}, containers: map[string][]*binding{},
	}
	for i := range p.all {
		p.all[i].index = i
		p.index(&p.all[i])
	}
	if minimum != "" {
		p.withhold(minimum)
	}
	return p
}

func (p *bindingPass) index(b *binding) {
	switch b.Role {
	case facts.RoleFallback:
		p.fallbacks = append(p.fallbacks, b)
	case facts.RoleImplementer:
		if key := (implementerKey{b.analyzer, b.Symbol}); p.implementers[key] == nil {
			p.implementers[key] = b
		}
		if b.State == facts.StateDead {
			p.deadImplementers = append(p.deadImplementers, b)
		}
	case facts.RoleConsumer:
		p.bySchema[b.Schema] = append(p.bySchema[b.Schema], b)
	case facts.RoleSchema:
		p.bySchema[b.Schema] = append(p.bySchema[b.Schema], b)
		for _, used := range b.Uses {
			p.users[used] = append(p.users[used], b)
		}
		for _, member := range b.Members {
			p.containers[member] = append(p.containers[member], b)
		}
		if b.State == facts.StateDead {
			p.deadSchemas = append(p.deadSchemas, b)
		}
	}
}

// withhold drops every dead schema record promotedClass places below
// minimum, so it reads live from the first pass on.
func (p *bindingPass) withhold(minimum vocab.Class) {
	for _, s := range p.deadSchemas {
		if less(p.promotedClass(s), minimum) {
			p.dropped[s.component()] = true
		}
	}
}

// reads is the state a record reads as, with merge.md's four exceptions.
func (p *bindingPass) reads(b *binding) facts.State {
	switch {
	case b.Role == facts.RoleFallback, b.State == "":
		return facts.StateLive
	case b.State == facts.StateDead && p.dropped[b.component()]:
		return facts.StateLive
	case b.Role == facts.RoleConsumer && b.Via != nil:
		for _, name := range b.Via {
			implementer := p.implementer(b.analyzer, name)
			if implementer == nil || p.reads(implementer) == facts.StateLive {
				return facts.StateLive
			}
		}
		return facts.StateUnreferenced
	}
	return b.State
}

func (p *bindingPass) implementer(analyzer, symbol string) *binding {
	return p.implementers[implementerKey{analyzer, symbol}]
}

// bindings is a schema record's bindings, in working-set order: the consumers
// matching it and the fallbacks whose file matches and whose scope covers its
// finding's kind.
func (p *bindingPass) bindings(s *binding) []*binding {
	kind := s.Finding.Symbol.Kind
	var out []*binding
	for _, b := range p.bySchema[s.Schema] {
		if b.Role == facts.RoleConsumer && sameSchemaFile(b.SchemaFile, s.SchemaFile) {
			out = append(out, b)
		}
	}
	for _, b := range p.fallbacks {
		if sameSchemaFile(b.SchemaFile, s.SchemaFile) && (b.Scope == facts.ScopeAll || kind != "service" && kind != "rpc") {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b *binding) int { return cmp.Compare(a.index, b.index) })
	return out
}

func (p *bindingPass) schemaRecords(schema, file string) []*binding {
	var out []*binding
	for _, b := range p.bySchema[schema] {
		if b.Role == facts.RoleSchema && sameSchemaFile(b.SchemaFile, file) {
			out = append(out, b)
		}
	}
	return out
}

func (p *bindingPass) readsLive(records []*binding) bool {
	return slices.ContainsFunc(records, func(r *binding) bool { return p.reads(r) == facts.StateLive })
}

// dropsSchema reports whether binding rule 1 or 2 drops a dead schema record.
func (p *bindingPass) dropsSchema(s *binding) bool {
	bindings := p.bindings(s)
	if len(bindings) == 0 || p.readsLive(bindings) || p.readsLive(p.users[s.Schema]) {
		return true
	}
	return slices.ContainsFunc(s.Members, func(member string) bool { return p.readsLive(p.schemaRecords(member, s.SchemaFile)) })
}

// dropsImplementer reports whether implementer rule 1 or 2 drops a dead
// implementer record.
func (p *bindingPass) dropsImplementer(i *binding) bool {
	schemas := p.schemaRecords(i.Schema, i.SchemaFile)
	if len(schemas) == 0 || p.readsLive(schemas) {
		return true
	}
	return slices.ContainsFunc(i.Via, func(name string) bool {
		other := p.implementer(i.analyzer, name)
		return other != nil && p.reads(other) == facts.StateLive
	})
}

// drop is one pass of the binding rules over the dead records whose
// components are not dropped; it reports whether it dropped one.
func (p *bindingPass) drop() bool {
	grew := false
	for _, s := range p.deadSchemas {
		if !p.dropped[s.component()] && p.dropsSchema(s) {
			p.dropped[s.component()] = true
			grew = true
		}
	}
	for _, i := range p.deadImplementers {
		if !p.dropped[i.component()] && p.dropsImplementer(i) {
			p.dropped[i.component()] = true
			grew = true
		}
	}
	return grew
}

// promotedClass is the confidence binding rule 3 promotes a dead schema
// record's finding at: lowered to possible where a binding carries cap and
// the reader did not publish it certain, which it does only under
// proto.closed, and where a binding's language is a library.
func (p *bindingPass) promotedClass(s *binding) vocab.Class {
	bindings := p.bindings(s)
	class := s.Finding.Confidence
	capped := class != vocab.Certain && slices.ContainsFunc(bindings, func(b *binding) bool { return b.Cap != "" })
	if (capped || slices.ContainsFunc(bindings, func(b *binding) bool { return b.library })) && less(vocab.Possible, class) {
		class = vocab.Possible
	}
	return class
}

// bindingCarrier is one finding a dead record carries: its own, or one its
// carried list holds.
type bindingCarrier struct {
	record  *binding
	finding report.Finding
	carried bool
}

// carriers is every finding the dead records carry, stamped with their
// analyzer, a schema record's at its promoted class.
func (p *bindingPass) carriers() []bindingCarrier {
	var out []bindingCarrier
	for _, s := range p.deadSchemas {
		f := *s.Finding
		f.Analyzer = s.analyzer
		f.Confidence = p.promotedClass(s)
		out = append(out, bindingCarrier{record: s, finding: f})
	}
	for _, i := range p.deadImplementers {
		f := *i.Finding
		f.Analyzer = i.analyzer
		out = append(out, bindingCarrier{record: i, finding: f})
		for j := range i.Carried {
			c := i.Carried[j]
			c.Analyzer = i.analyzer
			out = append(out, bindingCarrier{record: i, finding: c, carried: true})
		}
	}
	return out
}

// findingKey is a finding's canonical key, report.CompareFindings' members.
type findingKey struct {
	path, ref, analyzer string
	code                vocab.Code
	line, column        int
}

func keyOf(f *report.Finding) findingKey {
	return findingKey{path: f.Position.Path, ref: f.Symbol.Ref, analyzer: f.Analyzer, code: f.Code, line: f.Position.Line, column: f.Position.Column}
}

// blocked is the canonical key of every finding a dropped component carries,
// in either relation: a finding several components carry is promoted only
// where none of them is dropped.
func (p *bindingPass) blocked(dead []*report.EdgeEvaluation, carriers []bindingCarrier) map[findingKey]bool {
	keys := map[findingKey]bool{}
	for _, e := range dead {
		if p.dropped[componentOfEvaluation(e)] {
			f := *e.Finding
			f.Analyzer = e.Analyzer
			keys[keyOf(&f)] = true
		}
	}
	for i := range carriers {
		if p.dropped[carriers[i].record.component()] {
			keys[keyOf(&carriers[i].finding)] = true
		}
	}
	return keys
}

// unpromoted is, per component no pass dropped, the debit of the findings it
// carries that blocked holds: a dropped component carries each of them too, so
// it stays live and no deletion of the promoted findings removes it. The debit
// is one symbol per such finding and the distinct lines they span that no
// other finding of the component spans; held is the carried report findings
// of no dropped component.
func (p *bindingPass) unpromoted(held []report.Finding, dead []*report.EdgeEvaluation, carriers []bindingCarrier, blocked map[findingKey]bool) map[componentKey]report.Component {
	type carriedBy struct {
		component componentKey
		finding   findingKey
	}
	debits := map[componentKey]report.Component{}
	all, kept := map[componentKey][]report.Position{}, map[componentKey][]report.Position{}
	seen := map[carriedBy]bool{}
	hold := func(component componentKey, f *report.Finding) {
		at := carriedBy{finding: keyOf(f), component: component}
		if p.dropped[component] || seen[at] {
			return
		}
		seen[at] = true
		all[component] = append(all[component], f.Position)
		if !blocked[at.finding] {
			kept[component] = append(kept[component], f.Position)
			return
		}
		d := debits[component]
		d.SymbolCount++
		debits[component] = d
	}
	for i := range held {
		hold(componentOfFinding(&held[i]), &held[i])
	}
	for _, e := range dead {
		f := *e.Finding
		f.Analyzer = e.Analyzer
		hold(componentOfEvaluation(e), &f)
	}
	for i := range carriers {
		hold(carriers[i].record.component(), &carriers[i].finding)
	}
	for component, d := range debits {
		d.DeletableLines = report.DistinctLines(all[component]) - report.DistinctLines(kept[component])
		debits[component] = d
	}
	return debits
}

// promoteBindings adds to the edge promotions the findings of the dead
// records whose components are not dropped, once per canonical key whichever
// records carry it, none that blocked holds. The result is in canonical
// order.
func promoteBindings(promoted []promotion, carriers []bindingCarrier, blocked map[findingKey]bool) []promotion {
	at := map[findingKey]int{}
	for i := range promoted {
		at[keyOf(&promoted[i].finding)] = i
	}
	for k := range carriers {
		c := &carriers[k]
		key := keyOf(&c.finding)
		if blocked[key] {
			continue
		}
		if i, held := at[key]; held {
			promoted[i].records = append(promoted[i].records, *c)
			continue
		}
		at[key] = len(promoted)
		promoted = append(promoted, promotion{finding: c.finding, records: []bindingCarrier{*c}})
	}
	slices.SortStableFunc(promoted, func(a, b promotion) int { return report.CompareFindings(&a.finding, &b.finding) })
	return promoted
}

// joins is the components a promoted record's finding joins before the edge
// joins, and whether it joins its container as a member.
func (p *bindingPass) joins(c *bindingCarrier, promoted map[componentKey]bool) (keys []componentKey, member bool) {
	switch {
	case c.carried:
		return nil, false
	case c.record.Role == facts.RoleImplementer:
		for _, s := range p.schemaRecords(c.record.Schema, c.record.SchemaFile) {
			if s.State == facts.StateDead && promoted[s.component()] {
				keys = append(keys, s.component())
			}
		}
		return keys, false
	case c.record.Role == facts.RoleSchema:
		keys = p.viaJoins(c.record, promoted)
		containers := p.containersOf(c.record, promoted)
		return append(keys, containers...), len(containers) > 0
	}
	return nil, false
}

// viaJoins is the promoted implementers a binding of s names in its via,
// where that binding read unreferenced.
func (p *bindingPass) viaJoins(s *binding, promoted map[componentKey]bool) []componentKey {
	var keys []componentKey
	for _, b := range p.bindings(s) {
		if b.Via == nil || p.reads(b) != facts.StateUnreferenced {
			continue
		}
		for _, name := range b.Via {
			if i := p.implementer(b.analyzer, name); i != nil && i.State == facts.StateDead && promoted[i.component()] {
				keys = append(keys, i.component())
			}
		}
	}
	return keys
}

// containersOf is the promoted schema records whose members name s.
func (p *bindingPass) containersOf(s *binding, promoted map[componentKey]bool) []componentKey {
	var keys []componentKey
	for _, container := range p.containers[s.Schema] {
		if container.State == facts.StateDead && promoted[container.component()] && sameSchemaFile(container.SchemaFile, s.SchemaFile) {
			keys = append(keys, container.component())
		}
	}
	return keys
}

// notes is merge.md step 6's notes from the facts: one unbound-schema note per
// schema file no dead schema record of which has a binding, and one
// binding-unsupported note per generated entry of an unsupported row.
func (p *bindingPass) notes(inputs []Input) []report.Note {
	var out []report.Note
	var files []string
	bound := map[string]bool{}
	for _, s := range p.deadSchemas {
		path := s.Finding.Position.Path
		if _, seen := bound[path]; !seen {
			files = append(files, path)
		}
		bound[path] = bound[path] || len(p.bindings(s)) > 0
	}
	for _, path := range files {
		if !bound[path] {
			out = append(out, report.Note{Kind: report.NoteKindUnboundSchema, Path: path, Key: string(vocab.KeyProtoPlugins), Message: unboundSchemaMessage})
		}
	}
	return append(out, unsupportedNotes(inputs)...)
}

func unsupportedNotes(inputs []Input) []report.Note {
	unsupported := map[string]bool{}
	rows := vocab.GeneratorRows()
	for i := range rows {
		unsupported[rows[i].ID] = rows[i].Cell == "unsupported"
	}
	var out []report.Note
	for i := range inputs {
		if inputs[i].Facts == nil {
			continue
		}
		for j := range inputs[i].Facts.Generated {
			if g := &inputs[i].Facts.Generated[j]; g.Generator != nil && unsupported[g.Generator.Row] {
				out = append(out, report.Note{Kind: report.NoteKindBindingUnsupported, Path: g.Path, Message: bindingUnsupportedMessage})
			}
		}
	}
	return out
}
