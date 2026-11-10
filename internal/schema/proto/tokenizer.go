package proto

import (
	"bytes"
	"fmt"
	"strings"
)

// The tokenizer is google/protobuf/io/tokenizer.cc (v36.2) step for step,
// because a position, a comment's attachment and the order of errors must be
// the ones protoc reports.

type tokenType int

const (
	tokenStart tokenType = iota
	tokenEnd
	tokenIdentifier
	tokenInteger
	tokenFloat
	tokenString
	tokenSymbol
	tokenWhitespace
	tokenNewline
	tokenURLChars
)

// token positions are 0-based; a column counts a tab to the next multiple of
// tabWidth.
type token struct {
	text      string
	kind      tokenType
	line      int32
	column    int32
	endColumn int32
}

const tabWidth = 8

type tokenizer struct {
	record           *strings.Builder
	text             strings.Builder
	lineComments     []LineComment
	buf              []byte
	errs             Errors
	current          token
	previous         token
	pos              int
	recordStart      int
	line             int32
	column           int32
	cur              byte
	atEnd            bool
	reportWhitespace bool
	reportNewlines   bool
	shComments       bool
	allowFAfterFloat bool
	reportURLChars   bool
}

func newTokenizer(src []byte) *tokenizer {
	t := &tokenizer{buf: src, current: token{kind: tokenStart}}
	if len(src) > 0 {
		t.cur = src[0]
	} else {
		t.atEnd = true
	}
	return t
}

func (t *tokenizer) errorAt(line, column int32, msg string) {
	t.errs = append(t.errs, fileError{Line: int(line) + 1, Column: int(column) + 1, Message: msg})
}

func (t *tokenizer) addError(msg string) { t.errorAt(t.line, t.column, msg) }

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}
func isWhitespaceNoNewline(c byte) bool { return c != '\n' && isWhitespace(c) }
func isUnprintable(c byte) bool         { return c >= 1 && c < ' ' }
func isDigit(c byte) bool               { return c >= '0' && c <= '9' }
func isOctal(c byte) bool               { return c >= '0' && c <= '7' }
func isHex(c byte) bool                 { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }

