// Package proto reads Protocol Buffers source files the way protoc v36.2
// does, into a model shaped like google/protobuf/descriptor.proto's
// FileDescriptorProto, with its source locations and comments: Link reads,
// resolves and validates a set of files.
package proto

import (
	"fmt"
	"strings"
)

// fileError is one error protoc reports for a file. Line and Column are 1-based,
// a tab advancing the column to the next multiple of 8, as protoc prints
// them; both are 0 when protoc names no position.
type fileError struct {
	Message string
	Line    int
	Column  int
}

func (e fileError) Error() string {
	if e.Line == 0 {
		return e.Message
	}
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, e.Message)
}

// Errors is every error of one file, in the order protoc reports them.
type Errors []fileError

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i := range e {
		lines[i] = e[i].Error()
	}
	return strings.Join(lines, "\n")
}

// fieldType is FieldDescriptorProto.Type.
type fieldType int32

// The FieldDescriptorProto.Type numbers.
const (
	fieldDouble   fieldType = 1
	fieldFloat    fieldType = 2
	fieldInt64    fieldType = 3
	fieldUint64   fieldType = 4
	fieldInt32    fieldType = 5
	fieldFixed64  fieldType = 6
	fieldFixed32  fieldType = 7
	fieldBool     fieldType = 8
	fieldString   fieldType = 9
	fieldGroup    fieldType = 10
	fieldMessage  fieldType = 11
	fieldBytes    fieldType = 12
	fieldUint32   fieldType = 13
	fieldEnum     fieldType = 14
	fieldSfixed32 fieldType = 15
	fieldSfixed64 fieldType = 16
	fieldSint32   fieldType = 17
	fieldSint64   fieldType = 18
)

// fieldLabel is FieldDescriptorProto.Label.
type fieldLabel int32

// The FieldDescriptorProto.Label numbers.
const (
	labelOptional fieldLabel = 1
	labelRequired fieldLabel = 2
	labelRepeated fieldLabel = 3
)

// visibility is SymbolVisibility, the edition 2024 `local` and `export`
// modifiers of a message or an enum.
type visibility int32

// The SymbolVisibility numbers.
const (
	visibilityUnset  visibility = 0
	visibilityLocal  visibility = 1
	visibilityExport visibility = 2
)

// Location is one SourceCodeInfo.Location. Path is the descriptor field path
// of the element; Span is 0-based [start line, start column, end line, end
// column], the end line omitted when it is the start line. A comment is ""
// when absent.
type Location struct {
	//deadset:ignore DS1301 -- protoc's leading comment, which the differential holds to protoc's SourceCodeInfo; no reader layer reads it
	Leading string
	//deadset:ignore DS1301 -- protoc's trailing comment, which the differential holds to protoc's SourceCodeInfo; no reader layer reads it
	Trailing string
	Path     []int32
	Span     []int32
	Detached []string
}

// File is one parsed source file. Syntax is "proto2", "proto3" or "editions"
// as the file states it, "" when it states none, which reads as proto2;
// Edition is the edition's name ("2023", "2024", "2026") under "editions".
// Public and Weak index Deps.
type File struct {
	Options    *Options
	Name       string
	Package    string
	Syntax     string
	Edition    string
	Deps       []string
	OptionDeps []string
	Public     []int32
	Weak       []int32
	Messages   []*Message
	Enums      []*Enum
	Services   []*Service
	Extensions []*Field
	Locations  []*Location
	// LineComments is every // comment of the file, in source order.
	LineComments []LineComment
	features     featureSet
}

// LineComment is one // comment: its text from the first slash to the end of
// its line, its 0-based position as a Location span counts it, and whether
// no token precedes it on its line.
type LineComment struct {
	Text   string
	Line   int32
	Column int32
	Alone  bool
}

// Message is a DescriptorProto. A reserved range is [start, end). FullName
// is set by Link.
type Message struct {
	Options        *Options
	parent         *Message
	file           *File
	Name           string
	FullName       string
	Fields         []*Field
	Nested         []*Message
	Enums          []*Enum
	Extensions     []*Field
	ExtRanges      []*extRange
	Oneofs         []*oneofDecl
	ReservedRanges [][2]int32
	ReservedNames  []string
	features       featureSet
	Visibility     visibility
	MapEntry       bool
}

// extRange is an extension range, [Start, End).
type extRange struct {
	Options    *Options
	Start, End int32
}

// Field is a FieldDescriptorProto. As parsed, Type is 0 for a named type and
// TypeName, Extendee hold names as written; Link sets Type and rewrites each
// name it resolves to its fully-qualified ".pkg.Name" form, leaving a name it
// cannot resolve as written, with Type 0. FullName is set by Link.
type Field struct {
	Options        *Options
	Default        *string
	JSONName       *string
	OneofIndex     *int32
	owner          *Message // the containing message, or an extension's scope
	oneof          *oneofDecl
	msgType        *Message
	enumType       *Enum
	extendee       *Message
	Name           string
	FullName       string
	TypeName       string
	Extendee       string
	features       featureSet
	Number         int32
	Label          fieldLabel
	Type           fieldType
	Proto3Optional bool
}

// oneofDecl is a OneofDescriptorProto.
type oneofDecl struct {
	Options  *Options
	Name     string
	features featureSet
}

// Enum is an EnumDescriptorProto. A reserved range is [start, end], both
// inclusive, as EnumReservedRange is. FullName and Open, whether the enum's
// resolved enum_type feature is OPEN, are set by Link.
type Enum struct {
	Options        *Options
	parent         *Message
	Name           string
	FullName       string
	Values         []*EnumValue
	ReservedRanges [][2]int32
	ReservedNames  []string
	features       featureSet
	Visibility     visibility
	Open           bool
}

// EnumValue is an EnumValueDescriptorProto. FullName, a sibling of its enum
// as protoc scopes it, is set by Link.
type EnumValue struct {
	Options  *Options
	Name     string
	FullName string
	features featureSet
	Number   int32
}

// Service is a ServiceDescriptorProto. FullName is set by Link.
type Service struct {
	Options  *Options
	Name     string
	FullName string
	Methods  []*Method
	features featureSet
}

// Method is a MethodDescriptorProto, its types as written until Link
// rewrites each it resolves to ".pkg.Name". FullName is set by Link.
type Method struct {
	Options      *Options
	Name         string
	FullName     string
	Input        string
	Output       string
	features     featureSet
	ClientStream bool
	ServerStream bool
}

// Options holds the options of one element. Link moves each option it
// interprets from Uninterpreted into Interpreted, a value of the element's
// options message; an option naming an extension no scope resolves stays
// uninterpreted.
type Options struct {
	Interpreted   *MessageValue
	Uninterpreted []*uninterpretedOption
}

// uninterpretedOption is one option as written: its name and exactly one
// value. Line and Column locate the name, ValueLine and ValueColumn the
// value, 1-based as fileError counts.
type uninterpretedOption struct {
	Identifier  *string
	Positive    *uint64
	Negative    *int64
	Double      *float64
	String      *string
	Aggregate   *string
	Name        []namePart
	Line        int
	Column      int
	ValueLine   int
	ValueColumn int
}

// namePart is one dot-separated part of an option name; an extension part is
// the name written in parentheses.
type namePart struct {
	Part        string
	IsExtension bool
}
