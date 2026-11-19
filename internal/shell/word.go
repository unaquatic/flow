package shell

import (
	"strings"
	"unicode/utf8"
)

// WordMode is where a word stands, which changes how it is read.
type WordMode int

const (
	// WordArg is a word anywhere WordCommand and WordRegex do not tell.
	WordArg WordMode = iota
	// WordCommand is the first word of a simple command, which may be an
	// assignment to an array element, whose [subscript] may hold blanks.
	WordCommand
	// WordRegex is the right side of =~ in [[ ]], where parentheses and |
	// are part of the word, and blanks too inside parentheses.
	WordRegex
	// WordElem is an element of an array, which cannot be an array itself.
	WordElem
)

// ScanWord reads the word at Pos as mode tells, reading the commands
// substituted in it, and reports false when no word starts there.
func (p *Parser) ScanWord(mode WordMode) (Word, bool) {
	defer p.Quiet()()
	regex := mode == WordRegex
	start := p.Pos
	var b strings.Builder
	literal, quoted := true, false
	glob, bracket, brace := false, false, false
	braceDepth, braceSep := 0, false
	parens := 0
loop:
	for !p.EOF() {
		c := p.Src[p.Pos]
		switch {
		case c == '\\':
			if p.Peek(1) == '\n' {
				p.Pos += 2
				continue
			}
			quoted = true
			if p.Pos+1 < len(p.Src) {
				b.WriteByte(p.Src[p.Pos+1])
				p.Pos += 2
			} else {
				b.WriteByte('\\')
				p.Pos++
			}
		case c == '\'':
			quoted = true
			b.WriteString(p.ScanSingle())
		case c == '"':
			quoted = true
			literal = p.ScanDouble(&b) && literal
		case c == '$':
			if n := p.Peek(1); n == '\'' || n == '"' {
				quoted = true
			}
			literal = p.ScanDollar(&b, false) && literal
		case c == '`':
			p.ScanBackquote(&b, false)
			literal = false
		case (c == '<' || c == '>') && p.Peek(1) == '(':
			open := p.Pos
			p.Pos += 2
			p.ParseList()
			p.Close(")", open, "process substitution")
			b.WriteString(p.Src[open:p.Pos])
			literal = false
		case strings.IndexByte("?*+@!", c) >= 0 && p.Peek(1) == '(' && p.Peek(2) != ')':
			p.ScanExtglob(&b)
			literal = false
		case c == '(' && (mode == WordArg || mode == WordCommand) && strings.HasSuffix(p.Src[start:p.Pos], "=") && isAssignment(p.Src[start:p.Pos]):
			open := p.Pos
			p.ScanArray()
			b.WriteString(p.Src[open:p.Pos])
			literal = false
			break loop
		case c == '[' && mode == WordCommand && validName(p.Src[start:p.Pos]):
			open := p.Pos
			p.Pos++
			if !p.ScanArith("]") {
				p.Fail(open, "unclosed subscript")
			}
			b.WriteString(p.Src[open:p.Pos])
			glob = true
		case regex && c == '(':
			parens++
			b.WriteByte(c)
			p.Pos++
		case regex && c == ')' && parens > 0:
			parens--
			b.WriteByte(c)
			p.Pos++
		case regex && (c == '|' || parens > 0 && (c == ' ' || c == '\t')):
			b.WriteByte(c)
			p.Pos++
		case isMeta(c):
			break loop
		default:
			switch c {
			case '*', '?':
				glob = true
			case '[':
				bracket = true
			case ']':
				glob = glob || bracket
			case '{':
				braceDepth++
			case ',':
				braceSep = braceSep || braceDepth > 0
			case '.':
				braceSep = braceSep || braceDepth > 0 && p.Peek(1) == '.'
			case '}':
				if braceDepth > 0 {
					braceDepth--
					brace = brace || braceSep
				}
			}
			b.WriteByte(c)
			p.Pos++
		}
	}
	if p.Pos == start {
		return Word{}, false
	}
	tilde := p.Src[start] == '~'
	return Word{
		Raw:     p.Src[start:p.Pos],
		Value:   b.String(),
		Literal: literal && !glob && !brace && !tilde,
		Quoted:  quoted,
		Offset:  p.Offset(start),
	}, true
}

// ScanSingle reads the single-quoted string at Pos and returns its content.
func (p *Parser) ScanSingle() string {
	open := p.Pos
	i := strings.IndexByte(p.Src[open+1:], '\'')
	if i < 0 {
		p.Fail(open, "unclosed single quote")
	}
	p.Pos = open + 1 + i + 1
	return p.Src[open+1 : open+1+i]
}

