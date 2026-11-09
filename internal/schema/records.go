package schema

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/schema/proto"
)

// The symbol kinds of a Protocol Buffers declaration, kinds.json's symbol_kinds.
const (
	kindMessage = "message"
	kindEnum    = "enum"
	kindField   = "field"
	kindValue   = "enum-member"
	kindService = "service"
	kindRPC     = "rpc"
)

// The FileDescriptorProto and DescriptorProto field numbers a declaration's
// location path is built from, descriptor.proto's.
const (
	fdMessage, fdEnum, fdService, fdExtension = 4, 5, 6, 7
	dpField, dpNested, dpEnum, dpExtension    = 2, 3, 4, 6
	edValue, sdMethod                         = 2, 2
	fldTypeName, fldExtendee                  = 6, 2
	mdInput, mdOutput                         = 2, 3
	mapValueNumber                            = 2
)

// codeOf is schemas.19: the code a dead record of each symbol kind is
// reported under.
var codeOf = map[string]vocab.Code{
	kindMessage: codeType, kindEnum: codeType, kindField: codeField, kindValue: codeValue, kindService: codeService, kindRPC: codeRPC,
}

// The DS18 codes, kinds.json's.
const (
	codeType    vocab.Code = "DS1801"
	codeField   vocab.Code = "DS1802"
	codeValue   vocab.Code = "DS1803"
	codeService vocab.Code = "DS1804"
	codeRPC     vocab.Code = "DS1805"
)

var messageOf = map[vocab.Code]string{
	codeType:    "schema type is used by no binding in any analyzed language and by no live schema declaration",
	codeField:   "schema field is read by no binding in any analyzed language",
	codeValue:   "schema enum value is named by no binding in any analyzed language",
	codeService: "schema service is used by no binding in any analyzed language",
	codeRPC:     "schema operation is called by no client binding in any analyzed language",
}

// decl is one declaration of a schema file of the target and its record.
type decl struct {
	file    *schemaFile
	parent  *decl
	field   *proto.Field
	enum    *proto.Enum
	value   *proto.EnumValue
	finding *report.Finding
	// held names what publishes the record live, empty while it is dead.
	held              string
	ref, name, kind   string
	class             vocab.Class
	path              []int32
	uses, members     []string
	line, column, end int
}

// bufDoc is one buf file as the subset read it.
type bufDoc struct {
	doc *yamlNode
	err error
}

// reading is one [Read] in progress.
type reading struct {
	in   *Input
	tree fs.FS
	// built is tree less the schema files no buf module builds, which the
	// import roots are read from.
	built  fs.FS
	byFull map[string]*decl
	byRef  map[string]*decl
	// unclean is why each schema file not read cleanly is so, by its
	// target-relative path: the target's schema files less the clean ones.
	unclean  map[string]uncleanFile
	units    []*proto.Unit
	files    []*schemaFile
	bufFiles []string
	bufDocs  map[string]bufDoc
	genFiles []string
	decls    []*decl
	notes    []report.Note
	checks   []report.Finding
	stale    []report.StaleSuppression
	inEffect int
	reasons  int
	withheld report.Withheld
}

// declare publishes one record per declaration of every file that compiled,
// schemas.1 and schemas.2.
func (r *reading) declare() {
	refs := r.typeRefs()
	for _, f := range r.files {
		if f.unit != nil {
			(&walker{r: r, f: f, refs: refs, scope: scopeOf(f.unit.File.Package)}).file(f.unit.File)
			unresolved(f)
		}
	}
	for _, d := range r.decls {
		if d.parent != nil {
			d.parent.members = append(d.parent.members, d.ref)
		}
	}
	for _, d := range r.decls {
		slices.Sort(d.uses)
		d.uses = slices.Compact(d.uses)
		slices.Sort(d.members)
	}
}

// typeRefs is the reference of every message and enum of every linked file,
// by its full name, the files outside the target included.
func (r *reading) typeRefs() map[string]string {
	refs := map[string]string{}
	var message func(m *proto.Message, scope, prefix string)
	enum := func(e *proto.Enum, scope, prefix string) { refs[e.FullName] = scope + prefix + e.Name }
	message = func(m *proto.Message, scope, prefix string) {
		refs[m.FullName] = scope + prefix + m.Name
		for _, n := range m.Nested {
			message(n, scope, prefix+m.Name+".")
		}
		for _, e := range m.Enums {
			enum(e, scope, prefix+m.Name+".")
		}
	}
	for _, u := range r.units {
		if u == nil || u.File == nil {
			continue
		}
		scope := scopeOf(u.File.Package)
		for _, m := range u.File.Messages {
			message(m, scope, "")
		}
		for _, e := range u.File.Enums {
			enum(e, scope, "")
		}
	}
	return refs
}

