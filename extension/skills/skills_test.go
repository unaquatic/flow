package skills

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
)

// write writes text to path, making its folders.
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillFile returns a SKILL.md named name, described as desc, whose
// instructions are body.
func skillFile(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

// names returns the names of skills, in order.
func names(skills []Skill) []string {
	var out []string
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return out
}

// TestFind checks that each folder that holds a SKILL.md is a skill, found
// depth first in a root, and that the folders under it, hidden folders and
// node_modules are not looked in; that a skill without a description is
// left out and reported; that a name an earlier root found wins; that a
// name the standard does not allow is reported but found; and that a root
// that does not exist holds nothing.
func TestFind(t *testing.T) {
	root := t.TempDir()
	user, proj := filepath.Join(root, "user"), filepath.Join(root, "proj")
	write(t, filepath.Join(user, "review", File), skillFile("review", "Review code.", "User review."))
	write(t, filepath.Join(user, "group", "deploy", File), skillFile("deploy", "Deploy it.", "Deploy."))
	write(t, filepath.Join(user, "group", "deploy", "nested", File), skillFile("nested", "Nested.", "Never found."))
	write(t, filepath.Join(user, ".hidden", File), skillFile("hidden", "Hidden.", "Never found."))
	write(t, filepath.Join(user, "node_modules", "dep", File), skillFile("dep", "Dep.", "Never found."))
	write(t, filepath.Join(user, "bare", File), "---\nname: bare\n---\nNo description.\n")
	write(t, filepath.Join(proj, "review", File), skillFile("review", "Project review.", "Shadowed."))
	write(t, filepath.Join(proj, "Bad_Name", File), "---\ndescription: Named by its folder.\n---\n")

	skills, err := Find(user, proj, filepath.Join(root, "missing"))
	if got, want := names(skills), []string{"deploy", "review", "Bad_Name"}; !slices.Equal(got, want) {
		t.Errorf("skills = %v, want %v", got, want)
	}
	if s, _ := Lookup(skills, "review"); s.Description != "Review code." || s.Path != filepath.Join(user, "review", File) {
		t.Errorf("review = %+v, want the user's", s)
	}
	for _, want := range []string{"bare", "no description", "name taken by", "Bad_Name", "characters other than"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to tell of %q", err, want)
		}
	}
}

// TestFindLinks checks that a link to a skill's folder is followed, and that
// a skill reached by two paths is found once, without a collision.
func TestFindLinks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "store", "review", File), skillFile("review", "Review code.", "Review."))
	user := filepath.Join(root, "user")
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "store", "review"), filepath.Join(user, "review")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	// A link back up would loop, were folders not read once.
	if err := os.Symlink(user, filepath.Join(user, "loop")); err != nil {
		t.Fatal(err)
	}
	skills, err := Find(user, filepath.Join(root, "store"))
	if err != nil || !slices.Equal(names(skills), []string{"review"}) {
		t.Errorf("Find = %v, %v; want review once, without an error", names(skills), err)
	}
}

// TestRoots checks that the user's skills come first, and then those of each
// folder of the project, nearest first, up to the repository's root.
func TestRoots(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := Roots("/home/u", sub)
	want := []string{filepath.Join("/home/u", Dir), filepath.Join(sub, Dir), filepath.Join(repo, Dir)}
	if !slices.Equal(got, want) {
		t.Errorf("Roots = %v, want %v", got, want)
	}
	if got := Roots("", ""); len(got) != 0 {
		t.Errorf("Roots of nothing = %v, want none", got)
	}
}

// TestParseCommand checks how a prompt names the skill it invokes.
func TestParseCommand(t *testing.T) {
	for _, tt := range []struct {
		text, name, args string
		ok               bool
	}{
		{"/skill:review", "review", "", true},
		{"  /skill:review  check main.go\nand tests ", "review", "check main.go\nand tests", true},
		{"/skill:review\nnext line", "review", "next line", true},
		{"/skill:", "", "", false},
		{"/review", "", "", false},
		{"run /skill:review", "", "", false},
	} {
		name, args, ok := ParseCommand(tt.text)
		if name != tt.name || args != tt.args || ok != tt.ok {
			t.Errorf("ParseCommand(%q) = %q, %q, %v; want %q, %q, %v", tt.text, name, args, ok, tt.name, tt.args, tt.ok)
		}
	}
}

