package engine

import "strings"

// configValue returns the last value of key in the section named section of
// a Git configuration file, and reports whether the file sets it. Section and
// key names match case-insensitively, and a section with a subsection never
// matches. A key without a value does not count as set. Include directives
// and values continued onto the next line are not supported.
func configValue(data []byte, section, key string) (string, bool) {
	var (
		inSection bool
		value     string
		found     bool
	)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "["); ok {
			name, after, ok := strings.Cut(rest, "]")
			if !ok {
				continue
			}
			inSection = strings.EqualFold(strings.TrimSpace(name), section)
			line = strings.TrimSpace(after)
		}
		if !inSection {
			continue
		}
		if v, ok := configEntry(line, key); ok {
			value, found = v, true
		}
	}
	return value, found
}

// configEntry returns the value line assigns to key, reporting false when
// line assigns no value to key.
func configEntry(line, key string) (string, bool) {
	if line == "" || line[0] == '#' || line[0] == ';' {
		return "", false
	}
	name, raw, ok := strings.Cut(line, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), key) {
		return "", false
	}
	return unquote(strings.TrimSpace(stripComment(raw))), true
}

// stripComment returns raw up to its first "#" or ";" outside double quotes.
func stripComment(raw string) string {
	quoted := false
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++
		case '"':
			quoted = !quoted
		case '#', ';':
			if !quoted {
				return raw[:i]
			}
		}
	}
	return raw
}

// unquote removes the double quotes from a configuration value and resolves
// its backslash escapes.
func unquote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(unescape(s[i]))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unescape returns the byte a configuration value means by a backslash
// followed by c.
func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'b':
		return '\b'
	}
	return c
}
