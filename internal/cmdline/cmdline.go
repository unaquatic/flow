// Package cmdline builds command-line programs from a tree of commands on
// top of the standard flag package.
//
// Each command names an options type holding its flag values. The tree is
// declared as values and run by [Main]:
//
//	type ServeOptions struct{ Port int }
//
//	var serveCmd = &cmdline.Command[ServeOptions]{
//		Name:  "serve",
//		Short: "Run the server",
//		Flags: func(fs *flag.FlagSet, o *ServeOptions) {
//			fs.IntVar(&o.Port, "port", 4096, "port to listen on")
//		},
//		Run: func(ctx context.Context, o ServeOptions, args []string) error {
//			return listen(ctx, o.Port)
//		},
//	}
//
// The first argument selects a subcommand; everything after it is parsed as
// that subcommand's flags and arguments.
package cmdline

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"
)

// NoOptions is the options type of a command that takes no flags.
type NoOptions = struct{}

// An EnvVar is an environment variable that a command reads.
type EnvVar struct {
	// Name is the variable's name, such as "PROVIDER_A_API_KEY".
	Name string
	// Usage describes what the variable sets.
	Usage string
}

// A Node is a command in the tree. Only [Command] implements it.
type Node interface {
	info() info
	exec(ctx context.Context, sc scope, args []string) error
}

// A Command is a single command, optionally holding subcommands of its own.
// T is the type holding the command's flag values.
type Command[T any] struct {
	// Name is the word that selects the command on the command line.
	Name string

	// Aliases are additional names that select the command.
	Aliases []string

	// Usage describes the positional arguments, such as "<hash>".
	Usage string

	// Short is the one-line description shown in command listings.
	Short string

	// Banner is the text that help shows first, above the usage. It may
	// span several lines. A subcommand without a Banner shows its parent's.
	Banner string

	// ColorBanner is Banner as help shows it on terminals that show colors,
	// marked up with escape sequences. Help shows Banner when it is empty, or
	// where colors are not shown.
	ColorBanner string

	// Env lists the environment variables the command reads, which help
	// shows after its flags.
	Env []EnvVar

	// Flags binds the command's flags to the options passed to Run.
	// Subcommands do not inherit them. Binding one field under two names
	// with the same kind of flag, such as two StringVar calls, defines an
	// alias, and help reports the names together.
	Flags func(*flag.FlagSet, *T)

	// Exclusive lists groups of flags of which at most one may be given; the
	// command fails before Run when more are. A flag counts as given when it
	// is set to a value other than its default, so -x=false does not count.
	// Naming one name of an aliased flag covers all of its names.
	Exclusive [][]string

	// Args checks the positional arguments left after flag parsing. A
	// command without Args takes none, and one with subcommands reports
	// its first argument as an unknown command instead. A command with
	// both takes the arguments that name no subcommand as its own.
	Args func([]string) error

	// Run executes the command. A command without Run prints its help.
	Run func(ctx context.Context, opts T, args []string) error

	// Commands are the subcommands, matched against the first argument.
	Commands []Node

	// Hidden leaves the command out of its parent's help, which still runs
	// it. Its own help shows as any other's.
	Hidden bool
}

// A UsageError reports that a command was invoked wrongly: with flags,
// arguments, or a subcommand it does not accept.
type UsageError struct {
	// Scope names the command, such as "flow login".
	Scope string
	// Err is what was wrong.
	Err error
	// Usage is the command's usage section, as help prints it: its usage
	// lines, subcommands, and flags.
	Usage string

	// boldUsage is Usage in bold where help shows bold, for terminals.
	boldUsage string
}

func (e *UsageError) Error() string { return e.Scope + ": " + e.Err.Error() }

func (e *UsageError) Unwrap() error { return e.Err }

// Main executes root with the command-line arguments, reports a failure on
// standard error as Report does, and exits with the status Report returns.
func Main(ctx context.Context, root Node) {
	err := root.exec(ctx, rootScope(root.info()), os.Args[1:])
	if status := Report(os.Stderr, err, UseColor(os.Stderr)); status != 0 {
		os.Exit(status)
	}
}

