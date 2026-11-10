package proto

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// The parser is google/protobuf/compiler/parser.cc (v36.2) step for step:
// its locations, its comment attachment, its error recovery and its messages.

// Field numbers of descriptor.proto, the components of a location's path.
const (
	nameField        = 1 // every named descriptor's name is its field 1
	fdPackage        = 2
	fdDependency     = 3
	fdMessageType    = 4
	fdEnumType       = 5
	fdService        = 6
	fdExtension      = 7
	fdOptions        = 8
	fdPublicDep      = 10
	fdWeakDep        = 11
	fdSyntax         = 12
	fdOptionDep      = 15
	dpField          = 2
	dpNested         = 3
	dpEnumType       = 4
	dpExtRange       = 5
	dpExtension      = 6
	dpOptions        = 7
	dpOneof          = 8
	dpReservedRange  = 9
	dpReservedName   = 10
	erOptions        = 3
	fldExtendee      = 2
	fldNumber        = 3
	fldLabel         = 4
	fldType          = 5
	fldTypeName      = 6
	fldDefault       = 7
	fldOptions       = 8
	fldJSONName      = 10
	odOptions        = 2
	edValue          = 2
	edOptions        = 3
	edReservedRange  = 4
	edReservedName   = 5
	evNumber         = 2
	evOptions        = 3
	sdMethod         = 2
	sdOptions        = 3
	mdInput          = 2
	mdOutput         = 3
	mdOptions        = 4
	mdClientStream   = 5
	mdServerStream   = 6
	rangeStart       = 1
	rangeEnd         = 2
	uoName           = 2
	uoIdent          = 3
	uoPos            = 4
	uoNeg            = 5
	uoDouble         = 6
	uoString         = 7
	uoAggregate      = 8
	npNamePart       = 1
	uninterpretedNum = 999
)

// The syntax a File states.
const (
	syntaxProto2   = "proto2"
	syntaxProto3   = "proto3"
	syntaxEditions = "editions"
)

// The Edition numbers the parser compares against.
const (
	edition2023     = 1000
	edition2024     = 1001
	editionUnstable = 9999
)

// editionNumbers is descriptor.proto's Edition enum by the name an edition
// statement spells, less EDITION_UNKNOWN, PROTO2 and PROTO3, which it refuses.
var editionNumbers = map[string]int{
	"LEGACY": 900, "2023": edition2023, "2024": edition2024, "2026": 1002,
	"UNSTABLE": editionUnstable, "1_TEST_ONLY": 1, "2_TEST_ONLY": 2, "99997_TEST_ONLY": 99997,
	"99998_TEST_ONLY": 99998, "99999_TEST_ONLY": 99999, "MAX": math.MaxInt32,
}

const (
	maxNestingDepth  = 32        // MaxMessageDeclarationNestingDepth
	maxFieldNumber   = 536870911 // FieldDescriptor::kMaxNumber
	maxRangeSentinel = -1        // an end written as max, until the block knows whether it is a message set
)

// parseFile parses one source file. On a syntax error it returns the file as far
// as protoc's error recovery built it, and its Errors.
func parseFile(name string, src []byte) (*File, error) {
	p := &parser{t: newTokenizer(src), file: &File{Name: name}}
	p.parse()
	p.file.LineComments = p.t.lineComments
	if len(p.t.errs) > 0 {
		return p.file, p.t.errs
	}
	return p.file, nil
}

// count is len(s) as a descriptor index; protoc's own indexes are C++ ints,
// and no source holds MaxInt32 declarations of one kind.
func count[T any](s []T) int32 { return index(len(s)) }

// index is i, a slice index, as a descriptor index, clamped as count is.
func index(i int) int32 {
	switch {
	case i < 0:
		return 0
	case i > math.MaxInt32:
		return math.MaxInt32
	}
	return int32(i)
}

type parser struct {
	t                *tokenizer
	file             *File
	syntax           string
	upcomingDoc      string
	upcomingDetached []string
	edition          int
	depth            int
	hasPackage       bool
}

// rec is parser.cc's LocationRecorder: a location opened at the current
// token, ended at the previous one unless ended explicitly.
type rec struct {
	p    *parser
	loc  *Location
	locs *[]*Location
}

func (p *parser) newLoc(locs *[]*Location, path []int32) *Location {
	l := &Location{Path: slices.Clone(path), Span: []int32{p.t.current.line, p.t.current.column}}
	*locs = append(*locs, l)
	return l
}

func (r *rec) child(path ...int32) *rec { return r.childIn(r.locs, path...) }

func (r *rec) childIn(locs *[]*Location, path ...int32) *rec {
	return &rec{p: r.p, loc: r.p.newLoc(locs, append(slices.Clone(r.loc.Path), path...)), locs: locs}
}

func (r *rec) add(component int32) { r.loc.Path = append(r.loc.Path, component) }

func (r *rec) startAtToken(t token) { r.loc.Span[0], r.loc.Span[1] = t.line, t.column }

func (r *rec) startAtRec(o *rec) { r.loc.Span[0], r.loc.Span[1] = o.loc.Span[0], o.loc.Span[1] }

func (r *rec) endAt(t token) {
	if t.line != r.loc.Span[0] {
		r.loc.Span = append(r.loc.Span, t.line)
	}
	r.loc.Span = append(r.loc.Span, t.endColumn)
}

func (r *rec) end() {
	if len(r.loc.Span) <= 2 {
		r.endAt(r.p.t.previous)
	}
}

func (r *rec) attach(leading, trailing string, detached []string) {
	r.loc.Leading = leading
	r.loc.Trailing = trailing
	r.loc.Detached = append(r.loc.Detached, detached...)
}

func (p *parser) lookingAt(s string) bool           { return p.t.current.text == s }
func (p *parser) lookingAtType(kind tokenType) bool { return p.t.current.kind == kind }
func (p *parser) atEnd() bool                       { return p.lookingAtType(tokenEnd) }
func (p *parser) next()                             { p.t.next() }

func (p *parser) recordErrorAt(t token, msg string) { p.t.errorAt(t.line, t.column, msg) }

func (p *parser) recordError(msg string) { p.recordErrorAt(p.t.current, msg) }

func (p *parser) tryConsume(s string) bool {
	if p.lookingAt(s) {
		p.next()
		return true
	}
	return false
}

func (p *parser) consume(s string) bool { return p.consumeOr(s, `Expected "`+s+`".`) }

func (p *parser) consumeOr(s, msg string) bool {
	if p.tryConsume(s) {
		return true
	}
	p.recordError(msg)
	return false
}

func (p *parser) consumeIdent(out *string, msg string) bool {
	if p.lookingAtType(tokenIdentifier) {
		*out = p.t.current.text
		p.next()
		return true
	}
	p.recordError(msg)
	return false
}

// consumeNameAt reads a declaration's name under a location of its own.
func (p *parser) consumeNameAt(parent *rec, out *string, msg string) bool {
	loc := parent.child(nameField)
	defer loc.end()
	return p.consumeIdent(out, msg)
}

func (p *parser) consumeIntegerAt(parent *rec, field int32, out *int32, msg string) bool {
	loc := parent.child(field)
	defer loc.end()
	return p.consumeInteger(out, msg)
}

func (p *parser) consumeSignedIntegerAt(parent *rec, field int32, out *int32, msg string) bool {
	loc := parent.child(field)
	defer loc.end()
	return p.consumeSignedInteger(out, msg)
}

