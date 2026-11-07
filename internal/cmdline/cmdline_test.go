package cmdline

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// ExclusiveOptions are the flags of the command TestExclusive runs.
type ExclusiveOptions struct {
	Alpha bool
	Beta  bool
	Name  string
	Other bool
}

// exclusiveCommand returns a command with an aliased flag -alpha and -a, a
// flag -beta, a string flag -name, and a flag -other, whose flags in groups
// exclude each other. Run records in ran that it was called.
func exclusiveCommand(groups [][]string, ran *bool) *Command[ExclusiveOptions] {
	return &Command[ExclusiveOptions]{
		Name: "app",
		Flags: func(fs *flag.FlagSet, o *ExclusiveOptions) {
			fs.BoolVar(&o.Alpha, "alpha", false, "Choose alpha")
			fs.BoolVar(&o.Alpha, "a", false, "Choose alpha")
			fs.BoolVar(&o.Beta, "beta", false, "Choose beta")
			fs.StringVar(&o.Name, "name", "", "Choose a `name`")
			fs.BoolVar(&o.Other, "other", false, "Belong to no group")
		},
		Exclusive: groups,
		Run: func(context.Context, ExclusiveOptions, []string) error {
			*ran = true
			return nil
		},
	}
}

func TestExclusive(t *testing.T) {
	group := [][]string{{"alpha", "beta", "name"}}
	tests := []struct {
		name    string
		groups  [][]string
		args    []string
		wantErr string
		usage   bool // whether the error is a UsageError
	}{
		{name: "no flags", groups: group},
		{name: "one flag", groups: group, args: []string{"-beta"}},
		{name: "one flag by alias", groups: group, args: []string{"-a"}},
		{name: "both aliases", groups: group, args: []string{"-alpha", "-a"}},
		{name: "flag outside the group", groups: group, args: []string{"-a", "-other"}},
		{name: "set to its default", groups: group, args: []string{"-a=false", "-beta"}},
		{name: "empty string", groups: group, args: []string{"-name=", "-beta"}},
		{name: "two flags", groups: group, args: []string{"-alpha", "-beta"},
			wantErr: "app: use only one of --alpha, --beta and --name", usage: true},
		{name: "alias and string", groups: group, args: []string{"-a", "-name", "x"},
			wantErr: "app: use only one of --alpha, --beta and --name", usage: true},
		{name: "aliases in one group", groups: [][]string{{"a", "alpha", "beta"}}, args: []string{"-a"}},
		{name: "second group", groups: [][]string{{"alpha", "name"}, {"beta", "other"}},
			args: []string{"-alpha", "-beta", "-other"}, wantErr: "app: use only one of --beta and --other", usage: true},
		{name: "undefined flag", groups: [][]string{{"alpha", "missing"}},
			wantErr: "app: exclusive flag -missing is not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran bool
			err := exclusiveCommand(tt.groups, &ran).Execute(t.Context(), tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("execute %q: err = %v, want %q", tt.args, err, tt.wantErr)
				}
				if _, usage := errors.AsType[*UsageError](err); usage != tt.usage {
					t.Errorf("execute %q: usage error = %v, want %v", tt.args, usage, tt.usage)
				}
				if ran {
					t.Errorf("execute %q ran the command despite the error", tt.args)
				}
				return
			}
			if err != nil || !ran {
				t.Errorf("execute %q: ran = %v, err = %v; want a run", tt.args, ran, err)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	failed := errors.New("failed")
	leaf := &Command[NoOptions]{
		Name: "leaf",
		Run:  func(context.Context, NoOptions, []string) error { return nil },
	}
	pair := &Command[NoOptions]{
		Name: "pair",
		Args: ExactArgs(2),
		Run:  func(context.Context, NoOptions, []string) error { return nil },
	}
	fails := &Command[NoOptions]{
		Name: "fails",
		Run:  func(context.Context, NoOptions, []string) error { return failed },
	}
	root := &Command[NoOptions]{
		Name:     "app",
		Run:      func(context.Context, NoOptions, []string) error { return nil },
		Commands: []Node{leaf, pair, fails},
	}

	const rootUsage = "Usage:\n  app [flags]\n  app <command> [flags]\n\nCommands:\n"
	tests := []struct {
		name    string
		args    []string
		wantErr string // "" runs the command
		usage   string // how the usage section of a UsageError starts
	}{
		{name: "root", args: nil},
		{name: "leaf", args: []string{"leaf"}},
		{name: "two arguments", args: []string{"pair", "a", "b"}},
		{name: "unknown flag", args: []string{"-x"},
			wantErr: "app: flag provided but not defined: -x", usage: rootUsage},
		{name: "unknown command", args: []string{"nope"},
			wantErr: `app: unknown command "nope"`, usage: rootUsage},
		{name: "argument to a command without Args", args: []string{"leaf", "extra"},
			wantErr: "app leaf: takes no arguments, got 1", usage: "Usage:\n  app leaf [flags]\n"},
		{name: "wrong argument count", args: []string{"pair", "a"},
			wantErr: "app pair: expects 2 arguments, got 1", usage: "Usage:\n  app pair [flags]\n"},
		{name: "run error", args: []string{"fails"}, wantErr: "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := root.Execute(t.Context(), tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("execute %q: %v", tt.args, err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("execute %q: err = %v, want %q", tt.args, err, tt.wantErr)
			}
			ue, usage := errors.AsType[*UsageError](err)
			if usage != (tt.usage != "") {
				t.Fatalf("execute %q: usage error = %v, want %v", tt.args, usage, tt.usage != "")
			}
			if !usage {
				return
			}
			if !strings.HasPrefix(tt.wantErr, ue.Scope+": ") {
				t.Errorf("execute %q: scope = %q, want the prefix of %q", tt.args, ue.Scope, tt.wantErr)
			}
			if !strings.HasPrefix(ue.Usage, tt.usage) {
				t.Errorf("execute %q: usage section = %q, want it to start with %q", tt.args, ue.Usage, tt.usage)
			}
		})
	}
}

