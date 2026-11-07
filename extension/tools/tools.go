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
	"slices"

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

// Load loads the extension with the tools flow offers the model: bash,
// which finds the installed shells as it loads.
func Load(context.Context, extension.Host) (extension.Extension, error) {
	return New(builtin.Bash()), nil
}

// All returns every built-in tool, in a stable order, whether or not Load
// offers it, as the demo calls them all.
func All() []agent.Tool { return builtin.All() }

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Tools returns the Toolset. The slice is a copy.
func (e *Extension) Tools() []agent.Tool { return slices.Clone(e.Toolset) }
