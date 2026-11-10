package schema

import (
	"fmt"
	"io/fs"
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"
)

// yamlNode is one value of the YAML subset schemas.17 admits: a scalar, a
// sequence or a mapping.
type yamlNode struct {
	values     map[string]*yamlNode
	scalarText string
	items      []*yamlNode
	isScalar   bool
}

// unreadError is a buf file outside the subset, naming the construct.
type unreadError struct{ construct string }

func (e *unreadError) Error() string {
	return "the file uses " + e.construct + ", which the reader does not read"
}

type yamlLine struct {
	text   string
	number int
	indent int
}

func (r *reading) readYAML(name string) (*yamlNode, error) {
	data, err := fs.ReadFile(r.tree, name)
	if err != nil {
		return nil, err
	}
	return parseYAML(string(data))
}

// parseYAML reads block mappings, block sequences and flow sequences of
// scalars, and refuses an anchor, an alias, a tag, a block scalar, a flow
// mapping, a complex key and a second document.
func parseYAML(src string) (*yamlNode, error) {
	lines, err := yamlLines(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return &yamlNode{values: map[string]*yamlNode{}}, nil
	}
	p := &yamlParser{lines: lines}
	node, err := p.block(lines[0].indent)
	if err == nil && p.at < len(lines) {
		err = fmt.Errorf("line %d: unexpected indentation", lines[p.at].number)
	}
	return node, err
}

// yamlLines is every line holding a value, its comment dropped, with its
// indentation.
func yamlLines(src string) ([]yamlLine, error) {
	var lines []yamlLine
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		text := stripComment(raw)
		trimmed := strings.TrimLeft(text, " ")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if trimmed == "---" || trimmed == "..." {
			if len(lines) > 0 || trimmed == "..." {
				return nil, &unreadError{construct: "a second document"}
			}
			continue
		}
		if strings.Contains(text, "\t") && strings.TrimLeft(text, " \t") != trimmed {
			return nil, &unreadError{construct: "a tab in indentation"}
		}
		lines = append(lines, yamlLine{text: strings.TrimRight(trimmed, " "), number: i + 1, indent: len(text) - len(trimmed)})
	}
	return lines, nil
}

// stripComment drops a comment: a # at the start or after a space, outside a
// quoted scalar.
func stripComment(line string) string {
	for i := range unquoted(line, 0) {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ') {
			return line[:i]
		}
	}
	return line
}

// unquoted is the index of every byte of text outside a quoted scalar and its
// quotes, text starting depth flow collections deep. A quote opens a scalar
// only as a node's first character: at the start, after ": ", "- ", "[" or "{"
// and any spaces, and after "," inside a flow collection alone, because a
// block plain scalar may hold a comma (https://yaml.org/spec/1.2.2/#733-plain-style).
// A single-quoted scalar ends at a quote no second quote follows, and a
// double-quoted one at a quote no backslash escapes.
func unquoted(text string, depth int) iter.Seq[int] {
	return func(yield func(int) bool) {
		s := quoteScanner{opens: true, depth: depth}
		for i := 0; i < len(text); i++ {
			skip, inside := s.inside(text, i)
			if !inside && !yield(i) {
				return
			}
			i += skip
		}
	}
}

// quoteScanner is unquoted's state: the open quote, the last byte outside
// quotes other than a space, whether a quote opens a scalar here, and how
// many flow collections deep the scanner is.
type quoteScanner struct {
	depth       int
	quote, last byte
	opens       bool
}

// inside reports whether text[i] is a quoted scalar's or one of its quotes,
// and how many bytes after it an escape takes with it.
func (s *quoteScanner) inside(text string, i int) (skip int, inside bool) {
	if s.quote != 0 {
		return s.quoted(text, i), true
	}
	c := text[i]
	switch {
	case s.opens && (c == '\'' || c == '"'):
		s.quote = c
		return 0, true
	case c == ' ':
		s.opens = s.opens || s.last == ':' || s.last == '-'
	default:
		s.flow(c)
		s.last = c
	}
	return 0, false
}

// flow steps over c outside quotes: a "[" or "{" where a node starts, or
// inside a flow collection, opens one, and a "]" or "}" closes it; in block
// context a bracket is a plain scalar's character.
func (s *quoteScanner) flow(c byte) {
	switch {
	case (c == '[' || c == '{') && (s.opens || s.depth > 0):
		s.depth++
		s.opens = true
	case (c == ']' || c == '}') && s.depth > 0:
		s.depth--
		s.opens = false
	default:
		s.opens = c == ',' && s.depth > 0
	}
}