// TestListing checks that skills are listed in a skill_instructions tag, as
// the standard writes them, escaped, for the tool that reads files, without
// those only a prompt invokes, and that nothing is listed when no tool could
// read them.
func TestListing(t *testing.T) {
	skills := []Skill{
		{Name: "review", Description: "Review <code> & tests.", Path: "/home/u/.agents/skills/review/SKILL.md"},
		{Name: "secret", Description: "Only on demand.", Path: "/s/SKILL.md", DisableModelInvocation: true},
	}
	want := `<skill_instructions>
The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (the folder that holds SKILL.md) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>review</name>
    <description>Review &lt;code&gt; &amp; tests.</description>
    <location>/home/u/.agents/skills/review/SKILL.md</location>
  </skill>
</available_skills>
</skill_instructions>`
	if got := Listing(skills, "read"); got != want {
		t.Errorf("Listing = %q, want %q", got, want)
	}
	if got := Listing(skills, ""); got != "" {
		t.Errorf("Listing without a tool = %q, want none", got)
	}
	if got := Listing(skills[1:], "read"); got != "" {
		t.Errorf("Listing of skills only a prompt invokes = %q, want none", got)
	}
}

// TestListingTool checks that the model is told to read skills with read,
// or else bash, and with nothing else.
func TestListingTool(t *testing.T) {
	defs := func(names ...string) []agent.ToolDefinition {
		var out []agent.ToolDefinition
		for _, n := range names {
			out = append(out, agent.ToolDefinition{Name: n})
		}
		return out
	}
	for _, tt := range []struct {
		tools []agent.ToolDefinition
		want  string
	}{
		{defs("bash", "read"), "read"},
		{defs("grep", "bash"), "bash"},
		{defs("grep"), ""},
		{nil, ""},
	} {
		if got := ListingTool(tt.tools); got != tt.want {
			t.Errorf("ListingTool(%v) = %q, want %q", tt.tools, got, tt.want)
		}
	}
}

// TestExtensionSystemPrompt checks that the extension lists the skills it
// finds when the session can read them, and lists none when it cannot.
func TestExtensionSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "review", File), skillFile("review", "Review code.", "Review."))
	e := New(dir)
	got := e.SystemPrompt(t.Context(), extension.System{Tools: []agent.ToolDefinition{{Name: "read"}}})
	if !strings.Contains(got, "<name>review</name>") {
		t.Errorf("SystemPrompt = %q, want it to list review", got)
	}
	if got := e.SystemPrompt(t.Context(), extension.System{}); got != "" {
		t.Errorf("SystemPrompt without tools = %q, want none", got)
	}
}

// TestExtensionExpandPrompt checks that the text of a prompt that invokes a
// skill expands into the skill's instructions as they read as it is sent,
// without the frontmatter, and then what it asks, with no trace of the
// command; that one that asks nothing more expands to the instructions
// alone; and that one that invokes no skill, or one that is not found, is
// not expanded.
func TestExtensionExpandPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review", File)
	write(t, path, skillFile("review", "Review code.", "# Review\n\nRead scripts/check.sh."))
	e := New(dir)
	body := "# Review\n\nRead scripts/check.sh."
	for _, tt := range []struct{ text, want string }{
		{"/skill:review  check main.go\nand tests", body + "\n\ncheck main.go\nand tests"},
		{"/skill:review", body},
	} {
		if got, ok := e.ExpandPrompt(t.Context(), extension.Prompt{Text: tt.text}); !ok || got != tt.want {
			t.Errorf("ExpandPrompt(%q) = %q, %v; want %q", tt.text, got, ok, tt.want)
		}
	}
	for _, text := range []string{"review it", "/skill:missing"} {
		if got, ok := e.ExpandPrompt(t.Context(), extension.Prompt{Text: text}); ok {
			t.Errorf("ExpandPrompt(%q) = %q, want it not expanded", text, got)
		}
	}
}

// TestBlock checks the text a prompt that invokes a skill is stored and
// sent as: the instructions, and then what the prompt asks, a blank line
// under them, if anything.
func TestBlock(t *testing.T) {
	b := Block{Body: "# Review\n\nSteps.", Args: "check main.go"}
	if got, want := b.Text(), "# Review\n\nSteps.\n\ncheck main.go"; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	b.Args = ""
	if got, want := b.Text(), "# Review\n\nSteps."; got != want {
		t.Errorf("Text without args = %q, want %q", got, want)
	}
}
