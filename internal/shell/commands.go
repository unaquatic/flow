package shell

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// CommandWords returns the words of src that name the commands it runs, in
// a new slice ordered by offset: the name of every simple command that Parse
// finds, wherever it stands, and after it the words that Outline names after
// it: the command a wrapper runs, the commands of the script that a shell's
// -c, eval or a joined wrapper such as ssh runs, and a subcommand. In
//
//	cd repo && sudo -u root make test | tee log; bash -c 'go vet'
//
// they are cd, sudo, make, test, tee, bash, go and vet. A name that is not
// literal, such as "$cmd", is left out, and so are the commands it runs.
//
// Each word stands in src as written: src[w.Offset:] starts with w.Raw. A
// word that does not, such as a command of a script whose quotes hold
// escapes, is left out.
//
// On a syntax error CommandWords returns the words of the commands read
// before the error, and a *SyntaxError.
func CommandWords(src string) ([]Word, error) {
	cmds, err := Parse(src)
	var words []Word
	for _, c := range cmds {
		words = AppendCommandWords(words, c.Words, 0)
	}
	words = slices.DeleteFunc(words, func(w Word) bool { return !strings.HasPrefix(src[w.Offset:], w.Raw) })
	slices.SortStableFunc(words, func(a, b Word) int { return cmp.Compare(a.Offset, b.Offset) })
	return words, err
}

// AppendCommandWords appends to dst the words of a simple command, words,
// its name and arguments, that name what it runs depth wrappers deep, as
// CommandWords describes them, following the wrappers Wrapped follows, and
// returns the extended slice.
func AppendCommandWords(dst, words []Word, depth int) []Word {
	name := words[0]
	if !name.Literal {
		return dst
	}
	dst = append(dst, name)
	if depth >= MaxWrapDepth {
		return dst
	}
	args := words[1:]
	switch base := path.Base(name.Value); {
	case base == "eval":
		return AppendScriptWords(dst, args, depth)
	case slices.Contains(Shells, base):
		if script, ok := ShellScript(args); ok {
			return AppendScriptWords(dst, []Word{script}, depth)
		}
	case base == "find":
		for _, cmd := range FindExecs(args) {
			dst = AppendCommandWords(dst, cmd, depth+1)
		}
	default:
		w, ok := Wrappers[base]
		if !ok {
			if sub, ok := Subcommand(base, args); ok {
				dst = append(dst, sub)
			}
			return dst
		}
		cmd, ok := w.Command(args)
		switch {
		case !ok:
		case w.Joined && len(cmd) == 1 && cmd[0].Literal:
			return AppendScriptWords(dst, cmd, depth)
		default:
			return AppendCommandWords(dst, cmd, depth+1)
		}
	}
	return dst
}

// AppendScriptWords appends to dst the words that name the commands of the
// script that words make up, joined a space apart as eval joins them, run
// depth wrappers deep, as CommandWords describes them, and returns the
// extended slice. Their offsets are in the source of words, and those of a
// word whose value is not written as it reads, as ValueOffset tells, are
// left out. It appends none when a word is not literal or the script would
// not parse.
func AppendScriptWords(dst, words []Word, depth int) []Word {
	script, ok := JoinLiteral(words)
	if !ok {
		return dst
	}
	cmds, err := Parse(script)
	if err != nil {
		return dst
	}
	// origin holds the offset in the source of each byte of script, or -1
	// for a byte that is not written there as it reads.
	origin := make([]int, 0, len(script))
	for i, w := range words {
		if i > 0 {
			origin = append(origin, -1) // the space between words
		}
		at, ok := w.ValueOffset()
		for j := range len(w.Value) {
			if !ok {
				origin = append(origin, -1)
				continue
			}
			origin = append(origin, w.Offset+at+j)
		}
	}
	for _, c := range cmds {
		for _, n := range AppendCommandWords(nil, c.Words, depth+1) {
			if n.Offset = origin[n.Offset]; n.Offset >= 0 {
				dst = append(dst, n)
			}
		}
	}
	return dst
}

// ValueOffset returns the offset in w.Raw at which w.Value is written as it
// reads, in a word written bare or in one pair of quotes, as make or
// 'cd x && make' are, and reports false when quote removal changed it
// otherwise, as it changes "a\"b" or a'b'.
func (w Word) ValueOffset() (int, bool) {
	raw, v := w.Raw, w.Value
	if raw == v {
		return 0, true
	}
	if n := len(raw); n == len(v)+2 && (raw[0] == '\'' || raw[0] == '"') && raw[n-1] == raw[0] && raw[1:n-1] == v {
		return 1, true
	}
	return 0, false
}
