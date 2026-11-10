package proto

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// MessageValue is an interpreted value of a message type: the fields it
// sets, by field number.
type MessageValue struct {
	Type   *Message
	Fields map[int32]*fieldValue
}

// fieldValue is what one field or extension of a MessageValue holds, in the
// order the source sets it.
type fieldValue struct {
	Field  *Field
	Values []*optionValue
}

// optionValue is one interpreted value: Message for a message or a group, else
// Scalar in canonical text. An integer or an enum value's number is decimal,
// a bool "true" or "false", a float Go's shortest form for its width ("inf",
// "-inf", "nan"), a string or bytes value its raw content. An Any written as
// an expansion holds its type_url and, as field 2, a MessageValue of the type
// it names.
type optionValue struct {
	Message *MessageValue
	Scalar  string
}

func newMessageValue(t *Message) *MessageValue {
	return &MessageValue{Type: t, Fields: map[int32]*fieldValue{}}
}

func (m *MessageValue) slot(f *Field) *fieldValue {
	fv := m.Fields[f.Number]
	if fv == nil {
		fv = &fieldValue{Field: f}
		m.Fields[f.Number] = fv
	}
	return fv
}

// scalar is the last value set for the field at path, nil when unset.
func (m *MessageValue) scalar(path ...int32) *optionValue {
	for i, n := range path {
		if m == nil {
			return nil
		}
		fv := m.Fields[n]
		if fv == nil || len(fv.Values) == 0 {
			return nil
		}
		v := fv.Values[len(fv.Values)-1]
		if i == len(path)-1 {
			return v
		}
		m = v.Message
	}
	return nil
}

func (o *Options) isTrue(path ...int32) bool {
	if o == nil {
		return false
	}
	v := o.Interpreted.scalar(path...)
	return v != nil && v.Scalar == litTrue
}

func (o *Options) has(path ...int32) bool { return o != nil && o.Interpreted.scalar(path...) != nil }

// The FeatureSet field numbers and the enum numbers the checks compare.
const (
	featureFieldPresence           = 1
	featureEnumType                = 2
	featureRepeatedFieldEncoding   = 3
	featureUTF8Validation          = 4
	featureMessageEncoding         = 5
	featureJSONFormat              = 6
	featureEnforceNamingStyle      = 7
	featureDefaultSymbolVisibility = 8
	presenceImplicit               = 2
	presenceLegacyRequired         = 3
	enumTypeOpen                   = 1
	encodingPacked                 = 1
	encodingExpanded               = 2
	encodingDelimited              = 2
	jsonFormatLegacyBestEffort     = 2
	style2024                      = 1
	style2026                      = 3
)

// featureSet holds a FeatureSet, each value by its field number; 0 is unset.
type featureSet [10]int32

// The options messages and the fields of them the checks read.
const (
	fileOptions             = "google.protobuf.FileOptions"
	messageOptions          = "google.protobuf.MessageOptions"
	fieldOptions            = "google.protobuf.FieldOptions"
	oneofOptions            = "google.protobuf.OneofOptions"
	enumOptions             = "google.protobuf.EnumOptions"
	enumValueOptions        = "google.protobuf.EnumValueOptions"
	serviceOptions          = "google.protobuf.ServiceOptions"
	methodOptions           = "google.protobuf.MethodOptions"
	extensionRangeOptions   = "google.protobuf.ExtensionRangeOptions"
	featureSetMessage       = "google.protobuf.FeatureSet"
	foOptimizeFor           = 9
	foCCGenericServices     = 16
	foJavaGenericServices   = 17
	foJavaStringCheckUTF8   = 27
	moMessageSetWireFormat  = 1
	moLegacyJSONConflicts   = 11
	fieldOptCtype           = 1
	fieldOptPacked          = 2
	fieldOptLazy            = 5
	fieldOptJstype          = 6
	fieldOptUnverifiedLazy  = 15
	fieldOptEditionDefaults = 20
	editionDefaultValue     = 2
	editionDefaultEdition   = 3
	eoAllowAlias            = 2
	eoLegacyJSONConflicts   = 6
	enumValueJSON           = 998 // (pb.enumvalue.json)
	optimizeLiteRuntime     = 3
)