// An ExitError ends a command with exit status Code, once the command has
// written all it has to say, such as a check that reports what it found:
// Report writes nothing for it.
type ExitError struct {
	// Code is the exit status, which is not 0.
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Report writes err to w after an "error:" label and returns the exit status
// for it: 2 for a [UsageError], which it follows with the command's usage
// section, the code of an [ExitError], which it does not write, 1 for any
// other error, and 0 for nil, which it does not write. When color is true,
// the label is red and the usage section is bold where help is.
func Report(w io.Writer, err error, color bool) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*ExitError](err); ok {
		return ee.Code
	}
	label := "error:"
	if color {
		label = "\x1b[31m" + label + "\x1b[39m"
	}
	fmt.Fprintln(w, label, err)
	if ue, ok := errors.AsType[*UsageError](err); ok {
		usage := ue.Usage
		if color && ue.boldUsage != "" {
			usage = ue.boldUsage
		}
		if usage != "" {
			fmt.Fprint(w, "\n", usage)
		}
		return 2
	}
	return 1
}

// UseColor reports whether f shows colors: it is a terminal, NO_COLOR is
// empty, and TERM is not dumb.
func UseColor(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// Execute parses args, selecting and running a command below c.
func (c *Command[T]) Execute(ctx context.Context, args []string) error {
	return c.exec(ctx, rootScope(c.info()), args)
}

// A scope locates the running command: the path of command names leading to
// it, and the banner its help shows, plain and in color.
type scope struct {
	path        []string
	banner      string
	colorBanner string
}

// rootScope returns the scope of in as the root of its tree.
func rootScope(in info) scope {
	return scope{path: []string{in.name}, banner: in.banner, colorBanner: in.colorBanner}
}

// child returns the scope of in as a subcommand below s. It keeps the banners
// of s unless in has its own.
func (s scope) child(in info) scope {
	banner, colorBanner := s.banner, s.colorBanner
	if in.banner != "" {
		banner, colorBanner = in.banner, in.colorBanner
	}
	return scope{path: append(slices.Clip(s.path), in.name), banner: banner, colorBanner: colorBanner}
}

func (s scope) String() string { return strings.Join(s.path, " ") }

func (c *Command[T]) exec(ctx context.Context, sc scope, args []string) error {
	if len(args) > 0 {
		if sub := lookup(c.Commands, args[0]); sub != nil {
			return sub.exec(ctx, sc.child(sub.info()), args[1:])
		}
	}

	var opts T
	fs, args, err := c.parse(sc, &opts, args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		printHelp(sc, c.info(), fs)
		return nil
	case err != nil:
		return c.usageError(sc, fs, err)
	}
	return c.run(ctx, sc, opts, fs, args)
}

// usageError returns err as a UsageError of c, which sc names, with the usage
// section of c and its flags fs.
func (c *Command[T]) usageError(sc scope, fs *flag.FlagSet, err error) *UsageError {
	var plain, bold strings.Builder
	writeUsageSection(&plain, sc, c.info(), fs, style{})
	writeUsageSection(&bold, sc, c.info(), fs, style{bold: true})
	return &UsageError{Scope: sc.String(), Err: err, Usage: plain.String(), boldUsage: bold.String()}
}

// lookup returns the command selected by name, or nil if there is none.
func lookup(nodes []Node, name string) Node {
	if strings.HasPrefix(name, "-") {
		return nil
	}
	for _, n := range nodes {
		in := n.info()
		if in.name == name || slices.Contains(in.aliases, name) {
			return n
		}
	}
	return nil
}

// parse builds the flag set for c, binding it to opts, parses args into it,
// as parseInterspersed does, and returns the positional arguments. The flag
// set is returned even on failure, so that help can report it.
func (c *Command[T]) parse(sc scope, opts *T, args []string) (*flag.FlagSet, []string, error) {
	fs := flag.NewFlagSet(sc.String(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if c.Flags != nil {
		c.Flags(fs, opts)
	}
	positional, err := parseInterspersed(fs, args)
	return fs, positional, err
}

// parseInterspersed parses args into fs, taking flags wherever they stand
// among the positional arguments, which it returns in order, so that
// "prompt -m model" sets -m as "-m model prompt" does. A "--" ends the
// flags: each argument after it is positional, even one that starts with a
// dash. So does a "--" given as the value of a flag, which flag cannot tell
// apart from one that ends the flags.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		// Parse stops at a positional argument, which it leaves in rest, or
		// after a "--", which it takes.
		if consumed := len(args) - len(rest); len(rest) == 0 || consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (c *Command[T]) run(ctx context.Context, sc scope, opts T, fs *flag.FlagSet, args []string) error {
	if len(args) > 0 && len(c.Commands) > 0 && c.Args == nil {
		return c.usageError(sc, fs, fmt.Errorf("unknown command %q", args[0]))
	}
	if c.Run == nil {
		printHelp(sc, c.info(), fs)
		return nil
	}
	checkArgs := c.Args
	if checkArgs == nil {
		checkArgs = ExactArgs(0)
	}
	if err := checkArgs(args); err != nil {
		return c.usageError(sc, fs, err)
	}
	if err := c.checkExclusive(sc, fs); err != nil {
		return err
	}
	return c.Run(ctx, opts, args)
}

// checkExclusive reports, as a UsageError, the first of c.Exclusive in which
// fs was given more than one flag. A group naming a flag that fs does not
// define is a mistake in c, which it reports as a plain error.
func (c *Command[T]) checkExclusive(sc scope, fs *flag.FlagSet) error {
	groups := c.Exclusive
	if len(groups) == 0 {
		return nil
	}
	// alias maps each flag name to the first name bound to the same
	// variable, as help pairs them.
	alias := make(map[string]string)
	for _, g := range flagGroups(fs) {
		for _, name := range g.names {
			alias[name] = g.names[0]
		}
	}
	given := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		if f.Value.String() != f.DefValue {
			given[alias[f.Name]] = true
		}
	})
	for _, group := range groups {
		// Aliases named in one group count once.
		set := make(map[string]bool)
		for _, name := range group {
			canonical, ok := alias[name]
			if !ok {
				return fmt.Errorf("%s: exclusive flag -%s is not defined", sc, name)
			}
			if given[canonical] {
				set[canonical] = true
			}
		}
		if len(set) > 1 {
			return c.usageError(sc, fs, fmt.Errorf("use only one of %s", flagList(group)))
		}
	}
	return nil
}

// flagList returns names as a list of flags, such as "--alpha, -b and --gamma".
func flagList(names []string) string {
	dashed := make([]string, len(names))
	for i, name := range names {
		dashed[i] = dash(name)
	}
	if len(dashed) < 2 {
		return strings.Join(dashed, "")
	}
	return strings.Join(dashed[:len(dashed)-1], ", ") + " and " + dashed[len(dashed)-1]
}

// info describes a command independently of its options type.
type info struct {
	name        string
	aliases     []string
	usage       string
	short       string
	banner      string
	colorBanner string
	env         []EnvVar
	runnable    bool
	commands    []Node
	hidden      bool
}

func (c *Command[T]) info() info {
	return info{
		name:        c.Name,
		aliases:     c.Aliases,
		usage:       c.Usage,
		short:       c.Short,
		banner:      c.Banner,
		colorBanner: c.ColorBanner,
		env:         c.Env,
		runnable:    c.Run != nil,
		commands:    c.Commands,
		hidden:      c.Hidden,
	}
}

// AnyArgs is a [Command.Args] check that takes any number of arguments.
func AnyArgs([]string) error { return nil }

// MinArgs returns a [Command.Args] check demanding at least n arguments.
func MinArgs(n int) func([]string) error {
	return func(args []string) error {
		if len(args) >= n {
			return nil
		}
		noun := "arguments"
		if n == 1 {
			noun = "argument"
		}
		return fmt.Errorf("expects at least %d %s, got %d", n, noun, len(args))
	}
}

// MaxArgs returns a [Command.Args] check taking at most n arguments.
func MaxArgs(n int) func([]string) error {
	return func(args []string) error {
		if len(args) <= n {
			return nil
		}
		noun := "arguments"
		if n == 1 {
			noun = "argument"
		}
		return fmt.Errorf("takes at most %d %s, got %d", n, noun, len(args))
	}
}

// ExactArgs returns a [Command.Args] check demanding exactly n arguments.
func ExactArgs(n int) func([]string) error {
	return func(args []string) error {
		if len(args) == n {
			return nil
		}
		if n == 0 {
			return fmt.Errorf("takes no arguments, got %d", len(args))
		}
		noun := "arguments"
		if n == 1 {
			noun = "argument"
		}
		return fmt.Errorf("expects %d %s, got %d", n, noun, len(args))
	}
}