func isLetter(c byte) bool       { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' }
func isAlphanumeric(c byte) bool { return isLetter(c) || isDigit(c) }
func isURLChar(c byte) bool      { return isAlphanumeric(c) || strings.IndexByte("-.~!$&()*+,;=%/", c) >= 0 }
func isEscape(c byte) bool       { return c != 0 && strings.IndexByte(`abfnrtv\?'"`, c) >= 0 }

func (t *tokenizer) nextChar() {
	if t.atEnd {
		return
	}
	switch t.cur {
	case '\n':
		t.line++
		t.column = 0
	case '\t':
		t.column += tabWidth - t.column%tabWidth
	default:
		t.column++
	}
	t.pos++
	if t.pos < len(t.buf) {
		t.cur = t.buf[t.pos]
	} else {
		t.cur = 0
		t.atEnd = true
	}
}

func (t *tokenizer) recordTo(sb *strings.Builder) {
	t.record = sb
	t.recordStart = t.pos
}

func (t *tokenizer) stopRecording() {
	if t.record != nil && t.pos > t.recordStart {
		t.record.Write(t.buf[t.recordStart:t.pos])
	}
	t.record = nil
}

func (t *tokenizer) tryConsume(c byte) bool {
	if t.cur == c {
		t.nextChar()
		return true
	}
	return false
}

// tryConsumeSeq consumes the bytes of s while they match, reporting whether
// all did.
func (t *tokenizer) tryConsumeSeq(s string) bool {
	for i := range len(s) {
		if !t.tryConsume(s[i]) {
			return false
		}
	}
	return true
}

func (t *tokenizer) tryConsumeOne(class func(byte) bool) bool {
	if class(t.cur) {
		t.nextChar()
		return true
	}
	return false
}

func (t *tokenizer) consumeZeroOrMore(class func(byte) bool) {
	for class(t.cur) {
		t.nextChar()
	}
}

func (t *tokenizer) consumeOneOrMore(class func(byte) bool, msg string) {
	if !class(t.cur) {
		t.addError(msg)
		return
	}
	t.consumeZeroOrMore(class)
}

func (t *tokenizer) startToken() {
	t.current = token{kind: tokenStart, line: t.line, column: t.column}
	t.text.Reset()
	t.recordTo(&t.text)
}

func (t *tokenizer) endToken() {
	t.stopRecording()
	t.current.text = t.text.String()
	t.current.endColumn = t.column
}

func (t *tokenizer) consumeString(delim byte) {
	for {
		switch t.cur {
		case 0:
			t.addError("Unexpected end of string.")
			return
		case '\n':
			t.addError(`Multiline strings are not allowed. Did you miss a "?.`)
			return
		case '\\':
			t.nextChar()
			t.consumeEscape()
		default:
			if t.cur == delim {
				t.nextChar()
				return
			}
			t.nextChar()
		}
	}
}

func (t *tokenizer) consumeEscape() {
	switch {
	case t.tryConsumeOne(isEscape), t.tryConsumeOne(isOctal):
	case t.tryConsume('x') || t.tryConsume('X'):
		if !t.tryConsumeOne(isHex) {
			t.addError("Expected hex digits for escape sequence.")
		}
	case t.tryConsume('u'):
		if !t.hexDigits(4) {
			t.addError(`Expected four hex digits for \u escape sequence.`)
		}
	case t.tryConsume('U'):
		if !t.tryConsumeSeq("00") || (!t.tryConsume('0') && !t.tryConsume('1')) || !t.hexDigits(5) {
			t.addError(`Expected eight hex digits up to 10ffff for \U escape sequence`)
		}
	default:
		t.addError("Invalid escape sequence in string literal.")
	}
}

// hexDigits consumes n hex digits, stopping at the first byte that is none.
func (t *tokenizer) hexDigits(n int) bool {
	for range n {
		if !t.tryConsumeOne(isHex) {
			return false
		}
	}
	return true
}

func (t *tokenizer) consumeNumber(startedWithZero, startedWithDot bool) tokenType {
	isFloat := false
	switch {
	case startedWithZero && (t.tryConsume('x') || t.tryConsume('X')):
		t.consumeOneOrMore(isHex, `"0x" must be followed by hex digits.`)
	case startedWithZero && isDigit(t.cur):
		t.consumeZeroOrMore(isOctal)
		if isDigit(t.cur) {
			t.addError("Numbers starting with leading zero must be in octal.")
			t.consumeZeroOrMore(isDigit)
		}
	default:
		isFloat = t.consumeDecimal(startedWithDot)
	}
	switch {
	case isLetter(t.cur):
		t.addError("Need space between number and identifier.")
	case t.cur == '.' && isFloat:
		t.addError("Already saw decimal point or exponent; can't have another one.")
	case t.cur == '.':
		t.addError("Hex and octal numbers must be integers.")
	}
	if isFloat {
		return tokenFloat
	}
	return tokenInteger
}

// consumeDecimal reads the rest of a decimal number and reports whether it
// is a float.
func (t *tokenizer) consumeDecimal(startedWithDot bool) bool {
	isFloat := startedWithDot
	t.consumeZeroOrMore(isDigit)
	if !startedWithDot && t.tryConsume('.') {
		isFloat = true
		t.consumeZeroOrMore(isDigit)
	}
	if t.tryConsume('e') || t.tryConsume('E') {
		isFloat = true
		_ = t.tryConsume('-') || t.tryConsume('+')
		t.consumeOneOrMore(isDigit, `"e" must be followed by exponent.`)
	}
	if t.allowFAfterFloat && (t.tryConsume('f') || t.tryConsume('F')) {
		isFloat = true
	}
	return isFloat
}

func (t *tokenizer) consumeSymbol() {
	if t.cur&0x80 != 0 {
		t.addError(fmt.Sprintf("Interpreting non ascii codepoint %d.", t.cur))
	}
	t.nextChar()
}

func (t *tokenizer) consumeLineComment(content *strings.Builder) {
	if !t.shComments {
		start := t.pos - len("//")
		end := len(t.buf)
		if i := bytes.IndexByte(t.buf[start:], '\n'); i >= 0 {
			end = start + i
		}
		t.lineComments = append(t.lineComments, LineComment{
			Text: string(t.buf[start:end]), Line: t.line, Column: t.column - int32(len("//")),
			Alone: t.current.kind == tokenStart || t.current.line != t.line,
		})
	}
	if content != nil {
		t.recordTo(content)
	}
	for t.cur != 0 && t.cur != '\n' {
		t.nextChar()
	}
	t.tryConsume('\n')
	if content != nil {
		t.stopRecording()
	}
}

func (t *tokenizer) consumeBlockComment(content *strings.Builder) {
	startLine, startColumn := t.line, t.column-2
	if content != nil {
		t.recordTo(content)
	}
	for ended := false; !ended; {
		ended = t.blockCommentStep(content, startLine, startColumn)
	}
}

// blockCommentStep reads a block comment up to its next line break, star or
// slash and reports whether the comment ended there. A line's leading
// whitespace and star are left out of content, as protoc leaves them out.
func (t *tokenizer) blockCommentStep(content *strings.Builder, startLine, startColumn int32) bool {
	for t.cur != 0 && t.cur != '*' && t.cur != '/' && t.cur != '\n' {
		t.nextChar()
	}
	switch {
	case t.tryConsume('\n'):
		if content != nil {
			t.stopRecording()
		}
		t.consumeZeroOrMore(isWhitespaceNoNewline)
		if t.tryConsumeSeq("*/") {
			return true
		}
		if content != nil {
			t.recordTo(content)
		}
	case t.tryConsumeSeq("*/"):
		if content != nil {
			t.stopRecording()
			held := content.String()
			content.Reset()
			content.WriteString(held[:len(held)-2])
		}
		return true
	case t.tryConsume('/') && t.cur == '*':
		t.addError(`"/*" inside block comment.  Block comments cannot be nested.`)
	case t.cur == 0:
		t.addError("End-of-file inside block comment.")
		t.errorAt(startLine, startColumn, "  Comment started here.")
		if content != nil {
			t.stopRecording()
		}
		return true
	}
	return false
}

type commentStart int

const (
	lineComment commentStart = iota
	blockComment
	slashNotComment
	noComment
)

func (t *tokenizer) tryConsumeCommentStart() commentStart {
	if t.shComments {
		if t.tryConsume('#') {
			return lineComment
		}
		return noComment
	}
	if !t.tryConsume('/') {
		return noComment
	}
	switch {
	case t.tryConsume('/'):
		return lineComment
	case t.tryConsume('*'):
		return blockComment
	}
	t.current = token{kind: tokenSymbol, text: "/", line: t.line, column: t.column - 1, endColumn: t.column}
	return slashNotComment
}

func (t *tokenizer) tryConsumeWhitespace() bool {
	if t.reportNewlines {
		if t.tryConsumeOne(isWhitespaceNoNewline) {
			t.consumeZeroOrMore(isWhitespaceNoNewline)
			t.current.kind = tokenWhitespace
			return true
		}
		return false
	}
	if t.tryConsumeOne(isWhitespace) {
		t.consumeZeroOrMore(isWhitespace)
		t.current.kind = tokenWhitespace
		return t.reportWhitespace
	}
	return false
}

func (t *tokenizer) tryConsumeNewline() bool {
	if !t.reportWhitespace || !t.reportNewlines {
		return false
	}
	if t.tryConsume('\n') {
		t.current.kind = tokenNewline
		return true
	}
	return false
}

// next is Tokenizer::Next: it reads the next token into current, false at
// the end of the input.
func (t *tokenizer) next() bool {
	t.previous = t.current
	for !t.atEnd {
		t.startToken()
		report := t.tryConsumeWhitespace() || t.tryConsumeNewline()
		t.endToken()
		if report {
			return true
		}
		switch t.tryConsumeCommentStart() {
		case lineComment:
			t.consumeLineComment(nil)
			continue
		case blockComment:
			t.consumeBlockComment(nil)
			continue
		case slashNotComment:
			return true
		case noComment:
		}
		if t.atEnd {
			break
		}
		if isUnprintable(t.cur) || t.cur == 0 {
			t.skipControlCharacters()
			continue
		}
		t.startToken()
		t.current.kind = t.consumeToken()
		t.endToken()
		return true
	}
	t.current = token{kind: tokenEnd, line: t.line, column: t.column, endColumn: t.column}
	return false
}

func (t *tokenizer) skipControlCharacters() {
	t.addError("Invalid control characters encountered in text.")
	t.nextChar()
	for t.tryConsumeOne(isUnprintable) || (!t.atEnd && t.tryConsume(0)) { //nolint:revive // the condition consumes
	}
}

func (t *tokenizer) consumeToken() tokenType {
	if t.reportURLChars {
		if t.tryConsumeOne(isURLChar) {
			t.consumeZeroOrMore(isURLChar)
			return tokenURLChars
		}
		t.consumeSymbol()
		return tokenSymbol
	}
	switch {
	case t.tryConsumeOne(isLetter):
		t.consumeZeroOrMore(isAlphanumeric)
		return tokenIdentifier
	case t.tryConsume('0'):
		return t.consumeNumber(true, false)
	case t.tryConsume('.'):
		if !t.tryConsumeOne(isDigit) {
			return tokenSymbol
		}
		if t.previous.kind == tokenIdentifier && t.current.line == t.previous.line && t.current.column == t.previous.endColumn {
			t.errorAt(t.line, t.column-2, "Need space between identifier and decimal point.")
		}
		return t.consumeNumber(false, true)
	case t.tryConsumeOne(isDigit):
		return t.consumeNumber(false, false)
	case t.tryConsume('"'):
		t.consumeString('"')
		return tokenString
	case t.tryConsume('\''):
		t.consumeString('\'')
		return tokenString
	}
	t.consumeSymbol()
	return tokenSymbol
}

// commentCollector is tokenizer.cc's CommentCollector.
type commentCollector struct {
	prevTrailing    *string
	detached        *[]string
	nextLeading     *string
	buffer          strings.Builder
	numComments     int
	hasTrailing     bool
	hasComment      bool
	isLine          bool
	canAttachToPrev bool
}

func (c *commentCollector) bufferForLine() *strings.Builder {
	if c.hasComment && !c.isLine {
		c.flush()
	}
	c.hasComment, c.isLine = true, true
	return &c.buffer
}

func (c *commentCollector) bufferForBlock() *strings.Builder {
	if c.hasComment {
		c.flush()
	}
	c.hasComment, c.isLine = true, false
	return &c.buffer
}

func (c *commentCollector) flush() {
	if !c.hasComment {
		return
	}
	if c.canAttachToPrev {
		if c.prevTrailing != nil {
			*c.prevTrailing += c.buffer.String()
		}
		c.hasTrailing = true
		c.canAttachToPrev = false
	} else if c.detached != nil {
		*c.detached = append(*c.detached, c.buffer.String())
	}
	c.buffer.Reset()
	c.hasComment = false
	c.numComments++
}

func (c *commentCollector) maybeDetach() {
	count := c.numComments
	if c.hasComment {
		count++
	}
	if count != 1 {
		return
	}
	if c.hasTrailing && c.prevTrailing != nil {
		if c.detached != nil {
			*c.detached = append([]string{*c.prevTrailing}, *c.detached...)
		}
		*c.prevTrailing = ""
	}
	c.flush()
}

func (c *commentCollector) finish() {
	if c.nextLeading != nil && c.hasComment {
		*c.nextLeading = c.buffer.String()
	}
}

// nextWithComments is Tokenizer::NextWithComments: it reads the next token
// and sorts the comments before it into the previous token's trailing
// comment, detached comments, and the next token's leading comment.
func (t *tokenizer) nextWithComments(prevTrailing *string, detached *[]string, nextLeading *string) {
	c := &commentCollector{prevTrailing: prevTrailing, detached: detached, nextLeading: nextLeading, canAttachToPrev: true}
	if prevTrailing != nil {
		*prevTrailing = ""
	}
	if detached != nil {
		*detached = nil
	}
	if nextLeading != nil {
		*nextLeading = ""
	}
	defer c.finish()

	prevLine := t.line
	trailingEndLine := int32(-1)
	if t.current.kind == tokenStart {
		if t.tryConsume(0xEF) && !t.tryConsumeSeq("\xBB\xBF") {
			t.addError("Proto file starts with 0xEF but not UTF-8 BOM. Only UTF-8 is accepted for proto file.")
			return
		}
		c.canAttachToPrev = false
		prevLine = -1
	} else if t.readTrailingComment(c, &trailingEndLine) {
		return
	}
	t.readLeadingComments(c, prevLine, trailingEndLine)
}

// readTrailingComment reads a comment that opens on the previous token's
// line, and reports whether it read the next token instead.
func (t *tokenizer) readTrailingComment(c *commentCollector, trailingEndLine *int32) bool {
	t.consumeZeroOrMore(isWhitespaceNoNewline)
	switch t.tryConsumeCommentStart() {
	case lineComment:
		*trailingEndLine = t.line
		t.consumeLineComment(c.bufferForLine())
		c.flush()
	case blockComment:
		t.consumeBlockComment(c.bufferForBlock())
		*trailingEndLine = t.line
		t.consumeZeroOrMore(isWhitespaceNoNewline)
		c.flush()
	case slashNotComment:
		return true
	case noComment:
		if !t.tryConsume('\n') {
			t.next()
			return true
		}
	}
	return false
}

// readLeadingComments reads the comments before the next token, then the
// token.
func (t *tokenizer) readLeadingComments(c *commentCollector, prevLine, trailingEndLine int32) {
	for {
		t.consumeZeroOrMore(isWhitespaceNoNewline)
		switch t.tryConsumeCommentStart() {
		case lineComment:
			t.consumeLineComment(c.bufferForLine())
		case blockComment:
			t.consumeBlockComment(c.bufferForBlock())
			t.consumeZeroOrMore(isWhitespaceNoNewline)
			t.tryConsume('\n')
		case slashNotComment:
			return
		case noComment:
			if t.tryConsume('\n') {
				c.flush()
				c.canAttachToPrev = false
				continue
			}
			result := t.next()
			if !result || t.current.text == "}" || t.current.text == "]" || t.current.text == ")" {
				c.flush()
			}
			if result && (prevLine == t.line || trailingEndLine == t.line) {
				c.maybeDetach()
			}
			return
		}
	}
}
