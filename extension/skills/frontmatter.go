package skills

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FrontmatterFence opens and closes the frontmatter of a SKILL.md, each on a
// line of its own.
const FrontmatterFence = "---"

// ErrUnterminated reports frontmatter that opens and never closes.
var ErrUnterminated = errors.New("frontmatter has no closing ---")

// Frontmatter is the YAML block that opens a SKILL.md, as far as flow reads
// it: the scalars of its top-level keys, by key. A key whose value is a
// mapping or a list, such as metadata, holds "". It reads the forms of
// scalar that skills write: plain, over one line or more, single- and
// double-quoted, and the literal (|) and folded (>) blocks, with their
// chomping indicators. It reads no anchors, tags, or flow collections, whose
// text it keeps as a plain scalar.
type Frontmatter map[string]string

// Bool reports whether key holds YAML's true, in any of its cases.
func (f Frontmatter) Bool(key string) bool {
	switch f[key] {
	case "true", "True", "TRUE":
		return true
	}
	return false
}

// SplitFrontmatter returns the frontmatter that opens text, between a first
// line of FrontmatterFence and the next, and the body after it. Text that
// does not open with a fence has no frontmatter: it returns nil and the
// whole text. A fence that opens and does not close is ErrUnterminated, and
// a line it cannot read an error that names it.
func SplitFrontmatter(text string) (Frontmatter, string, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	first, rest, _ := strings.Cut(text, "\n")
	if strings.TrimRight(first, " \t") != FrontmatterFence {
		return nil, text, nil
	}
	var lines []string
	for {
		if rest == "" {
			return nil, "", ErrUnterminated
		}
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		if t := strings.TrimRight(line, " \t"); t == FrontmatterFence || t == "..." {
			break
		}
		lines = append(lines, line)
	}
	fm, err := ParseFrontmatter(lines)
	if err != nil {
		return nil, "", err
	}
	return fm, rest, nil
}

// ParseFrontmatter reads lines, the frontmatter between its fences, as
// Frontmatter describes.
func ParseFrontmatter(lines []string) (Frontmatter, error) {
	fm := Frontmatter{}
	for i := 0; i < len(lines); {
		line := lines[i]
		if isBlankOrComment(line) {
			i++
			continue
		}
		if indent(line) > 0 {
			return nil, fmt.Errorf("frontmatter line %d: indented line outside a key: %q", i+1, line)
		}
		key, value, ok := cutKey(line)
		if !ok {
			return nil, fmt.Errorf("frontmatter line %d: want key: value, got %q", i+1, line)
		}
		// The lines after the key's own that belong to its value: those
		// indented, and blank lines among and after them, which only a
		// block scalar that keeps its final line breaks keeps.
		end := i + 1
		for end < len(lines) && (indent(lines[end]) > 0 || strings.TrimSpace(lines[end]) == "") {
			end++
		}
		more := lines[i+1 : end]
		var err error
		switch {
		case value == "":
			// A mapping or a list, which flow does not read.
			fm[key] = ""
		case value[0] == '|' || value[0] == '>':
			fm[key], err = blockScalar(value, more)
		case value[0] == '"':
			fm[key], err = doubleQuoted(joinFolded(value, more))
		case value[0] == '\'':
			fm[key], err = singleQuoted(joinFolded(value, more))
		default:
			fm[key] = plainScalar(value, more)
		}
		if err != nil {
			return nil, fmt.Errorf("frontmatter key %s: %w", key, err)
		}
		i = end
	}
	return fm, nil
}

// cutKey splits a top-level line into its key and the value after the
// colon, without the spaces around it, and reports whether the line is one.
func cutKey(line string) (key, value string, ok bool) {
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		if i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t' {
			key = strings.TrimSpace(line[:i])
			if key == "" {
				return "", "", false
			}
			return unquoteKey(key), strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}

// unquoteKey returns key without the quotes around it, if it has them.
func unquoteKey(key string) string {
	if len(key) >= 2 && (key[0] == '"' || key[0] == '\'') && key[len(key)-1] == key[0] {
		return key[1 : len(key)-1]
	}
	return key
}

// indent returns the spaces and tabs that start line.
func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// isBlankOrComment reports whether line holds nothing but space, or a
// comment.
func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || t[0] == '#'
}

// stripComment returns a plain scalar's text without the comment that ends
// it, which starts at a # after a space.
func stripComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "\t#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// plainScalar returns a plain scalar that starts with first and goes on over
// the lines of more, folded: each line break a space, and each blank line a
// line break.
func plainScalar(first string, more []string) string {
	lines := []string{stripComment(first)}
	for _, l := range more {
		lines = append(lines, stripComment(l))
	}
	return fold(lines)
}

// fold joins lines, without the spaces around them, as YAML folds the lines
// of a flow scalar: a line break between two lines is a space, and each
// blank line between them a line break.
func fold(lines []string) string {
	var b strings.Builder
	breaks := 0
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" && i > 0 {
			breaks++
			continue
		}
		if i > 0 {
			if breaks == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(strings.Repeat("\n", breaks))
			}
		}
		breaks = 0
		b.WriteString(l)
	}
	return b.String()
}

