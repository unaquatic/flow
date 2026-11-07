// Package ignore matches slash-separated paths against gitignore patterns.
//
// Patterns follow gitignore(5). A pattern without a slash matches a base name
// at any depth below its directory. A leading or inner slash anchors the
// pattern to its directory. A trailing slash restricts it to directories, and
// a "**" segment spans any number of directories. Matching is case-sensitive.
package ignore

import (
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Pattern is one line of an ignore file.
type Pattern struct {
	// Dir is the slash-separated directory, relative to the worktree root,
	// of the file holding the pattern. It is empty for the root and for
	// files that apply to the whole worktree.
	Dir string

	// Glob is the pattern without its "!" prefix and trailing slash. A slash
	// anywhere in Glob anchors it to Dir; otherwise it matches base names.
	Glob string

	// Negate reports whether the pattern re-includes what it matches.
	Negate bool

	// DirOnly reports whether the pattern matches only directories.
	DirOnly bool
}

// ParsePattern parses line, one line of an ignore file held in dir. It
// reports false for blank lines and comments.
func ParsePattern(line, dir string) (Pattern, bool) {
	line = trimTrailingSpaces(line)
	if line == "" || line[0] == '#' {
		return Pattern{}, false
	}
	p := Pattern{Dir: dir}
	line, p.Negate = strings.CutPrefix(line, "!")
	line, p.DirOnly = strings.CutSuffix(line, "/")
	if line == "" {
		return Pattern{}, false
	}
	p.Glob = line
	return p, true
}

// Parse parses the contents of an ignore file held in dir into a Matcher
// holding its patterns in file order.
func Parse(data []byte, dir string) Matcher {
	text := strings.TrimPrefix(string(data), "\ufeff")
	var patterns Matcher
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if p, ok := ParsePattern(line, dir); ok {
			patterns = append(patterns, p)
		}
	}
	return patterns
}

// Match reports whether p matches name, a slash-separated path relative to
// the worktree root whose file type is typ. Match ignores p.Negate.
func (p Pattern) Match(name string, typ fs.FileMode) bool {
	if p.DirOnly && !typ.IsDir() {
		return false
	}
	rel, ok := relative(name, p.Dir)
	if !ok {
		return false
	}
	if !strings.Contains(p.Glob, "/") {
		return matchSegment(p.Glob, path.Base(rel))
	}
	return matchPath(strings.TrimPrefix(p.Glob, "/"), rel)
}

// Matcher holds patterns in increasing order of precedence. When several
// patterns match a path, the last one decides.
//
// Git consults, from lowest to highest precedence, core.excludesFile,
// $GIT_DIR/info/exclude, and the .gitignore files from the worktree root down
// to the path's own directory.
type Matcher []Pattern

// With returns a Matcher holding the patterns of m followed by patterns,
// which take precedence over them. It leaves m unchanged.
func (m Matcher) With(patterns ...Pattern) Matcher {
	if len(patterns) == 0 {
		return m
	}
	return slices.Concat(m, patterns)
}

// Match reports whether name, whose file type is typ, is ignored. It does not
// consult the parent directories of name: as in Git, a path inside an ignored
// directory is ignored because a walker never descends into that directory.
func (m Matcher) Match(name string, typ fs.FileMode) bool {
	for _, p := range slices.Backward(m) {
		if p.Match(name, typ) {
			return !p.Negate
		}
	}
	return false
}

// relative returns name relative to dir. It reports false when name is not
// strictly below dir.
func relative(name, dir string) (string, bool) {
	if dir == "" {
		return name, name != ""
	}
	rest, ok := strings.CutPrefix(name, dir)
	rest, sep := strings.CutPrefix(rest, "/")
	return rest, ok && sep && rest != ""
}

// trimTrailingSpaces removes unquoted trailing spaces from line. A space
// preceded by a backslash is kept.
func trimTrailingSpaces(line string) string {
	cut := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			if cut < 0 {
				cut = i
			}
		case '\\':
			i++
			cut = -1
		default:
			cut = -1
		}
	}
	if cut < 0 {
		return line
	}
	return line[:cut]
}