func TestReport(t *testing.T) {
	usage := &UsageError{Scope: "app sub", Err: errors.New("bad flag")}
	tests := []struct {
		name   string
		err    error
		color  bool
		want   string
		status int
	}{
		{name: "no error"},
		{name: "plain", err: errors.New("failed"), want: "error: failed\n", status: 1},
		{name: "usage", err: usage, want: "error: app sub: bad flag\n", status: 2},
		{name: "wrapped usage", err: fmt.Errorf("outer: %w", usage),
			want: "error: outer: app sub: bad flag\n", status: 2},
		{name: "plain in color", err: errors.New("failed"), color: true,
			want: "\x1b[31merror:\x1b[39m failed\n", status: 1},
		{name: "usage in color", err: usage, color: true,
			want: "\x1b[31merror:\x1b[39m app sub: bad flag\n", status: 2},
		{name: "usage section",
			err:  &UsageError{Scope: "app sub", Err: errors.New("bad flag"), Usage: "Usage:\n  app sub [flags]\n"},
			want: "error: app sub: bad flag\n\nUsage:\n  app sub [flags]\n", status: 2},
		{name: "usage section in color", color: true,
			err: &UsageError{Scope: "app sub", Err: errors.New("bad flag"),
				Usage: "Usage:\n  app sub [flags]\n", boldUsage: "\x1b[1mUsage:\x1b[22m\n  app sub [flags]\n"},
			want: "\x1b[31merror:\x1b[39m app sub: bad flag\n\n\x1b[1mUsage:\x1b[22m\n  app sub [flags]\n", status: 2},
		{name: "usage section in color without bold", color: true,
			err:  &UsageError{Scope: "app sub", Err: errors.New("bad flag"), Usage: "Usage:\n  app sub [flags]\n"},
			want: "\x1b[31merror:\x1b[39m app sub: bad flag\n\nUsage:\n  app sub [flags]\n", status: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			status := Report(&out, tt.err, tt.color)
			if out.String() != tt.want || status != tt.status {
				t.Errorf("report = %q, status %d; want %q, status %d", out.String(), status, tt.want, tt.status)
			}
		})
	}
}

// HelpOptions are the flags of the command that helpOf prints.
type HelpOptions struct {
	Verbose bool
	Name    string
	Count   int
}

// helpOf returns the help of a command with a subcommand, a boolean flag with
// an alias, a string flag, an integer flag with a default, and environment
// variables, in style st.
func helpOf(t *testing.T, st style) string {
	t.Helper()
	c := &Command[HelpOptions]{
		Name:  "app",
		Short: "Not shown in its own help",
		Env:   []EnvVar{{Name: "APP_KEY", Usage: "Key to use"}, {Name: "APP_HOME_DIR", Usage: "Data directory"}},
		Flags: func(fs *flag.FlagSet, o *HelpOptions) {
			fs.BoolVar(&o.Verbose, "verbose", false, "Report more")
			fs.BoolVar(&o.Verbose, "v", false, "Report more")
			fs.StringVar(&o.Name, "name", "", "`name` to greet")
			fs.IntVar(&o.Count, "count", 3, "greet `n` times")
		},
		Run:      func(context.Context, HelpOptions, []string) error { return nil },
		Commands: []Node{&Command[NoOptions]{Name: "sub", Short: "Do one thing"}},
	}
	var o HelpOptions
	sc := rootScope(c.info())
	fs, err := c.parse(sc, &o, nil)
	if err != nil {
		t.Fatal(err)
	}
	var help strings.Builder
	writeHelp(&help, sc, c.info(), fs, st)
	return help.String()
}

