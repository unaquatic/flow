package shell

import (
	"path"
	"slices"
	"strings"
)

// MaxWrapDepth is how many wrappers deep an outline follows the commands
// that commands run, as in "sudo env nice go".
const MaxWrapDepth = 8

// Wrapper describes a command that runs a command given in its arguments,
// after its options, as sudo and xargs do, so that an outline can show the
// command it runs. Options it does not name take no argument.
type Wrapper struct {
	// ArgOpts are the short options that take an argument, in the same word
	// or the next, such as "u" for sudo -u user.
	ArgOpts string
	// LongArgOpts are the long options that take the next word as their
	// argument, unless written with "=", such as "--user".
	LongArgOpts []string
	// NoRunOpts are the short options with which the wrapper runs no
	// command, such as "l" for sudo -l.
	NoRunOpts string
	// LongNoRunOpts are the long options with which the wrapper runs no
	// command, such as "--list".
	LongNoRunOpts []string
	// Operands is how many words come between the options and the command,
	// such as timeout's duration or ssh's host.
	Operands int
	// Assigns reports whether NAME=value words, and a lone "-", come
	// before the command, among the options or after them, as for env.
	Assigns bool
	// AfterDashDash reports whether the command is told only by a "--"
	// before it, as for watchexec, whose options are too many to list.
	AfterDashDash bool
	// Joined reports whether the wrapper joins the command's words and runs
	// them in a shell, so that one word may hold a whole script, as for
	// watch and ssh.
	Joined bool
}

// Wrappers are the wrappers an outline follows, by the base name of the
// command.
var Wrappers = map[string]Wrapper{
	"sudo": {
		ArgOpts:       "CDgpRrTtUu",
		LongArgOpts:   []string{"--chdir", "--chroot", "--close-from", "--command-timeout", "--group", "--host", "--other-user", "--prompt", "--role", "--type", "--user"},
		NoRunOpts:     "eKlVv",
		LongNoRunOpts: []string{"--edit", "--help", "--list", "--remove-timestamp", "--validate", "--version"},
		Assigns:       true,
	},
	"doas":       {ArgOpts: "Cu"},
	"env":        {ArgOpts: "CSu", LongArgOpts: []string{"--chdir", "--split-string", "--unset"}, Assigns: true},
	"nice":       {ArgOpts: "n", LongArgOpts: []string{"--adjustment"}},
	"nohup":      {},
	"time":       {ArgOpts: "fo", LongArgOpts: []string{"--format", "--output"}},
	"timeout":    {ArgOpts: "ks", LongArgOpts: []string{"--kill-after", "--signal"}, Operands: 1},
	"xargs":      {ArgOpts: "adEILnPs", LongArgOpts: []string{"--arg-file", "--delimiter", "--max-args", "--max-chars", "--max-lines", "--max-procs", "--process-slot-var"}},
	"exec":       {ArgOpts: "a"},
	"command":    {NoRunOpts: "vV"},
	"builtin":    {},
	"stdbuf":     {ArgOpts: "eio", LongArgOpts: []string{"--error", "--input", "--output"}},
	"caffeinate": {ArgOpts: "tw"},
	"ionice":     {ArgOpts: "cn", NoRunOpts: "pPu"},
	"taskset":    {NoRunOpts: "p", Operands: 1},
	"chronic":    {},
	"unbuffer":   {},
	"watch":      {ArgOpts: "nq", LongArgOpts: []string{"--equexit", "--interval"}, Joined: true},
	"watchexec":  {AfterDashDash: true, Joined: true},
	"ssh":        {ArgOpts: "BbcDEeFIiJLlmOoPpQRSWw", Operands: 1, Joined: true},
}

