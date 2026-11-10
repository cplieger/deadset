package proto

import (
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

//go:embed include/google/protobuf/*.proto include/google/protobuf/compiler/*.proto
var include embed.FS

// wellKnown is protobuf v36.2's include/ tree, the import root searched last
// as protoc searches its own include directory.
var wellKnown = func() fs.FS {
	sub, err := fs.Sub(include, "include")
	if err != nil {
		panic(err)
	}
	return sub
}()

const descriptorProto = "google/protobuf/descriptor.proto"

// Embedded reports whether name is a file of the include directory Link
// searches last.
func Embedded(name string) bool {
	info, err := fs.Stat(wellKnown, name)
	return err == nil && !info.IsDir()
}

// Unit is one file a Link call loaded.
//
// Missing and Unresolved are what protoc refuses. Unless the link is strict,
// Link reads a missing import as an empty file, as protoc's
// AllowUnknownDependencies mode does, and, by schemas.5, leaves a name no
// scope resolves as written only in a file with a missing import. Such a
// name in any other file, and an import of a file that failed, fail the
// file, as in protoc.
type Unit struct {
	File    *File    // nil when the file could not be read
	Err     error    // the file's Errors, or its read error; nil when it links
	Missing []string // imports no root holds, in import order
	// Unresolved is the location path of every name no scope resolves, in
	// protoc's resolution order: a field's type_name or extendee, a method's
	// input or output type, or an uninterpreted option.
	Unresolved [][]int32
}

// Detached is a schema file no import root holds, read by its caller.
type Detached struct {
	Name string
	Src  []byte
}

// Files is what one [Link] call links.
type Files struct {
	// Same, where set, is the name in Names of the file root number root
	// holds at name, else "": a file an import reaches under a second name
	// is that file, linked once.
	Same     func(root int, name string) string
	Names    []string
	Detached []Detached
	// Strict links as protoc does with no unknown dependency allowed: a
	// missing import is protoc's error, so the file fails, and so is every
	// unresolved name; Missing still lists the imports no root holds.
	Strict bool
}

// Link loads files.Names and every file they import from roots, searched in
// order as protoc's --proto_path list and then the google/protobuf files
// embedded here, and links each file as protoc v36.2 does: name resolution,
// option interpretation, feature resolution and validation. The first result
// holds the Unit of every loaded file by import name, a file Same names by
// its name in Names alone; a name no root holds has none. Each detached
// file is then linked into the same pool under its Name, its imports
// searched the same way, and no import resolves to it; its Unit is the
// second result's entry of the same index.
func Link(roots []fs.FS, files Files) (map[string]*Unit, []*Unit) {
	l := &linker{
		roots: append(slices.Clone(roots), wellKnown), searched: len(roots), same: files.Same, strict: files.Strict,
		units: map[string]*Unit{}, renamed: map[string]string{}, syms: map[string]*symbol{},
	}
	l.load(descriptorProto)
	for _, n := range files.Names {
		l.load(n)
	}
	outside := make([]*Unit, len(files.Detached))
	for i, d := range files.Detached {
		outside[i] = &Unit{}
		l.compile(outside[i], d.Name, d.Src, false)
	}
	for _, n := range files.Names {
		checkReserved(l.units[n])
	}
	for _, u := range outside {
		checkReserved(u)
	}
	return l.units, outside
}

func checkReserved(u *Unit) {
	if u != nil && u.Err == nil {
		checkReservedNumbers(u)
	}
}

type linker struct {
	same     func(root int, name string) string
	units    map[string]*Unit
	renamed  map[string]string // an import name Same answered, to its name
	syms     map[string]*symbol
	defaults map[int32]featureSet
	roots    []fs.FS
	stack    []*Unit // the files being loaded, outermost first
	searched int     // the roots Same answers for, ahead of wellKnown
	strict   bool
}

// failed stands for a dependency still being loaded, an import cycle, which
// fails its importer as a file that failed does.
var failed = &Unit{}

func (l *linker) load(name string) *Unit {
	if u, ok := l.units[l.named(name)]; ok {
		return u
	}
	for i, p := range l.stack {
		if p.File.Name == name {
			l.cycle(i)
			return failed
		}
	}
	src, root, found, err := l.read(name)
	if !found {
		return nil
	}
	if l.same != nil && root < l.searched {
		if other := l.same(root, name); other != "" && other != name {
			l.renamed[name] = other
			return l.load(other)
		}
	}
	u := &Unit{}
	if err != nil {
		u.Err = err
	} else {
		l.compile(u, name, src, true)
	}
	l.units[name] = u
	return u
}

// compile parses src as name and links it after loading its imports. Only a
// stacked file is one an import can reach, so only it can close a cycle.
func (l *linker) compile(u *Unit, name string, src []byte, stacked bool) {
	u.File, u.Err = parseFile(name, src)
	if u.Err != nil {
		return
	}
	if stacked {
		l.stack = append(l.stack, u)
	}
	imports := append(slices.Clone(u.File.Deps), u.File.OptionDeps...)
	deps := make([]*Unit, len(imports))
	for i, d := range imports {
		deps[i] = l.load(d)
	}
	if stacked {
		l.stack = l.stack[:len(l.stack)-1]
	}
	if u.Err == nil {
		l.build(u, deps)
	}
}

// cycle fails the file at stack[i] for importing itself, at its import of
// the next file in the cycle, with protoc's message. The first cycle found
// stands: protoc reads a file that failed as one, so a later import of it
// is a plain import error.
func (l *linker) cycle(i int) {
	u := l.stack[i]
	if u.Err != nil {
		return
	}
	chain := make([]string, 0, len(l.stack)-i+1)
	for _, p := range l.stack[i:] {
		chain = append(chain, p.File.Name)
	}
	chain = append(chain, u.File.Name)
	f := u.File
	var path []int32
	next := func(d string) bool { return l.named(d) == chain[1] }
	if j := slices.IndexFunc(f.Deps, next); j >= 0 {
		path = []int32{fdDependency, index(j)}
	} else if j := slices.IndexFunc(f.OptionDeps, next); j >= 0 {
		path = []int32{fdOptionDep, index(j)}
	}
	b := &builder{f: f, locs: indexLocations(f.Locations)}
	b.errorAt(path, "File recursively imports itself: "+strings.Join(chain, " -> "))
	u.Err = b.errs
}

// named is the name the file an import name reaches is loaded under.
func (l *linker) named(name string) string {
	if other, ok := l.renamed[name]; ok {
		return other
	}
	return name
}

// read returns name from the first root holding it, with that root's index.
// A name protoc's source tree refuses (absolute, non-canonical, or with a
// backslash) is held by none.
func (l *linker) read(name string) (src []byte, root int, found bool, err error) {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return nil, 0, false, nil
	}
	for i, r := range l.roots {
		src, err := fs.ReadFile(r, name)
		switch {
		case err == nil:
			return src, i, true, nil
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			continue
		default:
			return nil, i, true, err
		}
	}
	return nil, 0, false, nil
}