// optionsElement is OptionsToInterpret: one element's options, the options
// message they set, the scope option names resolve from, and the location
// path of the options.
type optionsElement struct {
	opts     *Options
	typeName string
	scope    string
	path     []int32
}

// optionElements lists the elements with options in the order protoc's
// builder allocates them, which is the order it reports their errors.
func optionElements(f *File) []optionsElement {
	var l elementLister
	for i, m := range f.Messages {
		l.message(m, []int32{fdMessageType, int32(i)})
	}
	for i, e := range f.Enums {
		l.enum(e, []int32{fdEnumType, int32(i)})
	}
	for i, s := range f.Services {
		for j, m := range s.Methods {
			l.add(m.Options, methodOptions, m.FullName, []int32{fdService, int32(i), sdMethod, int32(j), mdOptions})
		}
		l.add(s.Options, serviceOptions, s.FullName, []int32{fdService, int32(i), sdOptions})
	}
	for i, x := range f.Extensions {
		l.field(x, []int32{fdExtension, int32(i)})
	}
	l.add(f.Options, fileOptions, f.Package+".dummy", []int32{fdOptions})
	return l.out
}

type elementLister struct{ out []optionsElement }

func (l *elementLister) add(o *Options, typeName, scope string, path []int32) {
	if o != nil && len(o.Uninterpreted) > 0 {
		l.out = append(l.out, optionsElement{opts: o, typeName: typeName, scope: scope, path: path})
	}
}

func (l *elementLister) field(fl *Field, path []int32) {
	l.add(fl.Options, fieldOptions, fl.FullName, cat(path, fldOptions))
}

func (l *elementLister) enum(e *Enum, path []int32) {
	for i, v := range e.Values {
		l.add(v.Options, enumValueOptions, v.FullName, cat(path, edValue, int32(i), evOptions))
	}
	l.add(e.Options, enumOptions, e.FullName, cat(path, edOptions))
}

func (l *elementLister) message(m *Message, path []int32) {
	for i, o := range m.Oneofs {
		l.add(o.Options, oneofOptions, m.FullName+"."+o.Name, cat(path, dpOneof, int32(i), odOptions))
	}
	for i, fl := range m.Fields {
		l.field(fl, cat(path, dpField, int32(i)))
	}
	for i, e := range m.Enums {
		l.enum(e, cat(path, dpEnumType, int32(i)))
	}
	for i, r := range m.ExtRanges {
		l.add(r.Options, extensionRangeOptions, m.FullName, cat(path, dpExtRange, int32(i), erOptions))
	}
	for i, x := range m.Extensions {
		l.field(x, cat(path, dpExtension, int32(i)))
	}
	l.add(m.Options, messageOptions, m.FullName, cat(path, dpOptions))
	for i, n := range m.Nested {
		l.message(n, cat(path, dpNested, int32(i)))
	}
}

// interpreter is OptionInterpreter for one file.
type interpreter struct {
	b           *builder
	interpreted map[string][]int32 // source location path to the path of the field it set
	repeated    map[string]int32
	kept        map[*Options][]*uninterpretedOption
}

// interpretOptions interprets every option, non-extension options first and
// features resolved between the two passes, as BuildFileImpl orders them.
func (b *builder) interpretOptions() {
	it := &interpreter{b: b, interpreted: map[string][]int32{}, repeated: map[string]int32{}, kept: map[*Options][]*uninterpretedOption{}}
	elements := optionElements(b.f)
	for _, e := range elements {
		it.interpret(e, false)
	}
	b.resolveFeatures()
	for _, e := range elements {
		it.interpret(e, true)
		e.opts.Uninterpreted = it.kept[e.opts]
	}
	if len(it.interpreted) > 0 {
		b.f.Locations = updateSourceCodeInfo(b.f.Locations, it.interpreted)
	}
}

