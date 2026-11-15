// Package ui is the terminal side of the skills extension: a /name command
// of each skill, marked "[skill]" in the slash menu, and /skills, which
// lists the skills to pick from, and fills the input with the instructions
// of the one picked, for the user to say what to do under them and send. A
// prompt sent so shows as any prompt does, as its text.
package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/skills"
	"github.com/unaquatic/flow/terminal/ui"
)

// Command is the slash command that lists the skills.
const Command ui.Command = "skills"

// NoSkillsToast is the toast /skills shows when no skill was found.
const NoSkillsToast = "No skills in ~/.agents/skills or the project's .agents/skills"

// Extension adds the skills to the terminal UI. It implements
// ui.CommandExtension.
type Extension struct {
	// Skills are the skills /skills lists, as they were found when the UI
	// started.
	Skills []skills.Skill
}

var _ ui.CommandExtension = (*Extension)(nil)

// New returns the terminal side of the skills that list holds, in order.
func New(list ...skills.Skill) *Extension { return &Extension{Skills: list} }

// NewPartView returns nil: the extension shows no part of its own.
func (*Extension) NewPartView(message.ContentPart, ui.ViewHost) ui.PartView { return nil }

// NewSegmentView returns nil: the extension shows nothing under a message.
func (*Extension) NewSegmentView(ui.ChatMessage, ui.ViewHost) ui.SegmentView { return nil }

// Hint follows the name of a skill's command in the slash menu, in grey.
const Hint = "[skill]"

// SlashCommands returns /skills, which runs as ShowMenu does, and then a
// command of each of Skills, as SkillCommand makes it, in order. A skill
// whose name is taken by one of the UI's own commands, or by /skills, has
// no command of its own: /skills still lists it, and /skill:name still
// invokes it.
func (e *Extension) SlashCommands() []ui.SlashCommand {
	cmds := []ui.SlashCommand{{Name: Command, Desc: "Pick a skill to invoke", Run: e.ShowMenu}}
	for _, sk := range e.Skills {
		name := ui.Command(sk.Name)
		if _, taken := ui.LookupCommand(cmds, name); taken {
			continue
		}
		if _, taken := ui.LookupCommand(ui.SlashCommands, name); taken {
			continue
		}
		cmds = append(cmds, SkillCommand(sk))
	}
	return cmds
}

// SkillCommand returns the slash command of sk: /name, marked Hint in the
// menu and described by the first paragraph of its description, as OneLine
// gives it. Picked from the menu, or typed alone or with what to do after
// it, as in "/review main.go", it sends "/skill:review main.go", which the
// skills extension expands into the skill's instructions as the prompt is
// sent. The UI runs it only while no other command takes its name, as
// ui.CommandsWith leaves such a command out.
func SkillCommand(sk skills.Skill) ui.SlashCommand {
	send := func(s *ui.Service, args string) []ui.UIEvent {
		text := strings.TrimSpace(skills.CommandPrefix + sk.Name + " " + args)
		return slices.Concat(s.QueuePrompt(text), s.SendQueued())
	}
	return ui.SlashCommand{
		Name:    ui.Command(sk.Name),
		Desc:    OneLine(sk.Description),
		Hint:    Hint,
		Run:     func(s *ui.Service) []ui.UIEvent { return send(s, "") },
		RunArgs: send,
	}
}

// ShowMenu lists Skills, searched from the input, as MenuItems gives them,
// or shows NoSkillsToast when there are none.
func (e *Extension) ShowMenu(s *ui.Service) []ui.UIEvent {
	if len(e.Skills) == 0 {
		return s.ShowToast(NoSkillsToast)
	}
	items := e.MenuItems(s)
	return s.ShowSearchMenu(&ui.SearchMenu{Title: "Skills", Items: func() []ui.MenuItem { return items }})
}

// MenuItems returns a row of /skills for each of Skills, in order: its name,
// and the first paragraph of its description, as OneLine gives it. Choosing
// one closes the list and fills the input with the skill's instructions, its
// SKILL.md as it reads now, without the frontmatter, and a blank line under
// them, where what to ask of it follows; the input is then sent as any
// other. A skill whose file can no longer be read shows the error in a
// toast instead.
func (e *Extension) MenuItems(s *ui.Service) []ui.MenuItem {
	items := make([]ui.MenuItem, 0, len(e.Skills))
	for _, sk := range e.Skills {
		items = append(items, ui.MenuItem{
			Name: sk.Name,
			Desc: OneLine(sk.Description),
			Choose: s.Closing(func() []ui.UIEvent {
				b, err := skills.NewBlock(sk, "")
				if err != nil {
					return s.ShowToast(fmt.Sprintf("Skill %s: %v", sk.Name, err))
				}
				return s.SetInput(b.Text() + "\n\n")
			}),
		})
	}
	return items
}

// OneLine returns the first paragraph of text on one line, its runs of
// space one space each.
func OneLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n\n")
	return strings.Join(strings.Fields(first), " ")
}