type symKind int

const (
	symPackage symKind = iota
	symMessage
	symEnum
	symEnumValue
	symField
	symOneof
	symService
	symMethod
)

type symbol struct {
	file  *File
	msg   *Message
	enum  *Enum
	field *Field
	kind  symKind
}

func (s *symbol) isType() bool { return s.kind == symMessage || s.kind == symEnum }

func (s *symbol) isAggregate() bool {
	return s.isType() || s.kind == symPackage || s.kind == symService
}

func (s *symbol) fullName() string {
	switch s.kind {
	case symMessage:
		return s.msg.FullName
	case symEnum:
		return s.enum.FullName
	}
	return s.field.FullName
}

// builder is DescriptorBuilder for one file: its errors, the symbols it
// added to the pool (removed again when it fails), and its visible files.
type builder struct {
	l        *linker
	u        *Unit
	f        *File
	locs     map[string]*Location
	deps     map[*File]bool
	optDeps  map[*File]bool
	byNumber map[string]*Field
	hints    map[*Message]*hint
	errs     Errors
	added    []string
	hinted   []*Message
	edition  int32
}

func (l *linker) build(u *Unit, deps []*Unit) {
	b := &builder{
		l: l, u: u, f: u.File, locs: indexLocations(u.File.Locations), edition: editionOf(u.File),
		deps: map[*File]bool{}, optDeps: map[*File]bool{}, byNumber: map[string]*Field{}, hints: map[*Message]*hint{},
	}
	b.run(deps)
	if len(b.errs) > 0 {
		for _, n := range b.added {
			delete(l.syms, n)
		}
		u.Err = b.errs
	}
}

func (b *builder) run(deps []*Unit) {
	f := b.f
	b.checkEdition()
	if f.Package != "" {
		b.addPackage(f.Package)
	}
	b.recordDeps(deps)
	for i, m := range f.Messages {
		b.buildMessage(m, f.Package, nil, []int32{fdMessageType, int32(i)})
	}
	for i, e := range f.Enums {
		b.buildEnum(e, f.Package, nil, []int32{fdEnumType, int32(i)})
	}
	for i, s := range f.Services {
		b.buildService(s, []int32{fdService, int32(i)})
	}
	for i, x := range f.Extensions {
		b.buildField(x, nil, f.Package, true, []int32{fdExtension, int32(i)})
	}
	b.crossLink()
	b.suggestFieldNumbers()
	if len(b.errs) == 0 {
		b.interpretOptions()
	}
	if len(b.errs) == 0 {
		b.validate()
	}
	if len(b.errs) == 0 {
		b.validateNaming()
	}
	if len(b.errs) == 0 {
		b.validateStrictVisibility()
	}
}

// The editions protoc v36.2's compiled feature defaults cover.
const (
	editionProto2 = 998
	editionProto3 = 999
	edition2026   = 1002
)

