package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// Error is a document Decode refuses, or a report Encode refuses to write:
// the value at fault and what is wrong with it.
type Error struct {
	// Err says what is wrong with the value.
	Err error

	// Pointer is the JSON Pointer (RFC 6901) of the value at fault: the member
	// that is undeclared, absent, named twice, null or out of range, or the
	// object whose members break a rule between them. The empty string names
	// the whole document.
	Pointer string
}

// Error renders the refusal as one line naming the pointer.
func (e *Error) Error() string {
	if e.Pointer == "" {
		return "report: " + e.Err.Error()
	}
	return "report: " + e.Pointer + ": " + e.Err.Error()
}

// Unwrap is the refusal's cause.
func (e *Error) Unwrap() error { return e.Err }

// The refusals a decode or an encode makes, each carried by an [*Error].
var (
	errUndeclared = errors.New("the schema declares no such member")
	errMissing    = errors.New("a required member is absent")
	errDuplicate  = errors.New("the member is named twice")
	errNull       = errors.New("null is not a value the schema admits")
	errForbidden  = errors.New("the member is present where the schema forbids it")
	errShape      = errors.New("no single shape the schema admits")
	errValue      = errors.New("a value the schema refuses")
)

// unmarshalerType is the type a value implements when it decodes itself.
var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// Decode reads one report document. It refuses, with an [*Error] naming the
// value at fault, a document that is not strict JSON, a member the schema does
// not declare (member names compare as bytes), a required member that is
// absent, a member named twice, a null, and every value the report and finding
// schemas refuse. It does not compare the document's schema_version or its
// conformance result with anything.
func Decode(data []byte) (*Report, error) {
	var read Report
	if err := json.Unmarshal(data, &read); err != nil {
		if located, ok := errors.AsType[*Error](err); ok {
			return nil, located
		}
		return nil, &Error{Err: err}
	}
	return &read, nil
}

// UnmarshalJSON reads one report document as [Decode] does, so a report
// decoded by encoding/json is held to the same rules.
func (r *Report) UnmarshalJSON(data []byte) error {
	var read Report
	if err := decodeObject(data, reflect.ValueOf(&read).Elem()); err != nil {
		return err
	}
	if err := read.validate(); err != nil {
		return err
	}
	*r = read
	return nil
}

// Encode writes r in the encoding a merged report is compared in: UTF-8, two
// spaces of indentation, one member or element to a line, an empty array or
// object on one line, every object's members in the schema's order, and one
// trailing line feed. It refuses, with an [*Error] and before writing
// anything, a report [Decode] would refuse, so what it writes reads back. It
// writes to w once, and returns the error of that write wrapped.
func Encode(w io.Writer, r *Report) error {
	if err := r.validate(); err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		return fmt.Errorf("report: encode: %w", err)
	}
	return nil
}

// decodeObject reads the JSON object in data into the struct into holds. Each
// field declares one member by its json tag name, compared as bytes; a tag
// carrying omitzero declares an optional member and every other tag a required
// one. A present optional member must not decode to its field's zero value,
// because the encoding omits a zero optional member; a member whose
// present-and-empty value is meaningful is a pointer or a slice.
func decodeObject(data []byte, into reflect.Value) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return fmt.Errorf("%w: an object is required", errValue)
	}
	object := objectOf(into)
	for decoder.More() {
		if err := object.read(decoder); err != nil {
			return err
		}
	}
	for i, field := range object.fields {
		if !object.seen[i] && !field.optional {
			return at(field.name, errMissing)
		}
	}
	return nil
}

// object is one struct a decode is filling: the member each field declares,
// and which members the document has named so far.
type object struct {
	into   reflect.Value
	byName map[string]int
	fields []declared
	seen   []bool
}

// declared is one member a field declares.
type declared struct {
	name     string
	optional bool
}