// joinFolded returns the text of a quoted scalar that starts on the key's
// line with first and goes on over more, folded as fold folds it.
func joinFolded(first string, more []string) string {
	return fold(append([]string{first}, more...))
}

// singleQuoted returns the scalar s, which a single quote opens, without its
// quotes, each doubled quote within it one.
func singleQuoted(s string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		if rest := strings.TrimSpace(s[i+1:]); rest != "" && rest[0] != '#' {
			return "", fmt.Errorf("text after the closing quote: %q", rest)
		}
		return b.String(), nil
	}
	return "", errors.New("single-quoted scalar has no closing quote")
}

// doubleQuoted returns the scalar s, which a double quote opens, without its
// quotes, with its escapes decoded.
func doubleQuoted(s string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			if rest := strings.TrimSpace(s[i+1:]); rest != "" && rest[0] != '#' {
				return "", fmt.Errorf("text after the closing quote: %q", rest)
			}
			return b.String(), nil
		case '\\':
			if i+1 == len(s) {
				return "", errors.New("double-quoted scalar ends in a backslash")
			}
			i++
			n, err := unescape(&b, s[i:])
			if err != nil {
				return "", err
			}
			i += n
		default:
			b.WriteByte(c)
		}
	}
	return "", errors.New("double-quoted scalar has no closing quote")
}

// simpleEscapes are the one-character escapes of a double-quoted scalar, by
// the character after the backslash.
var simpleEscapes = map[byte]string{
	'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v", 'f': "\f",
	'r': "\r", 'e': "\x1b", ' ': " ", '"': "\"", '/': "/", '\\': "\\",
	'N': "\u0085", '_': "\u00a0", 'L': "\u2028", 'P': "\u2029",
}

// unescape writes to b the character that the escape at the start of s,
// after its backslash, stands for, and returns the bytes it took past the
// first.
func unescape(b *strings.Builder, s string) (int, error) {
	if r, ok := simpleEscapes[s[0]]; ok {
		b.WriteString(r)
		return 0, nil
	}
	digits := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[0]]
	if digits == 0 || len(s) < 1+digits {
		return 0, fmt.Errorf("unknown escape \\%c", s[0])
	}
	code, err := strconv.ParseUint(s[1:1+digits], 16, 32)
	if err != nil || !utf8.ValidRune(rune(code)) {
		return 0, fmt.Errorf("bad escape \\%s", s[:1+digits])
	}
	b.WriteRune(rune(code))
	return digits, nil
}

// blockScalar returns the literal (|) or folded (>) block scalar whose
// header is header, such as "|" or ">-", and whose lines are lines.
func blockScalar(header string, lines []string) (string, error) {
	folded := header[0] == '>'
	chomp, explicit := byte(0), 0
	for _, c := range []byte(stripComment(header[1:])) {
		switch {
		case (c == '-' || c == '+') && chomp == 0:
			chomp = c
		case c >= '1' && c <= '9' && explicit == 0:
			explicit = int(c - '0')
		default:
			return "", fmt.Errorf("bad block scalar header %q", header)
		}
	}
	// The block's indentation is its first line's that is not blank,
	// unless the header gives it.
	ind := explicit
	if ind == 0 {
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				ind = indent(l)
				break
			}
		}
	}
	content := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= ind && strings.TrimSpace(l[:ind]) == "" {
			content[i] = l[ind:]
		} else {
			content[i] = strings.TrimLeft(l, " \t")
		}
	}
	var text string
	if folded {
		text = foldBlock(content)
	} else {
		text = strings.Join(content, "\n")
	}
	switch body := strings.TrimRight(text, "\n"); chomp {
	case '-':
		return body, nil
	case '+':
		return text + "\n", nil
	default:
		if body == "" {
			return "", nil
		}
		return body + "\n", nil
	}
}

// foldBlock joins the lines of a folded block scalar: a line break between
// two lines of text is a space, and each blank line between them a line
// break in its place; a line indented beyond the block keeps the line breaks
// around it.
func foldBlock(lines []string) string {
	var b strings.Builder
	// started reports that a line of text has been written; blanks counts
	// the blank lines since the last, and lastMore reports whether it was
	// indented beyond the block.
	started, lastMore, blanks := false, false, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blanks++
			continue
		}
		more := l[0] == ' ' || l[0] == '\t'
		if started {
			switch {
			case blanks > 0:
				b.WriteString(strings.Repeat("\n", blanks))
				if more || lastMore {
					b.WriteByte('\n')
				}
			case more || lastMore:
				b.WriteByte('\n')
			default:
				b.WriteByte(' ')
			}
		} else {
			b.WriteString(strings.Repeat("\n", blanks))
		}
		b.WriteString(l)
		started, lastMore, blanks = true, more, 0
	}
	return b.String() + strings.Repeat("\n", blanks)
}
