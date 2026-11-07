package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
)

// recorder is an extension that implements every hook and records the
// events it is given. Its move hook reports one changed file.
type recorder struct {
	mu      sync.Mutex
	prompts []extension.Prompt
	turns   []agent.Turn
	moves   []extension.Move
}

func (*recorder) Name() string { return "recorder" }

func (r *recorder) OnPrompt(_ context.Context, p extension.Prompt, _ agent.PartSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts = append(r.prompts, p)
}

func (r *recorder) OnTurnEnd(_ context.Context, t agent.Turn, _ agent.PartSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = append(r.turns, t)
}

func (r *recorder) OnMove(_ context.Context, m extension.Move) (extension.MoveResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moves = append(r.moves, m)
	return extension.MoveResult{Files: []extension.FileDiff{{File: "a.txt", Status: "modified"}}}, nil
}

// TestHooks_DescribeTheirEvents checks that each hook is told which session
// and message its event is about: the prompt sent, the turn that answers it,
// and the move back before them, whose files the hook reports.
func TestHooks_DescribeTheirEvents(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	rec := &recorder{}
	a.extensions = []extension.Extension{rec}

	events := a.Subscribe(t.Context())
	send(t, a, "ses_hooks", SendInput{Text: "hi"})
	collectUntilIdle(t, events)
	branch := branchOf(t, a.Sessions(), "ses_hooks")
	if len(branch) != 2 {
		t.Fatalf("stored %d messages, want the prompt and its reply", len(branch))
	}

	rec.mu.Lock()
	prompts, turns := rec.prompts, rec.turns
	rec.mu.Unlock()
	if len(prompts) != 1 {
		t.Fatalf("prompt hook ran %d times, want 1", len(prompts))
	}
	if p := prompts[0]; p.SessionID != "ses_hooks" || p.MessageID != branch[0].ID || p.Text != "hi" || p.Model != branch[0].Model {
		t.Errorf("prompt = %+v, want the stored prompt %q of ses_hooks", p, branch[0].ID)
	}
	if len(turns) != 1 {
		t.Fatalf("turn hook ran %d times, want 1", len(turns))
	}
	if tt := turns[0]; tt.SessionID != "ses_hooks" || tt.MessageID != branch[1].ID || tt.Model != branch[1].Model || tt.Stopped || tt.Err != nil {
		t.Errorf("turn = %+v, want the stored reply %q, completed", tt, branch[1].ID)
	}

	files, err := a.Session("ses_hooks").Undo()
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if len(files) != 1 || files[0].File != "a.txt" {
		t.Errorf("undo reported %+v, want the file the hook changed", files)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.moves) != 1 {
		t.Fatalf("move hook ran %d times, want 1", len(rec.moves))
	}
	if m := rec.moves[0]; m.SessionID != "ses_hooks" || m.Head != "" || len(m.Branch) != 2 {
		t.Errorf("move = %+v, want ses_hooks moved before its first message, on its branch", m)
	}
}

// toolset is an extension that adds tools.
type toolset struct {
	name  string
	tools []agent.Tool
}

func (s toolset) Name() string { return s.name }

func (s toolset) Tools() []agent.Tool { return s.tools }

// loadOf returns a load that loads e.
func loadOf(e extension.Extension) extension.Load {
	return func(context.Context, extension.Host) (extension.Extension, error) { return e, nil }
}

// TestNew_OffersTheToolsOfExtensions checks that the model is offered
// the tools that extensions add, and no others, and that a tool replaces one
// of the same name that an extension loaded before added.
func TestNew_OffersTheToolsOfExtensions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"catalog_source":"http://127.0.0.1:1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	offered := func(loads ...extension.Load) []agent.ToolDefinition {
		t.Helper()
		a, err := New(t.Context(), config.NewService(home), Options{Extensions: loads})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		return a.Tools().WireTools()
	}

	if got := offered(); len(got) != 0 {
		t.Errorf("an app without extensions offers %+v, want no tools", got)
	}
	got := offered(
		loadOf(toolset{"first", []agent.Tool{{Name: "read", Description: "first"}, {Name: "bash"}}}),
		loadOf(&recorder{}),
		loadOf(toolset{"second", []agent.Tool{{Name: "read", Description: "second"}}}),
	)
	want := []agent.ToolDefinition{{Name: "bash"}, {Name: "read", Description: "second"}}
	if !slices.EqualFunc(got, want, func(g, w agent.ToolDefinition) bool { return g.Name == w.Name && g.Description == w.Description }) {
		t.Errorf("offered %+v, want bash and the second extension's read", got)
	}
}