func editionOf(f *File) int32 {
	switch f.Syntax {
	case "", syntaxProto2:
		return editionProto2
	case syntaxProto3:
		return editionProto3
	}
	return int32(editionNumbers[f.Edition]) //nolint:gosec // every Edition number is an int32
}

func (b *builder) checkEdition() {
	switch {
	case b.edition < editionProto2:
		b.errorAt([]int32{fdSyntax}, "Edition "+b.f.Edition+" is earlier than the minimum supported edition PROTO2")
	case b.edition > edition2026 && b.edition != editionUnstable:
		b.errorAt([]int32{fdSyntax}, "Edition "+b.f.Edition+" is later than the maximum supported edition 2026")
	}
}

// recordDeps finds each import's file and the files the file can see: its
// imports and their public imports, transitively (dependencies_).
func (b *builder) recordDeps(deps []*Unit) {
	f := b.f
	seen := map[string]bool{}
	for i, d := range deps {
		var n string
		var path []int32
		into := b.deps
		if i < len(f.Deps) {
			n, path = f.Deps[i], []int32{fdDependency, index(i)}
		} else {
			n, path, into = f.OptionDeps[i-len(f.Deps)], []int32{fdOptionDep, index(i - len(f.Deps))}, b.optDeps
		}
		if seen[n] {
			b.errorAt(path, "Import "+quoted(n)+" was listed twice.")
		}
		seen[n] = true
		switch {
		case d == nil:
			b.u.Missing = append(b.u.Missing, n)
			if b.l.strict {
				b.errorAt(path, "Import "+quoted(n)+" was not found or had errors.")
			}
		case d.Err != nil || d == failed:
			b.errorAt(path, "Import "+quoted(n)+" was not found or had errors.")
		case d.File != nil:
			b.l.recordPublic(d.File, into)
		}
	}
}

func (l *linker) recordPublic(f *File, into map[*File]bool) {
	if into[f] {
		return
	}
	into[f] = true
	for _, i := range f.Public {
		if d := l.units[l.named(f.Deps[i])]; d != nil && d.Err == nil && d.File != nil {
			l.recordPublic(d.File, into)
		}
	}
}

func indexLocations(locs []*Location) map[string]*Location {
	m := make(map[string]*Location, len(locs))
	for _, l := range locs {
		k := pathKey(l.Path)
		if _, ok := m[k]; !ok {
			m[k] = l
		}
	}
	return m
}

func pathKey(p []int32) string {
	var sb strings.Builder
	for i, x := range p {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(int(x)))
	}
	return sb.String()
}

func cat(p []int32, xs ...int32) []int32 { return append(slices.Clone(p), xs...) }

func join(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "." + name
}

// errorAt records msg at the start of the location path names, the place
// protoc's parser recorded for the element; with no such location (or a nil
// path) protoc prints no position.
func (b *builder) errorAt(path []int32, msg string) {
	e := fileError{Message: msg}
	if path != nil {
		if l := b.locs[pathKey(path)]; l != nil {
			e.Line, e.Column = int(l.Span[0])+1, int(l.Span[1])+1
		}
	}
	b.errs = append(b.errs, e)
}

// errorAtFirst records msg at the first of paths with a location.
func (b *builder) errorAtFirst(msg string, paths ...[]int32) {
	for _, p := range paths {
		if b.locs[pathKey(p)] != nil {
			b.errorAt(p, msg)
			return
		}
	}
	b.errorAt(nil, msg)
}

func (b *builder) fieldTypeError(path []int32, msg string) {
	b.errorAtFirst(msg, cat(path, fldTypeName), cat(path, fldType))
}

// unresolved records the name no scope resolves at path, protoc's error
// unless the file may name unknown types.
func (b *builder) unresolved(path []int32, name string) {
	if !b.allowsUnknown() {
		b.errorAt(path, quoted(name)+" is not defined.")
		return
	}
	b.u.Unresolved = append(b.u.Unresolved, path)
}

// allowsUnknown reports whether a name no scope resolves is left as written:
// only in a link that is not strict and in a file with an import no root
// holds, which may declare it (schemas.5). protoc's AllowUnknownDependencies
// mode leaves every such name as written.
func (b *builder) allowsUnknown() bool { return !b.l.strict && len(b.u.Missing) > 0 }

// addSymbol is DescriptorBuilder::AddSymbol: false, with protoc's error at
// namePath, when the pool already holds full.
func (b *builder) addSymbol(full string, namePath []int32, s *symbol) bool {
	old, ok := b.l.syms[full]
	if !ok {
		b.l.syms[full] = s
		b.added = append(b.added, full)
		return true
	}
	switch scope, name, dotted := strings.CutLast(full, "."); {
	case old.file != b.f:
		b.errorAt(namePath, quoted(full)+" is already defined in file "+quoted(old.file.Name)+".")
	case !dotted:
		b.errorAt(namePath, quoted(full)+" is already defined.")
	default:
		b.errorAt(namePath, quoted(name)+" is already defined in "+quoted(scope)+".")
	}
	return false
}