// ScanDouble reads the double-quoted string at Pos into b, with its
// expansions as written, and reports whether it holds none.
func (p *Parser) ScanDouble(b *strings.Builder) (literal bool) {
	open := p.Pos
	p.Pos++
	literal = true
	for {
		if p.EOF() {
			p.Fail(open, "unclosed double quote")
		}
		switch c := p.Src[p.Pos]; c {
		case '"':
			p.Pos++
			return literal
		case '\\':
			switch n := p.Peek(1); n {
			case '\n':
				p.Pos += 2
			case '$', '`', '"', '\\':
				b.WriteByte(n)
				p.Pos += 2
			default:
				b.WriteByte('\\')
				p.Pos++
			}
		case '$':
			literal = p.ScanDollar(b, true) && literal
		case '`':
			p.ScanBackquote(b, true)
			literal = false
		default:
			b.WriteByte(c)
			p.Pos++
		}
	}
}

// ScanDollar reads what the $ at Pos starts into b: a $'...' or $"..." string's
// content, or else an expansion as written, or a lone $. It reports whether
// that stands for itself: a string with no expansions, or a lone $. In a
// double-quoted string, as inDouble tells, $' and $" do not start strings.
func (p *Parser) ScanDollar(b *strings.Builder, inDouble bool) (literal bool) {
	start := p.Pos
	switch n := p.Peek(1); {
	case n == '\'' && !inDouble:
		p.Pos++
		b.WriteString(p.ScanANSI())
		return true
	case n == '"' && !inDouble:
		p.Pos++
		return p.ScanDouble(b)
	case n == '(' && p.Peek(2) == '(':
		c := p.Checkpoint()
		p.Pos += 3
		if !p.ScanArith("))") {
			// Not arithmetic, as in $((cmd) ), but a command substitution.
			p.Restore(c)
			p.ScanCommandSubst()
		}
	case n == '(':
		p.ScanCommandSubst()
	case n == '[':
		p.Pos += 2
		if !p.ScanArith("]") {
			p.Fail(start, "unclosed %q", "$[")
		}
	case n == '{':
		if c := p.Peek(2); c == ' ' || c == '\t' || c == '\n' || c == '|' {
			// ${ list; } and ${| list; } run list in the current shell.
			p.Pos += 2
			if c == '|' {
				p.Pos++
			}
			p.ParseList("}")
			p.Expect("}", start, "${")
		} else {
			p.ScanParam(inDouble)
		}
	case n == '_' || isAlpha(n):
		for p.Pos++; !p.EOF() && (p.Src[p.Pos] == '_' || isAlpha(p.Src[p.Pos]) || isDigit(p.Src[p.Pos])); p.Pos++ {
		}
	case isDigit(n) || n != 0 && strings.IndexByte("@*#?$!-", n) >= 0:
		p.Pos += 2
	default:
		b.WriteByte('$')
		p.Pos++
		return true
	}
	b.WriteString(p.Src[start:p.Pos])
	return false
}

// ScanCommandSubst reads the $( ) command substitution at Pos.
func (p *Parser) ScanCommandSubst() {
	open := p.Pos
	p.Pos += 2
	p.ParseList()
	p.Close(")", open, "command substitution")
}

// ScanParam reads the ${ } parameter expansion at Pos, up to its first
// unquoted }. In a double-quoted string, as inDouble tells, single quotes
// in it are literal.
func (p *Parser) ScanParam(inDouble bool) {
	open := p.Pos
	p.Pos += 2
	var discard strings.Builder
	for {
		if p.EOF() {
			p.Fail(open, "unclosed parameter expansion")
		}
		switch p.Src[p.Pos] {
		case '}':
			p.Pos++
			return
		case '\\':
			p.Pos = min(p.Pos+2, len(p.Src))
		case '\'':
			if inDouble {
				p.Pos++
			} else {
				p.ScanSingle()
			}
		case '"':
			p.ScanDouble(&discard)
		case '$':
			p.ScanDollar(&discard, inDouble)
		case '`':
			p.ScanBackquote(&discard, inDouble)
		default:
			p.Pos++
		}
	}
}

// ScanArith reads from Pos through the close that ends it: "))" for
// arithmetic, with its parentheses balanced, or "]" for $[ ] arithmetic or
// a subscript, with its brackets balanced. It reports false, leaving Pos
// anywhere, when a ")" that does not close "))", or the end of Src, comes
// first.
func (p *Parser) ScanArith(close string) bool {
	defer p.Quiet()()
	parens, brackets := 0, 0
	var discard strings.Builder
	for !p.EOF() {
		switch p.Src[p.Pos] {
		case '(':
			parens++
			p.Pos++
		case ')':
			p.Pos++
			if close == "]" {
				continue
			}
			if parens > 0 {
				parens--
				continue
			}
			if p.HasPrefix(")") {
				p.Pos++
				return true
			}
			return false
		case '[':
			brackets++
			p.Pos++
		case ']':
			p.Pos++
			if close == "]" && brackets == 0 {
				return true
			}
			brackets = max(brackets-1, 0)
		case '\\':
			p.Pos = min(p.Pos+2, len(p.Src))
		case '\'':
			p.ScanSingle()
		case '"':
			p.ScanDouble(&discard)
		case '$':
			p.ScanDollar(&discard, false)
		case '`':
			p.ScanBackquote(&discard, false)
		default:
			p.Pos++
		}
	}
	return false
}