func (p *parser) consumeInteger(out *int32, msg string) bool {
	if !p.lookingAtType(tokenInteger) {
		p.recordError(msg)
		return false
	}
	v, ok := parseInteger(p.t.current.text, math.MaxInt32)
	if !ok {
		p.recordError("Integer out of range.")
	}
	*out = int32(v) //nolint:gosec // v is at most MaxInt32
	p.next()
	return true
}

func (p *parser) consumeSignedInteger(out *int32, msg string) bool {
	limit := uint64(math.MaxInt32)
	negative := p.tryConsume("-")
	if negative {
		limit++
	}
	var v uint64
	if !p.consumeInteger64(limit, &v, msg) {
		return false
	}
	if negative {
		*out = int32(-int64(v))
	} else {
		*out = int32(v)
	}
	return true
}

func (p *parser) consumeInteger64(limit uint64, out *uint64, msg string) bool {
	if !p.lookingAtType(tokenInteger) {
		p.recordError(msg)
		return false
	}
	v, ok := parseInteger(p.t.current.text, limit)
	if !ok {
		p.recordError("Integer out of range.")
	}
	*out = v
	p.next()
	return true
}

func (p *parser) tryConsumeInteger64(limit uint64, out *uint64) bool {
	if !p.lookingAtType(tokenInteger) {
		return false
	}
	v, ok := parseInteger(p.t.current.text, limit)
	if !ok {
		return false
	}
	*out = v
	p.next()
	return true
}

func (p *parser) consumeNumber(out *float64, msg string) bool {
	switch {
	case p.lookingAtType(tokenFloat):
		*out = parseFloat(p.t.current.text)
	case p.lookingAtType(tokenInteger):
		if v, ok := parseInteger(p.t.current.text, math.MaxUint64); ok {
			*out = float64(v)
		} else if p.t.current.text[0] == '0' {
			p.recordError("Integer out of range.")
		} else {
			*out = parseFloat(p.t.current.text)
		}
	case p.lookingAt("inf"):
		*out = math.Inf(1)
	case p.lookingAt("nan"):
		*out = math.NaN()
	default:
		p.recordError(msg)
		return false
	}
	p.next()
	return true
}

func (p *parser) consumeString(out *string, msg string) bool {
	if !p.lookingAtType(tokenString) {
		p.recordError(msg)
		return false
	}
	var sb strings.Builder
	for p.lookingAtType(tokenString) {
		parseStringAppend(p.t.current.text, &sb)
		p.next()
	}
	*out = sb.String()
	return true
}

// tryConsumeEndOfDecl consumes text, which ends a declaration, and hands the
// comments read up to the next token to loc, or keeps them for the next
// declaration when loc is nil.
func (p *parser) tryConsumeEndOfDecl(text string, loc *rec) bool {
	if !p.lookingAt(text) {
		return false
	}
	var leading, trailing string
	var detached []string
	p.t.nextWithComments(&trailing, &detached, &leading)
	leading, p.upcomingDoc = p.upcomingDoc, leading
	switch {
	case loc != nil:
		detached, p.upcomingDetached = p.upcomingDetached, detached
		loc.attach(leading, trailing, detached)
	case text == "}":
		p.upcomingDetached = detached
	default:
		p.upcomingDetached = append(p.upcomingDetached, detached...)
	}
	return true
}

func (p *parser) consumeEndOfDecl(text string, loc *rec) bool {
	if p.tryConsumeEndOfDecl(text, loc) {
		return true
	}
	p.recordError(`Expected "` + text + `".`)
	return false
}

func (p *parser) skipStatement() {
	for !p.atEnd() {
		if p.lookingAtType(tokenSymbol) {
			switch {
			case p.tryConsumeEndOfDecl(";", nil):
				return
			case p.tryConsume("{"):
				p.skipRestOfBlock()
				return
			case p.lookingAt("}"):
				return
			}
		}
		p.next()
	}
}

func (p *parser) skipRestOfBlock() {
	depth := 1
	for !p.atEnd() {
		if p.lookingAtType(tokenSymbol) {
			if p.tryConsumeEndOfDecl("}", nil) {
				depth--
				if depth == 0 {
					return
				}
			} else if p.tryConsume("{") {
				depth++
			}
		}
		p.next()
	}
}

func (p *parser) parse() {
	if p.lookingAtType(tokenStart) {
		p.t.nextWithComments(nil, &p.upcomingDetached, &p.upcomingDoc)
	}
	root := &rec{p: p, loc: p.newLoc(&p.file.Locations, nil), locs: &p.file.Locations}
	defer root.end()
	if p.lookingAt("syntax") || p.lookingAt("edition") {
		if !p.parseSyntax(root) {
			return
		}
		p.file.Syntax = p.syntax
	} else {
		p.syntax = syntaxProto2
	}
	for !p.atEnd() {
		if p.parseTopLevel(root) {
			continue
		}
		p.skipStatement()
		if p.lookingAt("}") {
			p.recordError(`Unmatched "}".`)
			p.t.nextWithComments(nil, &p.upcomingDetached, &p.upcomingDoc)
		}
	}
}

func (p *parser) parseSyntax(parent *rec) bool {
	loc := parent.child(fdSyntax)
	defer loc.end()
	hasEdition := p.lookingAt("edition")
	p.next()
	if !p.consume("=") {
		return false
	}
	syntaxToken := p.t.current
	var s string
	if !p.consumeString(&s, "Expected syntax identifier.") || !p.consumeEndOfDecl(";", loc) {
		return false
	}
	if hasEdition {
		n, known := editionNumbers[s]
		if !known {
			p.recordErrorAt(syntaxToken, `Unknown edition "`+s+`".`)
			return false
		}
		p.edition = n
		p.syntax = syntaxEditions
		p.file.Edition = s
		return true
	}
	p.syntax = s
	if s != syntaxProto2 && s != syntaxProto3 {
		p.recordErrorAt(syntaxToken, `Unrecognized syntax identifier "`+s+`".  This parser only recognizes "proto2" and "proto3".`)
		return false
	}
	return true
}

func (p *parser) parseVisibility(out *visibility) bool {
	if p.syntax != syntaxEditions || p.edition <= edition2023 {
		return true
	}
	if p.tryConsume("export") {
		*out = visibilityExport
	} else if p.tryConsume("local") {
		*out = visibilityLocal
	}
	if *out != visibilityUnset && !p.lookingAt("message") && !p.lookingAt("enum") {
		p.recordError("'local' and 'export' visibility modifiers are valid only on 'message' and 'enum'")
		return false
	}
	return true
}