func (b *builder) addPackage(name string) {
	old, ok := b.l.syms[name]
	if !ok {
		b.l.syms[name] = &symbol{kind: symPackage, file: b.f}
		b.added = append(b.added, name)
		if parent, _, dotted := strings.CutLast(name, "."); dotted {
			b.addPackage(parent)
		}
		return
	}
	if old.kind != symPackage {
		b.errorAt([]int32{fdPackage}, fmt.Sprintf(`%s is already defined (as something other than a package) in file %s.`, quoted(name), quoted(old.file.Name)))
	}
}

// hint is MessageHints: how many free numbers to suggest for a top-level
// message, and where the first reason was reported.
type hint struct {
	path  []int32
	count int32
}

func (b *builder) requestHint(m *Message, path []int32, numbers [2]int32) {
	if m == nil {
		return
	}
	fit := func(v int32) int32 { return min(max(v, 0), maxFieldNumber) }
	h := b.hints[m]
	if h == nil {
		h = &hint{path: path}
		b.hints[m] = h
		b.hinted = append(b.hinted, m)
	}
	h.count = fit(h.count + fit(fit(numbers[1])-fit(numbers[0])))
}

func (b *builder) buildMessage(m *Message, scope string, parent *Message, path []int32) {
	m.FullName, m.parent, m.file = join(scope, m.Name), parent, b.f
	for i, o := range m.Oneofs {
		b.addSymbol(m.FullName+"."+o.Name, cat(path, dpOneof, int32(i), nameField), &symbol{kind: symOneof, file: b.f})
	}
	for i, fl := range m.Fields {
		b.buildField(fl, m, m.FullName, false, cat(path, dpField, int32(i)))
	}
	for i, e := range m.Enums {
		b.buildEnum(e, m.FullName, m, cat(path, dpEnumType, int32(i)))
	}
	for i, r := range m.ExtRanges {
		b.checkRange(m, cat(path, dpExtRange, int32(i)), [2]int32{r.Start, r.End}, "Extension")
	}
	for i, x := range m.Extensions {
		b.buildField(x, m, m.FullName, true, cat(path, dpExtension, int32(i)))
	}
	for i, r := range m.ReservedRanges {
		b.checkRange(m, cat(path, dpReservedRange, int32(i)), r, "Reserved")
	}
	for i, n := range m.Nested {
		b.buildMessage(n, m.FullName, m, cat(path, dpNested, int32(i)))
	}
	b.addSymbol(m.FullName, cat(path, nameField), &symbol{kind: symMessage, file: b.f, msg: m})
	reserved := b.checkReserved(m, path)
	for i, fl := range m.Fields {
		b.checkFieldNumber(m, fl, cat(path, dpField, int32(i)), path, reserved)
	}
	b.checkExtRangeOverlaps(m, path)
}

// checkRange is BuildExtensionRange's and BuildReservedRange's checks of one
// [start, end) range; kind is "Extension" or "Reserved".
func (b *builder) checkRange(m *Message, path []int32, r [2]int32, kind string) {
	if r[0] <= 0 {
		b.requestHint(m, path, r)
		b.errorAt(path, kind+" numbers must be positive integers.")
	}
	if r[0] >= r[1] {
		b.errorAt(path, kind+" range end number must be greater than start number.")
	}
}

// checkReserved checks a message's reserved ranges and names against each
// other and returns the reserved names.
func (b *builder) checkReserved(m *Message, path []int32) map[string]bool {
	for i, r1 := range m.ReservedRanges {
		for _, r2 := range m.ReservedRanges[i+1:] {
			if r1[1] > r2[0] && r2[1] > r1[0] {
				b.errorAt(cat(path, dpReservedRange, int32(i)), fmt.Sprintf("Reserved range %d to %d overlaps with already-defined range %d to %d.", r2[0], r2[1]-1, r1[0], r1[1]-1))
			}
		}
	}
	reserved := map[string]bool{}
	for _, n := range m.ReservedNames {
		if reserved[n] {
			b.errorAt(cat(path, nameField), "Field name "+quoted(n)+" is reserved multiple times.")
		}
		reserved[n] = true
	}
	return reserved
}

// checkFieldNumber checks one field of m, at fieldPath, against m's
// extension ranges and reserved numbers and names.
func (b *builder) checkFieldNumber(m *Message, fl *Field, fieldPath, path []int32, reserved map[string]bool) {
	for j, r := range m.ExtRanges {
		if r.Start <= fl.Number && fl.Number < r.End {
			p := cat(path, dpExtRange, int32(j))
			b.requestHint(m, p, [2]int32{0, 1})
			b.errorAt(p, fmt.Sprintf("Extension range %d to %d includes field %s (%d).", r.Start, r.End-1, quoted(fl.Name), fl.Number))
		}
	}
	for j, r := range m.ReservedRanges {
		if r[0] <= fl.Number && fl.Number < r[1] {
			p := cat(path, dpReservedRange, int32(j))
			b.requestHint(m, p, [2]int32{0, 1})
			b.errorAt(p, fmt.Sprintf("Field %s uses reserved number %d.", quoted(fl.Name), fl.Number))
		}
	}
	if reserved[fl.Name] {
		b.errorAt(cat(fieldPath, nameField), "Field name "+quoted(fl.Name)+" is reserved.")
	}
}