// quoted steps over text[i] inside a quoted scalar, closing it at its end
// quote, and returns how many bytes after it an escape takes with it.
func (s *quoteScanner) quoted(text string, i int) int {
	c := text[i]
	switch {
	case s.quote == '"' && c == '\\':
		return 1
	case s.quote == '\'' && c == '\'' && i+1 < len(text) && text[i+1] == '\'':
		return 1
	case c == s.quote:
		s.quote, s.opens = 0, false
	}
	return 0
}

// flowItems is the items of a flow sequence's body, split at each comma
// outside a quoted scalar; the body is one flow collection deep.
func flowItems(body string) []string {
	var items []string
	start := 0
	for i := range unquoted(body, 1) {
		if body[i] == ',' {
			items = append(items, body[start:i])
			start = i + 1
		}
	}
	return append(items, body[start:])
}

type yamlParser struct {
	lines []yamlLine
	at    int
}

func (p *yamlParser) block(indent int) (*yamlNode, error) {
	if strings.HasPrefix(p.lines[p.at].text, "-") && (len(p.lines[p.at].text) == 1 || p.lines[p.at].text[1] == ' ') {
		return p.sequence(indent)
	}
	return p.mapping(indent)
}

func (p *yamlParser) sequence(indent int) (*yamlNode, error) {
	node := &yamlNode{}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent && strings.HasPrefix(p.lines[p.at].text, "-") {
		rest := strings.TrimLeft(p.lines[p.at].text[1:], " ")
		if rest == "" {
			p.at++
			item, err := p.nested(indent)
			if err != nil {
				return nil, err
			}
			node.items = append(node.items, item)
			continue
		}
		inner := indent + len(p.lines[p.at].text) - len(rest)
		if _, _, isKey := cutKey(rest); isKey {
			p.lines[p.at] = yamlLine{text: rest, number: p.lines[p.at].number, indent: inner}
			item, err := p.mapping(inner)
			if err != nil {
				return nil, err
			}
			node.items = append(node.items, item)
			continue
		}
		item, err := inlineValue(rest)
		if err != nil {
			return nil, err
		}
		p.at++
		node.items = append(node.items, item)
	}
	return node, nil
}

func (p *yamlParser) mapping(indent int) (*yamlNode, error) {
	node := &yamlNode{values: map[string]*yamlNode{}}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent {
		line := p.lines[p.at]
		if strings.HasPrefix(line.text, "? ") || strings.HasPrefix(line.text, "-") {
			return nil, &unreadError{construct: "a complex key"}
		}
		key, rest, isKey := cutKey(line.text)
		if !isKey {
			return nil, fmt.Errorf("line %d: no key", line.number)
		}
		p.at++
		var value *yamlNode
		var err error
		switch {
		case rest != "":
			value, err = inlineValue(rest)
		case p.at < len(p.lines) && p.lines[p.at].indent == indent && strings.HasPrefix(p.lines[p.at].text, "-"):
			value, err = p.sequence(indent)
		default:
			value, err = p.nested(indent)
		}
		if err != nil {
			return nil, err
		}
		node.values[key] = value
	}
	return node, nil
}

// nested is the block more indented than indent, or an empty scalar.
func (p *yamlParser) nested(indent int) (*yamlNode, error) {
	if p.at < len(p.lines) && p.lines[p.at].indent > indent {
		return p.block(p.lines[p.at].indent)
	}
	return &yamlNode{isScalar: true}, nil
}

// cutKey splits "key: value" at the first ": " or a trailing ":" outside a
// quoted key.
func cutKey(text string) (key, rest string, ok bool) {
	if strings.HasPrefix(text, "'") || strings.HasPrefix(text, `"`) {
		end := strings.IndexByte(text[1:], text[0])
		if end < 0 {
			return "", "", false
		}
		after := text[end+2:]
		if after != ":" && !strings.HasPrefix(after, ": ") {
			return "", "", false
		}
		return text[1 : end+1], strings.TrimSpace(strings.TrimPrefix(after, ":")), true
	}
	if i := strings.Index(text, ": "); i > 0 {
		return text[:i], strings.TrimSpace(text[i+2:]), true
	}
	if strings.HasSuffix(text, ":") && len(text) > 1 {
		return text[:len(text)-1], "", true
	}
	return "", "", false
}