func (p *parser) parseTopLevel(root *rec) bool {
	f := p.file
	if p.tryConsumeEndOfDecl(";", nil) {
		return true
	}
	var vis visibility
	if (p.lookingAt("export") || p.lookingAt("local")) && !p.parseVisibility(&vis) {
		return false
	}
	switch {
	case p.lookingAt("message"):
		loc := root.child(fdMessageType, count(f.Messages))
		defer loc.end()
		p.depth = maxNestingDepth
		m := &Message{}
		f.Messages = append(f.Messages, m)
		return p.parseMessageDef(m, vis, loc)
	case p.lookingAt("enum"):
		loc := root.child(fdEnumType, count(f.Enums))
		defer loc.end()
		e := &Enum{}
		f.Enums = append(f.Enums, e)
		return p.parseEnumDef(e, vis, loc)
	case p.lookingAt("service"):
		loc := root.child(fdService, count(f.Services))
		defer loc.end()
		s := &Service{}
		f.Services = append(f.Services, s)
		return p.parseService(s, loc)
	case p.lookingAt("extend"):
		loc := root.child(fdExtension)
		defer loc.end()
		return p.parseExtend(&f.Extensions, &f.Messages, root, fdMessageType, loc)
	case p.lookingAt("import"):
		return p.parseImport(root)
	case p.lookingAt("package"):
		return p.parsePackage(root)
	case p.lookingAt("option"):
		loc := root.child(fdOptions)
		defer loc.end()
		if f.Options == nil {
			f.Options = &Options{}
		}
		return p.parseOption(f.Options, loc, true)
	}
	p.recordError(`Expected top-level statement (e.g. "message").`)
	return false
}

func (p *parser) parseMessageDef(m *Message, vis visibility, mloc *rec) bool {
	p.depth--
	defer func() { p.depth++ }()
	if p.depth <= 0 {
		p.recordError("Reached maximum recursion limit for nested messages.")
		return false
	}
	if !p.consume("message") {
		return false
	}
	if !p.consumeNameAt(mloc, &m.Name, "Expected message name.") || !p.parseMessageBlock(m, mloc) {
		return false
	}
	if p.syntax == syntaxProto3 {
		generateSyntheticOneofs(m)
	}
	m.Visibility = vis
	return true
}

// generateSyntheticOneofs gives each proto3 optional field its own oneof,
// named as protoc names it.
func generateSyntheticOneofs(m *Message) {
	names := map[string]bool{}
	for _, f := range m.Fields {
		names[f.Name] = true
	}
	for _, o := range m.Oneofs {
		names[o.Name] = true
	}
	for _, f := range m.Fields {
		if !f.Proto3Optional {
			continue
		}
		n := f.Name
		if n == "" || n[0] != '_' {
			n = "_" + n
		}
		for names[n] {
			n = "X" + n
		}
		names[n] = true
		idx := count(m.Oneofs)
		f.OneofIndex = &idx
		m.Oneofs = append(m.Oneofs, &oneofDecl{Name: n})
	}
}

func (u *uninterpretedOption) isTrue() bool { return u.Identifier != nil && *u.Identifier == litTrue }

func isMessageSet(m *Message) bool {
	if m.Options == nil {
		return false
	}
	for _, u := range m.Options.Uninterpreted {
		if len(u.Name) == 1 && !u.Name[0].IsExtension && u.Name[0].Part == "message_set_wire_format" && u.isTrue() {
			return true
		}
	}
	return false
}

func (p *parser) parseMessageBlock(m *Message, mloc *rec) bool {
	if !p.consumeEndOfDecl("{", mloc) {
		return false
	}
	for !p.tryConsumeEndOfDecl("}", nil) {
		if p.atEnd() {
			p.recordError("Reached end of input in message definition (missing '}').")
			return false
		}
		if !p.parseMessageStatement(m, mloc) {
			p.skipStatement()
		}
	}
	limit := int32(maxFieldNumber + 1)
	if isMessageSet(m) {
		limit = math.MaxInt32
	}
	for _, r := range m.ExtRanges {
		if r.End == maxRangeSentinel {
			r.End = limit
		}
	}
	for i := range m.ReservedRanges {
		if m.ReservedRanges[i][1] == maxRangeSentinel {
			m.ReservedRanges[i][1] = limit
		}
	}
	return p.validateMessage(m)
}

func (p *parser) validateMessage(m *Message) bool {
	if m.Options == nil {
		return true
	}
	for _, u := range m.Options.Uninterpreted {
		if len(u.Name) > 0 && !u.Name[0].IsExtension && u.Name[0].Part == "map_entry" {
			p.t.errs = append(p.t.errs, fileError{Line: u.Line, Column: u.Column, Message: "map_entry should not be set explicitly. Use map<KeyType, ValueType> instead."})
			return false
		}
	}
	return true
}

func (p *parser) parseMessageStatement(m *Message, mloc *rec) bool {
	if p.tryConsumeEndOfDecl(";", nil) {
		return true
	}
	var vis visibility
	if (p.lookingAt("export") || p.lookingAt("local")) && !p.parseVisibility(&vis) {
		return false
	}
	switch {
	case p.lookingAt("message"):
		loc := mloc.child(dpNested, count(m.Nested))
		defer loc.end()
		n := &Message{}
		m.Nested = append(m.Nested, n)
		return p.parseMessageDef(n, vis, loc)
	case p.lookingAt("enum"):
		loc := mloc.child(dpEnumType, count(m.Enums))
		defer loc.end()
		e := &Enum{}
		m.Enums = append(m.Enums, e)
		return p.parseEnumDef(e, vis, loc)
	case p.lookingAt("extensions"):
		loc := mloc.child(dpExtRange)
		defer loc.end()
		return p.parseExtensions(m, loc)
	case p.lookingAt("reserved"):
		return p.parseReserved(&m.ReservedRanges, &m.ReservedNames, mloc, dpReservedRange, dpReservedName, messageReserved)
	case p.lookingAt("extend"):
		loc := mloc.child(dpExtension)
		defer loc.end()
		return p.parseExtend(&m.Extensions, &m.Nested, mloc, dpNested, loc)
	case p.lookingAt("option"):
		loc := mloc.child(dpOptions)
		defer loc.end()
		if m.Options == nil {
			m.Options = &Options{}
		}
		return p.parseOption(m.Options, loc, true)
	case p.lookingAt("oneof"):
		idx := count(m.Oneofs)
		loc := mloc.child(dpOneof, idx)
		defer loc.end()
		o := &oneofDecl{}
		m.Oneofs = append(m.Oneofs, o)
		return p.parseOneof(o, m, idx, loc, mloc)
	}
	loc := mloc.child(dpField, count(m.Fields))
	defer loc.end()
	f := &Field{}
	m.Fields = append(m.Fields, f)
	return p.parseMessageField(f, &m.Nested, mloc, dpNested, loc)
}

func (p *parser) parseLabel(label *fieldLabel, floc *rec) bool {
	if !p.lookingAt("optional") && !p.lookingAt("repeated") && !p.lookingAt("required") {
		return false
	}
	if p.syntax == syntaxEditions {
		switch {
		case p.lookingAt("optional"):
			p.recordError(`Label "optional" is not supported in editions. By default, all singular fields have presence unless features.field_presence is set.`)
		case p.lookingAt("required"):
			p.recordError(`Label "required" is not supported in editions, use features.field_presence = LEGACY_REQUIRED.`)
		}
	}
	loc := floc.child(fldLabel)
	defer loc.end()
	switch {
	case p.tryConsume("optional"):
		*label = labelOptional
	case p.tryConsume("repeated"):
		*label = labelRepeated
	default:
		p.next()
		*label = labelRequired
	}
	return true
}

func (p *parser) parseMessageField(f *Field, msgs *[]*Message, parent *rec, nestedNum int32, floc *rec) bool {
	var label fieldLabel
	if p.parseLabel(&label, floc) {
		f.Label = label
		if label == labelOptional && p.syntax == syntaxProto3 {
			f.Proto3Optional = true
		}
	}
	return p.parseMessageFieldNoLabel(f, msgs, parent, nestedNum, floc)
}