func (it *interpreter) interpret(e optionsElement, extensions bool) {
	if e.opts.Interpreted == nil {
		t := it.b.l.syms[e.typeName]
		if t == nil || t.kind != symMessage {
			return
		}
		e.opts.Interpreted = newMessageValue(t.msg)
	}
	for i, u := range e.opts.Uninterpreted {
		if !u.Name[0].IsExtension && u.Name[0].Part == "uninterpreted_option" {
			if extensions {
				it.b.optionError(u, false, `Option must not use reserved name "uninterpreted_option".`)
				return
			}
			continue
		}
		if u.Name[0].IsExtension != extensions {
			continue
		}
		if !it.one(e, u, cat(e.path, uninterpretedNum, int32(i))) {
			return
		}
	}
}

func (it *interpreter) keep(e optionsElement, u *uninterpretedOption, src []int32) bool {
	it.kept[e.opts] = append(it.kept[e.opts], u)
	it.b.u.Unresolved = append(it.b.u.Unresolved, src)
	return true
}

// optionTarget is the field an option name sets: the field, the message
// fields leading to it, the location path of the field, and the name as
// protoc's messages spell it.
type optionTarget struct {
	field *Field
	name  string
	chain []*Field
	dest  []int32
}

// one is InterpretSingleOption: false, with protoc's error, when the option
// cannot be set. An option needing a name no scope resolves stays
// uninterpreted where the file may name unknown types, and is protoc's
// error otherwise.
func (it *interpreter) one(e optionsElement, u *uninterpretedOption, src []int32) bool {
	t, need, ok := it.target(e, u)
	switch {
	case need != "" && !it.b.allowsUnknown():
		it.b.optionError(u, false, unknownOption(t.name))
		return false
	case need != "":
		return it.keep(e, u, src)
	}
	if !ok {
		return false
	}
	field := t.field
	target := e.opts.Interpreted
	for _, c := range t.chain {
		fv := target.slot(c)
		if len(fv.Values) == 0 {
			fv.Values = append(fv.Values, &optionValue{Message: newMessageValue(c.msgType)})
		}
		target = fv.Values[0].Message
	}
	if fv := target.Fields[field.Number]; field.Label != labelRepeated && fv != nil && len(fv.Values) > 0 {
		it.b.optionError(u, false, "Option "+quoted(t.name)+" was already set.")
		return false
	}
	v, need, ok := it.value(field, u)
	if need != "" {
		return it.keep(e, u, src)
	}
	if !ok {
		return false
	}
	fv := target.slot(field)
	fv.Values = append(fv.Values, v)
	dest := t.dest
	if field.Label == labelRepeated {
		k := pathKey(dest)
		dest = append(dest, it.repeated[k])
		it.repeated[k]++
	}
	it.interpreted[pathKey(src)] = dest
	return true
}

// target resolves an option's name part by part, as InterpretSingleOption's
// loop does.
func (it *interpreter) target(e optionsElement, u *uninterpretedOption) (t optionTarget, need string, ok bool) {
	desc := e.opts.Interpreted.Type
	t.dest = cat(e.path)
	var name strings.Builder
	for i, np := range u.Name {
		if i > 0 {
			name.WriteByte('.')
		}
		if np.IsExtension {
			name.WriteString("(" + np.Part + ")")
		} else {
			name.WriteString(np.Part)
		}
		t.name = name.String()
		field, need := it.part(desc, np, e.scope)
		if need != "" {
			return t, need, false
		}
		t.field = field
		if need, ok := it.checkPart(u, np, desc, t, i == len(u.Name)-1); need != "" || !ok {
			return t, need, false
		}
		t.dest = append(t.dest, field.Number)
		if i < len(u.Name)-1 {
			t.chain = append(t.chain, field)
			desc = field.msgType
		}
	}
	if t.field.Type == 0 {
		return t, t.field.TypeName, false
	}
	return t, "", true
}

// part is the field one name part names: a field of desc, or an extension
// resolved from scope, nil when it names neither; need is an extension name
// no scope resolves.
func (it *interpreter) part(desc *Message, np namePart, scope string) (field *Field, need string) {
	if !np.IsExtension {
		return fieldNamed(desc, np.Part), ""
	}
	s := it.b.lookup(np.Part, scope, false)
	switch {
	case s == nil:
		return nil, np.Part
	case s.kind == symField:
		return s.field, ""
	}
	return nil, ""
}

