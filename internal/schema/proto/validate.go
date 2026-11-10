package proto

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// validate is DescriptorBuilder::ValidateOptions over the file's
// descriptors, in VisitDescriptors order.
func (b *builder) validate() {
	f := b.f
	b.validateFile()
	for i, m := range f.Messages {
		b.validateMessage(m, []int32{fdMessageType, int32(i)})
	}
	for i, e := range f.Enums {
		b.validateEnum(e, []int32{fdEnumType, int32(i)})
	}
	for i, x := range f.Extensions {
		b.validateField(x, []int32{fdExtension, int32(i)})
	}
	for i := range f.Services {
		if isLite(f) && (f.Options.isTrue(foCCGenericServices) || f.Options.isTrue(foJavaGenericServices)) {
			b.errorAt([]int32{fdService, int32(i), nameField}, "Files with optimize_for = LITE_RUNTIME cannot define services unless you set both options cc_generic_services and java_generic_services to false.")
		}
	}
}

func isLite(f *File) bool {
	v := f.Options.scalarOf(foOptimizeFor)
	return v != nil && v.Scalar == strconv.Itoa(optimizeLiteRuntime)
}

func (o *Options) scalarOf(path ...int32) *optionValue {
	if o == nil {
		return nil
	}
	return o.Interpreted.scalar(path...)
}

func (b *builder) validateFile() {
	f := b.f
	if b.edition >= edition2023 {
		if f.features[featureFieldPresence] == presenceLegacyRequired {
			b.errorAt([]int32{fdSyntax}, "Required presence can't be specified by default.")
		}
		if f.Options.isTrue(foJavaStringCheckUTF8) {
			b.errorAt([]int32{fdSyntax}, "File option java_string_check_utf8 is not allowed under editions. Use the (pb.java).utf8_validation feature to control this behavior.")
		}
	}
	if !isLite(f) {
		b.checkLiteImports()
	}
	if b.edition == editionProto3 {
		for i, x := range f.Extensions {
			b.validateProto3Field(x, []int32{fdExtension, int32(i)})
		}
		for i, m := range f.Messages {
			b.validateProto3Message(m, []int32{fdMessageType, int32(i)})
		}
	}
	if b.edition < edition2024 && len(f.OptionDeps) > 0 {
		b.errorAt(nil, "option imports are not supported before edition 2024.")
	}
	if b.edition >= edition2024 && len(f.Weak) > 0 {
		b.errorAt([]int32{fdWeakDep, count(f.Weak) - 1}, "weak imports are not allowed under edition 2024 and beyond.")
	}
}

// checkLiteImports refuses a file that is not lite importing one that is.
func (b *builder) checkLiteImports() {
	for i, d := range b.f.Deps {
		if u := b.l.units[b.l.named(d)]; u != nil && u.Err == nil && u.File != nil && isLite(u.File) {
			b.errorAt([]int32{fdDependency, int32(i)}, `Files that do not use optimize_for = LITE_RUNTIME cannot import files which do use this option.  This file is not lite, but it imports "`+d+`" which is.`)
			return
		}
	}
}

func (b *builder) validateProto3Message(m *Message, path []int32) {
	for i, n := range m.Nested {
		b.validateProto3Message(n, cat(path, dpNested, int32(i)))
	}
	for i, fl := range m.Fields {
		b.validateProto3Field(fl, cat(path, dpField, int32(i)))
	}
	for i, x := range m.Extensions {
		b.validateProto3Field(x, cat(path, dpExtension, int32(i)))
	}
	if len(m.ExtRanges) > 0 {
		b.errorAt(cat(path, dpExtRange, 0), "Extension ranges are not allowed in proto3.")
	}
	if m.Options.isTrue(moMessageSetWireFormat) {
		b.errorAt(cat(path, nameField), "MessageSet is not supported in proto3.")
	}
}

// customOptionExtendees are the options messages a proto3 file may extend.
var customOptionExtendees = map[string]bool{
	fileOptions: true, messageOptions: true, fieldOptions: true, enumOptions: true, enumValueOptions: true,
	serviceOptions: true, methodOptions: true, oneofOptions: true, extensionRangeOptions: true,
}