func (b *builder) checkExtRangeOverlaps(m *Message, path []int32) {
	for i, r1 := range m.ExtRanges {
		p := cat(path, dpExtRange, int32(i))
		for _, r2 := range m.ReservedRanges {
			if r1.End > r2[0] && r2[1] > r1.Start {
				b.errorAt(p, fmt.Sprintf("Extension range %d to %d overlaps with reserved range %d to %d.", r1.Start, r1.End-1, r2[0], r2[1]-1))
			}
		}
		for _, r2 := range m.ExtRanges[i+1:] {
			if r1.End > r2.Start && r2.End > r1.Start {
				b.errorAt(p, fmt.Sprintf("Extension range %d to %d overlaps with already-defined range %d to %d.", r2.Start, r2.End-1, r1.Start, r1.End-1))
			}
		}
	}
}

func (b *builder) buildField(fl *Field, owner *Message, scope string, extension bool, path []int32) {
	fl.FullName, fl.owner = join(scope, fl.Name), owner
	if fl.OneofIndex != nil && !extension {
		fl.oneof = owner.Oneofs[*fl.OneofIndex]
	}
	if fl.Proto3Optional && b.edition != editionProto3 {
		b.fieldTypeError(path, "The [proto3_optional=true] option may only be set on proto3fields, not "+fl.FullName)
	}
	if fl.Label == labelRequired && extension {
		b.fieldTypeError(path, "The extension "+fl.FullName+" cannot be required.")
	}
	if fl.Default != nil && fl.Label == labelRepeated {
		b.errorAt(cat(path, fldDefault), "Repeated fields can't have default values.")
	}
	if fl.Default != nil && fl.Type == fieldGroup {
		b.errorAt(cat(path, fldDefault), "Messages can't have default values.")
	}
	switch {
	case fl.Number <= 0:
		b.requestHint(owner, cat(path, fldNumber), [2]int32{0, 1})
		b.errorAt(cat(path, fldNumber), "Field numbers must be positive integers.")
	case !extension && fl.Number > maxFieldNumber:
		b.requestHint(owner, cat(path, fldNumber), [2]int32{0, 1})
		b.errorAt(cat(path, fldNumber), fmt.Sprintf("Field numbers cannot be greater than %d.", maxFieldNumber))
	}
	b.addSymbol(fl.FullName, cat(path, nameField), &symbol{kind: symField, file: b.f, field: fl})
}

func (b *builder) buildEnum(e *Enum, scope string, parent *Message, path []int32) {
	e.FullName, e.parent = join(scope, e.Name), parent
	if len(e.Values) == 0 {
		b.errorAt(cat(path, nameField), "Enums must contain at least one value.")
	}
	b.buildEnumValues(e, scope, path)
	for i, r := range e.ReservedRanges {
		if r[0] > r[1] {
			b.errorAt(cat(path, edReservedRange, int32(i)), "Reserved range end number must be greater than start number.")
		}
	}
	b.addSymbol(e.FullName, cat(path, nameField), &symbol{kind: symEnum, file: b.f, enum: e})
	for i, r1 := range e.ReservedRanges {
		for _, r2 := range e.ReservedRanges[i+1:] {
			if r1[1] >= r2[0] && r2[1] >= r1[0] {
				b.errorAt(cat(path, edReservedRange, int32(i)), fmt.Sprintf("Reserved range %d to %d overlaps with already-defined range %d to %d.", r2[0], r2[1], r1[0], r1[1]))
			}
		}
	}
	b.checkEnumReserved(e, path)
}

// buildEnumValues adds each value as a sibling of its enum, as protoc's C++
// scoping does, and explains a clash that is only one within the enum.
func (b *builder) buildEnumValues(e *Enum, scope string, path []int32) {
	inner := map[string]bool{}
	for i, v := range e.Values {
		v.FullName = join(scope, v.Name)
		namePath := cat(path, edValue, int32(i), nameField)
		outer := b.addSymbol(v.FullName, namePath, &symbol{kind: symEnumValue, file: b.f, enum: e})
		if !inner[v.Name] && !outer {
			within := quoted(scope)
			if scope == "" {
				within = "the global scope"
			}
			b.errorAt(namePath, `Note that enum values use C++ scoping rules, meaning that enum values are siblings of their type, not children of it.  Therefore, "`+
				v.Name+`" must be unique within `+within+`, not just within "`+e.Name+`".`)
		}
		inner[v.Name] = true
	}
}

