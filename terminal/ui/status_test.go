package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
)

// TestStatusLinesDimTags checks that the session info's Model row shows the
// bracketed ID of a model the catalog does not list, and the provider's ID
// after the model, in the labels' dim color, and the rest of the name in
// the text's, and that it has no Account row.
func TestStatusLinesDimTags(t *testing.T) {
	dim := DefaultTheme().TextMuted
	lines := StatusLines(StatusInfo{Provider: "provider-z", Model: "Unknown [model-z] Medium"}, nil)
	if slices.ContainsFunc(lines, func(l style.StyledText) bool { return strings.HasPrefix(l.PlainText(), "Account") }) {
		t.Errorf("status lines = %q, want no Account row", plainLines(lines))
	}
	for _, tt := range []struct {
		row  int
		want []string
	}{
		{2, []string{"Unknown ", "[model-z]", " Medium", " ", "[provider-z]"}},
	} {
		chunks := lines[tt.row].Chunks[1:]
		var got []string
		for _, c := range chunks {
			got = append(got, c.Text)
			if tagged := strings.HasPrefix(c.Text, "["); tagged != (c.Fg == dim) {
				t.Errorf("row %d chunk %q in %v, want the bracketed IDs alone dim", tt.row, c.Text, c.Fg)
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("row %d chunks = %q, want %q", tt.row, got, tt.want)
		}
	}
}

// TestGitBranch checks that the branch is read from the HEAD of the work
// tree that holds the directory, from a folder within it too, as the short
// commit when HEAD is detached, through the .git file of a linked work tree,
// and is empty outside any work tree.
func TestGitBranch(t *testing.T) {
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repo := t.TempDir()
	write(filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature/x\n")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	detached := t.TempDir()
	write(filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	linked := t.TempDir()
	write(filepath.Join(repo, ".git", "worktrees", "w", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(linked, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "w")+"\n")

	for _, tt := range []struct{ dir, want string }{
		{repo, "feature/x"},
		{sub, "feature/x"},
		{detached, "0123456"},
		{linked, "main"},
		{t.TempDir(), ""},
	} {
		if got := GitBranch(tt.dir); got != tt.want {
			t.Errorf("GitBranch(%s) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

// TestStatusLinesSession checks that the session info's Session row shows
// the session's title, or UntitledName without one, and then the last part
// of its ID, dim and bracketed.
func TestStatusLinesSession(t *testing.T) {
	const id = "01a0e33d-4571-7ed4-b06b-4229c726c313"
	for _, tt := range []struct{ title, want string }{
		{"Fix the login", "Session   Fix the login [4229c726c313]"},
		{"", "Session   " + UntitledName + " [4229c726c313]"},
	} {
		row := StatusLines(StatusInfo{Session: id, Title: tt.title}, nil)[0]
		if got := row.PlainText(); got != tt.want {
			t.Errorf("Session row = %q, want %q", got, tt.want)
		}
		if last := row.Chunks[len(row.Chunks)-1]; last.Text != "[4229c726c313]" || last.Fg != DefaultTheme().TextMuted {
			t.Errorf("ID chunk = %+v, want [4229c726c313] dim", last)
		}
	}
	if got := ShortSessionID("no-dash-here-x"); got != "x" {
		t.Errorf("ShortSessionID = %q, want the part after the last dash", got)
	}
	if got := ShortSessionID("s1"); got != "s1" {
		t.Errorf("ShortSessionID of an ID without a dash = %q, want it whole", got)
	}
}

// TestStatusLinesHomeDirectory checks that the session info's Directory row
// writes the home folder as ~, as the session list does.
func TestStatusLinesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home directory on Windows
	row := StatusLines(StatusInfo{Directory: filepath.Join(home, "code", "flow"), Branch: "main"}, nil)[3]
	if got, want := row.PlainText(), "Directory "+filepath.FromSlash("~/code/flow")+" [main]"; got != want {
		t.Errorf("Directory row = %q, want %q", got, want)
	}
}

// TestStatusLinesBranch checks that the session info's Directory row shows
// the git branch after the directory, dim and bracketed, and the directory
// alone without one.
func TestStatusLinesBranch(t *testing.T) {
	dim := DefaultTheme().TextMuted
	for _, tt := range []struct {
		branch, want string
	}{
		{"main", "Directory /work/project [main]"},
		{"", "Directory /work/project"},
	} {
		row := StatusLines(StatusInfo{Directory: "/work/project", Branch: tt.branch}, nil)[3]
		if got := row.PlainText(); got != tt.want {
			t.Errorf("Directory row = %q, want %q", got, tt.want)
		}
		if last := row.Chunks[len(row.Chunks)-1]; tt.branch != "" && (last.Text != "[main]" || last.Fg != dim) {
			t.Errorf("branch chunk = %+v, want [main] dim", last)
		}
	}
}

// TestStatusLinesContext checks that the session info's Context row reads
// the latest request's tokens, the window and the share of it they fill, or
// the tokens alone when the window is not known.
func TestStatusLinesContext(t *testing.T) {
	for _, tt := range []struct {
		usage SessionUsage
		want  string
	}{
		{SessionUsage{Context: 20_000, Window: 400_000}, "Context   20K / 400K 5%"},
		{SessionUsage{Window: 400_000}, "Context   0K / 400K 0%"},
		{SessionUsage{Context: 18_000}, "Context   18K"},
	} {
		lines := StatusLines(StatusInfo{Usage: tt.usage}, nil)
		i := slices.IndexFunc(lines, func(l style.StyledText) bool { return strings.HasPrefix(l.String(), "Context") })
		if i < 0 || lines[i].String() != tt.want {
			t.Errorf("usage %+v: context row = %q, want %q", tt.usage, rowText(lines, i), tt.want)
		}
	}
}

// TestStatusNoModel checks that a terminal started with no model holds none,
// not "/", and that /status reads its model as StatusNone, while a model
// that names no provider reads without one, and a model the catalog does not
// list reads as unknown, its provider's ID after it.
func TestStatusNoModel(t *testing.T) {
	for _, tt := range []struct {
		start, model string
		value        string
	}{
		{start: "", model: "", value: "Model     None"},
		{start: "model-x", model: "/model-x", value: "Model     Unknown [model-x]"},
		{start: "provider-z/lab-z/model-z", model: "provider-z/lab-z/model-z", value: "Model     Unknown [model-z] [provider-z]"},
	} {
		_, a := newTestService(t)
		state := &UIState{Model: tt.start}
		s := NewService(t.Context(), a, state)
		if state.Model != tt.model || state.ModelLabel != tt.model {
			t.Errorf("start %q: model, label = %q, %q, want %q", tt.start, state.Model, state.ModelLabel, tt.model)
		}
		s.InitSession(t.Context(), "")
		events := s.ShowInfo()
		if len(events) != 1 {
			t.Fatalf("start %q: ShowInfo returned %d events, want the notice alone", tt.start, len(events))
		}
		notice, _ := toastOf(events[0])
		row := func(text string) int {
			return slices.IndexFunc(notice.Lines, func(l style.StyledText) bool { return l.PlainText() == text })
		}
		if row(tt.value) < 0 || slices.ContainsFunc(notice.Lines, func(l style.StyledText) bool { return strings.HasPrefix(l.PlainText(), "Account") }) {
			t.Errorf("start %q: /status lines = %q, want %q and no Account row", tt.start, plainLines(notice.Lines), tt.value)
		}
	}
}

// TestStartLines checks that the start notice is titled by the product and
// its release without the leading v, and shows the model with its
// provider's ID after it, the model alone without a provider, StatusNone
// without a model, and then the directory.
func TestStartLines(t *testing.T) {
	if got, want := StartTitle("v1.2.3"), product.DisplayName+" 1.2.3"; got != want {
		t.Errorf("StartTitle = %q, want %q", got, want)
	}
	if got, want := StartTitle("(devel)"), product.DisplayName+" (devel)"; got != want {
		t.Errorf("StartTitle of a build with no version = %q, want %q", got, want)
	}
	for _, tt := range []struct {
		provider agent.ProviderID
		model    string
		want     string
	}{
		{providerA, "Model A Fast High", "Model     Model A Fast High [provider-a]"},
		{"", "Unknown [model-x]", "Model     Unknown [model-x]"},
		{"", "", "Model     " + StatusNone},
	} {
		got := plainLines(StartLines(StatusInfo{Provider: tt.provider, Model: tt.model, Directory: "/work/project", Branch: "main"}, nil))
		if want := []string{tt.want, "Directory /work/project [main]"}; !slices.Equal(got, want) {
			t.Errorf("provider %q, model %q: start lines = %q, want %q", tt.provider, tt.model, got, want)
		}
	}
}

// plainLines returns the plain text of each of lines.
func plainLines(lines []style.StyledText) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.PlainText()
	}
	return out
}

// rowText returns the text of lines[i], or "" when i is out of range.
func rowText(lines []style.StyledText, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i].String()
}
