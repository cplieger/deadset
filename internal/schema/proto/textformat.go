package proto

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// textRecursionLimit is TextFormat::Parser's default recursion limit.
const textRecursionLimit = 100

// textParser is TextFormat::Parser::ParserImpl as option interpretation
// configures it: '#' comments, an f after a float, singular overwrites
// forbidden, and extension and Any names resolved through the file's pool.
type textParser struct {
	b          *builder
	t          *tokenizer
	unresolved string
	errs       []string
	seen       int // tokenizer errors already copied into errs
	depth      int
}

// parseTextFormat parses an aggregate option value as a message of type t.
// It returns the value, or the name it needs that no scope resolves, or
// protoc's error text, its messages joined by "; " as protoc joins them.
func (b *builder) parseTextFormat(src string, t *Message) (m *MessageValue, need, msg string) {
	p := &textParser{b: b, t: newTokenizer([]byte(src)), depth: textRecursionLimit}
	p.t.shComments, p.t.allowFAfterFloat = true, true
	p.next()
	m = newMessageValue(t)
	ok := true
	for ok && p.t.current.kind != tokenEnd {
		ok = p.field(m)
	}
	switch {
	case p.unresolved != "":
		return nil, p.unresolved, ""
	case len(p.errs) > 0:
		return nil, "", strings.Join(p.errs, "; ")
	}
	return m, "", ""
}

func (p *textParser) next() {
	p.t.next()
	for ; p.seen < len(p.t.errs); p.seen++ {
		p.errs = append(p.errs, p.t.errs[p.seen].Message)
	}
}

func (p *textParser) fail(msg string) bool {
	p.errs = append(p.errs, msg)
	return false
}

// need records name as the one the value needs where the file may name
// unknown types, and otherwise fails with msg, protoc's error for it.
func (p *textParser) need(name, msg string) bool {
	if !p.b.allowsUnknown() {
		return p.fail(msg)
	}
	p.unresolved = name
	return false
}

func (p *textParser) text() string { return p.t.current.text }

func (p *textParser) lookingAt(s string) bool { return p.t.current.text == s }

func (p *textParser) tryConsume(s string) bool {
	if p.lookingAt(s) {
		p.next()
		return true
	}
	return false
}

func (p *textParser) consume(s string) bool {
	if p.tryConsume(s) {
		return true
	}
	return p.fail(`Expected "` + s + `", found "` + p.text() + `".`)
}

func (p *textParser) separator() bool {
	_ = p.tryConsume(";") || p.tryConsume(",")
	return true
}

func (p *textParser) enter() bool {
	p.depth--
	if p.depth < 0 {
		return p.fail("Message is too deep, the parser exceeded the configured recursion limit of " + strconv.Itoa(textRecursionLimit) + ".")
	}
	return true
}

func (p *textParser) leave() { p.depth++ }

// field is ConsumeField: one field of m, its name and its value or values.
func (p *textParser) field(m *MessageValue) bool {
	if m.Type.FullName == "google.protobuf.Any" && p.lookingAt("[") {
		return p.anyExpansion(m)
	}
	f, name, ok := p.fieldName(m)
	switch {
	case !ok:
		return false
	case f == nil:
		return p.skipField()
	case !p.settable(m, f, name):
		return false
	case f.Type == 0:
		return p.need(f.TypeName, quoted(f.TypeName)+" is not defined.")
	}
	message := f.Type == fieldMessage || f.Type == fieldGroup
	if message {
		p.tryConsume(":")
	} else if !p.consume(":") {
		return false
	}
	one := func() bool {
		if message {
			return p.fieldMessage(m, f)
		}
		return p.fieldValue(m, f)
	}
	if f.Label == labelRepeated && p.tryConsume("[") {
		ok = p.list(one)
	} else {
		ok = one()
	}
	return ok && p.separator()
}

// settable refuses a second value for a singular field or for a oneof.
func (p *textParser) settable(m *MessageValue, f *Field, name string) bool {
	if fv := m.Fields[f.Number]; f.Label != labelRepeated && fv != nil && len(fv.Values) > 0 {
		return p.fail("Non-repeated field " + quoted(name) + " is specified multiple times.")
	}
	if other := setInOneof(m, f); other != nil {
		return p.fail("Field " + quoted(name) + " is specified along with field " + quoted(other.Name) + ", another member of oneof " + quoted(f.oneof.Name) + ".")
	}
	return true
}