func unknownOption(name string) string {
	return "Option " + quoted(name) + " unknown. Ensure that your proto definition file imports the proto which defines the option (i.e. via import option after edition 2024)."
}

func (b *builder) definedHere(f *Field) bool {
	s := b.l.syms[f.FullName]
	return s != nil && s.file == b.f
}

// checkPart checks the field one name part resolved to: it must belong to
// desc and, before the last part, be a singular message.
func (it *interpreter) checkPart(u *uninterpretedOption, np namePart, desc *Message, t optionTarget, last bool) (need string, ok bool) {
	b, field := it.b, t.field
	switch {
	case field == nil:
		b.optionError(u, false, unknownOption(t.name))
		return "", false
	case np.IsExtension && field.extendee == nil:
		return field.Extendee, false
	case np.IsExtension && field.extendee != desc:
		b.optionError(u, false, "Option field "+quoted(t.name)+" is not a field or extension of message "+quoted(desc.Name)+".")
		return "", false
	case !u.Name[0].IsExtension && u.Name[0].Part == featuresOption && b.definedHere(field):
		b.optionError(u, false, "Feature "+quoted(t.name)+" can't be used in the same file it's defined in.")
		return "", false
	case last:
		return "", true
	case field.Type == 0:
		return field.TypeName, false
	case field.Type != fieldMessage && field.Type != fieldGroup:
		b.optionError(u, false, "Option "+quoted(t.name)+" is an atomic type, not a message.")
		return "", false
	case field.Label == labelRepeated:
		b.optionError(u, false, "Option field "+quoted(t.name)+" is a repeated message. Repeated message options must be initialized using an aggregate value.")
		return "", false
	}
	return "", true
}

