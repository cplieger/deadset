package proto

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The ExtensionRange field numbers, which no location path names.
const (
	erStart = 1
	erEnd   = 2
)

// wireField is one field of an encoded message: varint and fixed values in
// num, length-delimited and group contents in data.
type wireField struct {
	data []byte
	num  uint64
	tag  int32
	wire int
}

func readVarint(b []byte) (uint64, int, error) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, fmt.Errorf("bad varint")
	}
	return v, n, nil
}

// readFields splits an encoded message into its fields, in wire order. A
// group's contents run to its end tag.
func readFields(b []byte) ([]wireField, error) {
	var out []wireField
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		f := wireField{tag: int32(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			if f.num, n, err = readVarint(b); err != nil {
				return nil, err
			}
		case 1:
			if len(b) < 8 {
				return nil, fmt.Errorf("short fixed64")
			}
			f.num, n = binary.LittleEndian.Uint64(b), 8
		case 5:
			if len(b) < 4 {
				return nil, fmt.Errorf("short fixed32")
			}
			f.num, n = uint64(binary.LittleEndian.Uint32(b)), 4
		case 2:
			l, m, err := readVarint(b)
			if err != nil || uint64(len(b)-m) < l {
				return nil, fmt.Errorf("bad length")
			}
			f.data, n = b[m:m+int(l)], m+int(l)
		case 3:
			end, err := groupEnd(b, f.tag)
			if err != nil {
				return nil, err
			}
			f.data, n = b[:end.start], end.next
		default:
			return nil, fmt.Errorf("wire type %d", f.wire)
		}
		b = b[n:]
		out = append(out, f)
	}
	return out, nil
}

type groupBounds struct{ start, next int }

// groupEnd finds the end tag of the group tag opens, skipping nested fields.
func groupEnd(b []byte, tag int32) (groupBounds, error) {
	i := 0
	for i < len(b) {
		key, n, err := readVarint(b[i:])
		if err != nil {
			return groupBounds{}, err
		}
		start := i
		i += n
		switch key & 7 {
		case 0:
			_, m, err := readVarint(b[i:])
			if err != nil {
				return groupBounds{}, err
			}
			i += m
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			l, m, err := readVarint(b[i:])
			if err != nil {
				return groupBounds{}, err
			}
			i += m + int(l)
		case 3:
			inner, err := groupEnd(b[i:], int32(key>>3))
			if err != nil {
				return groupBounds{}, err
			}
			i += inner.next
		case 4:
			if int32(key>>3) == tag {
				return groupBounds{start: start, next: i}, nil
			}
			return groupBounds{}, fmt.Errorf("mismatched end group")
		}
		if i > len(b) {
			return groupBounds{}, fmt.Errorf("truncated group")
		}
	}
	return groupBounds{}, fmt.Errorf("unterminated group")
}

func packedInts(f wireField) ([]int32, error) {
	if f.wire == 0 {
		return []int32{int32(f.num)}, nil
	}
	var out []int32
	for b := f.data; len(b) > 0; {
		v, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		out = append(out, int32(v))
		b = b[n:]
	}
	return out, nil
}

// descriptorDecoder turns protoc's FileDescriptorProto into a File, its
// options decoded against the types a Link of the same file resolved.
type descriptorDecoder struct {
	types      map[string]*Message
	extensions map[string]map[int32]*Field // by extendee full name
	err        error
}

func newDescriptorDecoder(units map[string]*Unit) *descriptorDecoder {
	d := &descriptorDecoder{types: map[string]*Message{}, extensions: map[string]map[int32]*Field{}}
	var msg func(*Message)
	addExt := func(x *Field) {
		key := x.Extendee[min(1, len(x.Extendee)):]
		if d.extensions[key] == nil {
			d.extensions[key] = map[int32]*Field{}
		}
		d.extensions[key][x.Number] = x
	}
	msg = func(m *Message) {
		d.types[m.FullName] = m
		for _, n := range m.Nested {
			msg(n)
		}
		for _, x := range m.Extensions {
			addExt(x)
		}
	}
	for _, u := range units {
		if u.File == nil {
			continue
		}
		for _, m := range u.File.Messages {
			msg(m)
		}
		for _, x := range u.File.Extensions {
			addExt(x)
		}
	}
	return d
}