// checkEnumReserved checks an enum's reserved names, and its values against
// its reserved numbers and names.
func (b *builder) checkEnumReserved(e *Enum, path []int32) {
	reserved := map[string]bool{}
	for _, n := range e.ReservedNames {
		if reserved[n] {
			b.errorAt(cat(path, nameField), "Enum value "+quoted(n)+" is reserved multiple times.")
		}
		reserved[n] = true
	}
	for i, v := range e.Values {
		for j, r := range e.ReservedRanges {
			if r[0] <= v.Number && v.Number <= r[1] {
				b.errorAt(cat(path, edReservedRange, int32(j)), fmt.Sprintf("Enum value %s uses reserved number %d.", quoted(v.Name), v.Number))
			}
		}
		if reserved[v.Name] {
			b.errorAt(cat(path, edValue, int32(i), nameField), "Enum value "+quoted(v.Name)+" is reserved.")
		}
	}
}

func (b *builder) buildService(s *Service, path []int32) {
	s.FullName = join(b.f.Package, s.Name)
	for i, m := range s.Methods {
		m.FullName = s.FullName + "." + m.Name
		b.addSymbol(m.FullName, cat(path, sdMethod, int32(i), nameField), &symbol{kind: symMethod, file: b.f})
	}
	b.addSymbol(s.FullName, cat(path, nameField), &symbol{kind: symService, file: b.f})
}

func (b *builder) crossLink() {
	for i, m := range b.f.Messages {
		b.crossLinkMessage(m, []int32{fdMessageType, int32(i)})
	}
	for i, x := range b.f.Extensions {
		b.crossLinkField(x, []int32{fdExtension, int32(i)})
	}
	for i, s := range b.f.Services {
		for j, m := range s.Methods {
			b.crossLinkMethod(m, []int32{fdService, int32(i), sdMethod, int32(j)})
		}
	}
}

func (b *builder) crossLinkMessage(m *Message, path []int32) {
	for i, n := range m.Nested {
		b.crossLinkMessage(n, cat(path, dpNested, int32(i)))
	}
	for i, fl := range m.Fields {
		b.crossLinkField(fl, cat(path, dpField, int32(i)))
	}
	for i, x := range m.Extensions {
		b.crossLinkField(x, cat(path, dpExtension, int32(i)))
	}
	for i, o := range m.Oneofs {
		if !slices.ContainsFunc(m.Fields, func(fl *Field) bool { return fl.oneof == o }) {
			b.errorAt(cat(path, dpOneof, int32(i), nameField), "Oneof must have at least one field.")
		}
	}
}

// crossLinkField is DescriptorBuilder::CrossLinkField.
func (b *builder) crossLinkField(fl *Field, path []int32) {
	container := fl.owner
	if fl.Extendee != "" {
		container = nil
		switch s := b.lookup(fl.Extendee, fl.FullName, false); {
		case s == nil:
			b.unresolved(cat(path, fldExtendee), fl.Extendee)
		case s.kind != symMessage:
			b.errorAt(cat(path, fldExtendee), `"`+fl.Extendee+`" is not a message type.`)
			return
		case !b.visible(s):
			b.errorAt(cat(path, fldExtendee), b.visibilityError(s, "target of extend"))
			return
		default:
			container, fl.extendee, fl.Extendee = s.msg, s.msg, "."+s.msg.FullName
			if !slices.ContainsFunc(s.msg.ExtRanges, func(r *extRange) bool { return r.Start <= fl.Number && fl.Number < r.End }) {
				b.errorAt(cat(path, fldNumber), fmt.Sprintf(`%s does not declare %d as an extension number.`, quoted(s.msg.FullName), fl.Number))
			}
		}
	}
	if fl.TypeName != "" && !b.linkFieldType(fl, path) {
		return
	}
	if container == nil {
		return
	}
	key := container.FullName + "#" + strconv.Itoa(int(fl.Number))
	prev, used := b.byNumber[key]
	if !used {
		b.byNumber[key] = fl
		return
	}
	if fl.Extendee != "" {
		b.errorAt(cat(path, fldNumber), fmt.Sprintf(`Extension number %d has already been used in %s by extension %s.`, fl.Number, quoted(container.FullName), quoted(prev.FullName)))
		return
	}
	b.errorAt(cat(path, fldNumber), fmt.Sprintf(`Field number %d has already been used in %s by field %s. %s.`, fl.Number, quoted(container.FullName), quoted(prev.Name), nextFieldNumber(container)))
}