// scopeOf is the reference prefix of a package, symbol-ref.md's Protocol
// Buffers scope: the package, or a single dot for none.
func scopeOf(pkg string) string {
	if pkg == "" {
		pkg = "."
	}
	return "proto://" + pkg + "#"
}

// typeUses is the reference of each resolved type name, ".pkg.Name" as Link
// writes it; an unresolved name has none.
func typeUses(refs map[string]string, names ...string) []string {
	var out []string
	for _, n := range names {
		if ref, ok := refs[strings.TrimPrefix(n, ".")]; ok && strings.HasPrefix(n, ".") {
			out = append(out, ref)
		}
	}
	return out
}

type walker struct {
	r     *reading
	f     *schemaFile
	refs  map[string]string
	scope string
}

func (w *walker) file(file *proto.File) {
	for i, m := range file.Messages {
		w.message(m, nil, []int32{fdMessage, int32(i)})
	}
	for i, e := range file.Enums {
		w.enumDecl(e, nil, []int32{fdEnum, int32(i)})
	}
	for i, s := range file.Services {
		sd := w.add(kindService, s.Name, nil, []int32{fdService, int32(i)})
		for j, m := range s.Methods {
			md := w.add(kindRPC, m.Name, sd, []int32{fdService, int32(i), sdMethod, int32(j)})
			md.uses = typeUses(w.refs, m.Input, m.Output)
		}
	}
	for i, x := range file.Extensions {
		w.fieldDecl(x, nil, []int32{fdExtension, int32(i)})
	}
}

func (w *walker) add(kind, name string, parent *decl, path []int32) *decl {
	d := &decl{file: w.f, parent: parent, kind: kind, name: name, path: path}
	if parent != nil {
		d.name = parent.name + "." + name
	}
	d.ref = w.scope + d.name
	position(d)
	w.r.decls = append(w.r.decls, d)
	w.f.decls = append(w.f.decls, d)
	w.r.byRef[d.ref] = d
	return d
}

func (w *walker) message(m *proto.Message, parent *decl, path []int32) {
	if m.MapEntry {
		return
	}
	d := w.add(kindMessage, m.Name, parent, path)
	w.r.byFull[m.FullName] = d
	for i, fl := range m.Fields {
		fd := w.fieldDecl(fl, d, cat(path, dpField, int32(i)))
		if entry := mapEntry(m, fl); entry != nil {
			fd.uses = nil
			for _, v := range entry.Fields {
				if v.Number == mapValueNumber {
					fd.uses = typeUses(w.refs, v.TypeName)
				}
			}
		}
	}
	for i, n := range m.Nested {
		w.message(n, d, cat(path, dpNested, int32(i)))
	}
	for i, e := range m.Enums {
		w.enumDecl(e, d, cat(path, dpEnum, int32(i)))
	}
	for i, x := range m.Extensions {
		w.fieldDecl(x, d, cat(path, dpExtension, int32(i)))
	}
}

func (w *walker) fieldDecl(fl *proto.Field, parent *decl, path []int32) *decl {
	d := w.add(kindField, fl.Name, parent, path)
	d.field = fl
	d.uses = typeUses(w.refs, fl.TypeName)
	w.r.byFull[fl.FullName] = d
	return d
}

func (w *walker) enumDecl(e *proto.Enum, parent *decl, path []int32) {
	d := w.add(kindEnum, e.Name, parent, path)
	d.enum = e
	w.r.byFull[e.FullName] = d
	for i, v := range e.Values {
		vd := w.add(kindValue, v.Name, d, cat(path, edValue, int32(i)))
		vd.value = v
	}
}

// mapEntry is the entry message of a map field, nil for any other field.
func mapEntry(m *proto.Message, fl *proto.Field) *proto.Message {
	for _, n := range m.Nested {
		if n.MapEntry && "."+n.FullName == fl.TypeName {
			return n
		}
	}
	return nil
}

// unresolved is schemas.5: a declaration writing a type name no scope
// resolves is published live.
func unresolved(f *schemaFile) {
	for _, ref := range f.unit.Unresolved {
		for _, d := range f.decls {
			if len(ref) != len(d.path)+1 || !slices.Equal(ref[:len(d.path)], d.path) {
				continue
			}
			last := ref[len(d.path)]
			if (d.kind == kindField && (last == fldTypeName || last == fldExtendee)) || (d.kind == kindRPC && (last == mdInput || last == mdOutput)) {
				d.hold("unresolved-type")
			}
		}
	}
}

// hold publishes d live for reason, the first reason found standing.
func (d *decl) hold(reason string) {
	if d.held == "" {
		d.held = reason
	}
}