func (d *descriptorDecoder) fail(err error) {
	if d.err == nil && err != nil {
		d.err = err
	}
}

func (d *descriptorDecoder) fields(b []byte) []wireField {
	fs, err := readFields(b)
	d.fail(err)
	return fs
}

func str(f wireField) string { return string(f.data) }

func strp(f wireField) *string {
	s := str(f)
	return &s
}

func (d *descriptorDecoder) file(set []byte) *File {
	var raw []byte
	for _, f := range d.fields(set) {
		if f.tag == 1 {
			raw = f.data
		}
	}
	file := &File{}
	for _, f := range d.fields(raw) {
		switch f.tag {
		case 1:
			file.Name = str(f)
		case 2:
			file.Package = str(f)
		case 3:
			file.Deps = append(file.Deps, str(f))
		case fdPublicDep, fdWeakDep:
			ns, err := packedInts(f)
			d.fail(err)
			if f.tag == fdPublicDep {
				file.Public = append(file.Public, ns...)
			} else {
				file.Weak = append(file.Weak, ns...)
			}
		case fdOptionDep:
			file.OptionDeps = append(file.OptionDeps, str(f))
		case fdMessageType:
			file.Messages = append(file.Messages, d.message(f.data))
		case fdEnumType:
			file.Enums = append(file.Enums, d.enum(f.data))
		case fdService:
			file.Services = append(file.Services, d.service(f.data))
		case fdExtension:
			file.Extensions = append(file.Extensions, d.field(f.data))
		case fdOptions:
			file.Options = d.options(fileOptions, f.data)
		case 9:
			file.Locations = d.locations(f.data)
		case fdSyntax:
			file.Syntax = str(f)
		case 14:
			for name, n := range editionNumbers {
				if uint64(n) == f.num {
					file.Edition = name
				}
			}
		}
	}
	return file
}

func (d *descriptorDecoder) message(b []byte) *Message {
	m := &Message{}
	for _, f := range d.fields(b) {
		switch f.tag {
		case 1:
			m.Name = str(f)
		case dpField:
			m.Fields = append(m.Fields, d.field(f.data))
		case dpNested:
			m.Nested = append(m.Nested, d.message(f.data))
		case dpEnumType:
			m.Enums = append(m.Enums, d.enum(f.data))
		case dpExtRange:
			r := &extRange{}
			for _, g := range d.fields(f.data) {
				switch g.tag {
				case erStart:
					r.Start = int32(g.num)
				case erEnd:
					r.End = int32(g.num)
				case erOptions:
					r.Options = d.options(extensionRangeOptions, g.data)
				}
			}
			m.ExtRanges = append(m.ExtRanges, r)
		case dpExtension:
			m.Extensions = append(m.Extensions, d.field(f.data))
		case dpOptions:
			m.Options = d.options(messageOptions, f.data)
		case dpOneof:
			o := &oneofDecl{}
			for _, g := range d.fields(f.data) {
				switch g.tag {
				case 1:
					o.Name = str(g)
				case odOptions:
					o.Options = d.options(oneofOptions, g.data)
				}
			}
			m.Oneofs = append(m.Oneofs, o)
		case dpReservedRange:
			m.ReservedRanges = append(m.ReservedRanges, d.reservedRange(f.data))
		case dpReservedName:
			m.ReservedNames = append(m.ReservedNames, str(f))
		case 11:
			m.Visibility = visibility(f.num)
		}
	}
	return m
}

