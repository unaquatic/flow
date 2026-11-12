package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestSystemInstruction checks that the system prompt holds each
// instruction file in an instructions tag naming its path, and then the
// configured text, when it is not blank, in an instructions tag without one,
// a blank line apart, and nothing else: no default text, no environment, no
// empty tag, and nothing at all when there is neither.
func TestSystemInstruction(t *testing.T) {
	if got := SystemInstruction([]Instruction{{Path: "/home/a/AGENTS.md", Text: " \n"}}, " \n"); got != "" {
		t.Errorf("SystemInstruction of nothing = %q, want empty", got)
	}
	if got, want := SystemInstruction(nil, "\nBe brief.\n"), "<instructions>\nBe brief.\n</instructions>"; got != want {
		t.Errorf("SystemInstruction of the configured text = %q, want %q", got, want)
	}
	instructions := []Instruction{{Path: "/home/a/AGENTS.md", Text: "Call me Sam."}, {Path: "/work/project/AGENTS.md", Text: "# Project\n\nUse tabs."}}
	want := `<instructions path="/home/a/AGENTS.md">
Call me Sam.
</instructions>

<instructions path="/work/project/AGENTS.md">
# Project

Use tabs.
</instructions>

<instructions>
Be brief.
</instructions>`
	if got := SystemInstruction(instructions, "Be brief."); got != want {
		t.Errorf("SystemInstruction with instructions = %q, want %q", got, want)
	}
	want = "<instructions path=\"/home/a/AGENTS.md\">\nCall me Sam.\n</instructions>"
	if got := SystemInstruction(instructions[:1], ""); got != want {
		t.Errorf("SystemInstruction without configured text = %q, want %q", got, want)
	}
}

// TestInstructionTagged checks that a path that holds a quote or an
// ampersand is escaped in the path attribute, so that it cannot end the tag.
func TestInstructionTagged(t *testing.T) {
	got := Instruction{Path: `/work/a "b" & c/AGENTS.md`, Text: "Use tabs."}.Tagged()
	want := "<instructions path=\"/work/a &#34;b&#34; &amp; c/AGENTS.md\">\nUse tabs.\n</instructions>"
	if got != want {
		t.Errorf("Tagged = %q, want %q", got, want)
	}
}

// TestFindInstructionsNames checks that an AGENTS.override.md takes the
// place of the AGENTS.md of its own folder, in the data directory and the
// project alike, but of no other folder's; that a blank one is passed over
// for the folder's AGENTS.md.
func TestFindInstructionsNames(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(data, InstructionsFile), "User rules.")
	write(t, filepath.Join(data, InstructionsOverrideFile), "User override.")
	write(t, filepath.Join(repo, InstructionsFile), "Repo rules.")
	write(t, filepath.Join(repo, InstructionsOverrideFile), "Repo override.")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	texts := func(ins []Instruction) []string {
		var out []string
		for _, in := range ins {
			out = append(out, in.Text)
		}
		return out
	}
	if got, want := texts(FindInstructions(data, sub)), []string{"User override.", "Repo override."}; !slices.Equal(got, want) {
		t.Errorf("FindInstructions with overrides = %q, want %q", got, want)
	}

	// An override in a folder below takes the place of nothing above it:
	// the nearest folder with a file of instructions is the project's.
	write(t, filepath.Join(sub, InstructionsOverrideFile), "Sub override.")
	if got, want := texts(FindInstructions("", sub)), []string{"Sub override."}; !slices.Equal(got, want) {
		t.Errorf("FindInstructions below the repository's root = %q, want %q", got, want)
	}

	// A blank override is passed over for the AGENTS.md beside it.
	write(t, filepath.Join(repo, InstructionsOverrideFile), " \n")
	if got, want := FindInstructions("", repo), []Instruction{{Path: filepath.Join(repo, InstructionsFile), Text: "Repo rules."}}; !slices.Equal(got, want) {
		t.Errorf("FindInstructions past a blank override = %+v, want %+v", got, want)
	}
}

// write writes body to path, making the folders above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFindInstructions checks that the user's file comes first and then the
// project's nearest, found up to the root of the repository but not above
// it, nor above the working directory outside a repository, and that blank
// and missing files are left out, as is a file read already.
func TestFindInstructions(t *testing.T) {
	paths := func(ins []Instruction) []string {
		var out []string
		for _, in := range ins {
			out = append(out, in.Path)
		}
		return out
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "cmd", "tool")
	write(t, filepath.Join(data, InstructionsFile), "Call me Sam.")
	write(t, filepath.Join(root, InstructionsFile), "Above the repository.")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(repo, InstructionsFile), "\n  Use tabs.  \n\n")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	// Below the root of the repository, its file is found.
	got := FindInstructions(data, sub)
	if want := []string{filepath.Join(data, InstructionsFile), filepath.Join(repo, InstructionsFile)}; !slices.Equal(paths(got), want) {
		t.Fatalf("FindInstructions = %q, want the user's file and the repository's", paths(got))
	}
	if got[0].Text != "Call me Sam." || got[1].Text != "Use tabs." {
		t.Errorf("texts = %q, %q; want the files without the blank space around them", got[0].Text, got[1].Text)
	}

	// The nearest file wins over the repository's.
	write(t, filepath.Join(sub, InstructionsFile), "Tool rules.")
	if got := paths(FindInstructions("", sub)); !slices.Equal(got, []string{filepath.Join(sub, InstructionsFile)}) {
		t.Errorf("FindInstructions from a folder with its own file = %q, want that file alone", got)
	}

	// A blank file is left out, and the search goes on above it.
	write(t, filepath.Join(sub, InstructionsFile), " \n")
	if got := paths(FindInstructions("", sub)); !slices.Equal(got, []string{filepath.Join(repo, InstructionsFile)}) {
		t.Errorf("FindInstructions past a blank file = %q, want the repository's", got)
	}

	// Outside a repository only the working directory is looked in.
	outside := filepath.Join(root, "loose", "dir")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := FindInstructions("", outside); len(got) != 0 {
		t.Errorf("FindInstructions outside a repository = %q, want none from the folders above", paths(got))
	}

	// The data directory as the project reads its file once.
	if got := paths(FindInstructions(data, data)); !slices.Equal(got, []string{filepath.Join(data, InstructionsFile)}) {
		t.Errorf("FindInstructions in the data directory = %q, want its file once", got)
	}
	if got := FindInstructions("", ""); len(got) != 0 {
		t.Errorf("FindInstructions of no folders = %q, want none", paths(got))
	}
}