// objectOf is the members the struct into holds declares, none of them named
// yet.
func objectOf(into reflect.Value) *object {
	held := &object{into: into, byName: map[string]int{}}
	for field := range into.Type().Fields() {
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		held.byName[name] = len(held.fields)
		held.fields = append(held.fields, declared{name: name, optional: options == "omitzero"})
	}
	held.seen = make([]bool, len(held.fields))
	return held
}

// read reads the next member of the object from decoder into its field.
func (o *object) read(decoder *json.Decoder) error {
	key, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: %w", errValue, err)
	}
	name, _ := key.(string)
	i, isDeclared := o.byName[name]
	switch {
	case !isDeclared:
		return at(name, errUndeclared)
	case o.seen[i]:
		return at(name, errDuplicate)
	}
	o.seen[i] = true
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return at(name, fmt.Errorf("%w: %w", errValue, err))
	}
	field := o.into.Field(i)
	if err := decodeValue(raw, field); err != nil {
		return at(name, err)
	}
	if o.fields[i].optional && field.IsZero() {
		return at(name, fmt.Errorf("%w: %s is empty", errValue, raw))
	}
	return nil
}

// decodeValue reads the JSON value raw into the settable value into, refusing
// null wherever it appears: a value that decodes itself does so, a struct is
// an object decodeObject reads, a slice is an array read element by element,
// a pointer is the value it points to, and anything else is a string, a
// number or a boolean encoding/json reads.
func decodeValue(raw json.RawMessage, into reflect.Value) error {
	if bytes.Equal(raw, []byte("null")) {
		return errNull
	}
	if reflect.PointerTo(into.Type()).Implements(unmarshalerType) {
		return json.Unmarshal(raw, into.Addr().Interface())
	}
	switch into.Kind() {
	case reflect.Struct:
		return decodeObject(raw, into)
	case reflect.Slice:
		return decodeArray(raw, into)
	case reflect.Pointer:
		target := reflect.New(into.Type().Elem())
		if err := decodeValue(raw, target.Elem()); err != nil {
			return err
		}
		into.Set(target)
		return nil
	default:
		if err := json.Unmarshal(raw, into.Addr().Interface()); err != nil {
			return typeError(err)
		}
		return nil
	}
}

// decodeArray reads the JSON array raw into the settable slice into, element
// by element, so an empty array is a non-nil empty slice.
func decodeArray(raw json.RawMessage, into reflect.Value) error {
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return typeError(err)
	}
	slice := reflect.MakeSlice(into.Type(), len(elements), len(elements))
	for i, element := range elements {
		if err := decodeValue(element, slice.Index(i)); err != nil {
			return at(strconv.Itoa(i), err)
		}
	}
	into.Set(slice)
	return nil
}

// typeError is a JSON value of the wrong type, named by the JSON type it is
// and the type the schema admits.
func typeError(err error) error {
	if mismatch, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return fmt.Errorf("%w: the value is a %s, and the schema admits %s", errValue, mismatch.Value, jsonType(mismatch.Type))
	}
	return fmt.Errorf("%w: %w", errValue, err)
}

// jsonType is the JSON type a Go value of type t decodes from.
func jsonType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Slice:
		return "an array"
	case reflect.Int:
		return "an integer"
	default:
		return "an object"
	}
}

// at places err at the member or element segment names below the value it is
// reported against, so the pointer of an error raised deep in a document
// grows one segment per level it passes on its way out. A nil err stays nil.
func at(segment string, err error) error {
	if err == nil {
		return nil
	}
	escaped := "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(segment)
	if located, ok := errors.AsType[*Error](err); ok {
		return &Error{Pointer: escaped + located.Pointer, Err: located.Err}
	}
	return &Error{Pointer: escaped, Err: err}
}

// marshal is the compact JSON encoding of v, with no character escaped that
// strict JSON does not require escaped, so a value that writes itself writes
// the bytes its members would have.
func marshal(v any) ([]byte, error) {
	var written bytes.Buffer
	encoder := json.NewEncoder(&written)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(written.Bytes(), []byte("\n")), nil
}