// TestHelp checks that help lists the usage lines, subcommands, flags, and
// environment variables, in that order, without the command's own Short.
func TestHelp(t *testing.T) {
	const want = `Usage:
  app [flags]
  app <command> [flags]

Commands:
  sub  Do one thing

Flags:
      --count <n>    greet n times [default: 3]
      --name <name>  name to greet
  -v, --verbose      Report more
  -h, --help         Show help

Environment:
  APP_KEY       Key to use
  APP_HOME_DIR  Data directory
`
	if got := helpOf(t, style{}); got != want {
		t.Errorf("help =\n%s\nwant\n%s", got, want)
	}
}

// TestHelpBold checks that bold help shows only its headings in bold, and
// otherwise matches plain help.
func TestHelpBold(t *testing.T) {
	plain, bold := helpOf(t, style{}), helpOf(t, style{bold: true})
	want := plain
	for _, heading := range []string{"Usage:", "Commands:", "Flags:", "Environment:"} {
		want = strings.Replace(want, heading+"\n", "\x1b[1m"+heading+"\x1b[22m\n", 1)
	}
	if bold != want {
		t.Errorf("bold help =\n%q\nwant\n%q", bold, want)
	}
}

// TestHelpBanner checks that help starts with the root's banner, which a
// subcommand inherits unless it has its own. Bold help shows the color banner
// where the command whose banner it is has one.
func TestHelpBanner(t *testing.T) {
	leaf := &Command[NoOptions]{Name: "leaf"}
	own := &Command[NoOptions]{Name: "own", Banner: "own banner"}
	root := &Command[NoOptions]{Name: "app", Banner: "app banner", ColorBanner: "color app banner", Commands: []Node{leaf, own}}
	plain := &Command[NoOptions]{Name: "plain"}

	rootSc := rootScope(root.info())
	tests := []struct {
		name string
		sc   scope
		in   info
		bold bool
		want string
	}{
		{name: "root", sc: rootSc, in: root.info(), want: "app banner\n\nUsage:\n  app <command> [flags]\n"},
		{name: "inherited", sc: rootSc.child(leaf.info()), in: leaf.info(), want: "app banner\n\nUsage:\n  app leaf [flags]\n"},
		{name: "own", sc: rootSc.child(own.info()), in: own.info(), want: "own banner\n\nUsage:\n  app own [flags]\n"},
		{name: "none", sc: rootScope(plain.info()), in: plain.info(), want: "Usage:\n  plain [flags]\n"},
		{name: "root in color", sc: rootSc, in: root.info(), bold: true, want: "color app banner\n\n"},
		{name: "inherited in color", sc: rootSc.child(leaf.info()), in: leaf.info(), bold: true, want: "color app banner\n\n"},
		{name: "own without color", sc: rootSc.child(own.info()), in: own.info(), bold: true, want: "own banner\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var help strings.Builder
			writeHelp(&help, tt.sc, tt.in, flag.NewFlagSet(tt.sc.String(), flag.ContinueOnError), style{bold: tt.bold})
			if !strings.HasPrefix(help.String(), tt.want) {
				t.Errorf("help =\n%s\nwant it to start with\n%s", help.String(), tt.want)
			}
		})
	}
}

func TestUseColor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if UseColor(w) {
		t.Error("a pipe shows colors")
	}
}

// TestHelpFlagsLeftToCommand checks that the help row leaves out a help flag
// name that the command defines for itself.
func TestHelpFlagsLeftToCommand(t *testing.T) {
	var host string
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.StringVar(&host, "h", "", "host to dial")
	var got []string
	for _, g := range helpFlags(fs) {
		got = append(got, g.String())
	}
	if want := []string{"--help"}; !slices.Equal(got, want) {
		t.Errorf("help flags = %q, want %q", got, want)
	}
}

// TestFlagGroupsByVariable checks that help pairs the names bound to one
// variable, whatever their usage text, and only those.
func TestFlagGroupsByVariable(t *testing.T) {
	var a, b, c string
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.StringVar(&a, "alpha", "", "Same text")
	fs.StringVar(&a, "a", "", "Other text")
	fs.StringVar(&b, "beta", "", "Same text")
	fs.StringVar(&c, "gamma", "", "Same text")
	fs.Func("func", "Same text", func(string) error { return nil })
	fs.Func("f", "Same text", func(string) error { return nil })

	var got []string
	for _, g := range flagGroups(fs) {
		got = append(got, g.String())
	}
	want := []string{"-a, --alpha <string>", "--beta <string>", "-f <value>", "--func <value>", "--gamma <string>"}
	if !slices.Equal(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
}

func TestFlagList(t *testing.T) {
	tests := []struct {
		names []string
		want  string
	}{
		{names: []string{"a"}, want: "-a"},
		{names: []string{"a", "b"}, want: "-a and -b"},
		{names: []string{"a", "b", "c"}, want: "-a, -b and -c"},
		{names: []string{"alpha", "b", "gamma"}, want: "--alpha, -b and --gamma"},
	}
	for _, tt := range tests {
		if got := flagList(tt.names); got != tt.want {
			t.Errorf("flagList(%q) = %q, want %q", tt.names, got, tt.want)
		}
	}
}