// position reads a declaration's site from its file's location: its first
// token, where the finding is and a directive binds, and its last line.
func position(d *decl) {
	whole := d.file.locs[pathKey(d.path)]
	if whole == nil {
		return
	}
	d.line, d.end = int(whole.Span[0])+1, int(whole.Span[0])+1
	if len(whole.Span) == 4 {
		d.end = int(whole.Span[2]) + 1
	}
	d.column = utf16Column(d.file.src, int(whole.Span[0]), int(whole.Span[1]))
}

// utf16Column converts protoc's 0-based column on a 0-based line, a tab
// advancing it to the next multiple of 8 and every other byte by one, to the
// 1-based UTF-16 column a report position counts.
func utf16Column(src []byte, line, column int) int {
	for ; line > 0 && len(src) > 0; line-- {
		i := slices.Index(src, '\n')
		if i < 0 {
			return 1
		}
		src = src[i+1:]
	}
	at, col := 0, 0
	for at < len(src) && src[at] != '\n' && col < column {
		if src[at] == '\t' {
			col += 8 - col%8
		} else {
			col++
		}
		at++
	}
	units := 1
	for _, c := range string(src[:at]) {
		units++
		if c >= 0x10000 {
			units++
		}
	}
	return units
}

func cat(p []int32, xs ...int32) []int32 { return append(slices.Clone(p), xs...) }

// result mints each dead record's finding, schemas.19, and writes the facts
// and the report.
func (r *reading) result(evaluations []report.EdgeEvaluation) (*Result, error) {
	doc := &facts.Document{Analyzer: facts.Analyzer{Name: Name, Version: r.in.Version}, Language: vocab.Proto, Evaluated: true}
	for _, d := range r.decls {
		b := facts.Binding{Role: facts.RoleSchema, Symbol: d.ref, Schema: d.ref, SchemaFile: d.file.importPath, State: facts.StateLive, Uses: d.uses, Members: d.members}
		if d.held == "" {
			b.State, b.Finding = facts.StateDead, d.finding
		}
		doc.Bindings = append(doc.Bindings, b)
	}
	built, err := facts.Build(doc)
	if err != nil {
		return nil, fmt.Errorf("schema: the facts document: %w", err)
	}
	slices.SortStableFunc(r.notes, func(a, b report.Note) int { return strings.Compare(a.Path, b.Path) })
	rep, err := report.Build(&report.Input{
		Target:   report.Target{Root: orDot(r.in.Root), Identity: orDot(r.in.Identity)},
		Analyzer: report.Analyzer{Name: Name, Version: r.in.Version, Languages: []vocab.Language{vocab.Proto}, Conformance: r.in.Conformance},
		Findings: r.checks, StaleSuppressions: r.stale, EdgeEvaluations: evaluations, Notes: r.notes,
		SuppressionsInEffect: r.inEffect, ReasonsRecorded: r.reasons, Withheld: r.withheld,
	})
	if err != nil {
		return nil, fmt.Errorf("schema: the report: %w", err)
	}
	return &Result{Report: rep, Facts: built}, nil
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// mint gives every record still dead its pending finding, its component
// numbered in record order.
func (r *reading) mint() {
	n := 0
	for _, d := range r.decls {
		if d.held != "" {
			continue
		}
		n++
		d.finding = r.findingOf(d, n)
	}
	for i := range r.checks {
		n++
		r.checks[i].Component.ID = fmt.Sprintf("%s/c-%d", Name, n)
	}
}

func (r *reading) findingOf(d *decl, n int) *report.Finding {
	code := codeOf[d.kind]
	kind, _ := vocab.KindOf(code)
	exported := true
	size := max(d.end-d.line+1, 1)
	f := &report.Finding{
		Code: code, Kind: kind.Name, Language: vocab.Proto,
		Position:          report.Position{Path: d.file.rel, Line: max(d.line, 1), Column: max(d.column, 1), EndLine: max(d.end, d.line, 1)},
		Symbol:            report.Symbol{Ref: d.ref, Kind: d.kind, Name: d.name, Exported: &exported, SizeLines: size},
		ReachabilityClass: d.class, Confidence: d.class, LivenessRelation: report.RelationReferenceCounting,
		Component:  report.Component{ID: fmt.Sprintf("%s/c-%d", Name, n), Root: true, SymbolCount: 1, DeletableLines: size},
		RetainedBy: []string{}, Configurations: []string{}, ConsumersLoaded: []string{},
		Fixability: kind.Fixability, Severity: r.in.Settings.severity(code), Message: messageOf[code],
	}
	if d.parent != nil {
		f.Symbol.Parent = d.parent.ref
	}
	return f
}
