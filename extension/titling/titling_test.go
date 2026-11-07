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
	"github.com/unaquatic/flow/agent/message"
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

// fork is a fake extension.Fork that replies with reply, or fails with err,
// and records the model and messages of each call.
type fork struct {
	reply  string
	err    error
	models []string
	asks   [][]agent.Message
}

func (f *fork) Fork(_ context.Context, model string, msgs ...agent.Message) (*agent.Response, error) {
	f.models = append(f.models, model)
	f.asks = append(f.asks, msgs)
	if f.err != nil {
		return nil, f.err
	}
	return &agent.Response{Message: agent.NewAgentMessage(f.reply)}, nil
}

// titling returns the titling of a session titled title, forked with f.
func titling(f *fork, title string) extension.Titling {
	return extension.Titling{
		Prompt:  extension.Prompt{SessionID: "ses_a", MessageID: "msg_a", Text: "fix the login loop", Model: "provider-a/model-a:high"},
		History: []agent.Message{agent.NewUserMessage("fix the login loop")},
		Title:   title,
		Fork:    f.Fork,
	}
}

// TestLoad checks that settings that turn titling off load no extension,
// and that others load one with their model.
func TestLoad(t *testing.T) {
	if e := load(t, `{"enabled": false}`); e != nil {
		t.Errorf("disabled settings loaded %v, want none", e)
	}
	e, ok := load(t, `{"model": " provider-a/model-small "}`).(*Extension)
	if !ok || e.Model != "provider-a/model-small" {
		t.Fatalf("loaded %+v, want the extension with the model the settings name", e)
	}
}

// TestTitle checks that the extension forks the session with Ask, to the
// prompt's model or the one its settings name, and returns the reply as the
// model wrote it, without the spaces around it.
func TestTitle(t *testing.T) {
	ctx := t.Context()
	e := load(t, "").(*Extension)
	f := &fork{reply: "\n  Fix the login loop!  \n"}

	title, err := e.Title(ctx, titling(f, "Login bug"))
	if err != nil || title != "Fix the login loop!" {
		t.Fatalf("Title = %q, %v; want the reply as the model wrote it, without the spaces around it", title, err)
	}
	if !slices.Equal(f.models, []string{""}) {
		t.Errorf("forked to %q, want the prompt's model, named by none", f.models)
	}
	if len(f.asks) != 1 || len(f.asks[0]) != 1 || f.asks[0][0].TextContent() != Ask("Login bug").TextContent() {
		t.Errorf("forked with %+v, want Ask of the current title", f.asks)
	}

	e.Model = "provider-a/model-small"
	if _, err := e.Title(ctx, titling(f, "")); err != nil {
		t.Fatal(err)
	}
	if got := f.models[1]; got != "provider-a/model-small" {
		t.Errorf("forked to %q, want the model the settings name", got)
	}
}

// TestTitleFails checks that a failed fork fails, as does a blank reply.
func TestTitleFails(t *testing.T) {
	ctx := t.Context()
	e := load(t, "").(*Extension)
	errDown := errors.New("down")
	if _, err := e.Title(ctx, titling(&fork{err: errDown}, "")); !errors.Is(err, errDown) {
		t.Errorf("failed fork = %v, want %v", err, errDown)
	}
	if _, err := e.Title(ctx, titling(&fork{reply: " \n\t\n"}, "")); !errors.Is(err, ErrNoTitle) {
		t.Errorf("blank reply = %v, want ErrNoTitle", err)
	}
}

// TestAsk checks that the message is the user's, with the instructions and
// then the current title, or the instructions alone when there is none.
func TestAsk(t *testing.T) {
	if m := Ask(""); m.Role != message.RoleUser || m.TextContent() != Instructions {
		t.Errorf("Ask(\"\") = %+v, want the instructions alone, from the user", m)
	}
	if got, want := Ask("Fix the login loop").TextContent(), Instructions+"\n\nCurrent summary: Fix the login loop"; got != want {
		t.Errorf("Ask = %q, want %q", got, want)
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