func (d *descriptorDecoder) reservedRange(b []byte) [2]int32 {
	var r [2]int32
	for _, g := range d.fields(b) {
		if g.tag == rangeStart || g.tag == rangeEnd {
			r[g.tag-1] = int32(g.num)
		}
	}
	return r
}

func (d *descriptorDecoder) field(b []byte) *Field {
	fl := &Field{}
	for _, f := range d.fields(b) {
		switch f.tag {
		case 1:
			fl.Name = str(f)
		case fldExtendee:
			fl.Extendee = str(f)
		case fldNumber:
			fl.Number = int32(f.num)
		case fldLabel:
			fl.Label = fieldLabel(f.num)
		case fldType:
			fl.Type = fieldType(f.num)
		case fldTypeName:
			fl.TypeName = str(f)
		case fldDefault:
			fl.Default = strp(f)
		case fldOptions:
			fl.Options = d.options(fieldOptions, f.data)
		case 9:
			n := int32(f.num)
			fl.OneofIndex = &n
		case fldJSONName:
			fl.JSONName = strp(f)
		case 17:
			fl.Proto3Optional = f.num != 0
		}
	}
	return fl
}

func (d *descriptorDecoder) enum(b []byte) *Enum {
	e := &Enum{}
	for _, f := range d.fields(b) {
		switch f.tag {
		case 1:
			e.Name = str(f)
		case edValue:
			v := &EnumValue{}
			for _, g := range d.fields(f.data) {
				switch g.tag {
				case 1:
					v.Name = str(g)
				case evNumber:
					v.Number = int32(g.num)
				case evOptions:
					v.Options = d.options(enumValueOptions, g.data)
				}
			}
			e.Values = append(e.Values, v)
		case edOptions:
			e.Options = d.options(enumOptions, f.data)
		case edReservedRange:
			e.ReservedRanges = append(e.ReservedRanges, d.reservedRange(f.data))
		case edReservedName:
			e.ReservedNames = append(e.ReservedNames, str(f))
		case 6:
			e.Visibility = visibility(f.num)
		}
	}
	return e
}

func (d *descriptorDecoder) service(b []byte) *Service {
	s := &Service{}
	for _, f := range d.fields(b) {
		switch f.tag {
		case 1:
			s.Name = str(f)
		case sdMethod:
			m := &Method{}
			for _, g := range d.fields(f.data) {
				switch g.tag {
				case 1:
					m.Name = str(g)
				case mdInput:
					m.Input = str(g)
				case mdOutput:
					m.Output = str(g)
				case mdOptions:
					m.Options = d.options(methodOptions, g.data)
				case mdClientStream:
					m.ClientStream = g.num != 0
				case mdServerStream:
					m.ServerStream = g.num != 0
				}
			}
			s.Methods = append(s.Methods, m)
		case sdOptions:
			s.Options = d.options(serviceOptions, f.data)
		}
	}
	return s
}

func (d *descriptorDecoder) locations(b []byte) []*Location {
	var out []*Location
	for _, f := range d.fields(b) {
		if f.tag != 1 {
			continue
		}
		loc := &Location{}
		for _, g := range d.fields(f.data) {
			switch g.tag {
			case 1, 2:
				ns, err := packedInts(g)
				d.fail(err)
				if g.tag == 1 {
					loc.Path = append(loc.Path, ns...)
				} else {
					loc.Span = append(loc.Span, ns...)
				}
			case 3:
				loc.Leading = str(g)
			case 4:
				loc.Trailing = str(g)
			case 6:
				loc.Detached = append(loc.Detached, str(g))
			}
		}
		out = append(out, loc)
	}
	return out
}

func (d *descriptorDecoder) options(typeName string, b []byte) *Options {
	t := d.types[typeName]
	if t == nil {
		d.fail(fmt.Errorf("no type %s", typeName))
		return nil
	}
	return &Options{Interpreted: d.value(t, b)}
}