func (b *builder) validateProto3Field(fl *Field, path []int32) {
	if fl.Extendee != "" && (fl.extendee == nil || !customOptionExtendees[fl.extendee.FullName]) {
		b.errorAt(cat(path, fldExtendee), "Extensions in proto3 are only allowed for defining options.")
	}
	if fl.Label == labelRequired {
		b.fieldTypeError(path, "Required fields are not allowed in proto3.")
	}
	if fl.Default != nil {
		b.errorAt(cat(path, fldDefault), "Explicit default values are not allowed in proto3.")
	}
	if fl.enumType != nil && !fl.enumType.Open {
		b.fieldTypeError(path, `Enum type "`+fl.enumType.FullName+`" is not an open enum, but is used in "`+fl.container().FullName+`" which is a proto3 message type.`)
	}
	if fl.Type == fieldGroup {
		b.fieldTypeError(path, "Groups are not supported in proto3 syntax.")
	}
}

// container is the message a field belongs to on the wire: its extendee for
// an extension.
func (fl *Field) container() *Message {
	if fl.Extendee != "" {
		return fl.extendee
	}
	return fl.owner
}

func (b *builder) validateMessage(m *Message, path []int32) {
	if !m.Options.isTrue(moLegacyJSONConflicts) {
		b.checkJSONNames(m, path, false)
		b.checkJSONNames(m, path, true)
	}
	limit := int32(maxFieldNumber)
	if m.Options.isTrue(moMessageSetWireFormat) {
		limit = 1<<31 - 1
	}
	for _, r := range m.ExtRanges {
		if int64(r.End) > int64(limit)+1 {
			b.errorAt(nil, fmt.Sprintf("Extension numbers cannot be greater than %d.", limit))
		}
	}
	for i, e := range m.Enums {
		b.validateEnum(e, cat(path, dpEnumType, int32(i)))
	}
	for i, fl := range m.Fields {
		b.validateField(fl, cat(path, dpField, int32(i)))
	}
	for i, n := range m.Nested {
		b.validateMessage(n, cat(path, dpNested, int32(i)))
	}
	for i, x := range m.Extensions {
		b.validateField(x, cat(path, dpExtension, int32(i)))
	}
}