// Subcommands are the commands whose first operand, after their global
// options, names what they do, such as git's "commit" in
// "git -c x=y commit -m msg", so that an outline shows it after the name.
// Their options are told as a Wrapper's are; Operands is unused.
var Subcommands = map[string]Wrapper{
	"git": {
		ArgOpts:     "Cc",
		LongArgOpts: []string{"--config-env", "--git-dir", "--namespace", "--super-prefix", "--work-tree"},
	},
	"go":        {},
	"cargo":     {ArgOpts: "CZ", LongArgOpts: []string{"--color", "--config"}},
	"rustup":    {},
	"npm":       {ArgOpts: "C", LongArgOpts: []string{"--prefix", "--workspace"}},
	"pnpm":      {ArgOpts: "Cw", LongArgOpts: []string{"--dir", "--filter"}},
	"yarn":      {LongArgOpts: []string{"--cwd"}},
	"bun":       {LongArgOpts: []string{"--cwd"}},
	"deno":      {},
	"npx":       {ArgOpts: "p", LongArgOpts: []string{"--package"}},
	"pip":       {},
	"pip3":      {},
	"uv":        {LongArgOpts: []string{"--directory", "--project"}},
	"poetry":    {ArgOpts: "C", LongArgOpts: []string{"--directory"}},
	"docker":    {ArgOpts: "cHl", LongArgOpts: []string{"--config", "--context", "--host", "--log-level"}},
	"podman":    {LongArgOpts: []string{"--connection", "--root", "--url"}},
	"kubectl":   {ArgOpts: "ns", LongArgOpts: []string{"--cluster", "--context", "--kubeconfig", "--namespace", "--server", "--user"}},
	"helm":      {ArgOpts: "n", LongArgOpts: []string{"--kube-context", "--kubeconfig", "--namespace"}},
	"terraform": {},
	"gh":        {ArgOpts: "R", LongArgOpts: []string{"--repo"}},
	"brew":      {},
	"apt":       {ArgOpts: "ot", LongArgOpts: []string{"--option", "--target-release"}},
	"apt-get":   {ArgOpts: "ot", LongArgOpts: []string{"--option", "--target-release"}},
	"dnf":       {},
	"yum":       {},
	"systemctl": {ArgOpts: "HMpst", LongArgOpts: []string{"--host", "--machine", "--property", "--signal", "--type"}},
	"make":      {ArgOpts: "CfIjlOoW", LongArgOpts: []string{"--directory", "--file", "--include-dir", "--jobs", "--load-average", "--makefile", "--old-file", "--what-if"}, Assigns: true},
	"swift":     {},
	"dotnet":    {},
}

// Shells are the shells whose -c script an outline follows.
var Shells = []string{"ash", "bash", "dash", "ksh", "mksh", "sh", "zsh"}

// Wrapped returns how the command words show in an outline depth wrappers
// deep: its name, as written, and, for a wrapper whose name is literal,
// the outline of the command it runs after it.
func (o *OutlineBuilder) Wrapped(words []Word, depth int) string {
	name := words[0]
	if !name.Literal || depth >= MaxWrapDepth {
		return name.Raw
	}
	args := words[1:]
	switch base := path.Base(name.Value); {
	case base == "eval":
		src, ok := JoinLiteral(args)
		switch {
		case !ok:
			return name.Raw + " " + Ellipsis
		case strings.TrimSpace(src) == "":
			return name.Raw
		}
		return name.Raw + " " + o.Script(src, depth)
	case slices.Contains(Shells, base):
		script, found := ShellScript(args)
		switch {
		case !found:
			return name.Raw
		case !script.Literal:
			return name.Raw + " -c " + Ellipsis
		}
		return name.Raw + " -c " + o.Script(script.Value, depth)
	case base == "find":
		return name.Raw + o.FindExecs(args, depth)
	default:
		w, ok := Wrappers[base]
		if !ok {
			return name.Raw + Subcommand(base, args)
		}
		cmd, ok := w.Command(args)
		switch {
		case !ok:
			return name.Raw
		case w.Joined && len(cmd) == 1 && cmd[0].Literal:
			return name.Raw + " " + o.Script(cmd[0].Value, depth)
		}
		return name.Raw + " " + o.Wrapped(cmd, depth+1)
	}
}

