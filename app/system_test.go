package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestSystemPrompt checks that the system prompt holds the text of each
// instruction file as it is, and then the configured text when it is not
// blank, a blank line apart, and nothing else: no default text, no
// environment, no header naming a file, and nothing at all when there is
// neither.
func TestSystemPrompt(t *testing.T) {
	if got := SystemPrompt(nil, " \n"); got != "" {
		t.Errorf("SystemPrompt of nothing = %q, want empty", got)
	}
	if got := SystemPrompt(nil, "\nBe brief.\n"); got != "Be brief." {
		t.Errorf("SystemPrompt of the configured text = %q, want it alone", got)
	}
	instructions := []Instruction{{Path: "/home/a/AGENTS.md", Text: "Call me Sam."}, {Path: "/work/project/AGENTS.md", Text: "# Project\n\nUse tabs."}}
	want := "Call me Sam.\n\n# Project\n\nUse tabs.\n\nBe brief."
	if got := SystemPrompt(instructions, "Be brief."); got != want {
		t.Errorf("SystemPrompt with instructions = %q, want %q", got, want)
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
