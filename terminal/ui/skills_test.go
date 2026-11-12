package ui_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unaquatic/flow/extension/skills"
	skillsui "github.com/unaquatic/flow/extension/skills/ui"
	"github.com/unaquatic/flow/terminal/ui"
)

// These tests show what the skills extension shows: a prompt that invoked a
// skill, and /skills, which lists the skills to pick from.

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

// TestSkillsMenuFrames draws /skills: a list of the skills, searched from
// the input, each by its name and description; and then the input once one
// is chosen, which closes the list and fills the input with its
// instructions, and a blank line under them for what to ask.
func TestSkillsMenuFrames(t *testing.T) {
	dir := t.TempDir()
	var list []skills.Skill
	for _, sk := range []struct{ name, desc, body string }{
		{"review", "Review the changes for bugs and style.", "# Review\n\nRead scripts/check.sh."},
		{"deploy", "Deploy the service to staging.", "# Deploy\n\nRun make deploy."},
	} {
		path := filepath.Join(dir, sk.name, skills.File)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		text := "---\nname: " + sk.name + "\ndescription: " + sk.desc + "\n---\n\n" + sk.body + "\n"
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		list = append(list, skills.Skill{Name: sk.name, Description: sk.desc, Path: path})
	}
	u := ui.NewInputTestUI(t, skillsui.New(list...))
	u.Apply(u.Service().HandleSubmit("/skills"))
	ui.AssertInputFrames(t, u, "input_skills_menu", 60, 12)

	u.Apply(u.Service().HandleBannerSelect(1))
	if got, want := u.InputText(), "# Deploy\n\nRun make deploy.\n\n"; got != want {
		t.Errorf("input after choosing deploy = %q, want %q", got, want)
	}
	ui.AssertInputFrames(t, u, "input_skill_filled", 60, 10)
}