// ScanBackquote reads the backquoted command substitution at Pos, writing it
// as written into b, and reads its commands from its content with the
// backslashes that quote $, ` and \, and " in a double-quoted string, as
// inDouble tells, taken out.
func (p *Parser) ScanBackquote(b *strings.Builder, inDouble bool) {
	open := p.Pos
	p.Pos++
	var inner []byte
	var origin []int
	for {
		if p.EOF() {
			p.Fail(open, "unclosed backquote")
		}
		c := p.Src[p.Pos]
		if c == '`' {
			break
		}
		if n := p.Peek(1); c == '\\' && (n == '$' || n == '`' || n == '\\' || inDouble && n == '"') {
			p.Pos++
			c = n
		}
		inner = append(inner, c)
		origin = append(origin, p.Pos)
		p.Pos++
	}
	origin = append(origin, p.Pos)
	p.Pos++
	p.Nested(string(inner), origin).ParseScript()
	b.WriteString(p.Src[open:p.Pos])
}

// ScanExtglob reads the extended glob, such as @(a|b), at Pos into b.
func (p *Parser) ScanExtglob(b *strings.Builder) {
	open := p.Pos
	p.Pos += 2
	var discard strings.Builder
	for depth := 1; depth > 0; {
		if p.EOF() {
			p.Fail(open, "unclosed extended glob")
		}
		switch p.Src[p.Pos] {
		case '(':
			depth++
			p.Pos++
		case ')':
			depth--
			p.Pos++
		case '\\':
			p.Pos = min(p.Pos+2, len(p.Src))
		case '\'':
			p.ScanSingle()
		case '"':
			p.ScanDouble(&discard)
		case '$':
			p.ScanDollar(&discard, false)
		case '`':
			p.ScanBackquote(&discard, false)
		default:
			p.Pos++
		}
	}
	b.WriteString(p.Src[open:p.Pos])
}

// ScanArray reads the ( ) list of an array assignment at Pos.
func (p *Parser) ScanArray() {
	open := p.Pos
	p.Pos++
	for {
		p.SkipLinebreaks()
		if p.EOF() {
			p.Fail(open, "unclosed array")
		}
		if p.Src[p.Pos] == ')' {
			p.Pos++
			return
		}
		if _, ok := p.ScanWord(WordElem); !ok {
			p.Fail(p.Pos, "unexpected %s in array", p.Describe())
		}
	}
}

// ScanHeredocBody reads all of Src as the body of an unquoted
// here-document, reading the commands substituted in it.
func (p *Parser) ScanHeredocBody() {
	var discard strings.Builder
	for !p.EOF() {
		switch p.Src[p.Pos] {
		case '\\':
			p.Pos = min(p.Pos+2, len(p.Src))
		case '$':
			p.ScanDollar(&discard, true)
		case '`':
			p.ScanBackquote(&discard, false)
		default:
			p.Pos++
		}
	}
}

// ScanANSI reads the $'...' string whose quote is at Pos and returns its
// content with its backslash escapes decoded.
func (p *Parser) ScanANSI() string {
	open := p.Pos
	p.Pos++
	var b []byte
	for {
		if p.EOF() {
			p.Fail(open-1, "unclosed %q", "$'")
		}
		c := p.Src[p.Pos]
		p.Pos++
		if c == '\'' {
			return string(b)
		}
		if c != '\\' || p.EOF() {
			b = append(b, c)
			continue
		}
		e := p.Src[p.Pos]
		p.Pos++
		switch e {
		case 'a':
			b = append(b, '\a')
		case 'b':
			b = append(b, '\b')
		case 'e', 'E':
			b = append(b, 0x1b)
		case 'f':
			b = append(b, '\f')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'v':
			b = append(b, '\v')
		case '\\', '\'', '"', '?':
			b = append(b, e)
		case 'c':
			if !p.EOF() {
				b = append(b, p.Src[p.Pos]&0x1f)
				p.Pos++
			}
		case 'x', 'u', 'U':
			digits := 2
			switch e {
			case 'u':
				digits = 4
			case 'U':
				digits = 8
			}
			v, n := p.scanDigits(16, digits)
			switch {
			case n == 0:
				b = append(b, '\\', e)
			case e == 'x':
				b = append(b, byte(v))
			default:
				b = utf8.AppendRune(b, rune(v))
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			p.Pos--
			v, _ := p.scanDigits(8, 3)
			b = append(b, byte(v))
		default:
			b = append(b, '\\', e)
		}
	}
}

// scanDigits reads up to most digits in base 8 or 16 at Pos and returns
// their value and how many there were.
func (p *Parser) scanDigits(base, most int) (v, n int) {
	for ; n < most && !p.EOF(); n++ {
		d := digitValue(p.Src[p.Pos])
		if d < 0 || d >= base {
			break
		}
		v = v*base + d
		p.Pos++
	}
	return v, n
}

// digitValue returns the value of the hex digit c, or -1.
func digitValue(c byte) int {
	switch {
	case isDigit(c):
		return int(c - '0')
	case c|0x20 >= 'a' && c|0x20 <= 'f':
		return int(c|0x20-'a') + 10
	}
	return -1
}
