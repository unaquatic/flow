package main

import (
	"context"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
	demoui "github.com/unaquatic/flow/extension/demo/ui"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/snapshot"
	snapshotui "github.com/unaquatic/flow/extension/snapshot/ui"
	"github.com/unaquatic/flow/extension/titling"
	"github.com/unaquatic/flow/extension/tools"
	toolsui "github.com/unaquatic/flow/extension/tools/ui"
	"github.com/unaquatic/flow/terminal/ui"
)

// This file is the one place that names the extensions built into flow.
// Each is wired in at up to two layers, since the app must not import the
// terminal UI: its extension.Load adds it to the app, and its ui.Extension
// adds its widgets, tool formats, and slash commands to the terminal UI. The
// demo has no part in the app's runs, so it has a ui.Extension alone.

// extensions loads the extensions built into flow into the app, in the
// order their hooks run.
var extensions = extensionsWith(tools.Load)

// extensionsWith returns the extensions built into flow, as extensions
// loads them, with toolsLoad loading the tools extension, such as the
// Load of the tools.Selection a printed run's flags make.
func extensionsWith(toolsLoad extension.Load) []extension.Load {
	return []extension.Load{
		protocol.Load,
		toolsLoad,
		snapshot.Load,
		titling.Load,
	}
}

// views returns the terminal UI widgets of the extensions loaded into a.
func views(ctx context.Context, a *app.App) []ui.Extension {
	var out []ui.Extension
	if _, ok := extension.Find[*tools.Extension](a.Extensions()); ok {
		out = append(out, toolsui.New())
	}
	if s, ok := extension.Find[*snapshot.Extension](a.Extensions()); ok {
		out = append(out, snapshotui.New(ctx, s))
	}
	return append(out, demoui.New())
}
