package schema

import (
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// enumValueJSON is the field number of (pb.enumvalue.json), Edition 2026's
// enum value JSON name, in google/protobuf/json_enumvalue_options.proto.
const enumValueJSON = 998

// classify is schemas.9 to schemas.15 as far as the reader's own inputs
// decide them; the merge applies the rest from the bindings.
func (r *reading) classify() {
	reached := r.httpReached()
	for _, d := range r.decls {
		switch {
		case r.in.Settings.Closed:
			d.class = vocab.Certain
		case d.kind == kindService || d.kind == kindRPC, reached[d], d.file.gen.unknown, explicitJSON(d):
			d.class = vocab.Possible
		default:
			d.class = vocab.Probable
		}
	}
}

// explicitJSON is schemas.14: a field stating json_name, or an enum value
// stating its JSON name.
func explicitJSON(d *decl) bool {
	switch {
	case d.field != nil:
		return d.field.JSONName != nil
	case d.value != nil:
		o := d.value.Options
		return o != nil && o.Interpreted != nil && o.Interpreted.Fields[enumValueJSON] != nil
	}
	return false
}

// httpReached is schemas.11: every declaration reached, through RPC input
// and output types and field types, from a service of a file whose
// configuration serves it over HTTP.
func (r *reading) httpReached() map[*decl]bool {
	reached := map[*decl]bool{}
	for _, d := range r.decls {
		if d.kind == kindService && d.file.gen.http {
			r.reach(d, reached)
		}
	}
	return reached
}

// reach adds d to reached with the types it uses and, but for a message or
// an enum nested in it, its members.
func (r *reading) reach(d *decl, reached map[*decl]bool) {
	if d == nil || reached[d] {
		return
	}
	reached[d] = true
	for _, ref := range d.uses {
		r.reach(r.byRef[ref], reached)
	}
	for _, ref := range d.members {
		if m := r.byRef[ref]; m != nil && m.kind != kindMessage && m.kind != kindEnum {
			r.reach(m, reached)
		}
	}
}