type mapField struct {
	keyName, valueName string
	keyType, valueType fieldType
	isMap              bool
}

var scalarTypes = map[string]fieldType{
	typeDouble: fieldDouble, typeFloat: fieldFloat, typeUint64: fieldUint64, "fixed64": fieldFixed64, "fixed32": fieldFixed32,
	"bool": fieldBool, "string": fieldString, "group": fieldGroup, "bytes": fieldBytes, typeUint32: fieldUint32,
	"sfixed32": fieldSfixed32, "sfixed64": fieldSfixed64, typeInt32: fieldInt32, typeInt64: fieldInt64,
	"sint32": fieldSint32, "sint64": fieldSint64,
}

// unstableScalarTypes is the type table of EDITION_UNSTABLE, where the plain
// integer names are fixed-width.
var unstableScalarTypes = map[string]fieldType{
	typeDouble: fieldDouble, typeFloat: fieldFloat, typeUint64: fieldFixed64, "bool": fieldBool, "string": fieldString,
	"group": fieldGroup, "bytes": fieldBytes, typeUint32: fieldFixed32, typeInt32: fieldSfixed32, typeInt64: fieldSfixed64,
	"varint32": fieldInt32, "varint64": fieldInt64, "uvarint32": fieldUint32, "uvarint64": fieldUint64,
	"zigzag32": fieldSint32, "zigzag64": fieldSint64,
}

// unstableSpelling returns the EDITION_UNSTABLE name of the type a scalar
// type name spells, when that edition drops the name.
func unstableSpelling(name string) (string, bool) {
	t, scalar := scalarTypes[name]
	if _, kept := unstableScalarTypes[name]; !scalar || kept {
		return "", false
	}
	for spelling, u := range unstableScalarTypes {
		if u == t {
			return spelling, true
		}
	}
	return "", false
}

func (p *parser) useFixedWire() bool {
	return p.syntax == syntaxEditions && p.edition == editionUnstable
}

func (p *parser) parseType(typ *fieldType, name *string) bool {
	table := scalarTypes
	if p.useFixedWire() {
		table = unstableScalarTypes
	}
	text := p.t.current.text
	if t, ok := table[text]; ok {
		if p.syntax == syntaxEditions && t == fieldGroup {
			p.recordError("Group syntax is no longer supported in editions. To get group behavior you can specify features.message_encoding = DELIMITED on a message field.")
		}
		*typ = t
		p.next()
		return true
	}
	if replacement, ok := unstableSpelling(text); ok && p.useFixedWire() {
		p.recordError(fmt.Sprintf("Type '%s' is obsolete in unstable editions, use '%s' instead.", text, replacement))
		p.next()
		return true
	}
	return p.parseUserDefinedType(name)
}

func (p *parser) parseUserDefinedType(name *string) bool {
	*name = ""
	if _, ok := scalarTypes[p.t.current.text]; ok {
		p.recordError("Expected message type.")
		*name = p.t.current.text
		p.next()
		return true
	}
	var sb strings.Builder
	if p.tryConsume(".") {
		sb.WriteByte('.')
	}
	var id string
	if !p.consumeIdent(&id, "Expected type name.") {
		return false
	}
	sb.WriteString(id)
	for p.tryConsume(".") {
		sb.WriteByte('.')
		if !p.consumeIdent(&id, "Expected identifier.") {
			return false
		}
		sb.WriteString(id)
	}
	*name = sb.String()
	return true
}

func (p *parser) parseFieldType(f *Field, mf *mapField, floc *rec) bool {
	loc := floc.child()
	defer loc.end()
	typeParsed := false
	typ := fieldInt32
	var typeName string
	if p.tryConsume("map") {
		if p.lookingAt("<") {
			mf.isMap = true
			return p.parseMapType(mf, f, loc)
		}
		typeParsed = true
		typeName = "map"
	}
	if f.Label == 0 && (p.syntax == syntaxEditions || p.syntax == syntaxProto3) {
		f.Label = labelOptional
	}
	if f.Label == 0 {
		p.recordError(`Expected "required", "optional", or "repeated".`)
		f.Label = labelOptional
	}
	if !typeParsed && !p.parseType(&typ, &typeName) {
		return false
	}
	if typeName == "" {
		loc.add(fldType)
		f.Type = typ
	} else {
		loc.add(fldTypeName)
		f.TypeName = typeName
	}
	return true
}

func (p *parser) parseMessageFieldNoLabel(f *Field, msgs *[]*Message, parent *rec, nestedNum int32, floc *rec) bool {
	var mf mapField
	if !p.parseFieldType(f, &mf, floc) {
		return false
	}
	nameToken := p.t.current
	if !p.consumeNameAt(floc, &f.Name, "Expected field name.") ||
		!p.consumeOr("=", "Missing field number.") ||
		!p.consumeIntegerAt(floc, fldNumber, &f.Number, "Expected field number.") ||
		!p.parseFieldOptions(f, floc) {
		return false
	}
	if f.Type == fieldGroup {
		if !p.parseGroup(f, msgs, parent, nestedNum, floc, nameToken) {
			return false
		}
	} else if !p.consumeEndOfDecl(";", floc) {
		return false
	}
	if mf.isMap {
		generateMapEntry(&mf, f, msgs)
	}
	return true
}

// parseGroup reads a group's body as the nested message the group declares,
// named as written, the field taking the lowercased name.
func (p *parser) parseGroup(f *Field, msgs *[]*Message, parent *rec, nestedNum int32, floc *rec, nameToken token) bool {
	g := parent.child()
	defer g.end()
	g.startAtRec(floc)
	g.add(nestedNum)
	g.add(count(*msgs))
	group := &Message{Name: f.Name}
	*msgs = append(*msgs, group)
	for _, l := range []*rec{g.child(nameField), floc.child(fldTypeName)} {
		l.startAtToken(nameToken)
		l.endAt(nameToken)
	}
	if group.Name[0] < 'A' || group.Name[0] > 'Z' {
		p.recordErrorAt(nameToken, "Group names must start with a capital letter.")
	}
	f.Name = strings.ToLower(f.Name)
	f.TypeName = group.Name
	if !p.lookingAt("{") {
		p.recordError("Missing group body.")
		return false
	}
	return p.parseMessageBlock(group, g)
}

func (p *parser) parseMapType(mf *mapField, f *Field, loc *rec) bool {
	switch {
	case f.OneofIndex != nil:
		p.recordError("Map fields are not allowed in oneofs.")
		return false
	case f.Label != 0:
		p.recordError("Field labels (required/optional/repeated) are not allowed on map fields.")
		return false
	case f.Extendee != "":
		p.recordError("Map fields are not allowed to be extensions.")
		return false
	}
	f.Label = labelRepeated
	if !p.consume("<") || !p.parseType(&mf.keyType, &mf.keyName) || !p.consume(",") ||
		!p.parseType(&mf.valueType, &mf.valueName) || !p.consume(">") {
		return false
	}
	loc.add(fldTypeName)
	return true
}

