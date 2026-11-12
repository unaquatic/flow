// Package ui is the terminal side of the skills extension: a /skill:name
// command for each skill, which invokes it. A prompt that invoked a skill
// shows as any prompt does, as the text it was sent as: the skill's
// instructions, and then what it asks.
package ui

import (
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/skills"
	"github.com/unaquatic/flow/terminal/ui"
)

// Extension adds the skills to the terminal UI. It implements
// ui.CommandExtension.
type Extension struct {
	// Skills are the skills the UI offers commands for, as they were found
	// when it started.
	Skills []skills.Skill
}

var _ ui.CommandExtension = (*Extension)(nil)

// New returns the terminal side of the skills that list holds, in order.
func New(list ...skills.Skill) *Extension { return &Extension{Skills: list} }

// NewPartView returns nil: the extension shows no part of its own.
func (*Extension) NewPartView(message.ContentPart, ui.ViewHost) ui.PartView { return nil }

// NewSegmentView returns nil: the extension shows nothing under a message.
func (*Extension) NewSegmentView(ui.ChatMessage, ui.ViewHost) ui.SegmentView { return nil }

// SlashCommands returns a command for each of Skills, as Command makes it.
func (e *Extension) SlashCommands() []ui.SlashCommand {
	out := make([]ui.SlashCommand, 0, len(e.Skills))
	for _, s := range e.Skills {
		out = append(out, Command(s))
	}
	return out
}

// Command returns the command of s, named as the prompt that invokes it is,
// as in /skill:review, and described by the first line of its description,
// its spaces collapsed. Run sends that prompt, as it would be typed, for
// the app to expand; a prompt that asks the skill for something more, as in
// "/skill:review main.go", is typed and sent as any other.
func Command(s skills.Skill) ui.SlashCommand {
	name, _ := ui.CutCommand(s.Command())
	return ui.SlashCommand{
		Name: ui.Command(name),
		Desc: OneLine(s.Description),
		Run: func(svc *ui.Service) []ui.UIEvent {
			return slices.Concat(svc.QueuePrompt(s.Command()), svc.SendQueued())
		},
	}
}

// OneLine returns the first paragraph of text on one line, its runs of
// space one space each.
func OneLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n\n")
	return strings.Join(strings.Fields(first), " ")
}