// value decodes an encoded message of type t. An Any's value bytes are
// decoded as the type its type_url names, as optionValue holds an expansion.
func (d *descriptorDecoder) value(t *Message, b []byte) *MessageValue {
	mv := newMessageValue(t)
	for _, f := range d.fields(b) {
		if f.tag == 1 && f.wire == 3 && t.Options.isTrue(moMessageSetWireFormat) {
			f = d.messageSetItem(f.data)
		}
		fl := fieldNumbered(t, f.tag)
		if fl == nil {
			fl = d.extensions[t.FullName][f.tag]
		}
		if fl == nil {
			d.fail(fmt.Errorf("%s has no field %d", t.FullName, f.tag))
			continue
		}
		slot := mv.slot(fl)
		switch {
		case fl.msgType != nil:
			v := &optionValue{Message: d.value(fl.msgType, f.data)}
			slot.Values = append(slot.Values, v)
		case f.wire == 2 && fl.Type != fieldString && fl.Type != fieldBytes:
			for p := f.data; len(p) > 0; {
				var w wireField
				var n int
				switch fixedWidth(fl.Type) {
				case 8:
					w.num, n = binary.LittleEndian.Uint64(p), 8
				case 4:
					w.num, n = uint64(binary.LittleEndian.Uint32(p)), 4
				default:
					var err error
					if w.num, n, err = readVarint(p); err != nil {
						d.fail(err)
						return mv
					}
				}
				slot.Values = append(slot.Values, &optionValue{Scalar: scalarText(fl.Type, w)})
				p = p[n:]
			}
		default:
			slot.Values = append(slot.Values, &optionValue{Scalar: scalarText(fl.Type, f)})
		}
	}
	if t.FullName == "google.protobuf.Any" {
		d.expandAny(mv)
	}
	return mv
}

// messageSetItem reads a MessageSet item group as the extension field its
// type_id numbers, holding its message bytes.
func (d *descriptorDecoder) messageSetItem(b []byte) wireField {
	item := wireField{wire: 2}
	for _, g := range d.fields(b) {
		switch g.tag {
		case 2:
			item.tag = int32(g.num)
		case 3:
			item.data = g.data
		}
	}
	return item
}

func (d *descriptorDecoder) expandAny(mv *MessageValue) {
	url, value := mv.scalar(1), mv.Fields[2]
	if url == nil || value == nil || len(value.Values) != 1 {
		return
	}
	if t := d.types[url.Scalar[strings.LastIndex(url.Scalar, "/")+1:]]; t != nil {
		value.Values[0] = &optionValue{Message: d.value(t, []byte(value.Values[0].Scalar))}
	}
}

func fixedWidth(t fieldType) int {
	switch t {
	case fieldDouble, fieldFixed64, fieldSfixed64:
		return 8
	case fieldFloat, fieldFixed32, fieldSfixed32:
		return 4
	}
	return 0
}

// scalarText writes a decoded scalar in optionValue's canonical text.
func scalarText(t fieldType, f wireField) string {
	switch t {
	case fieldDouble:
		return formatFloat(math.Float64frombits(f.num), false)
	case fieldFloat:
		return formatFloat(float64(math.Float32frombits(uint32(f.num))), true)
	case fieldInt32, fieldEnum:
		return strconv.FormatInt(int64(int32(f.num)), 10)
	case fieldInt64:
		return strconv.FormatInt(int64(f.num), 10)
	case fieldSint32, fieldSint64:
		return strconv.FormatInt(int64(f.num>>1)^-int64(f.num&1), 10)
	case fieldSfixed32:
		return strconv.FormatInt(int64(int32(uint32(f.num))), 10)
	case fieldSfixed64:
		return strconv.FormatInt(int64(f.num), 10)
	case fieldBool:
		return strconv.FormatBool(f.num != 0)
	case fieldString, fieldBytes:
		return string(f.data)
	}
	return strconv.FormatUint(f.num, 10)
}
