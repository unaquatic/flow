package shell

import (
	"errors"
	"strings"
	"testing"
)

// marked returns src with each of words, at its offset, in «».
func marked(t *testing.T, src string, words []Word) string {
	t.Helper()
	var b strings.Builder
	at := 0
	for _, w := range words {
		if w.Offset < at || !strings.HasPrefix(src[w.Offset:], w.Raw) {
			t.Fatalf("word %q at %d of %q: want it written there, after the word before", w.Raw, w.Offset, src)
		}
		b.WriteString(src[at:w.Offset] + "«" + w.Raw + "»")
		at = w.Offset + len(w.Raw)
	}
	b.WriteString(src[at:])
	return b.String()
}

// TestCommandWords checks that the words that name the commands a script
// runs are found where they are written: the names of its simple commands,
// wherever they stand, and the commands that wrappers and scripts in them
// run, and subcommands, as an outline names them.
func TestCommandWords(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"one":                {"go test ./... -v", "«go» «test» ./... -v"},
		"empty":              {"", ""},
		"lists":              {"cd repo && FOO=1 make -j4 2>&1 | tee log; ls", "«cd» repo && FOO=1 «make» -j4 2>&1 | «tee» log; «ls»"},
		"newlines":           {"cd calc\ngo vet ./...", "«cd» calc\n«go» «vet» ./..."},
		"substitutions":      {"echo \"$(date)\" `id -u` <(ls) ${x:-$(pwd)}", "«echo» \"$(«date»)\" `«id» -u` <(«ls») ${x:-$(«pwd»)}"},
		"nested backquotes":  {"echo `a \\`b\\``", "«echo» `«a» \\`«b»\\``"},
		"heredoc":            {"cat <<EOF | grep -v x\n$(whoami)\nEOF", "«cat» <<EOF | «grep» -v x\n$(«whoami»)\nEOF"},
		"quoted heredoc":     {"cat <<'EOF'\n$(whoami)\nEOF", "«cat» <<'EOF'\n$(whoami)\nEOF"},
		"compound":           {"for f in *.go; do gofmt -l \"$f\"; done", "for f in *.go; do «gofmt» -l \"$f\"; done"},
		"if and tests":       {"if [ -f x ]; then rm x; fi", "if «[» -f x ]; then «rm» x; fi"},
		"function":           {"f() { go vet; }; f", "f() { «go» «vet»; }; «f»"},
		"comment":            {"ls # rm -rf x", "«ls» # rm -rf x"},
		"quoted name":        {"'my tool' x", "«'my tool'» x"},
		"not literal":        {"\"$cmd\" x; ${tool} y; sudo \"$c\" z", "\"$cmd\" x; ${tool} y; «sudo» \"$c\" z"},
		"sudo":               {"sudo -u root rm -rf /tmp/x", "«sudo» -u root «rm» -rf /tmp/x"},
		"nested wrappers":    {"sudo -E env A=1 nice -n 5 go test", "«sudo» -E «env» A=1 «nice» -n 5 «go» «test»"},
		"wrapper runs none":  {"sudo -l; command -v ls", "«sudo» -l; «command» -v ls"},
		"xargs":              {"find . -name '*.go' | xargs -0 gofmt -l", "«find» . -name '*.go' | «xargs» -0 «gofmt» -l"},
		"find -exec":         {"find . -exec rm -f {} \\; -ok chmod 644 {} +", "«find» . -exec «rm» -f {} \\; -ok «chmod» 644 {} +"},
		"subcommand":         {"git -C repo commit -m msg", "«git» -C repo «commit» -m msg"},
		"subcommand builtin": {"go test ./... && npm help", "«go» «test» ./... && «npm» «help»"},
		"no subcommand":      {"git --version; make -j8", "«git» --version; «make» -j8"},
		"bash -c":            {"bash -c 'cd app && make'", "«bash» -c '«cd» app && «make»'"},
		"sh -c double":       {"sh -c \"go test ./...\"", "«sh» -c \"«go» «test» ./...\""},
		"nested scripts":     {"bash -c 'sudo sh -c \"rm -rf x; ls\"'", "«bash» -c '«sudo» «sh» -c \"«rm» -rf x; «ls»\"'"},
		"script escapes":     {"bash -c \"echo \\\"\\$x\\\"; ls\"", "«bash» -c \"echo \\\"\\$x\\\"; ls\""},
		"script not literal": {"bash -c \"$script\"", "«bash» -c \"$script\""},
		"script bad":         {"bash -c 'echo \"x'", "«bash» -c 'echo \"x'"},
		"eval":               {"eval \"cd x && make\"", "«eval» \"«cd» x && «make»\""},
		"eval words":         {"eval cd x '&&' make", "«eval» «cd» x '&&' «make»"},
		"ssh":                {"ssh -p 22 host.example 'cd app && make'", "«ssh» -p 22 host.example '«cd» app && «make»'"},
		"watch words":        {"watch -n2 ls -la", "«watch» -n2 «ls» -la"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			words, err := CommandWords(tt.src)
			if err != nil {
				t.Fatalf("CommandWords(%q) error: %v", tt.src, err)
			}
			if got := marked(t, tt.src, words); got != tt.want {
				t.Errorf("CommandWords(%q) marks %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestCommandWords_Error checks that source bash would not run gives the
// words of the commands read before its error, and the error.
func TestCommandWords_Error(t *testing.T) {
	const src = "ls && echo 'x"
	words, err := CommandWords(src)
	if _, ok := errors.AsType[*SyntaxError](err); !ok {
		t.Fatalf("CommandWords(%q) error = %v; want a *SyntaxError", src, err)
	}
	if got, want := marked(t, src, words), "«ls» && echo 'x"; got != want {
		t.Errorf("CommandWords(%q) marks %q; want %q", src, got, want)
	}
}

// TestWord_ValueOffset checks that a word's value is found in it only where
// it is written as it reads.
func TestWord_ValueOffset(t *testing.T) {
	tests := []struct {
		raw, value string
		at         int
		ok         bool
	}{
		{"make", "make", 0, true},
		{"'cd x && make'", "cd x && make", 1, true},
		{`"go test"`, "go test", 1, true},
		{`"a'b"`, "a'b", 1, true},
		{`"a\"b"`, `a"b`, 0, false},
		{"a'b'", "ab", 0, false},
		{`\ls`, "ls", 0, false},
		{"$'a'", "a", 0, false},
	}
	for _, tt := range tests {
		at, ok := Word{Raw: tt.raw, Value: tt.value}.ValueOffset()
		if at != tt.at || ok != tt.ok {
			t.Errorf("ValueOffset of %s = %d, %v; want %d, %v", tt.raw, at, ok, tt.at, tt.ok)
		}
	}
}
