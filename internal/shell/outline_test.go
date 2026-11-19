package shell

import (
	"errors"
	"testing"
)

// TestOutline checks that an outline keeps each command's name, as written,
// and the operators and compound commands between them, and nothing else.
func TestOutline(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"one":              {"go test ./... -run 'Test(A|B)' -v", "go test"},
		"empty":            {"", ""},
		"operators":        {"cd calc && go test ./... | grep -v ok; go vet ./...", "cd && go test | grep; go vet"},
		"or and pipe-all":  {"a x || b y |& c z", "a || b |& c"},
		"newlines":         {"cd calc\ntest -z \"$(gofmt -l .)\"", "cd; test"},
		"blank lines":      {"a\n\n\nb;\n\nc", "a; b; c"},
		"comments":         {"echo `date`; # comment $(x)\n\nls # more", "echo; ls"},
		"continuation":     {"a &&\n  b \\\n  --flag", "a && b"},
		"background":       {"sleep 1 &", "sleep &"},
		"background list":  {"FOO=1 a . &> log &\nwait", "a & wait"},
		"substitutions":    {"echo $(whoami) `id` <(ls) \"${x:-$(y)}\"", "echo"},
		"redirects":        {"{ a; b; } > out 2>&1", "{ a; b; }"},
		"subshell":         {"(cd /tmp; rm -rf y) && ls", "(cd; rm) && ls"},
		"subshell newline": {"(\n a\n b\n)", "(a; b)"},
		"assign only":      {"X=1 Y=2 && Z+=3", "X= && Z="},
		"redirect only":    {"> out", ">"},
		"if":               {"if [[ -n $x ]]; then make; elif a; then b; else echo no; fi", "if [[ … ]]; then make; elif a; then b; else echo; fi"},
		"if newlines":      {"if a\nthen\n  b\nfi", "if a; then b; fi"},
		"while":            {"while read -r l; do echo \"$l\"; done < <(ls)", "while read; do echo; done"},
		"until newlines":   {"until a\ndo\n b\ndone", "until a; do b; done"},
		"for":              {"for f in *.go; do gofmt -w \"$f\"; done", "for f in …; do gofmt; done"},
		"for newline":      {"for f in a b\ndo\n c\ndone", "for f in …; do c; done"},
		"for no in":        {"for f; do a; done", "for f; do a; done"},
		"c-style for":      {"for ((i=0;i<3;i++)); do echo $i; done", "for ((…)); do echo; done"},
		"arithmetic":       {"(( n++ )) || true", "((…)) || true"},
		"case":             {"case $x in\n a|b) ls;;\n *) pwd\n ;;\nesac", "case … in a|b) ls;; *) pwd;; esac"},
		"case last item":   {"case x in y) a; esac", "case … in y) a; esac"},
		"tests":            {"[ -f x ] && [[ -d y ]] && rm x", "[ … ] && [[ … ]] && rm"},
		"functions":        {"f() { a; }; function g { b; }; f", "f() { a; }; g() { b; }; f"},
		"not and time":     {"! grep -q x f || time -p go build", "! grep || time go build"},
		"time alone":       {"time; ls", "time; ls"},
		"coproc":           {"coproc NAME { a; }", "coproc NAME { a; }"},
		"heredoc":          {"cat <<EOF > x\nhello $(world)\nEOF\nchmod +x x", "cat; chmod"},
		"unexpanded name":  {"\"$cmd\" --flag; ${tool} x; ~/bin/a", "\"$cmd\"; ${tool}; ~/bin/a"},
		"quoted name":      {"'my tool' x", "'my tool'"},
		"newline in name":  {"'a\nb' x", "'a b'"},
		"nested compound":  {"while a; do if b; then c; fi; done", "while a; do if b; then c; fi; done"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Outline(tt.src)
			if err != nil {
				t.Fatalf("Outline(%q) error: %v", tt.src, err)
			}
			if got != tt.want {
				t.Errorf("Outline(%q) = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestOutline_Wrappers checks that a wrapper shows its name and then the
// command it runs, without its own options, through wrappers and scripts.
func TestOutline_Wrappers(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"sudo":                   {"sudo rm -rf /tmp/x", "sudo rm"},
		"sudo options":           {"sudo -u root -E -- rm x", "sudo rm"},
		"sudo attached arg":      {"sudo -uroot rm x", "sudo rm"},
		"sudo long arg":          {"sudo --user root --preserve-env=PATH rm x", "sudo rm"},
		"sudo long arg =":        {"sudo --user=root rm x", "sudo rm"},
		"sudo assigns":           {"sudo A=1 rm x", "sudo rm"},
		"sudo runs nothing":      {"sudo -l; sudo -e /etc/hosts; sudo --list", "sudo; sudo; sudo"},
		"sudo alone":             {"sudo -i", "sudo"},
		"path to wrapper":        {"/usr/bin/sudo rm x", "/usr/bin/sudo rm"},
		"nested":                 {"sudo -E env A=1 nice -n 5 go test", "sudo env nice go test"},
		"env":                    {"env -u HOME -i A=1 - B=2 go run .", "env go run"},
		"env alone":              {"env A=1", "env"},
		"nice old style":         {"nice -10 make", "nice make"},
		"timeout":                {"timeout -k 5 30s go test ./...", "timeout go test"},
		"timeout no command":     {"timeout 30s", "timeout"},
		"xargs":                  {"find . -name '*.go' | xargs -0 -n1 -I {} gofmt -l {}", "find | xargs gofmt"},
		"xargs attached":         {"xargs -I{} -P4 cp {} /tmp", "xargs cp"},
		"xargs alone":            {"ls | xargs", "ls | xargs"},
		"nohup background":       {"nohup make run > log 2>&1 &", "nohup make run &"},
		"command":                {"command -p ls; command -v ls", "command ls; command"},
		"exec":                   {"exec -a name go run .; exec 3>&-", "exec go run; exec"},
		"stdbuf":                 {"stdbuf -oL make | tee log", "stdbuf make | tee"},
		"time binary":            {"/usr/bin/time -f %e go build", "/usr/bin/time go build"},
		"watchexec":              {"watchexec -e go -w . -- go test ./...", "watchexec go test"},
		"watchexec script":       {"watchexec -r -- 'go build && ./app'", "watchexec 'go build && ./app'"},
		"watchexec no --":        {"watchexec -e go make", "watchexec"},
		"watch":                  {"watch -n 2 'kubectl get pods'", "watch 'kubectl get'"},
		"watch words":            {"watch -n2 ls -la", "watch ls"},
		"ssh":                    {"ssh -p 22 -i key host.example 'cd app && make'", "ssh 'cd && make'"},
		"ssh login":              {"ssh host.example", "ssh"},
		"bash -c":                {"bash -c 'cd x && make'", "bash -c 'cd && make'"},
		"bash -c one":            {"sh -c \"go test ./...\"", "sh -c 'go test'"},
		"bash -lc":               {"bash -lc 'make'", "bash -c make"},
		"bash options":           {"bash -o pipefail -e -c 'a | b' arg0 x", "bash -c 'a | b'"},
		"bash file":              {"bash ./run.sh -c x", "bash"},
		"bash stdin":             {"curl -s https://get.example | bash -s", "curl | bash"},
		"bash -c not literal":    {"bash -c \"$script\"", "bash -c …"},
		"bash -c bad":            {"bash -c 'echo \"x'", "bash -c …"},
		"bash -c empty":          {"bash -c ''", "bash -c ''"},
		"bash -c nested":         {"bash -c 'sudo sh -c \"rm -rf x; ls\"'", `bash -c "sudo sh -c 'rm; ls'"`},
		"zsh":                    {"zsh -c 'for f in *; do echo $f; done'", "zsh -c 'for f in …; do echo; done'"},
		"eval":                   {"eval \"cd x && make\"", "eval 'cd && make'"},
		"eval words":             {"eval cd x '&&' make", "eval 'cd && make'"},
		"eval not literal":       {"eval \"$(ssh-agent -s)\"", "eval …"},
		"find -exec":             {"find . -name '*.tmp' -exec rm -f {} \\;", "find -exec rm"},
		"find -exec +":           {"find . -type f -execdir chmod 644 {} + -print", "find -execdir chmod"},
		"find two execs":         {"find . -exec grep -l x {} \\; -ok rm {} ';'", "find -exec grep -ok rm"},
		"find -exec sh":          {"find . -exec sh -c 'a && b' _ {} \\;", "find -exec sh -c 'a && b'"},
		"find no exec":           {"find . -name x -delete", "find"},
		"not literal":            {"\"$wrap\" rm x; sudo \"$cmd\" x", "\"$wrap\"; sudo \"$cmd\""},
		"too deep":               {"sudo sudo sudo sudo sudo sudo sudo sudo sudo sudo rm", "sudo sudo sudo sudo sudo sudo sudo sudo sudo"},
		"no subcommand":          {"git --version; go; make -j8", "git; go; make"},
		"git options":            {"cd repo && git add x && git -c commit.gpgsign=false commit -q -F - <<'EOF'\nmsg\nEOF\ngit log -1 --stat", "cd && git add && git commit; git log"},
		"git long options":       {"git --no-pager --git-dir .git --work-tree=. log", "git log"},
		"subcommand not literal": {"git \"$sub\" x; npm $cmd", "git; npm"},
		"cargo toolchain":        {"cargo +nightly -Z unstable-options build", "cargo build"},
		"make assigns":           {"make -C sub CC=clang -j 4 test", "make test"},
		"kubectl namespace":      {"kubectl -n kube-system get pods", "kubectl get"},
		"sudo apt":               {"sudo apt-get -y install curl", "sudo apt-get install"},
		"not a wrapper":          {"git -C dir commit -m x", "git commit"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Outline(tt.src)
			if err != nil {
				t.Fatalf("Outline(%q) error: %v", tt.src, err)
			}
			if got != tt.want {
				t.Errorf("Outline(%q) = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestOutline_Error checks that source bash would not run has no outline.
func TestOutline_Error(t *testing.T) {
	got, err := Outline("ls && echo 'x")
	if _, ok := errors.AsType[*SyntaxError](err); !ok || got != "" {
		t.Errorf("Outline = %q, %v; want \"\" and a *SyntaxError", got, err)
	}
}
