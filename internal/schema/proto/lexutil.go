package proto

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"
)

func digitValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'z':
		return c - 'a' + 10
	case c >= 'A' && c <= 'Z':
		return c - 'A' + 10
	}
	return 36
}

// parseInteger is Tokenizer::ParseInteger: a leading 0x is hex, any other
// leading 0 octal; false, with 0, when text is no integer or exceeds limit.
func parseInteger(text string, limit uint64) (uint64, bool) {
	base := uint64(10)
	s := text
	if s != "" && s[0] == '0' {
		if len(s) > 1 && (s[1] == 'x' || s[1] == 'X') {
			base = 16
			s = s[2:]
		} else {
			base = 8
		}
	}
	var r uint64
	for i := range len(s) {
		d := uint64(digitValue(s[i]))
		if d >= base || r > (math.MaxUint64-d)/base {
			return 0, false
		}
		r = r*base + d
	}
	if r > limit {
		return 0, false
	}
	return r, true
}

// parseFloat is Tokenizer::ParseFloat over a token the tokenizer accepted as
// a float, whose text format f suffix and dangling exponent marker strtod
// leaves unread; an overflow is the infinity strtod returns.
func parseFloat(text string) float64 {
	v, err := strconv.ParseFloat(strings.TrimRight(strings.TrimRight(text, "fF"), "eE+-"), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0
	}
	return v
}

// appendUTF8 is tokenizer.cc's AppendUTF8: a surrogate is encoded as three
// raw bytes, not replaced, and a code point past U+10FFFF is written back as
// its \U escape.
func appendUTF8(cp uint32, sb *strings.Builder) {
	var encoded uint32
	var n int
	switch {
	case cp <= 0x7f:
		encoded, n = cp, 1
	case cp <= 0x7ff:
		encoded, n = 0xc080|(cp&0x7c0)<<2|cp&0x3f, 2
	case cp <= 0xffff:
		encoded, n = 0xe08080|(cp&0xf000)<<4|(cp&0xfc0)<<2|cp&0x3f, 3
	case cp <= 0x10ffff:
		encoded, n = 0xf0808080|(cp&0x1c0000)<<6|(cp&0x3f000)<<4|(cp&0xfc0)<<2|cp&0x3f, 4
	default:
		h := strconv.FormatUint(uint64(cp), 16)
		sb.WriteString(`\U` + strings.Repeat("0", 8-len(h)) + h)
		return
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], encoded)
	sb.Write(b[4-n:])
}

func readHex(s string, n int) (uint32, bool) {
	if len(s) < n || n == 0 {
		return 0, false
	}
	var r uint32
	for i := range n {
		r = r<<4 + uint32(digitValue(s[i]))
	}
	return r, true
}

// parseStringAppend is Tokenizer::ParseStringAppend: text is a quoted string
// token, its escapes already checked by the tokenizer.
func parseStringAppend(text string, sb *strings.Builder) {
	if text == "" {
		return
	}
	quote := text[0]
	for i := 1; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\\' && i+1 < len(text):
			i = appendEscape(text, i+1, sb)
		case c == quote && i == len(text)-1:
		default:
			sb.WriteByte(c)
		}
	}
}

// appendEscape writes the escape whose letter is text[i] and returns the index
// of its last byte.
func appendEscape(text string, i int, sb *strings.Builder) int {
	c := text[i]
	switch {
	case isOctal(c):
		return appendCode(text, i, digitValue(c), 8, isOctal, sb)
	case c == 'x' || c == 'X':
		return appendCode(text, i, 0, 16, isHex, sb)
	case c == 'u' || c == 'U':
		return appendUnicode(text, i, sb)
	}
	sb.WriteByte(translateEscape(c))
	return i
}

// appendCode writes the byte that up to two more digits of class after
// text[i] spell in base, starting from code, and returns the index of the
// last digit read. A code past 255 keeps its low byte, as protoc's char cast
// does.
func appendCode(text string, i int, code, base byte, class func(byte) bool, sb *strings.Builder) int {
	for range 2 {
		if i+1 < len(text) && class(text[i+1]) {
			i++
			code = code*base + digitValue(text[i])
		}
	}
	sb.WriteByte(code)
	return i
}

// appendUnicode writes the code point of a \u or \U escape at text[i], a
// \u surrogate pair joined, or the letter alone when its digits are missing.
func appendUnicode(text string, i int, sb *strings.Builder) int {
	n := 4
	if text[i] == 'U' {
		n = 8
	}
	cp, ok := readHex(text[i+1:], n)
	if !ok {
		sb.WriteByte(text[i])
		return i
	}
	j := i + 1 + n
	if cp >= 0xd800 && cp < 0xdc00 && j+1 < len(text) && text[j] == '\\' && text[j+1] == 'u' {
		if trail, ok := readHex(text[j+2:], 4); ok && trail >= 0xdc00 && trail < 0xe000 {
			cp = 0x10000 + ((cp-0xd800)<<10 | (trail - 0xdc00))
			j += 6
		}
	}
	appendUTF8(cp, sb)
	return j - 1
}

func translateEscape(c byte) byte {
	switch c {
	case 'a':
		return '\a'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	case '\\', '?', '\'', '"':
		return c
	}
	return '?'
}

// cEscape is absl::CEscape, which protoc applies to a bytes field's default:
// octal for every byte outside printable ASCII.
func cEscape(s string) string {
	var sb strings.Builder
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '"':
			sb.WriteString(`\"`)
		case '\'':
			sb.WriteString(`\'`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			if c < 0x20 || c >= 0x7f {
				sb.WriteByte('\\')
				sb.WriteByte('0' + c>>6)
				sb.WriteByte('0' + (c>>3)&7)
				sb.WriteByte('0' + c&7)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	return sb.String()
}

// simpleDtoa is io::SimpleDtoa, the text protoc writes for a float default:
// %.15g, or %.17g when that does not read back as the same value.
func simpleDtoa(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return litInf
	case math.IsInf(v, -1):
		return "-" + litInf
	case math.IsNaN(v):
		return litNaN
	}
	s := strconv.FormatFloat(v, 'g', 15, 64)
	if f, _ := strconv.ParseFloat(s, 64); f != v {
		s = strconv.FormatFloat(v, 'g', 17, 64)
	}
	return s
}

// Words protoc's grammar, its option values and the text format share.
const (
	litTrue        = "true"
	litFalse       = "false"
	litInf         = "inf"
	litNaN         = "nan"
	featuresOption = "features"
	typeDouble     = "double"
	typeFloat      = "float"
	typeInt32      = "int32"
	typeInt64      = "int64"
	typeUint32     = "uint32"
	typeUint64     = "uint64"
)

// quoted is s in double quotes, unescaped, as protoc's messages quote names.
func quoted(s string) string { return `"` + s + `"` }
