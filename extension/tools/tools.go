// Package tools is the extension that gives the model flow's built-in tools,
// which its internal package builtin implements. It plugs into flow at two of
// the points package extension describes:
//
//   - Behaviour: it adds its tools through extension.ToolHook, as the app
//     starts.
//   - Terminal UI: package ui describes what the calls of the built-in
//     tools show, such as a command line for bash and a diff for edit, in
//     the box the transcript draws every tool call in.
package tools

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/tools/internal/builtin"
)

// Name names the extension.
const Name = "tools"

// Extension adds a fixed set of tools to the app. It implements
// extension.ToolHook.
type Extension struct {
	// Toolset holds the tools the extension adds.
	Toolset []agent.Tool
}

var _ extension.ToolHook = (*Extension)(nil)

// New returns the extension over ts.
func New(ts ...agent.Tool) *Extension { return &Extension{Toolset: ts} }

// Load loads the extension with the tools flow offers the model by default,
// DefaultTools, as the zero Selection loads it.
func Load(ctx context.Context, host extension.Host) (extension.Extension, error) {
	return Selection{}.Load(ctx, host)
}

// DefaultTools names the tools flow offers the model unless a Selection
// names others: read, write, and edit, which work on the files of the
// working directory, bash, which finds the installed shells as it loads,
// and todo, which writes the model's todo list.
var DefaultTools = []string{"read", "write", "edit", "bash", "todo"}

// Selection chooses the built-in tools the extension offers the model, by
// their names or by patterns of them, as path.Match reads them, such as
// "*". The zero Selection offers DefaultTools.
type Selection struct {
	// Only names the tools to offer, among every built-in tool, in place
	// of DefaultTools. Nil offers DefaultTools, and an empty list none.
	Only []string
	// Exclude names the tools to leave out of those that Only, or else
	// DefaultTools, offers.
	Exclude []string
}

// Load loads the extension with the tools s chooses, as Tools returns
// them, and fails as Tools does.
func (s Selection) Load(context.Context, extension.Host) (extension.Extension, error) {
	ts, err := s.Tools()
	if err != nil {
		return nil, err
	}
	return New(ts...), nil
}

// Tools returns the built-in tools s chooses, in the order All returns
// them. It fails when a name or pattern of s is malformed or matches no
// built-in tool.
func (s Selection) Tools() ([]agent.Tool, error) {
	all := builtin.All()
	only := DefaultTools
	if s.Only != nil {
		only = s.Only
	}
	offered, err := Matcher(all, only)
	if err != nil {
		return nil, err
	}
	excluded, err := Matcher(all, s.Exclude)
	if err != nil {
		return nil, err
	}
	var out []agent.Tool
	for _, t := range all {
		if offered(t.Name) && !excluded(t.Name) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Matcher returns the func that reports whether a tool name matches any of
// patterns, names or path.Match patterns. It fails when a pattern is
// malformed or matches none of tools, naming the tools there are.
func Matcher(tools []agent.Tool, patterns []string) (func(name string) bool, error) {
	match := func(pattern, name string) bool {
		ok, _ := path.Match(pattern, name)
		return ok
	}
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("tool pattern %q: %w", p, err)
		}
		if !slices.ContainsFunc(tools, func(t agent.Tool) bool { return match(p, t.Name) }) {
			names := make([]string, 0, len(tools))
			for _, t := range tools {
				names = append(names, t.Name)
			}
			slices.Sort(names)
			return nil, fmt.Errorf("unknown tool %q; the built-in tools are %s", p, strings.Join(names, ", "))
		}
	}
	return func(name string) bool {
		return slices.ContainsFunc(patterns, func(p string) bool { return match(p, name) })
	}, nil
}

// All returns every built-in tool, in a stable order, whether or not Load
// offers it, as the demo calls them all, and a Selection picks from.
func All() []agent.Tool { return builtin.All() }

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Tools returns the Toolset. The slice is a copy.
func (e *Extension) Tools() []agent.Tool { return slices.Clone(e.Toolset) }
