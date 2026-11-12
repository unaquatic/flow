package titling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
)

// load loads the extension with settings, which is a JSON object or empty
// for none.
func load(t *testing.T, settings string) extension.Extension {
	t.Helper()
	cfg := &config.Config{}
	if settings != "" {
		cfg.Extensions = map[string]json.RawMessage{Name: json.RawMessage(settings)}
	}
	e, err := Load(t.Context(), extension.Host{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// replier is a fake extension.Reply that replies with reply, or fails with
// err, and records the model and system message of each call.
type replier struct {
	reply   string
	err     error
	models  []string
	systems []string
}

func (r *replier) Reply(_ context.Context, model, system string) (*agent.Response, error) {
	r.models = append(r.models, model)
	r.systems = append(r.systems, system)
	if r.err != nil {
		return nil, r.err
	}
	return &agent.Response{Message: agent.NewAgentMessage(r.reply)}, nil
}

// titling returns the titling of a session titled title, replied to by r.
func titling(r *replier, title string) extension.Titling {
	return extension.Titling{
		Prompt:  extension.Prompt{SessionID: "ses_a", MessageID: "msg_a", Text: "fix the login loop", Model: "provider-a/lab-a/model-a:high"},
		Prompts: []string{"fix the login loop"},
		Title:   title,
		Reply:   r.Reply,
	}
}

// TestLoad checks that settings that turn titling off load no extension,
// and that others load one with their model.
func TestLoad(t *testing.T) {
	if e := load(t, `{"enabled": false}`); e != nil {
		t.Errorf("disabled settings loaded %v, want none", e)
	}
	e, ok := load(t, `{"model": " provider-a/lab-a/model-small "}`).(*Extension)
	if !ok || e.Model != "provider-a/lab-a/model-small" {
		t.Fatalf("loaded %+v, want the extension with the model the settings name", e)
	}
}

// TestTitle checks that the extension asks for the thread's reply with
// Instructions as its system message, from the prompt's model or the one
// its settings name, and returns the reply as the model wrote it, without
// the spaces around it.
func TestTitle(t *testing.T) {
	ctx := t.Context()
	e := load(t, "").(*Extension)
	r := &replier{reply: "\n  Fix the login loop!  \n"}

	title, err := e.Title(ctx, titling(r, "Login bug"))
	if err != nil || title != "Fix the login loop!" {
		t.Fatalf("Title = %q, %v; want the reply as the model wrote it, without the spaces around it", title, err)
	}
	if !slices.Equal(r.models, []string{""}) {
		t.Errorf("asked %q, want the prompt's model, named by none", r.models)
	}
	if !slices.Equal(r.systems, []string{Instructions}) {
		t.Errorf("asked with system %q, want Instructions", r.systems)
	}

	e.Model = "provider-a/lab-a/model-small"
	if _, err := e.Title(ctx, titling(r, "")); err != nil {
		t.Fatal(err)
	}
	if got := r.models[1]; got != "provider-a/lab-a/model-small" {
		t.Errorf("asked %q, want the model the settings name", got)
	}
}

// TestTitleFails checks that a failed reply fails, as does a blank one.
func TestTitleFails(t *testing.T) {
	ctx := t.Context()
	e := load(t, "").(*Extension)
	errDown := errors.New("down")
	if _, err := e.Title(ctx, titling(&replier{err: errDown}, "")); !errors.Is(err, errDown) {
		t.Errorf("failed reply = %v, want %v", err, errDown)
	}
	if _, err := e.Title(ctx, titling(&replier{reply: " \n\t\n"}, "")); !errors.Is(err, ErrNoTitle) {
		t.Errorf("blank reply = %v, want ErrNoTitle", err)
	}
}

// TestInstructions checks that the instructions open with the length limit,
// and use no symbols or quotes themselves.
func TestInstructions(t *testing.T) {
	first, _, _ := strings.Cut(Instructions, "\n")
	if want := fmt.Sprintf("at most %d characters", MaxTitleRunes); !strings.Contains(first, want) {
		t.Errorf("first line %q does not hold %q", first, want)
	}
	if strings.ContainsFunc(Instructions, unicode.IsSymbol) || strings.ContainsAny(Instructions, "\"'`") {
		t.Errorf("instructions hold symbols or quotes: %q", Instructions)
	}
}