// fieldName reads a field name, an extension's in brackets: nil with ok
// for a reserved name, whose value is skipped.
func (p *textParser) fieldName(m *MessageValue) (f *Field, name string, ok bool) {
	if p.lookingAt("[") {
		typeName, prefix, ok := p.typeURL()
		switch {
		case !ok:
			return nil, "", false
		case prefix != "":
			return nil, "", p.fail(`Extension name contains "/"`)
		}
		s := p.b.lookup(typeName, m.Type.FullName, false)
		notExtension := "Extension " + quoted(typeName) + " is not defined or is not an extension of " + quoted(m.Type.FullName) + "."
		if s == nil {
			return nil, "", p.need(typeName, notExtension)
		}
		if f = extensionOf(s, m.Type); f == nil {
			return nil, "", p.fail(notExtension)
		}
		return f, typeName, true
	}
	if p.t.current.kind != tokenIdentifier {
		return nil, "", p.fail("Expected identifier, got: " + p.text())
	}
	name = p.text()
	p.next()
	if g := fieldNamed(m.Type, name); g != nil {
		return g, name, true
	}
	if g := fieldNamed(m.Type, strings.ToLower(name)); g != nil && g.Type == fieldGroup && g.msgType != nil && g.msgType.Name == name {
		return g, name, true
	}
	if !slices.Contains(m.Type.ReservedNames, name) {
		return nil, "", p.fail("Message type " + quoted(m.Type.FullName) + " has no field named " + quoted(name) + ".")
	}
	return nil, name, true
}

// list reads the rest of a bracketed list of values after its "[".
func (p *textParser) list(one func() bool) bool {
	if p.tryConsume("]") {
		return true
	}
	for {
		if !one() {
			return false
		}
		if p.tryConsume("]") {
			return true
		}
		if !p.consume(",") {
			return false
		}
	}
}

// extensionOf is AggregateOptionFinder::FindExtension's result for s: an
// extension of m, or for a message set the extension of m a message type
// declares for itself.
func extensionOf(s *symbol, m *Message) *Field {
	switch s.kind {
	case symField:
		if s.field.extendee == m {
			return s.field
		}
	case symMessage:
		if m.Options.isTrue(moMessageSetWireFormat) {
			for _, x := range s.msg.Extensions {
				if x.extendee == m && x.Type == fieldMessage && x.Label == labelOptional && x.msgType == s.msg {
					return x
				}
			}
		}
	}
	return nil
}

func setInOneof(m *MessageValue, f *Field) *Field {
	if f.oneof == nil {
		return nil
	}
	for _, fv := range m.Fields {
		if fv.Field.oneof == f.oneof && len(fv.Values) > 0 {
			return fv.Field
		}
	}
	return nil
}

// typeURL is ConsumeAnyTypeUrlOrFullTypeName: the bracketed name, split at
// its last slash into a URL prefix and a type name.
func (p *textParser) typeURL() (name, prefix string, ok bool) {
	p.t.reportURLChars = true
	p.next()
	var sb strings.Builder
	for p.t.current.kind == tokenURLChars {
		sb.WriteString(p.text())
		p.next()
	}
	p.t.reportURLChars = false
	if !p.consume("]") {
		return "", "", false
	}
	name = sb.String()
	if before, after, found := strings.CutLast(name, "/"); found {
		prefix, name = before+"/", after
		if msg := checkURLPrefix(prefix); msg != "" {
			return "", "", p.fail(msg)
		}
	}
	for part := range strings.SplitSeq(name, ".") {
		if !isIdentifier(part) {
			return "", "", p.fail(`Invalid identifier in type name, got: "` + part + `"`)
		}
	}
	return name, prefix, true
}

func checkURLPrefix(prefix string) string {
	switch {
	case prefix == "/":
		return "Type URL prefix is empty"
	case prefix[0] == '/':
		return `Type URL starts with "/"`
	}
	for j := range len(prefix) {
		if prefix[j] == '%' && (j+2 >= len(prefix) || !isHex(prefix[j+1]) || !isHex(prefix[j+2])) {
			return `Invalid percent encode, got: "` + prefix[j:min(j+3, len(prefix))] + `"`
		}
	}
	return ""
}

