package cmdline

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
)

// A style marks up help text. The zero style is plain; a bold style, for
// terminals that show colors, shows the headings in bold and the banner in
// color where the command has a [Command.ColorBanner].
type style struct {
	bold bool
}

// banner returns the banner of sc as st shows it.
func (st style) banner(sc scope) string {
	if st.bold && sc.colorBanner != "" {
		return sc.colorBanner
	}
	return sc.banner
}

// heading writes the heading of a section of help, in bold when st is bold.
func (st style) heading(w io.Writer, title string) {
	if st.bold {
		fmt.Fprintf(w, "\x1b[1m%s:\x1b[22m\n", title)
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
}

// row writes a row of a list: cell, padded to width, then text.
func row(w io.Writer, cell string, width int, text string) {
	fmt.Fprintf(w, "  %-*s  %s\n", width, cell, text)
}

// printHelp writes the help of in to standard output, in bold where it shows
// colors.
func printHelp(sc scope, in info, fs *flag.FlagSet) {
	writeHelp(os.Stdout, sc, in, fs, style{bold: UseColor(os.Stdout)})
}

// writeHelp prints the banner of sc, the usage section of in, and its
// environment variables.
func writeHelp(w io.Writer, sc scope, in info, fs *flag.FlagSet, st style) {
	if banner := st.banner(sc); banner != "" {
		fmt.Fprintf(w, "%s\n\n", banner)
	}
	writeUsageSection(w, sc, in, fs, st)
	writeEnv(w, in.env, st)
}

// writeUsageSection prints the usage lines, subcommands and flags of in.
func writeUsageSection(w io.Writer, sc scope, in info, fs *flag.FlagSet, st style) {
	writeUsage(w, sc, in, st)
	writeCommands(w, in.commands, st)
	writeFlags(w, fs, st)
}

func writeUsage(w io.Writer, sc scope, in info, st style) {
	st.heading(w, "Usage")
	if in.runnable || len(in.commands) == 0 {
		fmt.Fprintf(w, "  %s%s [flags]\n", sc, argsSuffix(in.usage))
	}
	if len(in.commands) > 0 {
		fmt.Fprintf(w, "  %s <command> [flags]\n", sc)
	}
}

func argsSuffix(usage string) string {
	if usage == "" {
		return ""
	}
	return " " + usage
}

func writeCommands(w io.Writer, nodes []Node, st style) {
	if len(nodes) == 0 {
		return
	}
	subs := make([]info, len(nodes))
	width := 0
	for i, n := range nodes {
		subs[i] = n.info()
		width = max(width, len(subs[i].name))
	}
	fmt.Fprintln(w)
	st.heading(w, "Commands")
	for _, sub := range subs {
		row(w, sub.name, width, sub.short)
	}
}

func writeFlags(w io.Writer, fs *flag.FlagSet, st style) {
	groups := append(flagGroups(fs), helpFlags(fs)...)
	if len(groups) == 0 {
		return
	}
	// Long names start in one column, after the widest one-letter names.
	shortWidth := 0
	for _, g := range groups {
		shortWidth = max(shortWidth, len(g.shortFlags()))
	}
	cells := make([]string, len(groups))
	width := 0
	for i, g := range groups {
		cells[i] = g.cell(shortWidth)
		width = max(width, len(cells[i]))
	}
	fmt.Fprintln(w)
	st.heading(w, "Flags")
	for i, g := range groups {
		row(w, cells[i], width, g.help())
	}
}

// writeEnv prints vars with their names in a column.
func writeEnv(w io.Writer, vars []EnvVar, st style) {
	if len(vars) == 0 {
		return
	}
	width := 0
	for _, v := range vars {
		width = max(width, len(v.Name))
	}
	fmt.Fprintln(w)
	st.heading(w, "Environment")
	for _, v := range vars {
		row(w, v.Name, width, v.Usage)
	}
}

// A flagGroup is a set of flag names sharing one destination.
type flagGroup struct {
	names []string
	kind  string
	usage string
	def   string
}

// String renders the group as a flag list, such as "-m, --model <pattern>".
func (g flagGroup) String() string {
	return strings.Join(slices.DeleteFunc([]string{g.shortFlags(), g.longFlags()}, func(s string) bool { return s == "" }), ", ") + g.kindSuffix()
}

// shortFlags returns the one-letter names of g as flags, such as "-m".
func (g flagGroup) shortFlags() string {
	return g.dashed(func(name string) bool { return len(name) == 1 })
}

// longFlags returns the longer names of g as flags, such as "--model".
func (g flagGroup) longFlags() string {
	return g.dashed(func(name string) bool { return len(name) > 1 })
}

// dashed returns the names of g that keep reports true for, as flags joined
// with commas.
func (g flagGroup) dashed(keep func(string) bool) string {
	var flags []string
	for _, name := range g.names {
		if keep(name) {
			flags = append(flags, dash(name))
		}
	}
	return strings.Join(flags, ", ")
}

// cell returns the flags of g as a row of help writes them. The one-letter
// names take shortWidth characters, so that the long names of every row start
// in one column.
func (g flagGroup) cell(shortWidth int) string {
	short, long := g.shortFlags(), g.longFlags()
	var names string
	switch {
	case short != "" && long != "":
		names = short + ", " + strings.Repeat(" ", shortWidth-len(short)) + long
	case short != "":
		names = short
	case shortWidth > 0:
		names = strings.Repeat(" ", shortWidth+len(", ")) + long
	default:
		names = long
	}
	return names + g.kindSuffix()
}

// kindSuffix returns the kind of value g takes after a space, such as
// " <string>", or "" for a boolean flag.
func (g flagGroup) kindSuffix() string {
	if g.kind == "" {
		return ""
	}
	return " <" + g.kind + ">"
}

// dash returns name as help writes the flag: "-x" for a one-letter name and
// "--name" otherwise. Parsing accepts either form.
func dash(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// help renders the usage text, noting a non-zero default value.
func (g flagGroup) help() string {
	switch g.def {
	case "", "false", "0":
		return g.usage
	}
	return fmt.Sprintf("%s [default: %s]", g.usage, g.def)
}

// helpFlags returns the group of the help flags, --help and -h, that the flag
// package handles itself, leaving out a name that fs defines.
func helpFlags(fs *flag.FlagSet) []flagGroup {
	var names []string
	for _, name := range []string{"help", "h"} {
		if fs.Lookup(name) == nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []flagGroup{{names: names, usage: "Show help"}}
}

// flagGroups collects the flags of fs, merging the names bound to one
// variable, which share their flag.Value. A flag whose Value cannot be
// compared, such as one that flag.Func defines, stands alone. The type, usage
// text and default of a group are those of its first name. The groups are
// ordered by their first long name, or their first name when they have none.
func flagGroups(fs *flag.FlagSet) []flagGroup {
	key := func(g flagGroup) string {
		if i := slices.IndexFunc(g.names, func(name string) bool { return len(name) > 1 }); i >= 0 {
			return g.names[i]
		}
		return g.names[0]
	}
	groups := collectFlagGroups(fs)
	slices.SortStableFunc(groups, func(a, b flagGroup) int { return cmp.Compare(key(a), key(b)) })
	return groups
}

// collectFlagGroups returns the groups of flagGroups in the order that fs
// visits their first names, each listing its names in that order.
func collectFlagGroups(fs *flag.FlagSet) []flagGroup {
	index := make(map[any]int)
	var groups []flagGroup
	fs.VisitAll(func(f *flag.Flag) {
		kind, usage := flag.UnquoteUsage(f)
		var key any = f.Name
		if reflect.ValueOf(f.Value).Comparable() {
			key = f.Value
		}
		if i, ok := index[key]; ok {
			groups[i].names = append(groups[i].names, f.Name)
			return
		}
		index[key] = len(groups)
		groups = append(groups, flagGroup{
			names: []string{f.Name},
			kind:  kind,
			usage: usage,
			def:   f.DefValue,
		})
	})
	return groups
}