// toJSONName is descriptor.cc's ToJsonName.
func toJSONName(name string) string {
	var sb strings.Builder
	up := false
	for i := range len(name) {
		c := name[i]
		switch {
		case c == '_':
			up = true
		case up:
			sb.WriteByte(upper(c))
			up = false
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c - 'A' + 'a'
	}
	return c
}

// jsonName is GetJsonNameDetails: the JSON name a field takes in one pass of
// the uniqueness check.
type jsonName struct {
	field  *Field
	name   string
	custom bool
}

func (j jsonName) kind() string {
	if j.custom {
		return "custom"
	}
	return "default"
}

// checkJSONNames is CheckFieldJsonNameUniqueness, by default JSON names
// alone or with custom ones; a conflict involving a default name is a
// warning under LEGACY_BEST_EFFORT, which Link does not report.
func (b *builder) checkJSONNames(m *Message, path []int32, custom bool) {
	seen := map[string]jsonName{}
	for i, fl := range m.Fields {
		namePath := cat(path, dpField, int32(i), nameField)
		d := jsonName{field: fl, name: toJSONName(fl.Name)}
		if custom && fl.JSONName != nil && *fl.JSONName != d.name {
			d = jsonName{field: fl, name: *fl.JSONName, custom: true}
		}
		if d.custom && strings.HasPrefix(d.name, "[") && strings.HasSuffix(d.name, "]") {
			b.errorAt(namePath, fmt.Sprintf(`The custom JSON name of field %s (%s) is invalid: JSON names may not start with '[' and end with ']'.`, quoted(fl.Name), quoted(d.name)))
			continue
		}
		prev, ok := seen[d.name]
		switch {
		case !ok:
			seen[d.name] = d
		case custom && !d.custom && !prev.custom:
		case m.features[featureJSONFormat] == jsonFormatLegacyBestEffort && (!d.custom || !prev.custom):
		default:
			b.errorAt(namePath, jsonConflict(d, prev))
		}
	}
}

func jsonConflict(d, prev jsonName) string {
	suffix := ""
	if d.name != prev.name {
		suffix = " (" + quoted(prev.name) + ")"
	}
	return fmt.Sprintf("The %s JSON name of field %s (%s) conflicts with the %s JSON name of field %s%s.",
		d.kind(), quoted(d.field.Name), quoted(d.name), prev.kind(), quoted(prev.field.Name), suffix)
}

func (fl *Field) packable() bool {
	if fl.Label != labelRepeated {
		return false
	}
	switch fl.Type {
	case 0, fieldString, fieldBytes, fieldMessage, fieldGroup:
		return false
	}
	return true
}

// hasPresence is FieldDescriptor::has_presence.
func (fl *Field) hasPresence() bool {
	if fl.Label == labelRepeated {
		return false
	}
	return fl.Type == fieldMessage || fl.Type == fieldGroup || fl.Extendee != "" || fl.oneof != nil ||
		fl.features[featureFieldPresence] != presenceImplicit
}

// validateField is ValidateOptions for a field. A field whose type no scope
// resolves gets only the checks its type does not decide.
func (b *builder) validateField(fl *Field, path []int32) {
	b.validateFieldFeatures(fl, path)
	o := fl.Options
	if b.edition >= edition2024 && o.has(fieldOptCtype) {
		b.fieldTypeError(path, "ctype option is not allowed under edition 2024 and beyond. Use the feature string_type = VIEW|CORD|STRING|... instead.")
	}
	if fl.Extendee != "" && fl.JSONName != nil && *fl.JSONName != toJSONName(fl.Name) {
		b.errorAt(cat(path, fldJSONName), "option json_name is not allowed on extension fields.")
	}
	if fl.JSONName != nil && strings.ContainsRune(*fl.JSONName, 0) {
		b.errorAt(cat(path, fldJSONName), "json_name cannot have embedded null characters.")
	}
	if fl.Type != 0 {
		b.validateFieldType(fl, path)
	}
}

func (b *builder) validateFieldType(fl *Field, path []int32) {
	o := fl.Options
	if o.isTrue(fieldOptLazy) || o.isTrue(fieldOptUnverifiedLazy) {
		if fl.Type != fieldMessage {
			b.fieldTypeError(path, "[lazy = true] can only be specified for submessage fields.")
		}
		if o.isTrue(fieldOptUnverifiedLazy) && fl.Extendee != "" {
			b.fieldTypeError(path, "Extensions cannot be marked as unverified_lazy.")
		}
	}
	if o.isTrue(fieldOptPacked) && !fl.packable() {
		b.fieldTypeError(path, "[packed = true] can only be specified for repeated primitive fields.")
	}
	b.checkMessageSetField(fl, path)
	if fl.extendee != nil && isLite(b.f) && !isLite(fl.extendee.file) {
		b.errorAt(cat(path, fldExtendee), "Extensions to non-lite types can only be declared in non-lite files.  Note that you cannot extend a non-lite type to contain a lite type, but the reverse is allowed.")
	}
	if fl.Type == fieldMessage && fl.msgType != nil && fl.msgType.MapEntry {
		b.checkMapField(fl, path)
	}
	b.checkJSType(fl, path)
}

func (b *builder) checkMessageSetField(fl *Field, path []int32) {
	c := fl.container()
	switch {
	case c == nil || !c.Options.isTrue(moMessageSetWireFormat):
	case fl.Extendee == "":
		b.errorAt(cat(path, nameField), "MessageSets cannot have fields, only extensions.")
	case fl.Label != labelOptional || fl.Type != fieldMessage:
		b.fieldTypeError(path, "Extensions of MessageSets must be optional messages.")
	}
}

// checkMapField refuses a map entry type used as a field type other than by
// the map field it was synthesised for.
func (b *builder) checkMapField(fl *Field, path []int32) {
	t := fl.msgType
	if fl.Label == labelRepeated && t.Name == toCamelCase(fl.Name)+"Entry" && t.parent == fl.owner {
		return
	}
	msg := t.FullName + " is a synthetic MapEntry message type, which is not allowed to be used as a field type."
	if t.parent != nil {
		if s := b.lookup(t.Name, t.parent.FullName, true); s != nil && s.kind == symMessage && s.msg != t {
			msg += ` Maybe you meant ".` + s.msg.FullName + `"?`
		}
	}
	b.fieldTypeError(path, msg)
}

// toCamelCase is descriptor.cc's ToCamelCase with an upper-case first letter.
func toCamelCase(name string) string {
	var sb strings.Builder
	up := true
	for i := range len(name) {
		switch c := name[i]; {
		case c == '_':
			up = true
		case up:
			sb.WriteByte(upper(c))
			up = false
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// The FieldOptions.JSType numbers.
const (
	jsNormal = "0"
	jsString = "1"
	jsNumber = "2"
)

func (b *builder) checkJSType(fl *Field, path []int32) {
	v := fl.Options.scalarOf(fieldOptJstype)
	if v == nil || v.Scalar == jsNormal {
		return
	}
	switch fl.Type {
	case fieldUint64, fieldInt64, fieldSint64, fieldFixed64, fieldSfixed64:
		if v.Scalar == jsString || v.Scalar == jsNumber {
			return
		}
		b.fieldTypeError(path, "Illegal jstype for int64, uint64, sint64, fixed64 or sfixed64 field: "+v.Scalar)
	default:
		b.fieldTypeError(path, "jstype is only allowed on int64, uint64, sint64, fixed64 or sfixed64 fields.")
	}
}

// validateFieldFeatures is ValidateFieldFeatures: the rules an editions
// file's fields and their own features follow.
func (b *builder) validateFieldFeatures(fl *Field, path []int32) {
	if b.edition < edition2023 {
		return
	}
	name := cat(path, nameField)
	if fl.Label == labelRequired {
		b.errorAt(name, "Required label is not allowed under editions.  Use the feature field_presence = LEGACY_REQUIRED to control this behavior.")
	}
	if fl.Type == fieldGroup {
		b.errorAt(name, "Group types are not allowed under editions.  Use the feature message_encoding = DELIMITED to control this behavior.")
	}
	if fl.Options.has(fieldOptPacked) {
		b.errorAt(name, "Field option packed is not allowed under editions.  Use the repeated_field_encoding feature to control this behavior.")
	}
	if fl.Type != 0 && fl.Label != labelRepeated && !fl.hasPresence() {
		if fl.Default != nil {
			b.errorAt(name, "Implicit presence fields can't specify defaults.")
		}
		if fl.enumType != nil && !fl.enumType.Open {
			b.errorAt(name, "Implicit presence enum fields must always be open.")
		}
	}
	if fl.Extendee != "" && fl.features[featureFieldPresence] == presenceLegacyRequired {
		b.errorAt(name, "Extensions can't be required.")
	}
	if c := fl.container(); c == nil || !c.MapEntry {
		b.validateOwnFeatures(fl, name)
	}
}

// validateOwnFeatures checks the features a field sets itself against what
// the field is.
func (b *builder) validateOwnFeatures(fl *Field, name []int32) {
	own, _ := ownFeatures(fl.Options)
	if p := own[featureFieldPresence]; p != 0 {
		b.validateOwnPresence(fl, name, p)
	}
	if fl.Label != labelRepeated && own[featureRepeatedFieldEncoding] != 0 {
		b.errorAt(name, "Only repeated fields can specify repeated field encoding.")
	}
	if fl.Type == 0 {
		return
	}
	if fl.Type != fieldString && !fl.stringMap() && own[featureUTF8Validation] != 0 {
		b.errorAt(name, "Only string fields can specify utf8 validation.")
	}
	if !fl.packable() && own[featureRepeatedFieldEncoding] == encodingPacked {
		b.errorAt(name, "Only repeated primitive fields can specify PACKED repeated field encoding.")
	}
	if (fl.Type != fieldMessage && fl.Type != fieldGroup || fl.msgType != nil && fl.msgType.MapEntry) && own[featureMessageEncoding] != 0 {
		b.errorAt(name, "Only message fields can specify message encoding.")
	}
}

func (b *builder) validateOwnPresence(fl *Field, name []int32, presence int32) {
	switch {
	case fl.oneof != nil:
		b.errorAt(name, "Oneof fields can't specify field presence.")
	case fl.Label == labelRepeated:
		b.errorAt(name, "Repeated fields can't specify field presence.")
	case fl.Extendee != "" && presence != presenceLegacyRequired:
		b.errorAt(name, "Extensions can't specify field presence.")
	case fl.Type == fieldMessage && presence == presenceImplicit:
		b.errorAt(name, "Message fields can't specify implicit presence.")
	}
}

func (fl *Field) stringMap() bool {
	if fl.msgType == nil || !fl.msgType.MapEntry {
		return false
	}
	for _, f := range fl.msgType.Fields {
		if f.Type == fieldString {
			return true
		}
	}
	return false
}

// customJSON is an enum value's (pb.enumvalue.json).string option.
func (v *EnumValue) customJSON() (string, bool) {
	if v.Options == nil || v.Options.Interpreted == nil {
		return "", false
	}
	fv := v.Options.Interpreted.Fields[enumValueJSON]
	if fv == nil || len(fv.Values) == 0 || fv.Field.FullName != "pb.enumvalue.json" {
		return "", false
	}
	s := fv.Values[len(fv.Values)-1].Message.scalar(1)
	if s == nil {
		return "", false
	}
	return s.Scalar, true
}

func (b *builder) validateEnum(e *Enum, path []int32) {
	b.checkEnumPascalNames(e, path)
	b.checkEnumCustomJSON(e)
	if e.Open && len(e.Values) > 0 && e.Values[0].Number != 0 {
		b.errorAt(cat(path, edValue, 0, evNumber), "The first enum value must be zero for open enums.")
	}
	if !e.Options.isTrue(eoAllowAlias) {
		b.checkEnumAliases(e, path)
	}
	for _, v := range e.Values {
		custom, ok := v.customJSON()
		if !ok {
			continue
		}
		if strings.ContainsRune(custom, 0) {
			b.errorAt(nil, "Custom JSON strings cannot contain embedded null characters.")
		}
		if n, err := strconv.ParseInt(custom, 10, 32); err == nil && n != int64(v.Number) {
			b.errorAt(nil, fmt.Sprintf("Custom JSON string %s parses as integer %d, which does not match the enum value's number %d.", quoted(custom), n, v.Number))
		}
	}
}

// checkEnumAliases refuses two values with one number in an enum that does
// not allow aliases.
func (b *builder) checkEnumAliases(e *Enum, path []int32) {
	used := map[int32]string{}
	for i, v := range e.Values {
		prev, ok := used[v.Number]
		if !ok {
			used[v.Number] = v.FullName
			continue
		}
		msg := quoted(v.FullName) + " uses the same enum value as " + quoted(prev) + ". If this is intended, set 'option allow_alias = true;' to the enum definition."
		if next := nextEnumNumber(e, v.Number); next < 1<<31-1 {
			msg += " The next available enum value is " + strconv.FormatInt(next, 10) + "."
		}
		b.errorAt(cat(path, edValue, int32(i), evNumber), msg)
	}
}

func nextEnumNumber(e *Enum, n int32) int64 {
	used := map[int64]bool{}
	for _, v := range e.Values {
		used[int64(v.Number)] = true
	}
	next := int64(n) + 1
	for used[next] {
		next++
	}
	return next
}

// checkEnumPascalNames is CheckEnumValueUniqueness: no two values may agree
// once the enum's name is stripped as a prefix and the rest PascalCased.
func (b *builder) checkEnumPascalNames(e *Enum, path []int32) {
	var prefix strings.Builder
	for i := range len(e.Name) {
		if e.Name[i] != '_' {
			prefix.WriteByte(lower(e.Name[i]))
		}
	}
	seen := map[string]*EnumValue{}
	for i, v := range e.Values {
		key := enumValueToPascalCase(stripEnumPrefix(v.Name, prefix.String()))
		prev, ok := seen[key]
		switch {
		case !ok:
			seen[key] = v
		case prev.Name == v.Name || prev.Number == v.Number:
		case e.Options.isTrue(eoLegacyJSONConflicts) && b.edition == editionProto2:
		default:
			b.errorAt(cat(path, edValue, int32(i), nameField), fmt.Sprintf("Enum name %s has the same name as %s if you ignore case and strip out the enum name prefix (if any). (If you are using allow_alias, please assign the same number to each enum value name.)", v.Name, prev.Name))
		}
	}
}

// stripEnumPrefix is PrefixRemover::MaybeRemove.
func stripEnumPrefix(s, prefix string) string {
	i, j := 0, 0
	for ; i < len(s) && j < len(prefix); i++ {
		if s[i] == '_' {
			continue
		}
		if lower(s[i]) != prefix[j] {
			return s
		}
		j++
	}
	if j < len(prefix) {
		return s
	}
	for i < len(s) && s[i] == '_' {
		i++
	}
	if i == len(s) {
		return s
	}
	return s[i:]
}

func enumValueToPascalCase(s string) string {
	var sb strings.Builder
	up := true
	for i := range len(s) {
		switch c := s[i]; {
		case c == '_':
			up = true
		case up:
			sb.WriteByte(upper(c))
			up = false
		default:
			sb.WriteByte(lower(c))
		}
	}
	return sb.String()
}

// checkEnumCustomJSON is CheckEnumCustomStringUniqueness. protoc records no
// position for an enum value's option value, so these errors carry none.
func (b *builder) checkEnumCustomJSON(e *Enum) {
	alias := e.Options.isTrue(eoAllowAlias)
	first := map[int32]*EnumValue{}
	byName := map[string]*EnumValue{}
	for _, v := range e.Values {
		custom, hasCustom := v.customJSON()
		if primary, ok := first[v.Number]; alias && ok {
			b.checkAliasJSON(v, primary)
		} else if alias {
			first[v.Number] = v
		}
		b.checkJSONString(byName, v, v.Name, false)
		if hasCustom {
			b.checkJSONString(byName, v, custom, true)
		}
	}
}

// checkAliasJSON refuses an alias whose custom JSON string differs from its
// primary value's.
func (b *builder) checkAliasJSON(v, primary *EnumValue) {
	custom, ok := v.customJSON()
	pc, pok := primary.customJSON()
	if pc == custom && pok == ok {
		return
	}
	quote := func(s string, ok bool) string {
		if ok {
			return quoted(s)
		}
		return "unset"
	}
	b.errorAt(nil, fmt.Sprintf("Alias values for number %d must have the same custom JSON string. %s has %s but primary alias %s has %s.",
		v.Number, quoted(v.Name), quote(custom, ok), quoted(primary.Name), quote(pc, pok)))
}

// checkJSONString records name as v's JSON string, refusing it when a value
// of another number already has it.
func (b *builder) checkJSONString(byName map[string]*EnumValue, v *EnumValue, name string, custom bool) {
	prev, ok := byName[name]
	if !ok {
		byName[name] = v
		return
	}
	if prev == v || prev.Number == v.Number {
		return
	}
	current, other := "Default", "custom"
	if custom {
		current = "Custom"
	}
	if prev.Name == name {
		other = "default"
	}
	b.errorAt(nil, fmt.Sprintf("%s JSON string %s conflicts with %s JSON string of %s.", current, quoted(name), other, quoted(prev.Name)))
}

// The suffixes protoc appends to a naming style error.
const (
	namingOptOut          = " (features.enforce_naming_style = STYLE_LEGACY can be used to opt out of this check)"
	namingCollisionOptOut = " (features.enforce_naming_style = STYLE2024 can be used to opt out of this check)"
)

func styleAtLeast(fs featureSet, style int32) bool {
	s := fs[featureEnforceNamingStyle]
	return s >= style && s != 2
}

// validateNaming is ValidateNamingStyle over every descriptor whose resolved
// enforce_naming_style is STYLE2024 or later.
func (b *builder) validateNaming() {
	f := b.f
	if styleAtLeast(f.features, style2024) && f.Package != "" {
		if msg := lowerSnakeCase(f.Package); msg != "" {
			b.errorAt([]int32{fdPackage}, "Package name "+f.Package+" "+msg+namingOptOut)
		}
	}
	for i, m := range f.Messages {
		b.nameMessage(m, []int32{fdMessageType, int32(i)})
	}
	for i, e := range f.Enums {
		b.nameEnum(e, []int32{fdEnumType, int32(i)})
	}
	for i, x := range f.Extensions {
		b.nameField(x, []int32{fdExtension, int32(i)})
	}
	for i, s := range f.Services {
		path := []int32{fdService, int32(i)}
		b.nameTitle("Service", s.Name, s.features, cat(path, nameField))
		for j, m := range s.Methods {
			b.nameTitle("Method", m.Name, m.features, cat(path, sdMethod, int32(j), nameField))
		}
	}
}

func (b *builder) nameTitle(kind, name string, fs featureSet, path []int32) {
	if !styleAtLeast(fs, style2024) {
		return
	}
	if msg := titleCase(name); msg != "" {
		b.errorAt(path, kind+" name "+name+" "+msg+namingOptOut)
	}
}

func (b *builder) nameMessage(m *Message, path []int32) {
	b.nameTitle("Message", m.Name, m.features, cat(path, nameField))
	for i, e := range m.Enums {
		b.nameEnum(e, cat(path, dpEnumType, int32(i)))
	}
	for i, o := range m.Oneofs {
		if !styleAtLeast(o.features, style2024) {
			continue
		}
		p := cat(path, dpOneof, int32(i), nameField)
		if msg := lowerSnakeCase(o.Name); msg != "" {
			b.errorAt(p, "Oneof name "+o.Name+" "+msg+namingOptOut)
		}
		if styleAtLeast(o.features, style2026) {
			if msg := collision(o.Name, m); msg != "" {
				b.errorAt(p, "Oneof name "+o.Name+" "+msg+namingCollisionOptOut)
			}
		}
	}
	for i, fl := range m.Fields {
		b.nameField(fl, cat(path, dpField, int32(i)))
	}
	for i, n := range m.Nested {
		b.nameMessage(n, cat(path, dpNested, int32(i)))
	}
	for i, x := range m.Extensions {
		b.nameField(x, cat(path, dpExtension, int32(i)))
	}
}

func (b *builder) nameField(fl *Field, path []int32) {
	if !styleAtLeast(fl.features, style2024) {
		return
	}
	p := cat(path, nameField)
	if msg := lowerSnakeCase(fl.Name); msg != "" {
		b.errorAt(p, "Field name "+fl.Name+" "+msg+namingOptOut)
	}
	if styleAtLeast(fl.features, style2026) {
		if msg := collision(fl.Name, fl.container()); msg != "" {
			b.errorAt(p, "Field name "+fl.Name+" "+msg+namingCollisionOptOut)
		}
	}
}

func (b *builder) nameEnum(e *Enum, path []int32) {
	b.nameTitle("Enum", e.Name, e.features, cat(path, nameField))
	for i, v := range e.Values {
		if !styleAtLeast(v.features, style2024) {
			continue
		}
		if msg := upperSnakeCase(v.Name); msg != "" {
			b.errorAt(cat(path, edValue, int32(i), nameField), "Enum value name "+v.Name+" "+msg+namingOptOut)
		}
	}
}

// collision is IsValidFieldNonCollisionName: why name would collide in
// generated code with a field or oneof of m, or "".
func collision(name string, m *Message) string {
	if m != nil {
		for _, p := range []string{"has_", "get_", "set_", "clear_"} {
			if rest, ok := strings.CutPrefix(name, p); ok && m.hasMember(rest) {
				return "should not begin with " + p + " if a field named " + rest + " exists. This can cause collisions in generated code."
			}
		}
		if rest, ok := strings.CutSuffix(name, "_value"); ok && m.hasMember(rest) {
			return "should not end with _value if a field named " + rest + " exists. This can cause collisions in generated code."
		}
	}
	if name == "descriptor" {
		return "should not be named descriptor. This can cause collisions in generated code."
	}
	return ""
}

// hasMember reports whether m has a field or a oneof named n.
func (m *Message) hasMember(n string) bool {
	return slices.ContainsFunc(m.Fields, func(f *Field) bool { return f.Name == n }) ||
		slices.ContainsFunc(m.Oneofs, func(o *oneofDecl) bool { return o.Name == n })
}

func isUpperLetter(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLowerLetter(c byte) bool { return c >= 'a' && c <= 'z' }

// badUnderscores is ContainsBadUnderscores.
func badUnderscores(n string) bool {
	if n == "" {
		return false
	}
	if n[0] == '_' || n[len(n)-1] == '_' {
		return true
	}
	for i := 1; i < len(n); i++ {
		if n[i-1] == '_' && !isUpperLetter(n[i]) && !isLowerLetter(n[i]) {
			return true
		}
	}
	return false
}

// titleCase is IsValidTitleCaseName: why n is not TitleCase, or "".
func titleCase(n string) string {
	if n == "" {
		return nameEmpty
	}
	for i := range len(n) {
		if !isUpperLetter(n[i]) && !isLowerLetter(n[i]) && !isDigit(n[i]) {
			return "should be TitleCase"
		}
	}
	if !isUpperLetter(n[0]) {
		return "should begin with a capital letter"
	}
	return ""
}

const nameEmpty = "is empty"

// lowerSnakeCase is IsValidLowerSnakeCaseName, which admits the dots of a
// package name.
func lowerSnakeCase(n string) string {
	if n == "" {
		return nameEmpty
	}
	for i := range len(n) {
		if c := n[i]; !isLowerLetter(c) && !isDigit(c) && c != '_' && c != '.' {
			return "should be lower_snake_case"
		}
	}
	if !isLowerLetter(n[0]) {
		return "should begin with a lower case letter"
	}
	if badUnderscores(n) {
		return "contains style violating underscores"
	}
	return ""
}

func upperSnakeCase(n string) string {
	if n == "" {
		return nameEmpty
	}
	for i := range len(n) {
		if c := n[i]; !isUpperLetter(c) && !isDigit(c) && c != '_' {
			return "should be UPPER_SNAKE_CASE"
		}
	}
	if !isUpperLetter(n[0]) {
		return "should begin with an upper case letter"
	}
	if badUnderscores(n) {
		return "contains style violating underscores"
	}
	return ""
}

// The DefaultSymbolVisibility numbers that make a nested type local.
const (
	visibilityLocalAll = 3
	visibilityStrict   = 4
)

// validateStrictVisibility is SymbolChecker::CheckSymbolVisibilityRules:
// under STRICT no nested type may be export, save an enum namespaced in a
// local message that reserves every number. protoc records no position for
// these errors.
func (b *builder) validateStrictVisibility() {
	var messages, enums []string
	var walk func(m *Message)
	walk = func(m *Message) {
		if m.parent != nil && m.Visibility == visibilityExport && m.features[featureDefaultSymbolVisibility] == visibilityStrict {
			messages = append(messages, m.FullName)
		}
		for _, e := range m.Enums {
			if e.Visibility == visibilityExport && e.features[featureDefaultSymbolVisibility] == visibilityStrict && !namespacedEnum(e, b.f) {
				enums = append(enums, e.FullName)
			}
		}
		for _, n := range m.Nested {
			walk(n)
		}
	}
	for _, m := range b.f.Messages {
		walk(m)
	}
	for _, n := range messages {
		b.errorAt(nil, `"`+n+"\" is a nested message and cannot be `export` with STRICT default_symbol_visibility. It must be moved to top-level, ideally in its own file in order to be `export`.")
	}
	for _, n := range enums {
		b.errorAt(nil, `"`+n+"\" is a nested enum and cannot be marked `export` with STRICT default_symbol_visibility. It must be moved to top-level, ideally in its own file in order to be `export`. For C++ namespacing of enums in a messages use: `local message <OuterNamespace> { export enum "+
			n+" {...} reserved 1 to max; }`")
	}
}

// namespacedEnum is SymbolChecker::IsNamespacedEnum.
func namespacedEnum(e *Enum, f *File) bool {
	c := e.parent
	toLocal := f.features[featureDefaultSymbolVisibility] == visibilityStrict || f.features[featureDefaultSymbolVisibility] == visibilityLocalAll
	if c == nil || c.parent != nil || toLocal && e.Visibility != visibilityExport {
		return false
	}
	local := c.Visibility == visibilityLocal || c.Visibility == visibilityUnset && toLocal
	return local && len(c.ReservedRanges) == 1 && c.ReservedRanges[0] == [2]int32{1, maxFieldNumber + 1}
}