// inlineValue is a scalar or a flow sequence of scalars written after a key or
// a dash.
func inlineValue(text string) (*yamlNode, error) {
	switch text[0] {
	case '&':
		return nil, &unreadError{construct: "an anchor"}
	case '*':
		return nil, &unreadError{construct: "an alias"}
	case '!':
		return nil, &unreadError{construct: "a tag"}
	case '{':
		return nil, &unreadError{construct: "a flow mapping"}
	case '|', '>':
		return nil, &unreadError{construct: "a block scalar"}
	case '[':
		if !strings.HasSuffix(text, "]") {
			return nil, &unreadError{construct: "a flow sequence over several lines"}
		}
		node := &yamlNode{}
		body := strings.TrimSpace(text[1 : len(text)-1])
		if body == "" {
			return node, nil
		}
		for _, part := range flowItems(body) {
			part = strings.TrimSpace(part)
			if part == "" || strings.ContainsAny(part[:1], "[{&*!") {
				return nil, &unreadError{construct: "a flow sequence of other than scalars"}
			}
			item, err := scalar(part)
			if err != nil {
				return nil, err
			}
			node.items = append(node.items, item)
		}
		return node, nil
	}
	return scalar(text)
}

func scalar(text string) (*yamlNode, error) {
	switch text[0] {
	case '"':
		s, ok := doubleQuoted(text)
		if !ok {
			return nil, &unreadError{construct: "a double-quoted scalar outside the subset"}
		}
		return &yamlNode{isScalar: true, scalarText: s}, nil
	case '\'':
		if len(text) < 2 || text[len(text)-1] != '\'' {
			return nil, &unreadError{construct: "an unterminated quoted scalar"}
		}
		return &yamlNode{isScalar: true, scalarText: strings.ReplaceAll(text[1:len(text)-1], "''", "'")}, nil
	}
	return &yamlNode{isScalar: true, scalarText: text}, nil
}

// yamlEscapes and yamlHexEscapes are the escapes of YAML 1.2.2's double-quoted
// scalars, https://yaml.org/spec/1.2.2/#57-escaped-characters: one character,
// or a code point in a fixed number of hexadecimal digits.
var (
	yamlEscapes = map[byte]rune{
		'0': 0, 'a': '\a', 'b': '\b', 't': '\t', '\t': '\t', 'n': '\n', 'v': '\v', 'f': '\f', 'r': '\r', 'e': 0x1b,
		' ': ' ', '"': '"', '/': '/', '\\': '\\', 'N': 0x85, '_': 0xa0, 'L': 0x2028, 'P': 0x2029,
	}
	yamlHexEscapes = map[byte]int{'x': 2, 'u': 4, 'U': 8}
)

// doubleQuoted decodes a one-line double-quoted scalar, reporting false for
// an unterminated one, an unescaped quote inside it, and an unknown or
// malformed escape.
func doubleQuoted(text string) (string, bool) {
	if len(text) < 2 || text[len(text)-1] != '"' {
		return "", false
	}
	body := text[1 : len(text)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '"':
			return "", false
		case c != '\\':
			out.WriteByte(c)
			continue
		}
		i++
		if i == len(body) {
			return "", false
		}
		if r, known := yamlEscapes[body[i]]; known {
			out.WriteRune(r)
			continue
		}
		width, known := yamlHexEscapes[body[i]]
		if !known || i+width >= len(body) {
			return "", false
		}
		n, err := strconv.ParseUint(body[i+1:i+1+width], 16, 32)
		if err != nil || n > utf8.MaxRune || !utf8.ValidRune(rune(n)) {
			return "", false
		}
		out.WriteRune(rune(n))
		i += width
	}
	return out.String(), true
}

// child is the value under key, nil where there is none.
func (n *yamlNode) child(key string) *yamlNode {
	if n == nil || n.values == nil {
		return nil
	}
	return n.values[key]
}

// scalar is the scalar under key, empty where there is none.
func (n *yamlNode) scalar(key string) string {
	if c := n.child(key); c != nil && c.isScalar {
		return c.scalarText
	}
	return ""
}

// list is the sequence under key.
func (n *yamlNode) list(key string) []*yamlNode {
	if c := n.child(key); c != nil {
		return c.items
	}
	return nil
}

// strings is the scalars of the sequence under key, or the scalar there as a
// list of one.
func (n *yamlNode) strings(key string) []string {
	c := n.child(key)
	if c != nil && c.isScalar {
		return []string{c.scalarText}
	}
	var out []string
	for _, item := range n.list(key) {
		if item.isScalar {
			out = append(out, item.scalarText)
		}
	}
	return out
}