// anyExpansion reads `[prefix/pkg.Type] { ... }` into an Any as its
// type_url and its value, kept parsed.
func (p *textParser) anyExpansion(m *MessageValue) bool {
	name, prefix, ok := p.typeURL()
	if !ok {
		return false
	}
	if prefix == "" {
		return p.fail("Type URL prefix is missing")
	}
	p.tryConsume(":")
	var t *Message
	if prefix == "type.googleapis.com/" || prefix == "type.googleprod.com/" {
		if p.b.l.syms[name] == nil {
			return p.need(name, `Could not find type "`+prefix+name+`" stored in google.protobuf.Any.`)
		}
		if s := p.b.findSymbol(name); s != nil && s.kind == symMessage {
			t = s.msg
		}
	}
	if t == nil {
		return p.fail(`Could not find type "` + prefix + name + `" stored in google.protobuf.Any.`)
	}
	value := newMessageValue(t)
	if !p.message(value) {
		return false
	}
	typeURL, valueField := fieldNumbered(m.Type, 1), fieldNumbered(m.Type, 2)
	if typeURL == nil || valueField == nil {
		return p.fail(`Could not find type "` + prefix + name + `" stored in google.protobuf.Any.`)
	}
	if len(m.Fields) > 0 {
		return p.fail("Non-repeated Any specified multiple times.")
	}
	m.slot(typeURL).Values = []*optionValue{{Scalar: prefix + name}}
	m.slot(valueField).Values = []*optionValue{{Message: value}}
	return p.separator()
}

func fieldNumbered(m *Message, n int32) *Field {
	for _, f := range m.Fields {
		if f.Number == n {
			return f
		}
	}
	return nil
}

// message is ConsumeMessageDelimiter and ConsumeMessage: a braced or
// angle-bracketed body of fields.
func (p *textParser) message(m *MessageValue) bool {
	end := ">"
	if !p.tryConsume("<") {
		if !p.consume("{") {
			return false
		}
		end = "}"
	}
	if !p.enter() {
		return false
	}
	defer p.leave()
	for !p.lookingAt(">") && !p.lookingAt("}") {
		if !p.field(m) {
			return false
		}
	}
	return p.consume(end)
}

func (p *textParser) fieldMessage(m *MessageValue, f *Field) bool {
	v := newMessageValue(f.msgType)
	if !p.message(v) {
		return false
	}
	fv := m.slot(f)
	fv.Values = append(fv.Values, &optionValue{Message: v})
	return true
}

// fieldValue is ConsumeFieldValue: one scalar of f's type.
func (p *textParser) fieldValue(m *MessageValue, f *Field) bool {
	var s string
	switch f.Type {
	case fieldInt32, fieldSint32, fieldSfixed32, fieldInt64, fieldSint64, fieldSfixed64:
		_, hi, _ := intRange(f.Type)
		v, ok := p.signedInteger(hi)
		if !ok {
			return false
		}
		s = strconv.FormatInt(v, 10)
	case fieldUint32, fieldFixed32, fieldUint64, fieldFixed64:
		_, hi, _ := intRange(f.Type)
		v, ok := p.unsignedInteger(hi)
		if !ok {
			return false
		}
		s = strconv.FormatUint(v, 10)
	case fieldFloat, fieldDouble:
		v, ok := p.double()
		if !ok {
			return false
		}
		s = formatFloat(v, f.Type == fieldFloat)
	case fieldString, fieldBytes:
		if p.t.current.kind != tokenString {
			return p.fail("Expected string, got: " + p.text())
		}
		var sb strings.Builder
		for p.t.current.kind == tokenString {
			parseStringAppend(p.text(), &sb)
			p.next()
		}
		s = sb.String()
	case fieldBool:
		v, ok := p.bool(f)
		if !ok {
			return false
		}
		s = v
	case fieldEnum:
		v, ok := p.enum(f)
		if !ok {
			return false
		}
		s = v
	}
	fv := m.slot(f)
	fv.Values = append(fv.Values, &optionValue{Scalar: s})
	return true
}

func (p *textParser) unsignedInteger(limit uint64) (uint64, bool) {
	if p.t.current.kind != tokenInteger {
		return 0, p.fail("Expected integer, got: " + p.text())
	}
	v, ok := parseInteger(p.text(), limit)
	if !ok {
		return 0, p.fail("Integer out of range (" + p.text() + ")")
	}
	p.next()
	return v, true
}

func (p *textParser) signedInteger(limit uint64) (int64, bool) {
	negative := p.tryConsume("-")
	if negative {
		limit++
	}
	v, ok := p.unsignedInteger(limit)
	switch {
	case !ok:
		return 0, false
	case negative && v == 1<<63:
		return math.MinInt64, true
	case negative:
		return -int64(v), true //nolint:gosec // v is below 1<<63 here
	}
	return int64(v), true //nolint:gosec // limit keeps v within int64
}