// linkFieldType resolves fl's type name; false when protoc stops linking fl
// on an error.
func (b *builder) linkFieldType(fl *Field, path []int32) bool {
	s := b.lookup(fl.TypeName, fl.FullName, true)
	if s == nil {
		b.unresolved(cat(path, fldTypeName), fl.TypeName)
		return true
	}
	if !b.visible(s) {
		b.fieldTypeError(path, b.visibilityError(s, ""))
		return false
	}
	written := fl.TypeName
	if fl.Type == 0 {
		switch s.kind {
		case symMessage:
			fl.Type = fieldMessage
		case symEnum:
			fl.Type = fieldEnum
		default:
			b.fieldTypeError(path, `"`+written+`" is not a type.`)
			return false
		}
	}
	if fl.Type == fieldEnum {
		if s.kind != symEnum {
			b.fieldTypeError(path, `"`+written+`" is not an enum type.`)
			return false
		}
		fl.enumType, fl.TypeName = s.enum, "."+s.enum.FullName
		b.checkEnumDefault(fl, path)
		return true
	}
	if s.kind != symMessage {
		b.fieldTypeError(path, `"`+written+`" is not a message type.`)
		return false
	}
	fl.msgType, fl.TypeName = s.msg, "."+s.msg.FullName
	if fl.Default != nil {
		b.errorAt(cat(path, fldDefault), "Messages can't have default values.")
	}
	return true
}

func (b *builder) checkEnumDefault(fl *Field, path []int32) {
	if fl.Default == nil {
		return
	}
	d := *fl.Default
	if !isIdentifier(d) {
		b.errorAt(cat(path, fldDefault), "Default value for an enum field must be an identifier.")
		return
	}
	if !slices.ContainsFunc(fl.enumType.Values, func(v *EnumValue) bool { return v.Name == d }) {
		b.errorAt(cat(path, fldDefault), fmt.Sprintf(`Enum type %s has no value named %s.`, quoted(fl.enumType.FullName), quoted(d)))
	}
}

func isIdentifier(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isAlphanumeric(s[i]) {
			return false
		}
	}
	return true
}

// nextFieldNumber is CrossLinkField's proposal: the first number past the
// run of used numbers and ranges that starts at 1.
func nextFieldNumber(m *Message) string {
	type span struct{ start, end int64 }
	used := make([]span, 0, len(m.Fields)+len(m.ExtRanges)+len(m.ReservedRanges))
	for _, fl := range m.Fields {
		used = append(used, span{int64(fl.Number), int64(fl.Number)})
	}
	for _, r := range m.ExtRanges {
		used = append(used, span{int64(r.Start), int64(r.End) - 1})
	}
	for _, r := range m.ReservedRanges {
		used = append(used, span{int64(r[0]), int64(r[1]) - 1})
	}
	slices.SortFunc(used, func(a, b span) int { return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end)) })
	used = slices.Compact(used) // protoc collects the spans in a set
	next := int64(1)
	for _, s := range used {
		if s.start > next || next > s.end {
			break
		}
		next = s.end + 1
	}
	if next > maxFieldNumber {
		return "There are no available field numbers"
	}
	return "Next available field number is " + strconv.FormatInt(next, 10)
}

func (b *builder) crossLinkMethod(m *Method, path []int32) {
	link := func(name *string, part int32) {
		switch s := b.lookup(*name, m.FullName, false); {
		case s == nil:
			b.unresolved(cat(path, part), *name)
		case s.kind != symMessage:
			b.errorAt(cat(path, part), `"`+*name+`" is not a message type.`)
		case !b.visible(s):
			b.errorAt(cat(path, part), b.visibilityError(s, ""))
		default:
			*name = "." + s.msg.FullName
		}
	}
	link(&m.Input, mdInput)
	link(&m.Output, mdOutput)
}

// suggestFieldNumbers is DescriptorBuilder::SuggestFieldNumbers, which only
// protoc's top-level messages get.
func (b *builder) suggestFieldNumbers() {
	for _, m := range b.hinted {
		if h := b.hints[m]; m.parent == nil && h.count > 0 {
			ids := suggest(usedOrdinals(m), min(3, h.count))
			b.errorAt(h.path, "Suggested field numbers for "+m.FullName+": "+strings.Join(ids, ", "))
		}
	}
}

// ordinals is a run of used field numbers, [from, to).
type ordinals struct{ from, to int64 }

func usedOrdinals(m *Message) []ordinals {
	var used []ordinals
	for _, fl := range slices.Concat(m.Fields, m.Extensions) {
		n := int64(fl.Number)
		switch {
		case n <= 0 || n > maxFieldNumber:
		case len(used) > 0 && n == used[len(used)-1].to:
			used[len(used)-1].to = n + 1
		default:
			used = append(used, ordinals{n, n + 1})
		}
	}
	add := func(r [2]int32) {
		from, to := int64(min(max(r[0], 0), maxFieldNumber+1)), int64(min(max(r[1], 0), maxFieldNumber+1))
		if from < to {
			used = append(used, ordinals{from, to})
		}
	}
	for _, r := range m.ReservedRanges {
		add(r)
	}
	for _, r := range m.ExtRanges {
		add([2]int32{r.Start, r.End})
	}
	used = append(used, ordinals{maxFieldNumber, maxFieldNumber + 1}, ordinals{19000, 19999})
	slices.SortFunc(used, func(a, b ordinals) int { return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to)) })
	return used
}

