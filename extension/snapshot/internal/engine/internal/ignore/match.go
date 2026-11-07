package ignore

import "strings"

// matchPath reports whether name matches glob, both slash-separated. A "**"
// segment matches any number of name segments, or at least one when it ends
// glob.
func matchPath(glob, name string) bool {
	for {
		g, globRest, globMore := strings.Cut(glob, "/")
		if g == "**" {
			if !globMore {
				return name != ""
			}
			return matchSuffix(globRest, name)
		}
		n, nameRest, nameMore := strings.Cut(name, "/")
		if !matchSegment(g, n) {
			return false
		}
		if !globMore || !nameMore {
			return globMore == nameMore
		}
		glob, name = globRest, nameRest
	}
}

// matchSuffix reports whether glob matches name or any trailing run of its
// segments.
func matchSuffix(glob, name string) bool {
	for {
		if matchPath(glob, name) {
			return true
		}
		_, rest, ok := strings.Cut(name, "/")
		if !ok {
			return false
		}
		name = rest
	}
}

// matchSegment reports whether name, which holds no slash, matches glob, one
// segment of a pattern. "*" matches any run of bytes, "?" matches one byte,
// "[...]" matches one byte from a set, and "\" quotes the next byte.
func matchSegment(glob, name string) bool {
	gi, ni := 0, 0
	// After a mismatch, resume at the last "*" with it absorbing one more
	// byte of name. retryN is zero until a "*" has been seen.
	retryG, retryN := 0, 0
	for gi < len(glob) || ni < len(name) {
		if gi < len(glob) && glob[gi] == '*' {
			retryG, retryN = gi, ni+1
			gi++
			continue
		}
		if gi < len(glob) && ni < len(name) {
			if w := matchByte(glob[gi:], name[ni]); w > 0 {
				gi += w
				ni++
				continue
			}
		}
		if retryN == 0 || retryN > len(name) {
			return false
		}
		gi, ni = retryG, retryN
	}
	return true
}

// matchByte matches c against the element at the start of glob. It returns
// the element's width, or 0 when c does not match.
func matchByte(glob string, c byte) int {
	switch glob[0] {
	case '?':
		return 1
	case '[':
		return matchClass(glob, c)
	case '\\':
		if len(glob) > 1 && glob[1] == c {
			return 2
		}
		return 0
	}
	if glob[0] == c {
		return 1
	}
	return 0
}

// matchClass matches c against the bracket expression at the start of glob.
// It returns the expression's width, or 0 when c is not in the set or the
// expression is malformed. A leading "!" or "^" negates the set, and a "]"
// directly after the opening bracket is an ordinary byte.
func matchClass(glob string, c byte) int {
	negated := len(glob) > 1 && (glob[1] == '!' || glob[1] == '^')
	start := 1
	if negated {
		start = 2
	}
	matched := false
	for i := start; i < len(glob); {
		if glob[i] == ']' && i > start {
			if matched == negated {
				return 0
			}
			return i + 1
		}
		w, hit := matchClassElem(glob[i:], c)
		if w == 0 {
			return 0
		}
		i += w
		matched = matched || hit
	}
	return 0
}

// matchClassElem matches c against the bracket-expression element at the
// start of s: a named class such as "[:alpha:]", a range such as "a-z", or one
// possibly quoted byte. It returns the element's width, which is 0 when the
// element is malformed, and whether c is in it.
func matchClassElem(s string, c byte) (int, bool) {
	if name, w := className(s); w > 0 {
		in, ok := classes[name]
		if !ok {
			return 0, false
		}
		return w, in(c)
	}
	lo, w := classByte(s)
	if w == 0 {
		return 0, false
	}
	if len(s) < w+2 || s[w] != '-' || s[w+1] == ']' {
		return w, c == lo
	}
	hi, hw := classByte(s[w+1:])
	if hw == 0 {
		return 0, false
	}
	return w + 1 + hw, lo <= c && c <= hi
}

// classByte returns the possibly quoted byte at the start of s and its width.
// The width is 0 when s is a lone backslash.
func classByte(s string) (byte, int) {
	if s[0] != '\\' {
		return s[0], 1
	}
	if len(s) < 2 {
		return 0, 0
	}
	return s[1], 2
}

// className returns the name and width of a "[:name:]" element at the start
// of s. The width is 0 when s does not start with one, in which case the "["
// is an ordinary byte.
func className(s string) (string, int) {
	rest, ok := strings.CutPrefix(s, "[:")
	if !ok {
		return "", 0
	}
	end := strings.IndexByte(rest, ']')
	if end < 1 || rest[end-1] != ':' {
		return "", 0
	}
	return rest[:end-1], end + 3
}

// classes holds the named classes allowed in bracket expressions. They cover
// ASCII only and follow Git's locale-independent definitions, so "space"
// excludes vertical tab and form feed.
var classes = map[string]func(byte) bool{
	"alnum":  func(c byte) bool { return isAlpha(c) || isDigit(c) },
	"alpha":  isAlpha,
	"blank":  func(c byte) bool { return c == ' ' || c == '\t' },
	"cntrl":  func(c byte) bool { return c < ' ' || c == 0x7f },
	"digit":  isDigit,
	"graph":  func(c byte) bool { return '!' <= c && c <= '~' },
	"lower":  func(c byte) bool { return 'a' <= c && c <= 'z' },
	"print":  func(c byte) bool { return ' ' <= c && c <= '~' },
	"punct":  isPunct,
	"space":  func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' },
	"upper":  func(c byte) bool { return 'A' <= c && c <= 'Z' },
	"xdigit": func(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' },
}

func isAlpha(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func isPunct(c byte) bool { return '!' <= c && c <= '~' && !isAlpha(c) && !isDigit(c) }
