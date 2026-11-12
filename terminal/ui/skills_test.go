package ui_test

import (
	"testing"

	"github.com/unaquatic/flow/extension/skills"
	skillsui "github.com/unaquatic/flow/extension/skills/ui"
	"github.com/unaquatic/flow/terminal/ui"
)

// These tests show what the skills extension shows: a prompt that invoked a
// skill, and the commands that invoke skills in the slash menu.

// skillViews returns the widgets of two skills, review and deploy.
func skillViews() ui.Extension {
	return skillsui.New(
		skills.Skill{Name: "review", Description: "Review the changes for bugs and style.", Path: "/home/u/.agents/skills/review/SKILL.md"},
		skills.Skill{Name: "deploy", Description: "Deploy the service to staging.", Path: "/work/project/.agents/skills/deploy/SKILL.md"},
	)
}

// TestSkillFrames draws a prompt that invoked a skill as the skills
// extension stores it, as any prompt shows: the whole text it was sent as,
// the skill's instructions and then what it asks, in one message.
func TestSkillFrames(t *testing.T) {
	b := skills.Block{Body: "# Review\n\nRead scripts/check.sh.", Args: "check main.go"}
	u := ui.NewInlineTestUI(t, skillViews())
	u.AddMessage(ui.UserText("u1", b.Text()))
	u.AddMessage(ui.AgentText("a1", "Reviewed main.go."))
	ui.AssertTranscriptFrames(t, u, "user_message_skill", 40, 16)
}

// TestSkillSlashMenuFrames draws the slash menu as /skill is typed: it offers
// the command of each skill, named as the prompt that invokes it, beside its
// description.
func TestSkillSlashMenuFrames(t *testing.T) {
	u := ui.NewInputTestUI(t, skillViews())
	ui.AssertSlashMenuFrames(t, u, "input_slash_skills", "/skill", 60, 12)
}
