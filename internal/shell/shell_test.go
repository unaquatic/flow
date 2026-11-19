package shell

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// names returns the names of the commands in src, each its value when it is
// literal and "?" and its raw text when it is not.
func names(t *testing.T, src string) []string {
	t.Helper()
	cmds, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	var out []string
	for _, c := range cmds {
		if n := c.Name(); n.Literal {
			out = append(out, n.Value)
		} else {
			out = append(out, "?"+n.Raw)
		}
	}
	return out
}

// TestParse_Names checks that Parse finds every simple command, wherever it
// stands, in the order of its name in the source.
func TestParse_Names(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"one":               {"ls", []string{"ls"}},
		"empty":             {"", nil},
		"blank and comment": {"  # just a comment\n\n", nil},
		"lists":             {"FOO=1 git status && cat x | grep -v \"$(whoami)\" ; (cd /tmp; rm -rf y)", []string{"git", "cat", "grep", "whoami", "cd", "rm"}},
		"background":        {"sleep 1 & wait", []string{"sleep", "wait"}},
		"or and pipe-all":   {"a || b |& c", []string{"a", "b", "c"}},
		"newline lists":     {"a &&\n b |\n c", []string{"a", "b", "c"}},
		"backquotes":        {"echo `uname -a` $(date)", []string{"echo", "uname", "date"}},
		"nested backquotes": {"echo `a \\`b\\``", []string{"echo", "a", "b"}},
		"nested subst":      {"echo $(a $(b) \"$(c)\")", []string{"echo", "a", "b", "c"}},
		"process subst":     {"diff <(a) >(b)", []string{"diff", "a", "b"}},
		"param expansion":   {"echo ${x:-$(a)} \"${y:+`b`}\"", []string{"echo", "a", "b"}},
		"brace subst":       {"echo ${ a; } ${| b; }", []string{"echo", "a", "b"}},
		"arithmetic":        {"echo $(( $(a) + 1 )) $[ $(b) ]; (( $(c) ))", []string{"echo", "a", "b", "c"}},
		"arith or subst":    {"echo $((a) ) $( (b) ); ((c); d)", []string{"echo", "a", "b", "c", "d"}},
		"assign only":       {"X=$(a) Y=`b`", []string{"a", "b"}},
		"array":             {"arr=( $(a) \"b\"\n c ) d", []string{"a", "d"}},
		"declare array":     {"declare -a x=( $(a) )", []string{"declare", "a"}},
		"redirects":         {"ls > $(a) 2>&1 <in &>/dev/null {fd}>x", []string{"ls", "a"}},
		"redirect first":    {">out a", []string{"a"}},
		"herestring":        {"cat <<< $(a)", []string{"cat", "a"}},
		"comment":           {"ls # $(rm x)\necho a#b", []string{"ls", "echo"}},
		"continuation":      {"l\\\ns -la \\\n x", []string{"ls"}},
		"group":             {"{ a; b; } > out", []string{"a", "b"}},
		"group in group":    {"{ { a; } }", []string{"a"}},
		"if":                {"if a; then b; elif c; then d; else e; fi", []string{"a", "b", "c", "d", "e"}},
		"while until":       {"while a; do b; done; until c\ndo d\ndone", []string{"a", "b", "c", "d"}},
		"compound in loop":  {"while a; do if b; then c; fi done", []string{"a", "b", "c"}},
		"for":               {"for f in $(ls) *.go; do cat \"$f\"; done", []string{"ls", "cat"}},
		"for newline in":    {"for f\nin a b\ndo c; done", []string{"c"}},
		"for no in":         {"for f; do a; done", []string{"a"}},
		"for braces":        {"for f in a; { b; }", []string{"b"}},
		"c-style for":       {"for ((i=$(a); i<3; i++)); do b; done", []string{"a", "b"}},
		"select":            {"select x in a b; do c; done", []string{"c"}},
		"case": {
			"case $(a) in\n x|y) b;;\n (z) c ;&\n *) d ;;&\n esac",
			[]string{"a", "b", "c", "d"},
		},
		"case last item":    {"case x in y) a; esac", []string{"a"}},
		"case in subst":     {"echo $(case x in y) a;; esac)", []string{"echo", "a"}},
		"test":              {"[[ -n $(a) && ( x =~ ^(b|c)$ ) ]] || d", []string{"a", "d"}},
		"test regex parens": {"[[ x =~ (a b) ]] && c", []string{"c"}},
		"bracket test":      {"[ -f x ] && a", []string{"[", "a"}},
		"function":          {"f() { a; }; function g { b; }; function h() ( c ); f", []string{"a", "b", "c", "f"}},
		"function newline":  {"f ()\n{\n a\n}", []string{"a"}},
		"time and not":      {"time -p a | b; ! c; time ! d; time", []string{"a", "b", "c", "d"}},
		"coproc":            {"coproc a; coproc NAME { b; }; coproc ( c )", []string{"a", "b", "c"}},
		"reserved as args":  {"echo if then { } fi done", []string{"echo"}},
		"reserved prefix":   {"iffy; done-it; {x}", []string{"iffy", "done-it", "{x}"}},
		"heredoc": {
			"cat <<EOF\n$(a)\n`b` ${x:-$(c)}\nEOF\nd",
			[]string{"cat", "a", "b", "c", "d"},
		},
		"quoted heredoc":   {"cat <<'EOF'\n$(a)\nEOF\nb", []string{"cat", "b"}},
		"escaped heredoc":  {"cat <<\\EOF\n$(a)\nEOF", []string{"cat"}},
		"heredoc tabs":     {"cat <<-EOF\n\t$(a)\n\tEOF\nb", []string{"cat", "a", "b"}},
		"heredoc pipe":     {"cat <<EOF | grep x\n$(a)\nEOF", []string{"cat", "grep", "a"}},
		"two heredocs":     {"a <<X <<'Y'\n$(b)\nX\n$(c)\nY\nd", []string{"a", "b", "d"}},
		"heredoc delim":    {"cat <<$(a)\nx\n$(a)\nb", []string{"cat", "b"}},
		"heredoc in subst": {"x=$(cat <<EOF\n$(a)\nEOF\n)", []string{"cat", "a"}},
		"heredoc no end":   {"cat <<EOF\n$(a)", []string{"cat", "a"}},
		"dollar alone":     {"echo $ \"$\" a$", []string{"echo"}},
		"extglob":          {"ls !(x|$(a))", []string{"ls", "a"}},
		"subscript assign": {"a[x $(b)]=1 c; d a[", []string{"b", "c", "d"}},
		"dup after dup":    {"a 2>&1>/dev/null", []string{"a"}},
		"old arithmetic":   {"echo $[(1]+$(a)]", []string{"echo", "a"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := names(t, tt.src); !slices.Equal(got, tt.want) {
				t.Errorf("names of %q = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestParse_Literal checks which command names stand for themselves, after
// quote removal, and which depend on an expansion.
func TestParse_Literal(t *testing.T) {
	tests := []struct {
		src     string
		value   string
		literal bool
		quoted  bool
	}{
		{"rm x", "rm", true, false},
		{`"rm" x`, "rm", true, true},
		{"'rm' x", "rm", true, true},
		{`r\m x`, "rm", true, true},
		{`r""m x`, "rm", true, true},
		{`$'r\x6d' x`, "rm", true, true},
		{`$'\162\u006d' x`, "rm", true, true},
		{`$"rm" x`, "rm", true, true},
		{"/usr/bin/rm x", "/usr/bin/rm", true, false},
		{"$cmd x", "$cmd", false, false},
		{`"$cmd" x`, "$cmd", false, true},
		{"${cmd} x", "${cmd}", false, false},
		{`"$(echo rm)" x`, "$(echo rm)", false, true},
		{"`echo rm` x", "`echo rm`", false, false},
		{"r* x", "r*", false, false},
		{"r? x", "r?", false, false},
		{"[r]m x", "[r]m", false, false},
		{`"r*" x`, "r*", true, true},
		{"{rm,ls} x", "{rm,ls}", false, false},
		{"{a..c} x", "{a..c}", false, false},
		{"{} x", "{}", true, false},
		{"~/bin/a x", "~/bin/a", false, false},
		{`"~/bin/a" x`, "~/bin/a", true, true},
		{"a~ x", "a~", true, false},
		{"[ x", "[", true, false},
	}
	for _, tt := range tests {
		cmds, err := Parse(tt.src)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.src, err)
			continue
		}
		var name Word
		for _, c := range cmds {
			if c.Offset() == 0 {
				name = c.Name()
			}
		}
		if name.Value != tt.value || name.Literal != tt.literal || name.Quoted != tt.quoted {
			t.Errorf("name of %q = %q literal %v quoted %v; want %q literal %v quoted %v",
				tt.src, name.Value, name.Literal, name.Quoted, tt.value, tt.literal, tt.quoted)
		}
	}
}

// TestParse_Command checks the words, assignments and redirects of a
// command, and their offsets in the source, also from inside backquotes.
func TestParse_Command(t *testing.T) {
	src := "A=1 B=\"x y\" git commit -m \"a b\" 2>&1 >$out <<EOF -q\nbody\nEOF\necho `ls \\$d`"
	cmds, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 {
		t.Fatalf("got %d commands; want 3", len(cmds))
	}
	git := cmds[0]
	var words, assigns, redirects []string
	for _, w := range git.Words {
		words = append(words, w.Value)
	}
	for _, w := range git.Assigns {
		assigns = append(assigns, w.Raw)
	}
	for _, r := range git.Redirects {
		redirects = append(redirects, r.N+r.Op+" "+r.Target.Value)
	}
	if want := []string{"git", "commit", "-m", "a b", "-q"}; !slices.Equal(words, want) {
		t.Errorf("words = %q; want %q", words, want)
	}
	if want := []string{"A=1", `B="x y"`}; !slices.Equal(assigns, want) {
		t.Errorf("assigns = %q; want %q", assigns, want)
	}
	if want := []string{"2>& 1", "> $out", "<< EOF"}; !slices.Equal(redirects, want) {
		t.Errorf("redirects = %q; want %q", redirects, want)
	}
	if got, want := git.Offset(), strings.Index(src, "git"); got != want {
		t.Errorf("git offset = %d; want %d", got, want)
	}
	if got, want := git.Redirects[1].Offset, strings.Index(src, ">$out"); got != want {
		t.Errorf("redirect offset = %d; want %d", got, want)
	}
	ls := cmds[2]
	if got, want := ls.Offset(), strings.Index(src, "ls"); got != want {
		t.Errorf("ls offset = %d; want %d", got, want)
	}
	if arg := ls.Args()[0]; arg.Raw != "$d" || arg.Literal || arg.Offset != strings.Index(src, "$d") {
		t.Errorf("ls arg = %+v; want $d, not literal, at the $ after the backslash", arg)
	}
}

// TestParse_Errors checks that source bash would not run fails with a
// *SyntaxError at the right place, and returns the commands before it.
func TestParse_Errors(t *testing.T) {
	tests := []struct {
		src       string
		line, col int
		msg       string
		before    int
	}{
		{"echo 'x", 1, 6, "unclosed single quote", 0},
		{"ls; echo \"x", 1, 10, "unclosed double quote", 1},
		{"echo `x", 1, 6, "unclosed backquote", 0},
		{"echo $(x", 1, 6, "unclosed command substitution", 1},
		{"echo ${x", 1, 6, "unclosed parameter expansion", 0},
		{"if a; then b", 1, 1, `"if" without "fi"`, 2},
		{"while a\ndo b", 1, 1, `"while" without "done"`, 2},
		{"{ a", 1, 1, `"{" without "}"`, 1},
		{"case x in\n y) a;;", 1, 1, `"case" without "esac"`, 1},
		{"[[ x", 1, 1, `"[[" without "]]"`, 0},
		{"(a", 1, 1, "unclosed subshell", 1},
		{"ls )", 1, 4, `unexpected ")"`, 1},
		{"fi", 1, 1, `unexpected "fi"`, 0},
		{"a\n;; b", 2, 1, `unexpected ";;"`, 1},
		{"; a", 1, 1, `unexpected ";"`, 0},
		{"a && ", 1, 6, "unexpected end of input", 1},
		{"echo (x)", 1, 7, "unexpected 'x' in function definition", 0},
		{"if a then b fi", 1, 1, `"if" without "then"`, 1},
		{"ls >", 1, 5, `unexpected end of input after ">"`, 0},
		{"{ a; } b", 1, 8, `unexpected 'b'`, 1},
		{"a>0>0", 1, 3, `unexpected "0" after ">"`, 0},
		{"a > 2>x", 1, 5, `unexpected "2" after ">"`, 0},
		{"a[", 1, 2, "unclosed subscript", 0},
		{"a=(b=(c))", 1, 6, `unexpected "(" in array`, 0},
		{"f() a", 1, 5, "unexpected 'a', want a compound command", 0},
		{"f(){a;}", 1, 4, `unexpected '{', want a compound command`, 0},
		{"a\necho `b\n'`", 3, 1, "unclosed single quote", 2},
		{strings.Repeat("$(", MaxDepth+1), 1, 2*MaxDepth + 1, "nesting deeper than 1000", 0},
	}
	for _, tt := range tests {
		cmds, err := Parse(tt.src)
		se, ok := errors.AsType[*SyntaxError](err)
		if !ok {
			t.Errorf("Parse(%.40q) error = %v; want a *SyntaxError", tt.src, err)
			continue
		}
		if se.Line != tt.line || se.Col != tt.col || se.Msg != tt.msg {
			t.Errorf("Parse(%.40q) error = %v; want %d:%d: %s", tt.src, se, tt.line, tt.col, tt.msg)
		}
		if len(cmds) != tt.before {
			t.Errorf("Parse(%.40q) returned %d commands; want the %d before the error", tt.src, len(cmds), tt.before)
		}
	}
}

// FuzzParse checks that Parse fails only with a *SyntaxError and gives
// offsets within the source, and that Outline fails just when Parse does,
// with an outline of one line.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"a | b && c; (d) & { e; }", "x=$(a `b` \"$(c)\") d <<E\n$(f)\nE\n",
		"case $x in a|b) c;; esac", "for ((i=0;i<$(a);i++)); do b; done",
		"[[ a =~ (b|c) ]]", "f() { $'\\x41' ${x:-$(y)}; }", "echo $((a) )",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		cmds, err := Parse(src)
		if _, ok := errors.AsType[*SyntaxError](err); err != nil && !ok {
			t.Fatalf("error %v is not a *SyntaxError", err)
		}
		for _, c := range cmds {
			for _, w := range slices.Concat(c.Assigns, c.Words) {
				if w.Offset < 0 || w.Offset >= len(src) {
					t.Fatalf("word %q at offset %d outside %d bytes", w.Raw, w.Offset, len(src))
				}
			}
		}
		outline, oerr := Outline(src)
		if (oerr == nil) != (err == nil) {
			t.Fatalf("Outline error %v; Parse error %v", oerr, err)
		}
		if strings.Contains(outline, "\n") {
			t.Fatalf("outline %q holds a newline", outline)
		}
	})
}

// BenchmarkParse measures parsing a typical multi-command line.
func BenchmarkParse(b *testing.B) {
	src := `cd "$dir" && FOO=1 go test ./... -run 'Test(A|B)' 2>&1 | tee "$(mktemp)" | grep -v ok; [[ $? -eq 0 ]] || exit 1`
	for b.Loop() {
		if _, err := Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