func (p *textParser) double() (float64, bool) {
	negative := p.tryConsume("-")
	var v float64
	switch text := p.text(); p.t.current.kind {
	case tokenInteger:
		if len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X' || isOctal(text[1])) {
			return 0, p.fail("Expect a decimal number, got: " + text)
		}
		if n, ok := parseInteger(text, math.MaxUint64); ok {
			v = float64(n)
		} else {
			v = parseFloat(text)
		}
	case tokenFloat:
		v = parseFloat(text)
	case tokenIdentifier:
		switch lower := strings.ToLower(text); lower {
		case litInf, "infinity":
			v = math.Inf(1)
		case litNaN:
			v = math.NaN()
		default:
			return 0, p.fail("Expected double, got: " + lower)
		}
	default:
		return 0, p.fail("Expected double, got: " + text)
	}
	p.next()
	if negative {
		v = -v
	}
	return v, true
}

func (p *textParser) bool(f *Field) (string, bool) {
	if p.t.current.kind == tokenInteger {
		v, ok := p.unsignedInteger(1)
		return strconv.FormatBool(v == 1), ok
	}
	if p.t.current.kind != tokenIdentifier {
		return "", p.fail("Expected identifier, got: " + p.text())
	}
	text := p.text()
	p.next()
	switch text {
	case litTrue, "True", "t":
		return litTrue, true
	case litFalse, "False", "f":
		return litFalse, true
	}
	return "", p.fail("Invalid value for boolean field " + quoted(f.Name) + `. Value: "` + text + `".`)
}

// enum reads an enum value by name or number; an open enum takes a number
// it does not declare.
func (p *textParser) enum(f *Field) (string, bool) {
	e := f.enumType
	var written string
	switch {
	case p.t.current.kind == tokenIdentifier:
		written = p.text()
		p.next()
		if i := slices.IndexFunc(e.Values, func(v *EnumValue) bool { return v.Name == written }); i >= 0 {
			return strconv.Itoa(int(e.Values[i].Number)), true
		}
	case p.lookingAt("-") || p.t.current.kind == tokenInteger:
		n, ok := p.signedInteger(math.MaxInt32)
		if !ok {
			return "", false
		}
		written = strconv.FormatInt(n, 10)
		if e.Open || slices.ContainsFunc(e.Values, func(v *EnumValue) bool { return int64(v.Number) == n }) {
			return written, true
		}
	default:
		return "", p.fail("Expected integer or identifier, got: " + p.text())
	}
	return "", p.fail("Unknown enumeration value of " + quoted(written) + " for field " + quoted(f.Name) + ".")
}

// skipField is SkipField after a reserved field name: its value or message
// is read and dropped.
func (p *textParser) skipField() bool {
	if p.tryConsume(":") && !p.lookingAt("{") && !p.lookingAt("<") {
		return p.skipValue() && p.separator()
	}
	return p.skipMessage() && p.separator()
}

func (p *textParser) skipMessage() bool {
	if !p.enter() {
		return false
	}
	defer p.leave()
	end := ">"
	if !p.tryConsume("<") {
		if !p.consume("{") {
			return false
		}
		end = "}"
	}
	for !p.lookingAt(">") && !p.lookingAt("}") {
		switch {
		case p.lookingAt("["):
			if _, _, ok := p.typeURL(); !ok {
				return false
			}
		case p.t.current.kind != tokenIdentifier:
			return p.fail("Expected identifier, got: " + p.text())
		default:
			p.next()
		}
		if !p.skipField() {
			return false
		}
	}
	return p.consume(end)
}

func (p *textParser) skipValue() bool {
	if !p.enter() {
		return false
	}
	defer p.leave()
	switch {
	case p.t.current.kind == tokenString:
		for p.t.current.kind == tokenString {
			p.next()
		}
		return true
	case p.tryConsume("["):
		return p.list(func() bool {
			if p.lookingAt("{") || p.lookingAt("<") {
				return p.skipMessage()
			}
			return p.skipValue()
		})
	}
	minus := p.tryConsume("-")
	switch p.t.current.kind {
	case tokenInteger, tokenFloat:
	case tokenIdentifier:
		if lower := strings.ToLower(p.text()); minus && lower != litInf && lower != "infinity" && lower != litNaN {
			return p.fail("Invalid float number: " + lower)
		}
	default:
		return p.fail("Cannot skip field value, unexpected token: " + p.text())
	}
	p.next()
	return true
}