// Subcommand returns the subcommand of the command named base, one of
// Subcommands, with args, its arguments, as written after a space, as
// " commit", or "" when it names none or none is given, or the subcommand
// is not literal. A cargo toolchain, as "+nightly", is skipped.
func Subcommand(base string, args []Word) string {
	spec, ok := Subcommands[base]
	if !ok {
		return ""
	}
	if base == "cargo" && len(args) > 0 && strings.HasPrefix(args[0].Value, "+") {
		args = args[1:] // the toolchain comes first
	}
	rest, ok := spec.Command(args)
	if !ok || !rest[0].Literal || strings.HasPrefix(rest[0].Value, "-") || isAssignment(rest[0].Raw) {
		return ""
	}
	return " " + rest[0].Raw
}

// Command returns the command that the wrapper w runs with args, its
// arguments, as the words from its name on, and reports false when it runs
// none.
func (w Wrapper) Command(args []Word) ([]Word, bool) {
	if w.AfterDashDash {
		i := slices.IndexFunc(args, func(a Word) bool { return a.Value == "--" })
		if i < 0 || i+1 == len(args) {
			return nil, false
		}
		return args[i+1:], true
	}
	i := 0
options:
	for i < len(args) {
		v := args[i].Value
		switch {
		case w.Assigns && (v == "-" || isAssignment(args[i].Raw)):
			i++ // as make takes them, among its options
			continue
		case !args[i].Literal || len(v) < 2 || v[0] != '-':
			break options
		case v == "--":
			i++
			break options
		case strings.HasPrefix(v, "--"):
			i++
			opt, _, hasArg := strings.Cut(v, "=")
			if slices.Contains(w.LongNoRunOpts, opt) {
				return nil, false
			}
			if !hasArg && slices.Contains(w.LongArgOpts, opt) {
				i++
			}
			continue
		}
		i++
		for j := 1; j < len(v); j++ {
			if strings.IndexByte(w.NoRunOpts, v[j]) >= 0 {
				return nil, false
			}
			if strings.IndexByte(w.ArgOpts, v[j]) >= 0 {
				if j == len(v)-1 {
					i++ // the argument is the next word
				}
				break
			}
		}
	}
	for w.Assigns && i < len(args) && (args[i].Value == "-" || isAssignment(args[i].Raw)) {
		i++
	}
	if i += w.Operands; i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

// ShellScript returns the script that a shell run with args, its
// arguments, reads from its -c option, and reports false when it reads no
// -c script, but a script file or its input.
func ShellScript(args []Word) (Word, bool) {
	c := false
	for i := 0; i < len(args); i++ {
		v := args[i].Value
		switch {
		case !args[i].Literal:
		case v == "--":
			if c && i+1 < len(args) {
				return args[i+1], true
			}
			return Word{}, false
		case v == "--rcfile" || v == "--init-file":
			i++
			continue
		case strings.HasPrefix(v, "--"):
			continue
		case len(v) >= 2 && (v[0] == '-' || v[0] == '+'):
			c = c || v[0] == '-' && strings.IndexByte(v, 'c') > 0
			if strings.ContainsAny(v[1:], "oO") {
				i++ // the option's name is the next word
			}
			continue
		}
		return args[i], c
	}
	return Word{}, false
}

// JoinLiteral returns the values of words a space apart, as eval joins its
// arguments, and reports false when one of them is not literal.
func JoinLiteral(words []Word) (string, bool) {
	values := make([]string, len(words))
	for i, w := range words {
		if !w.Literal {
			return "", false
		}
		values[i] = w.Value
	}
	return strings.Join(values, " "), true
}

// FindExecs returns the commands that find, with args, its arguments, runs
// on the files it finds, each after its action as " -exec rm", depth
// wrappers deep, or "" when it runs none.
func (o *OutlineBuilder) FindExecs(args []Word, depth int) string {
	var b strings.Builder
	for i := 0; i < len(args); i++ {
		action := args[i].Value
		switch action {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		end := i + 1
		for end < len(args) && args[end].Value != ";" && (args[end].Value != "+" || args[end-1].Value != "{}") {
			end++
		}
		if end > i+1 {
			b.WriteString(" " + action + " " + o.Wrapped(args[i+1:end], depth+1))
		}
		i = end
	}
	return b.String()
}
