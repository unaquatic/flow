package main

import (
	"context"
	"flag"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
	demoui "github.com/unaquatic/flow/extension/demo/ui"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/skills"
	skillsui "github.com/unaquatic/flow/extension/skills/ui"
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
var extensions = extensionsWith(tools.Load, false)

// extensionsWith returns the extensions built into flow, as extensions
// loads them, with toolsLoad loading the tools extension, such as the
// Load of the tools.Selection a printed run's flags make, and without the
// skills extension when noSkills is set, as --no-skills asks.
func extensionsWith(toolsLoad extension.Load, noSkills bool) []extension.Load {
	loads := []extension.Load{protocol.Load, toolsLoad}
	if !noSkills {
		loads = append(loads, skills.Load)
	}
	return append(loads, snapshot.Load, titling.Load)
}

// noSkillsFlag binds --no-skills, which loads no skills, to noSkills.
func noSkillsFlag(fs *flag.FlagSet, noSkills *bool) {
	fs.BoolVar(noSkills, "no-skills", false,
		"Load no skills: list none in the system prompt of a new session, and send /skill:name prompts as they are typed")
}

// views returns the terminal UI widgets of the extensions loaded into a.
func views(ctx context.Context, a *app.App) []ui.Extension {
	var out []ui.Extension
	if _, ok := extension.Find[*tools.Extension](a.Extensions()); ok {
		out = append(out, toolsui.New())
	}
	if s, ok := extension.Find[*skills.Extension](a.Extensions()); ok {
		out = append(out, skillsui.New(s.Skills()...))
	}
	if s, ok := extension.Find[*snapshot.Extension](a.Extensions()); ok {
		out = append(out, snapshotui.New(ctx, s))
	}
	return append(out, demoui.New())
}