// suggest lists up to left free numbers, lowest first.
func suggest(used []ordinals, left int32) []string {
	var ids []string
	current := int64(1)
	for _, r := range used {
		for current < r.from && left > 0 {
			ids = append(ids, strconv.FormatInt(current, 10))
			current++
			left--
		}
		if left == 0 {
			break
		}
		current = max(current, r.to)
	}
	return ids
}

func inPackage(f *File, name string) bool {
	return f.Package == name || strings.HasPrefix(f.Package, name+".")
}

// findSymbol is DescriptorBuilder::FindSymbol: a symbol of this file or of a
// file it can see, an option import's only for an extension.
func (b *builder) findSymbol(name string) *symbol {
	s := b.l.syms[name]
	switch {
	case s == nil:
		return nil
	case s.file == b.f || b.deps[s.file] || b.optDeps[s.file] && s.kind == symField:
		return s
	case s.kind == symPackage && b.seesPackage(name):
		return s
	}
	return nil
}

// seesPackage reports whether the file or a file it can see is in package
// name, which the first file to declare it may not be.
func (b *builder) seesPackage(name string) bool {
	if inPackage(b.f, name) {
		return true
	}
	for g := range b.deps {
		if inPackage(g, name) {
			return true
		}
	}
	for g := range b.optDeps {
		if inPackage(g, name) {
			return true
		}
	}
	return false
}

// lookup is DescriptorBuilder::LookupSymbolNoPlaceholder: name resolved from
// the innermost scope of relativeTo outward, its first part found first.
func (b *builder) lookup(name, relativeTo string, typesOnly bool) *symbol {
	if strings.HasPrefix(name, ".") {
		return b.findSymbol(name[1:])
	}
	first, _, compound := strings.Cut(name, ".")
	scope := relativeTo
	for {
		i := strings.LastIndexByte(scope, '.')
		if i < 0 {
			return b.findSymbol(name)
		}
		scope = scope[:i]
		s := b.findSymbol(scope + "." + first)
		switch {
		case s == nil:
		case compound && s.isAggregate():
			return b.findSymbol(scope + "." + name)
		case !compound && (!typesOnly || s.isType()):
			return s
		}
	}
}

// visible is Symbol::IsVisibleFrom: a message or an enum of another file is
// visible only when its effective visibility is export.
func (b *builder) visible(s *symbol) bool {
	if !s.isType() || s.file == b.f {
		return true
	}
	return effectiveVisibility(s) == visibilityExport
}

// The FeatureSet.VisibilityFeature.DefaultSymbolVisibility numbers.
const (
	visibilityExportAll      = 1
	visibilityExportTopLevel = 2
)

// typeVisibility is a type symbol's visibility keyword, whether it is
// nested, and its resolved default_symbol_visibility feature.
func typeVisibility(s *symbol) (keyword visibility, nested bool, feature int32) {
	if s.kind == symMessage {
		return s.msg.Visibility, s.msg.parent != nil, s.msg.features[featureDefaultSymbolVisibility]
	}
	return s.enum.Visibility, s.enum.parent != nil, s.enum.features[featureDefaultSymbolVisibility]
}

func effectiveVisibility(s *symbol) visibility {
	keyword, nested, feature := typeVisibility(s)
	if keyword != visibilityUnset {
		return keyword
	}
	switch feature {
	case visibilityExportAll, 0:
		return visibilityExport
	case visibilityExportTopLevel:
		if nested {
			return visibilityLocal
		}
		return visibilityExport
	}
	return visibilityLocal
}

// visibilityError is Symbol::GetVisibilityError, with its double space
// before "is" when usage is empty.
func (b *builder) visibilityError(s *symbol, usage string) string {
	keyword, _, feature := typeVisibility(s)
	reason := "It is explicitly marked 'local'"
	if keyword != visibilityLocal {
		reason = "It defaulted to local from file-level 'option features.default_symbol_visibility = '" + b.l.featureValueName(featureDefaultSymbolVisibility, feature) + "';"
	}
	return `Symbol "` + s.fullName() + `", defined in "` + s.file.Name + `" ` + usage + ` is not visible from "` + b.f.Name + `". ` + reason + " and cannot be accessed outside its own file"
}

// checkReservedNumbers is protoc's command-line check of an input file: no
// field or extension may use the numbers kept for the library.
func checkReservedNumbers(u *Unit) {
	var errs Errors
	check := func(fl *Field) {
		if fl.Number >= 19000 && fl.Number <= 19999 {
			errs = append(errs, fileError{Message: "Field numbers 19000 through 19999 are reserved for the protocol buffer library implementation."})
		}
	}
	var walk func(m *Message)
	walk = func(m *Message) {
		for _, fl := range m.Fields {
			check(fl)
		}
		for _, n := range m.Nested {
			walk(n)
		}
		for _, x := range m.Extensions {
			check(x)
		}
	}
	for _, m := range u.File.Messages {
		walk(m)
	}
	for _, x := range u.File.Extensions {
		check(x)
	}
	if len(errs) > 0 {
		u.Err = errs
	}
}