func fieldNamed(m *Message, name string) *Field {
	for _, f := range m.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// optionError records msg at the option's name or, for value, its value.
func (b *builder) optionError(u *uninterpretedOption, value bool, msg string) {
	e := fileError{Message: msg, Line: u.Line, Column: u.Column}
	if value {
		e.Line, e.Column = u.ValueLine, u.ValueColumn
	}
	b.errs = append(b.errs, e)
}

// intRange is the bounds of an integer field type, as protoc prints them.
func intRange(t fieldType) (lo int64, hi uint64, kind string) {
	switch t {
	case fieldInt32, fieldSint32, fieldSfixed32:
		return math.MinInt32, math.MaxInt32, typeInt32
	case fieldInt64, fieldSint64, fieldSfixed64:
		return math.MinInt64, math.MaxInt64, typeInt64
	case fieldUint32, fieldFixed32:
		return 0, math.MaxUint32, typeUint32
	}
	return 0, math.MaxUint64, typeUint64
}

// value is SetOptionValue: the option's value as field's type reads it.
// need names a type or extension an aggregate value needs and no scope
// resolves.
func (it *interpreter) value(field *Field, u *uninterpretedOption) (v *optionValue, need string, ok bool) {
	fail := func(msg string) (*optionValue, string, bool) {
		it.b.optionError(u, true, msg)
		return nil, "", false
	}
	name := quoted(field.FullName)
	var scalar, msg string
	switch t := field.Type; t {
	case fieldInt32, fieldSint32, fieldSfixed32, fieldInt64, fieldSint64, fieldSfixed64, fieldUint32, fieldFixed32, fieldUint64, fieldFixed64:
		scalar, msg = intOption(t, u, name)
	case fieldFloat, fieldDouble:
		scalar, msg = floatOption(t, u, name)
	case fieldBool:
		scalar, msg = boolOption(u, name)
	case fieldEnum:
		if u.Identifier == nil {
			return fail("Value must be identifier for enum-valued option " + name + ".")
		}
		return it.enumValue(field, *u.Identifier, fail)
	case fieldString, fieldBytes:
		if u.String == nil {
			return fail("Value must be quoted string for string option " + name + ".")
		}
		scalar = *u.String
	default:
		return it.aggregateValue(field, u, fail)
	}
	if msg != "" {
		return fail(msg)
	}
	return &optionValue{Scalar: scalar}, "", true
}

func (it *interpreter) aggregateValue(field *Field, u *uninterpretedOption, fail func(string) (*optionValue, string, bool)) (*optionValue, string, bool) {
	if u.Aggregate == nil {
		return fail("Option " + quoted(field.FullName) + ` is a message. To set the entire message, use syntax like "` + field.Name +
			` = { <proto text format> }". To set fields within it, use syntax like "` + field.Name + `.foo = value".`)
	}
	m, need, msg := it.b.parseTextFormat(*u.Aggregate, field.msgType)
	switch {
	case need != "":
		return nil, need, false
	case msg != "":
		return fail("Error while parsing option value for " + quoted(field.Name) + ": " + msg)
	}
	return &optionValue{Message: m}, "", true
}

func boolOption(u *uninterpretedOption, quotedName string) (value, msg string) {
	switch {
	case u.Identifier == nil:
		return "", "Value must be identifier for boolean option " + quotedName + "."
	case *u.Identifier != litTrue && *u.Identifier != litFalse:
		return "", `Value must be "true" or "false" for boolean option ` + quotedName + "."
	}
	return *u.Identifier, ""
}

// intOption reads an integer option's value in t's range; quotedName is the
// option's name as the message quotes it.
func intOption(t fieldType, u *uninterpretedOption, quotedName string) (value, msg string) {
	lo, hi, kind := intRange(t)
	bounds := fmt.Sprintf("%d to %d, for %s option %s.", lo, hi, kind, quotedName)
	switch {
	case u.Positive != nil && *u.Positive > hi, u.Negative != nil && lo < 0 && *u.Negative < lo:
		return "", "Value out of range, " + bounds
	case u.Positive != nil:
		return strconv.FormatUint(*u.Positive, 10), ""
	case u.Negative != nil && lo < 0:
		return strconv.FormatInt(*u.Negative, 10), ""
	}
	return "", "Value must be integer, from " + bounds
}

func floatOption(t fieldType, u *uninterpretedOption, quotedName string) (value, msg string) {
	var d float64
	switch {
	case u.Double != nil:
		d = *u.Double
	case u.Positive != nil:
		d = float64(*u.Positive)
	case u.Negative != nil:
		d = float64(*u.Negative)
	case u.Identifier != nil && *u.Identifier == litInf:
		d = math.Inf(1)
	case u.Identifier != nil && *u.Identifier == litNaN:
		d = math.NaN()
	default:
		kind := typeDouble
		if t == fieldFloat {
			kind = typeFloat
		}
		return "", "Value must be number for " + kind + " option " + quotedName + "."
	}
	return formatFloat(d, t == fieldFloat), ""
}

// enumValue resolves an enum option's identifier as a sibling of the enum,
// which is how protoc scopes enum values.
func (it *interpreter) enumValue(field *Field, ident string, fail func(string) (*optionValue, string, bool)) (*optionValue, string, bool) {
	e := field.enumType
	scope := strings.TrimSuffix(e.FullName, e.Name)
	if s := it.b.l.syms[scope+ident]; s != nil && s.kind == symEnumValue {
		if s.enum != e {
			return fail("Enum type " + quoted(e.FullName) + " has no value named " + quoted(ident) + " for option " + quoted(field.FullName) + ". This appears to be a value from a sibling type.")
		}
		for _, v := range e.Values {
			if v.Name == ident {
				return &optionValue{Scalar: strconv.Itoa(int(v.Number))}, "", true
			}
		}
	}
	return fail("Enum type " + quoted(e.FullName) + " has no value named " + quoted(ident) + " for option " + quoted(field.FullName) + ".")
}

// formatFloat writes a float option's value in Go's shortest form for its
// width.
func formatFloat(v float64, single bool) string {
	switch {
	case math.IsNaN(v):
		return litNaN
	case math.IsInf(v, 1):
		return litInf
	case math.IsInf(v, -1):
		return "-" + litInf
	case single:
		return strconv.FormatFloat(float64(float32(v)), 'g', -1, 32)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// updateSourceCodeInfo is OptionInterpreter::UpdateSourceCodeInfo: each
// interpreted option's location takes the path of the field it set, and the
// locations under it are dropped.
func updateSourceCodeInfo(locs []*Location, interpreted map[string][]int32) []*Location {
	out := make([]*Location, 0, len(locs))
	var matched []int32
	for _, l := range locs {
		if matched != nil && len(l.Path) >= len(matched) && slicesHavePrefix(l.Path, matched) {
			continue
		}
		matched = nil
		dest, ok := interpreted[pathKey(l.Path)]
		if !ok {
			out = append(out, l)
			continue
		}
		matched = l.Path
		r := *l
		r.Path = cat(dest)
		out = append(out, &r)
	}
	return out
}

func slicesHavePrefix(s, prefix []int32) bool {
	for i, x := range prefix {
		if s[i] != x {
			return false
		}
	}
	return true
}

// featureDefaults is the FeatureSet an edition starts from: for each feature
// of the loaded descriptor.proto's FeatureSet, the value of the latest of its
// edition_defaults at or before the edition.
func (l *linker) featureDefaults(edition int32) featureSet {
	if fs, ok := l.defaults[edition]; ok {
		return fs
	}
	var fs featureSet
	for _, f := range l.featureFields() {
		fs[f.Number] = editionDefault(f, edition)
	}
	if l.defaults == nil {
		l.defaults = map[int32]featureSet{}
	}
	l.defaults[edition] = fs
	return fs
}

// featureFields is the FeatureSet fields a featureSet holds.
func (l *linker) featureFields() []*Field {
	s := l.syms[featureSetMessage]
	if s == nil || s.kind != symMessage {
		return nil
	}
	var out []*Field
	for _, f := range s.msg.Fields {
		if f.Number > 0 && int(f.Number) < len(featureSet{}) && f.enumType != nil {
			out = append(out, f)
		}
	}
	return out
}

// editionDefault is the enum number of f's latest edition_defaults entry at
// or before edition, 0 when none applies.
func editionDefault(f *Field, edition int32) int32 {
	if f.Options == nil || f.Options.Interpreted == nil {
		return 0
	}
	best, value := int64(-1), int32(0)
	for _, d := range valuesOf(f.Options.Interpreted, fieldOptEditionDefaults) {
		e, err := strconv.ParseInt(d.Message.scalar(editionDefaultEdition).Scalar, 10, 32)
		if err != nil || e > int64(edition) || e <= best {
			continue
		}
		name := d.Message.scalar(editionDefaultValue).Scalar
		if i := slices.IndexFunc(f.enumType.Values, func(v *EnumValue) bool { return v.Name == name }); i >= 0 {
			best, value = e, f.enumType.Values[i].Number
		}
	}
	return value
}

func valuesOf(m *MessageValue, n int32) []*optionValue {
	if fv := m.Fields[n]; fv != nil {
		return fv.Values
	}
	return nil
}

// featureValueName is the enum value name of feature's value, as
// descriptor.proto spells it.
func (l *linker) featureValueName(feature, value int32) string {
	for _, f := range l.featureFields() {
		if f.Number != feature {
			continue
		}
		if i := slices.IndexFunc(f.enumType.Values, func(v *EnumValue) bool { return v.Number == value }); i >= 0 {
			return f.enumType.Values[i].Name
		}
	}
	return strconv.Itoa(int(value))
}

// ownFeatures is the features option an element sets itself.
func ownFeatures(o *Options) (fs featureSet, set bool) {
	if o == nil || o.Interpreted == nil {
		return fs, false
	}
	var features *fieldValue
	for _, fv := range o.Interpreted.Fields {
		if fv.Field.Name == featuresOption && fv.Field.Extendee == "" {
			features = fv
		}
	}
	if features == nil || len(features.Values) == 0 {
		return fs, false
	}
	for n, fv := range features.Values[0].Message.Fields {
		if n <= 0 || int(n) >= len(fs) || len(fv.Values) == 0 {
			continue
		}
		if v, err := strconv.ParseInt(fv.Values[len(fv.Values)-1].Scalar, 10, 32); err == nil {
			fs[n], set = int32(v), true
		}
	}
	return fs, set
}

// resolveFeatures is ResolveFeaturesImpl over the file's descriptors, each
// merged over its parent's resolved features.
func (b *builder) resolveFeatures() {
	f := b.f
	f.features = b.merge(b.l.featureDefaults(b.edition), f.Options, []int32{fdSyntax}, nil)
	for i, m := range f.Messages {
		b.resolveMessage(m, f.features, []int32{fdMessageType, int32(i)})
	}
	for i, e := range f.Enums {
		b.resolveEnum(e, f.features, []int32{fdEnumType, int32(i)})
	}
	for i, x := range f.Extensions {
		b.resolveField(x, f.features, []int32{fdExtension, int32(i)})
	}
	for i, s := range f.Services {
		path := []int32{fdService, int32(i)}
		s.features = b.merge(f.features, s.Options, cat(path, nameField), nil)
		for j, m := range s.Methods {
			m.features = b.merge(s.features, m.Options, cat(path, sdMethod, int32(j), nameField), nil)
		}
	}
}

func (b *builder) resolveMessage(m *Message, parent featureSet, path []int32) {
	m.features = b.merge(parent, m.Options, cat(path, nameField), nil)
	for i, e := range m.Enums {
		b.resolveEnum(e, m.features, cat(path, dpEnumType, int32(i)))
	}
	for i, o := range m.Oneofs {
		o.features = b.merge(m.features, o.Options, cat(path, dpOneof, int32(i), nameField), nil)
	}
	for i, fl := range m.Fields {
		p := m.features
		if fl.oneof != nil {
			p = fl.oneof.features
		}
		b.resolveField(fl, p, cat(path, dpField, int32(i)))
	}
	for i, n := range m.Nested {
		b.resolveMessage(n, m.features, cat(path, dpNested, int32(i)))
	}
	for i, x := range m.Extensions {
		b.resolveField(x, m.features, cat(path, dpExtension, int32(i)))
	}
	for i, r := range m.ExtRanges {
		b.merge(m.features, r.Options, cat(path, dpExtRange, int32(i)), nil)
	}
}

func (b *builder) resolveEnum(e *Enum, parent featureSet, path []int32) {
	e.features = b.merge(parent, e.Options, cat(path, nameField), nil)
	e.Open = e.features[featureEnumType] == enumTypeOpen
	for i, v := range e.Values {
		v.features = b.merge(e.features, v.Options, cat(path, edValue, int32(i), nameField), nil)
	}
}

func (b *builder) resolveField(fl *Field, parent featureSet, path []int32) {
	fl.features = b.merge(parent, fl.Options, cat(path, nameField), func(fs *featureSet) {
		if fl.Label == labelRequired {
			fs[featureFieldPresence] = presenceLegacyRequired
		}
		if fl.Type == fieldGroup {
			fs[featureMessageEncoding] = encodingDelimited
		}
		if fl.Options.isTrue(fieldOptPacked) {
			fs[featureRepeatedFieldEncoding] = encodingPacked
		}
		if b.edition == editionProto3 && fl.Options.has(fieldOptPacked) && !fl.Options.isTrue(fieldOptPacked) {
			fs[featureRepeatedFieldEncoding] = encodingExpanded
		}
	})
}

// merge is the element's own features over its parent's; a legacy file
// infers some from labels and options instead, and may set none itself.
func (b *builder) merge(parent featureSet, o *Options, namePath []int32, infer func(*featureSet)) featureSet {
	own, set := ownFeatures(o)
	legacy := b.edition < edition2023
	if legacy && set {
		b.errorAt(namePath, "Features are only valid under editions.")
	}
	if legacy && infer != nil {
		infer(&own)
	}
	for n, v := range own {
		if v != 0 {
			parent[n] = v
		}
	}
	return parent
}