func mapEntryName(field string) string {
	var sb strings.Builder
	capNext := true
	for i := range len(field) {
		c := field[i]
		switch {
		case c == '_':
			capNext = true
		case capNext:
			if c >= 'a' && c <= 'z' {
				c = c - 'a' + 'A'
			}
			sb.WriteByte(c)
			capNext = false
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteString("Entry")
	return sb.String()
}

// generateMapEntry adds the message a map field's entries are, carrying the
// field's enforce_utf8 to a string key or value and its features to both.
func generateMapEntry(mf *mapField, f *Field, msgs *[]*Message) {
	name := mapEntryName(f.Name)
	f.TypeName = name
	key := &Field{Name: "key", Label: labelOptional, Number: 1, Type: mf.keyType, TypeName: mf.keyName}
	val := &Field{Name: "value", Label: labelOptional, Number: 2, Type: mf.valueType, TypeName: mf.valueName}
	if f.Options != nil {
		for _, u := range f.Options.Uninterpreted {
			carryMapOption(u, key, val)
		}
	}
	*msgs = append(*msgs, &Message{Name: name, MapEntry: true, Fields: []*Field{key, val}})
}

func carryMapOption(u *uninterpretedOption, key, val *Field) {
	if u.Name[0].IsExtension {
		return
	}
	switch {
	case len(u.Name) == 1 && u.Name[0].Part == "enforce_utf8":
		for _, entry := range []*Field{key, val} {
			if entry.Type == fieldString {
				addUninterpreted(entry, u)
			}
		}
	case u.Name[0].Part == featuresOption:
		addUninterpreted(key, u)
		addUninterpreted(val, u)
	}
}

func addUninterpreted(f *Field, u *uninterpretedOption) {
	if f.Options == nil {
		f.Options = &Options{}
	}
	f.Options.Uninterpreted = append(f.Options.Uninterpreted, u)
}

func (p *parser) parseFieldOptions(f *Field, floc *rec) bool {
	if !p.lookingAt("[") {
		return true
	}
	loc := floc.child(fldOptions)
	defer loc.end()
	p.next()
	for {
		var ok bool
		switch {
		case p.lookingAt("default"):
			ok = p.parseDefault(f, floc)
		case p.lookingAt("json_name"):
			ok = p.parseJSONName(f, floc)
		default:
			if f.Options == nil {
				f.Options = &Options{}
			}
			ok = p.parseOption(f.Options, loc, false)
		}
		if !ok {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	return p.consume("]")
}

func (p *parser) parseDefault(f *Field, floc *rec) bool {
	if f.Default != nil {
		p.recordError(`Already set option "default".`)
		f.Default = nil
	}
	if !p.consume("default") || !p.consume("=") {
		return false
	}
	loc := floc.child(fldDefault)
	defer loc.end()
	var value string
	defer func() { f.Default = &value }()
	return p.parseDefaultValue(f.Type, &value)
}

// parseDefaultValue reads a default as protoc normalises it for typ: an
// integer in decimal, a float as SimpleDtoa writes it, bytes C-escaped, and a
// named type's default as its token, whatever the type turns out to be.
func (p *parser) parseDefaultValue(typ fieldType, value *string) bool {
	switch typ {
	case 0:
		*value = p.t.current.text
		p.next()
		return true
	case fieldInt32, fieldSint32, fieldSfixed32:
		return p.parseIntegerDefault(value, math.MaxInt32, true)
	case fieldInt64, fieldSint64, fieldSfixed64:
		return p.parseIntegerDefault(value, math.MaxInt64, true)
	case fieldUint32, fieldFixed32:
		return p.parseIntegerDefault(value, math.MaxUint32, false)
	case fieldUint64, fieldFixed64:
		return p.parseIntegerDefault(value, math.MaxUint64, false)
	case fieldFloat, fieldDouble:
		if p.tryConsume("-") {
			*value = "-"
		}
		var v float64
		if !p.consumeNumber(&v, "Expected number.") {
			return false
		}
		*value += simpleDtoa(v)
		return true
	case fieldBool:
		if !p.lookingAt("true") && !p.lookingAt("false") {
			p.recordError(`Expected "true" or "false".`)
			return false
		}
		*value = p.t.current.text
		p.next()
		return true
	case fieldString:
		return p.consumeString(value, "Expected string for field default value.")
	case fieldBytes:
		if !p.consumeString(value, "Expected string.") {
			return false
		}
		*value = cEscape(*value)
		return true
	}
	p.recordError("Messages can't have default values.")
	return false
}

func (p *parser) parseIntegerDefault(value *string, limit uint64, signed bool) bool {
	if p.tryConsume("-") {
		if signed {
			*value = "-"
			limit++
		} else {
			p.recordError("Unsigned field can't have negative default value.")
		}
	}
	var v uint64
	if !p.consumeInteger64(limit, &v, "Expected integer for field default value.") {
		return false
	}
	*value += strconv.FormatUint(v, 10)
	return true
}

func (p *parser) parseJSONName(f *Field, floc *rec) bool {
	if f.JSONName != nil {
		p.recordError(`Already set option "json_name".`)
		f.JSONName = nil
	}
	loc := floc.child(fldJSONName)
	defer loc.end()
	if !p.consume("json_name") || !p.consume("=") {
		return false
	}
	valueLoc := loc.child()
	defer valueLoc.end()
	var s string
	if !p.consumeString(&s, "Expected string for JSON name.") {
		return false
	}
	f.JSONName = &s
	return true
}

// parseOptionNamePart appends the part before reading it, as protoc adds the
// name part first, so an option whose name fails to parse still has one.
func (p *parser) parseOptionNamePart(u *uninterpretedOption, ploc *rec) bool {
	u.Name = append(u.Name, namePart{})
	part := &u.Name[len(u.Name)-1]
	var id string
	if !p.lookingAt("(") {
		loc := ploc.child(npNamePart)
		defer loc.end()
		if !p.consumeIdent(&id, "Expected identifier.") {
			return false
		}
		part.Part = id
		return true
	}
	p.next()
	loc := ploc.child(npNamePart)
	var sb strings.Builder
	if p.lookingAtType(tokenIdentifier) {
		sb.WriteString(p.t.current.text)
		p.next()
	}
	for p.tryConsume(".") {
		sb.WriteByte('.')
		if !p.consumeIdent(&id, "Expected identifier.") {
			part.Part = sb.String()
			loc.end()
			return false
		}
		sb.WriteString(id)
	}
	loc.end()
	part.Part = sb.String()
	if !p.consume(")") {
		return false
	}
	part.IsExtension = true
	return true
}

// parseUninterpretedBlock reads an aggregate value's text as protoc keeps
// it: the tokens between the braces, the space and line breaks between them
// rebuilt from their positions.
func (p *parser) parseUninterpretedBlock(out *string) bool {
	if !p.lookingAt("{") {
		p.recordError(`Expected "{".`)
		return false
	}
	depth := 1
	var sb strings.Builder
	whitespace, newlines := p.t.reportWhitespace, p.t.reportNewlines
	p.t.reportWhitespace, p.t.reportNewlines = true, true
	p.next()
	for !p.atEnd() {
		if p.lookingAt("{") {
			depth++
		} else if p.lookingAt("}") {
			depth--
			if depth == 0 {
				break
			}
		}
		cur, prev := p.t.current, p.t.previous
		prevEnd := prev.line
		if prev.kind == tokenNewline {
			prevEnd++
		}
		if cur.line > prevEnd {
			sb.WriteString(strings.Repeat("\n", int(cur.line-prevEnd)))
			sb.WriteString(strings.Repeat(" ", int(cur.column)))
		} else if cur.line == prevEnd && cur.column > prev.endColumn {
			sb.WriteString(strings.Repeat(" ", int(cur.column-prev.endColumn)))
		}
		sb.WriteString(cur.text)
		p.next()
	}
	p.t.reportWhitespace, p.t.reportNewlines = whitespace, newlines
	*out = sb.String()
	if depth != 0 {
		p.recordError("Unexpected end of stream while parsing aggregate value.")
		return false
	}
	p.next()
	return true
}

// parseOption reads one option into opts: an option statement when
// statement is true, else one assignment of a bracketed list.
func (p *parser) parseOption(opts *Options, oloc *rec, statement bool) bool {
	loc := oloc.child(uninterpretedNum, count(opts.Uninterpreted))
	defer loc.end()
	if statement && !p.consume("option") {
		return false
	}
	u := &uninterpretedOption{Line: int(p.t.current.line) + 1, Column: int(p.t.current.column) + 1}
	opts.Uninterpreted = append(opts.Uninterpreted, u)
	if !p.parseOptionName(u, loc) || !p.consume("=") || !p.parseOptionValue(u, loc) {
		return false
	}
	if statement {
		return p.consumeEndOfDecl(";", loc)
	}
	return true
}

func (p *parser) parseOptionName(u *uninterpretedOption, loc *rec) bool {
	nameLoc := loc.child(uoName)
	defer nameLoc.end()
	for first := true; first || p.tryConsume("."); first = false {
		partLoc := nameLoc.child(count(u.Name))
		ok := p.parseOptionNamePart(u, partLoc)
		partLoc.end()
		if !ok {
			return false
		}
	}
	return true
}

func (p *parser) parseOptionValue(u *uninterpretedOption, loc *rec) bool {
	valueLoc := loc.child()
	defer valueLoc.end()
	u.ValueLine, u.ValueColumn = int(p.t.current.line)+1, int(p.t.current.column)+1
	negative := p.tryConsume("-")
	switch p.t.current.kind {
	case tokenEnd:
		p.recordError("Unexpected end of stream while parsing option value.")
		return false
	case tokenIdentifier:
		valueLoc.add(uoIdent)
		var v string
		p.consumeIdent(&v, "Expected identifier.")
		if !negative {
			u.Identifier = &v
			return true
		}
		var d float64
		switch v {
		case litInf:
			d = math.Inf(-1)
		case litNaN:
			d = math.NaN()
		default:
			p.recordError("Identifier after '-' symbol must be inf or nan.")
			return false
		}
		u.Double = &d
		return true
	case tokenInteger, tokenFloat:
		return p.parseNumericOptionValue(u, valueLoc, negative)
	case tokenString:
		valueLoc.add(uoString)
		if negative {
			p.recordError("Invalid '-' symbol before string.")
			return false
		}
		var s string
		p.consumeString(&s, "Expected string.")
		u.String = &s
		return true
	case tokenSymbol:
		if !p.lookingAt("{") {
			p.recordError("Expected option value.")
			return false
		}
		valueLoc.add(uoAggregate)
		var s string
		if !p.parseUninterpretedBlock(&s) {
			return false
		}
		u.Aggregate = &s
		return true
	}
	p.recordError("Expected option value.")
	return false
}

func (p *parser) parseNumericOptionValue(u *uninterpretedOption, valueLoc *rec, negative bool) bool {
	if p.lookingAtType(tokenInteger) {
		limit := uint64(math.MaxUint64)
		if negative {
			limit = uint64(math.MaxInt64) + 1
		}
		var v uint64
		if p.tryConsumeInteger64(limit, &v) {
			if negative {
				valueLoc.add(uoNeg)
				n := int64(-v) //nolint:gosec // v is at most MaxInt64+1, whose negation is MinInt64
				u.Negative = &n
			} else {
				valueLoc.add(uoPos)
				u.Positive = &v
			}
			return true
		}
	}
	valueLoc.add(uoDouble)
	var d float64
	if !p.consumeNumber(&d, "Expected number.") {
		return false
	}
	if negative {
		d = -d
	}
	u.Double = &d
	return true
}

func (p *parser) parseExtensions(m *Message, eloc *rec) bool {
	if !p.consume("extensions") {
		return false
	}
	old := len(m.ExtRanges)
	for {
		loc := eloc.child(count(m.ExtRanges))
		r := &extRange{}
		m.ExtRanges = append(m.ExtRanges, r)
		ok := p.parseFieldRange(&r.Start, &r.End, loc, "Expected field number range.")
		loc.end()
		if !ok {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	if p.lookingAt("[") && !p.parseExtensionRangeOptions(m, eloc, old) {
		return false
	}
	return p.consumeEndOfDecl(";", eloc)
}

// parseFieldRange reads `start`, `start to end` or `start to max` of a
// message's extension or reserved range, end exclusive.
func (p *parser) parseFieldRange(start, end *int32, loc *rec, msg string) bool {
	startToken := p.t.current
	if !p.consumeFieldNumberAt(loc, start, msg) {
		return false
	}
	if !p.tryConsume("to") {
		sameAsStart(loc, startToken)
		*end = *start + 1
		return true
	}
	endLoc := loc.child(rangeEnd)
	defer endLoc.end()
	if p.tryConsume("max") {
		*end = maxRangeSentinel
		return true
	}
	if !p.consumeFieldNumber(end, "Expected integer.") {
		return false
	}
	*end++
	return true
}

// consumeFieldNumberAt reads a range's start under a location of its own.
func (p *parser) consumeFieldNumberAt(loc *rec, out *int32, msg string) bool {
	startLoc := loc.child(rangeStart)
	defer startLoc.end()
	return p.consumeFieldNumber(out, msg)
}

// consumeFieldNumber reads a range bound, which may not be MaxInt32: the
// exclusive end it becomes would overflow.
func (p *parser) consumeFieldNumber(out *int32, msg string) bool {
	if !p.consumeInteger(out, msg) {
		return false
	}
	if *out == math.MaxInt32 {
		p.recordError("Field number out of bounds.")
		return false
	}
	return true
}

// sameAsStart records a range's end location for a range written as its
// start alone: the start token's span.
func sameAsStart(loc *rec, startToken token) {
	endLoc := loc.child(rangeEnd)
	endLoc.startAtToken(startToken)
	endLoc.endAt(startToken)
}

// parseExtensionRangeOptions reads the options of an extensions statement,
// which every range it lists carries, each with its own copy of the options'
// locations.
func (p *parser) parseExtensionRangeOptions(m *Message, eloc *rec, first int) bool {
	idxPos := len(eloc.loc.Path)
	var info []*Location
	opts := &Options{}
	m.ExtRanges[first].Options = opts
	il := eloc.childIn(&info, 0)
	ol := il.child(erOptions)
	p.next()
	for {
		if !p.parseOption(opts, ol, false) {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	if !p.consume("]") {
		return false
	}
	ol.end()
	il.end()
	for i := first + 1; i < len(m.ExtRanges); i++ {
		m.ExtRanges[i].Options = &Options{Uninterpreted: slices.Clone(opts.Uninterpreted)}
	}
	for i := first; i < len(m.ExtRanges); i++ {
		for _, l := range info {
			if len(l.Path) == idxPos+1 {
				continue
			}
			copied := *l
			copied.Path = slices.Clone(l.Path)
			copied.Span = slices.Clone(l.Span)
			copied.Detached = slices.Clone(l.Detached)
			copied.Path[idxPos] = count(m.ExtRanges[:i])
			p.file.Locations = append(p.file.Locations, &copied)
		}
	}
	return true
}

type reservedOf int

const (
	messageReserved reservedOf = iota
	enumReserved
)

func (p *parser) parseReserved(ranges *[][2]int32, names *[]string, ploc *rec, rangeNum, nameNum int32, of reservedOf) bool {
	start := p.t.current
	p.next()
	switch {
	case p.lookingAtType(tokenString):
		if p.syntax == syntaxEditions {
			p.recordError("Reserved names must be identifiers in editions, not string literals.")
			return false
		}
		msg := "Expected field name string literal."
		if of == enumReserved {
			msg = "Expected enum value string literal."
		}
		return p.parseReservedNames(names, ploc, nameNum, start, func(out *string) bool { return p.consumeString(out, msg) })
	case p.lookingAtType(tokenIdentifier):
		if p.syntax != syntaxEditions {
			p.recordError("Reserved names must be string literals. (Only editions supports identifiers.)")
			return false
		}
		msg := "Expected field name identifier."
		if of == enumReserved {
			msg = "Expected enum value identifier."
		}
		return p.parseReservedNames(names, ploc, nameNum, start, func(out *string) bool { return p.consumeIdent(out, msg) })
	}
	return p.parseReservedRanges(ranges, ploc, rangeNum, start, of)
}

func (p *parser) parseReservedRanges(ranges *[][2]int32, ploc *rec, rangeNum int32, start token, of reservedOf) bool {
	loc := ploc.child(rangeNum)
	defer loc.end()
	loc.startAtToken(start)
	for first := true; ; first = false {
		l := loc.child(count(*ranges))
		*ranges = append(*ranges, [2]int32{})
		r := &(*ranges)[len(*ranges)-1]
		var ok bool
		if of == enumReserved {
			ok = p.parseEnumRange(&r[0], &r[1], l, first)
		} else {
			msg := "Expected field number range."
			if first {
				msg = "Expected field name or number range."
			}
			ok = p.parseFieldRange(&r[0], &r[1], l, msg)
		}
		l.end()
		if !ok {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	return p.consumeEndOfDecl(";", loc)
}

func (p *parser) parseReservedNames(names *[]string, ploc *rec, nameNum int32, start token, read func(*string) bool) bool {
	loc := ploc.child(nameNum)
	defer loc.end()
	loc.startAtToken(start)
	for {
		l := loc.child(count(*names))
		*names = append(*names, "")
		ok := read(&(*names)[len(*names)-1])
		l.end()
		if !ok {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	return p.consumeEndOfDecl(";", loc)
}

// parseEnumRange reads a reserved range of an enum: signed, both ends
// inclusive, max meaning MaxInt32.
func (p *parser) parseEnumRange(start, end *int32, loc *rec, first bool) bool {
	msg := "Expected enum number range."
	if first {
		msg = "Expected enum value or number range."
	}
	startToken := p.t.current
	if !p.consumeSignedIntegerAt(loc, rangeStart, start, msg) {
		return false
	}
	if !p.tryConsume("to") {
		sameAsStart(loc, startToken)
		*end = *start
		return true
	}
	endLoc := loc.child(rangeEnd)
	defer endLoc.end()
	if p.tryConsume("max") {
		*end = math.MaxInt32
		return true
	}
	return p.consumeSignedInteger(end, "Expected integer.")
}

func (p *parser) parseExtend(exts *[]*Field, msgs *[]*Message, parent *rec, nestedNum int32, extLoc *rec) bool {
	if !p.consume("extend") {
		return false
	}
	extendeeStart := p.t.current
	var extendee string
	if !p.parseUserDefinedType(&extendee) {
		return false
	}
	extendeeEnd := p.t.previous
	if !p.consumeEndOfDecl("{", extLoc) {
		return false
	}
	for {
		if p.atEnd() {
			p.recordError("Reached end of input in extend definition (missing '}').")
			return false
		}
		loc := extLoc.child(count(*exts))
		f := &Field{Extendee: extendee}
		*exts = append(*exts, f)
		el := loc.child(fldExtendee)
		el.startAtToken(extendeeStart)
		el.endAt(extendeeEnd)
		if !p.parseMessageField(f, msgs, parent, nestedNum, loc) {
			p.skipStatement()
		}
		loc.end()
		if p.tryConsumeEndOfDecl("}", nil) {
			return true
		}
	}
}

func (p *parser) parseOneof(o *oneofDecl, m *Message, idx int32, oloc, mloc *rec) bool {
	if !p.consume("oneof") {
		return false
	}
	if !p.consumeNameAt(oloc, &o.Name, "Expected oneof name.") || !p.consumeEndOfDecl("{", oloc) {
		return false
	}
	for {
		if p.atEnd() {
			p.recordError("Reached end of input in oneof definition (missing '}').")
			return false
		}
		if p.lookingAt("option") {
			if !p.parseOneofOption(o, oloc) {
				return false
			}
		} else {
			p.parseOneofField(m, idx, mloc)
		}
		if p.tryConsumeEndOfDecl("}", nil) {
			return true
		}
	}
}

func (p *parser) parseOneofOption(o *oneofDecl, oloc *rec) bool {
	l := oloc.child(odOptions)
	defer l.end()
	if o.Options == nil {
		o.Options = &Options{}
	}
	return p.parseOption(o.Options, l, true)
}

// parseOneofField reads a field of oneof idx, which is a field of m.
func (p *parser) parseOneofField(m *Message, idx int32, mloc *rec) {
	if p.lookingAt("required") || p.lookingAt("optional") || p.lookingAt("repeated") {
		p.recordError("Fields in oneofs must not have labels (required / optional / repeated).")
		p.next()
	}
	fl := mloc.child(dpField, count(m.Fields))
	defer fl.end()
	f := &Field{Label: labelOptional, OneofIndex: &idx}
	m.Fields = append(m.Fields, f)
	if !p.parseMessageFieldNoLabel(f, &m.Nested, mloc, dpNested, fl) {
		p.skipStatement()
	}
}

func (p *parser) parseEnumDef(e *Enum, vis visibility, eloc *rec) bool {
	if !p.consume("enum") || !p.consumeNameAt(eloc, &e.Name, "Expected enum name.") ||
		!p.consumeEndOfDecl("{", eloc) {
		return false
	}
	for !p.tryConsumeEndOfDecl("}", nil) {
		if p.atEnd() {
			p.recordError("Reached end of input in enum definition (missing '}').")
			return false
		}
		if !p.parseEnumStatement(e, eloc) {
			p.skipStatement()
		}
	}
	if !p.validateEnum(e) {
		return false
	}
	e.Visibility = vis
	return true
}

// validateEnum refuses an allow_alias option that changes nothing.
func (p *parser) validateEnum(e *Enum) bool {
	hasAllowAlias, allowAlias := false, false
	if e.Options != nil {
		for _, u := range e.Options.Uninterpreted {
			if len(u.Name) == 1 && !u.Name[0].IsExtension && u.Name[0].Part == "allow_alias" {
				hasAllowAlias = true
				allowAlias = u.isTrue()
				break
			}
		}
	}
	if hasAllowAlias && !allowAlias {
		p.recordError(`"` + e.Name + `" declares 'option allow_alias = false;' which has no effect. Please remove the declaration.`)
		return false
	}
	used := map[int32]bool{}
	shared := false
	for _, v := range e.Values {
		shared = shared || used[v.Number]
		used[v.Number] = true
	}
	if allowAlias && !shared {
		p.recordError(`"` + e.Name + `" declares support for enum aliases but no enum values share field numbers. Please remove the unnecessary 'option allow_alias = true;' declaration.`)
		return false
	}
	return true
}

func (p *parser) parseEnumStatement(e *Enum, eloc *rec) bool {
	switch {
	case p.tryConsumeEndOfDecl(";", nil):
		return true
	case p.lookingAt("option"):
		l := eloc.child(edOptions)
		defer l.end()
		if e.Options == nil {
			e.Options = &Options{}
		}
		return p.parseOption(e.Options, l, true)
	case p.lookingAt("reserved"):
		return p.parseReserved(&e.ReservedRanges, &e.ReservedNames, eloc, edReservedRange, edReservedName, enumReserved)
	}
	l := eloc.child(edValue, count(e.Values))
	defer l.end()
	v := &EnumValue{}
	e.Values = append(e.Values, v)
	if !p.consumeNameAt(l, &v.Name, "Expected enum constant name.") ||
		!p.consumeOr("=", "Missing numeric value for enum constant.") ||
		!p.consumeSignedIntegerAt(l, evNumber, &v.Number, "Expected integer.") {
		return false
	}
	if p.lookingAt("[") && !p.parseEnumValueOptions(v, l) {
		return false
	}
	return p.consumeEndOfDecl(";", l)
}

func (p *parser) parseEnumValueOptions(v *EnumValue, l *rec) bool {
	ol := l.child(evOptions)
	defer ol.end()
	p.next()
	v.Options = &Options{}
	for {
		if !p.parseOption(v.Options, ol, false) {
			return false
		}
		if !p.tryConsume(",") {
			break
		}
	}
	return p.consume("]")
}

func (p *parser) parseService(s *Service, sloc *rec) bool {
	if !p.consume("service") || !p.consumeNameAt(sloc, &s.Name, "Expected service name.") ||
		!p.consumeEndOfDecl("{", sloc) {
		return false
	}
	for !p.tryConsumeEndOfDecl("}", nil) {
		if p.atEnd() {
			p.recordError("Reached end of input in service definition (missing '}').")
			return false
		}
		if !p.parseServiceStatement(s, sloc) {
			p.skipStatement()
		}
	}
	return true
}

func (p *parser) parseServiceStatement(s *Service, sloc *rec) bool {
	switch {
	case p.tryConsumeEndOfDecl(";", nil):
		return true
	case p.lookingAt("option"):
		l := sloc.child(sdOptions)
		defer l.end()
		if s.Options == nil {
			s.Options = &Options{}
		}
		return p.parseOption(s.Options, l, true)
	}
	l := sloc.child(sdMethod, count(s.Methods))
	defer l.end()
	m := &Method{}
	s.Methods = append(s.Methods, m)
	return p.parseMethod(m, l)
}

func (p *parser) parseMethod(m *Method, mloc *rec) bool {
	if !p.consume("rpc") || !p.consumeNameAt(mloc, &m.Name, "Expected method name.") ||
		!p.consume("(") || !p.parseMethodType(mloc, mdClientStream, mdInput, &m.ClientStream, &m.Input) ||
		!p.consume(")") || !p.consume("returns") || !p.consume("(") ||
		!p.parseMethodType(mloc, mdServerStream, mdOutput, &m.ServerStream, &m.Output) || !p.consume(")") {
		return false
	}
	if !p.lookingAt("{") {
		return p.consumeEndOfDecl(";", mloc)
	}
	return p.parseMethodOptions(m, mloc)
}

func (p *parser) parseMethodOptions(m *Method, mloc *rec) bool {
	p.consumeEndOfDecl("{", mloc)
	for !p.tryConsumeEndOfDecl("}", nil) {
		if p.atEnd() {
			p.recordError("Reached end of input in method options (missing '}').")
			return false
		}
		if p.tryConsumeEndOfDecl(";", nil) {
			continue
		}
		l := mloc.child(mdOptions)
		if m.Options == nil {
			m.Options = &Options{}
		}
		if !p.parseOption(m.Options, l, true) {
			p.skipStatement()
		}
		l.end()
	}
	return true
}

// parseMethodType reads a method's input or output type, with its stream
// keyword.
func (p *parser) parseMethodType(mloc *rec, streamField, typeField int32, stream *bool, name *string) bool {
	if p.lookingAt("stream") {
		l := mloc.child(streamField)
		*stream = true
		p.next()
		l.end()
	}
	l := mloc.child(typeField)
	defer l.end()
	return p.parseUserDefinedType(name)
}

func (p *parser) parsePackage(root *rec) bool {
	f := p.file
	if p.hasPackage {
		p.recordError("Multiple package definitions.")
		f.Package = ""
		p.hasPackage = false
	}
	loc := root.child(fdPackage)
	defer loc.end()
	p.next()
	for {
		var id string
		if !p.consumeIdent(&id, "Expected identifier.") {
			return false
		}
		f.Package += id
		p.hasPackage = true
		if !p.tryConsume(".") {
			break
		}
		f.Package += "."
	}
	return p.consumeEndOfDecl(";", loc)
}

func (p *parser) parseImport(root *rec) bool {
	f := p.file
	start := p.t.current
	p.next()
	var name string
	if p.lookingAt("option") {
		if p.edition < edition2024 {
			p.recordError("option import is not supported before edition 2024.")
		}
		loc := root.child(fdOptionDep, count(f.OptionDeps))
		defer loc.end()
		loc.startAtToken(start)
		p.next()
		if !p.consumeString(&name, "Expected a string naming the file to import.") {
			return false
		}
		f.OptionDeps = append(f.OptionDeps, name)
		return p.consumeEndOfDecl(";", loc)
	}
	loc := root.child(fdDependency, count(f.Deps))
	defer loc.end()
	loc.startAtToken(start)
	if len(f.OptionDeps) > 0 {
		p.recordError("imports should precede any option imports to ensure proto files can roundtrip.")
	}
	switch {
	case p.lookingAt("public"):
		l := root.child(fdPublicDep, count(f.Public))
		p.next()
		l.end()
		f.Public = append(f.Public, count(f.Deps))
	case p.lookingAt("weak"):
		if p.edition >= edition2024 {
			p.recordError("weak import is not supported in edition 2024 and above. Consider using option import instead.")
		}
		l := root.child(fdWeakDep, count(f.Weak))
		p.next()
		l.end()
		f.Weak = append(f.Weak, count(f.Deps))
	}
	if !p.consumeString(&name, "Expected a string naming the file to import.") {
		return false
	}
	f.Deps = append(f.Deps, name)
	return p.consumeEndOfDecl(";", loc)
}

// Package is the package src declares, parsed as name, or "" where src does
// not parse.
func Package(name string, src []byte) string {
	f, err := parseFile(name, src)
	if err != nil {
		return ""
	}
	return f.Package
}
