package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// TestPromptText checks that the prompt is the arguments, joined by spaces,
// and what standard input holds, a blank line below them, and that a blank
// prompt fails.
func TestPromptText(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
		err   error
	}{
		{name: "arguments", args: []string{"fix", "the tests"}, want: "fix the tests"},
		{name: "standard input", stdin: strings.NewReader("diff --git a/x b/x\n"), want: "diff --git a/x b/x\n"},
		{name: "both", args: []string{"review this"}, stdin: strings.NewReader("+ added\n"), want: "review this\n\n+ added\n"},
		{name: "blank input", args: []string{"hi"}, stdin: strings.NewReader(" \n"), want: "hi"},
		{name: "nothing", err: errNoPrompt},
		{name: "blank", args: []string{" "}, stdin: strings.NewReader("\n"), err: errNoPrompt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := promptText(tt.args, tt.stdin)
			if !errors.Is(err, tt.err) || got != tt.want {
				t.Errorf("promptText = %q, %v; want %q, %v", got, err, tt.want, tt.err)
			}
		})
	}
}

// TestOutputFormat checks the names --format takes, in any case, and that
// it refuses others.
func TestOutputFormat(t *testing.T) {
	for in, want := range map[string]OutputFormat{"text": OutputText, "JSON": OutputJSON, " json ": OutputJSON} {
		var f OutputFormat
		if err := f.UnmarshalText([]byte(in)); err != nil || f != want {
			t.Errorf("format %q = %q, %v; want %q", in, f, err, want)
		}
	}
	var f OutputFormat
	if err := f.UnmarshalText([]byte("yaml")); err == nil {
		t.Errorf("format yaml = %q, want an error", f)
	}
}

// runEvents returns the events of a run that answers a prompt with two
// agent messages: one that streams a text part in two chunks beside its
// reasoning, and one that says "Done." on a line of its own.
func runEvents() []app.Event {
	const sid message.SessionID = "ses_run"
	base := app.EventBase{SessionID: sid}
	change := func(id message.ID, part message.ContentPart) app.PartChange {
		return app.PartChange{EventBase: base, MessageID: id, Part: part}
	}
	prompt := message.TextPart{ID: "prt_prompt", Text: "fix the tests"}
	thinking := message.ReasoningPart{ID: "prt_think", Text: "Looking."}
	first, second := message.TextPart{ID: "prt_1", Text: "Fixed"}, message.TextPart{ID: "prt_2", Text: "Done.\n"}
	grown := first
	grown.Text = "Fixed the tests."
	msg := func(id message.ID, role message.Role, parts ...message.ContentPart) app.Message {
		return app.Message{Content: message.Content{ID: id, Role: role, Parts: parts}}
	}
	return []app.Event{
		app.EventAgentStarted{EventBase: base},
		app.EventMessageStarted{EventBase: base, Message: msg("msg_prompt", message.RoleUser, prompt)},
		app.EventMessageEnded{EventBase: base, Message: msg("msg_prompt", message.RoleUser, prompt)},
		app.EventMessageStarted{EventBase: base, Message: msg("msg_1", message.RoleAgent)},
		app.EventPartStarted{PartChange: change("msg_1", thinking)},
		app.EventPartEnded{PartChange: change("msg_1", thinking)},
		app.EventPartStarted{PartChange: change("msg_1", first)},
		app.EventPartUpdated{PartChange: change("msg_1", grown)},
		app.EventPartEnded{PartChange: change("msg_1", grown)},
		app.EventMessageEnded{EventBase: base, Message: msg("msg_1", message.RoleAgent, thinking, grown), Finish: agent.StopToolUse},
		app.EventMessageStarted{EventBase: base, Message: msg("msg_2", message.RoleAgent)},
		app.EventPartStarted{PartChange: change("msg_2", second)},
		app.EventPartEnded{PartChange: change("msg_2", second)},
		app.EventMessageEnded{EventBase: base, Message: msg("msg_2", message.RoleAgent, second), Finish: agent.StopEndTurn},
		app.EventAgentEnded{EventBase: base},
	}
}

// TestRunPrinterText checks that text output is the last agent message's
// text alone, written once the run has finished, without the prompt, the
// reasoning, or the text of the steps before it.
func TestRunPrinterText(t *testing.T) {
	var out strings.Builder
	p := NewRunPrinter(&out, OutputText)
	for _, ev := range runEvents() {
		if err := p.Print(ev); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("output during the run = %q, want none", out.String())
	}
	if err := p.WriteAnswer(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Done.\n"; got != want {
		t.Errorf("text output = %q, want %q", got, want)
	}
}

// TestWriteAnswer checks that the answer ends with a line break, which it
// gains when it has none, and that a run without one writes nothing.
func TestWriteAnswer(t *testing.T) {
	for answer, want := range map[string]string{"Done.": "Done.\n", "Done.\n": "Done.\n", "": ""} {
		var out strings.Builder
		p := NewRunPrinter(&out, OutputText)
		p.Answer = answer
		if err := p.WriteAnswer(); err != nil || out.String() != want {
			t.Errorf("answer %q wrote %q, %v; want %q", answer, out.String(), err, want)
		}
	}
}

// TestRunPrinterJSON checks that JSON output is each event on a line of its
// own, an object with its type first and then the members the HTTP API
// streams it with.
func TestRunPrinterJSON(t *testing.T) {
	var out strings.Builder
	p := NewRunPrinter(&out, OutputJSON)
	events := runEvents()
	for _, ev := range events {
		if err := p.Print(ev); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(events) {
		t.Fatalf("JSON output has %d lines, want %d:\n%s", len(lines), len(events), out.String())
	}
	for i, line := range lines {
		ev := events[i]
		prefix := `{"type":"` + string(ev.EventType()) + `","session_id":"ses_run",`
		if !strings.HasPrefix(line, prefix) {
			t.Errorf("line %d = %s, want it to start with %s", i, line, prefix)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is no JSON object: %v", i, err)
		}
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]any
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		want["type"] = string(ev.EventType())
		if g, w := mustJSON(t, got), mustJSON(t, want); g != w {
			t.Errorf("line %d = %s, want %s", i, g, w)
		}
	}
	if got, want := p.Answer, "Done.\n"; got != want {
		t.Errorf("answer = %q, want %q", got, want)
	}
}

// mustJSON returns v as JSON, whose object members are sorted.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// newRunTestApp returns an app over a new data directory, in a new working
// directory, whose default model is test-model of the mock provider, which
// answers every request with answer. The test ends by closing it.
func newRunTestApp(t *testing.T, answer string) (*app.App, *agenttest.Gateway) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv(keyEnv, "")
	home := t.TempDir()
	settings := `{"default_provider":"mock","default_model":"test-model","snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t,
		`{"provider-a":{"id":"provider-a","format":"responses","env":["`+keyEnv+`"],"models":{}}}`))
	gw := &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse(answer), nil }}
	a, err := app.New(t.Context(), config.NewService(home), app.Options{Providers: agenttest.NewProviders(t, gw)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, gw
}

// TestRunPrompt checks that a run prints the final answer, writes it to
// the output file, and stores the prompt and the answer in a new session, which
// --continue then sends the next prompt to.
func TestRunPrompt(t *testing.T) {
	a, _ := newRunTestApp(t, "All green.")
	answer := filepath.Join(t.TempDir(), "answer.txt")

	var out strings.Builder
	if err := runPrompt(t.Context(), &out, a, RunOptions{Format: OutputText, Output: answer}, "fix the tests"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "All green.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(answer); err != nil || string(data) != "All green." {
		t.Errorf("answer file = %q, %v; want %q", data, err, "All green.")
	}
	sessions, err := a.Sessions().ListSessions(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %d, %v; want the one the run stored", len(sessions), err)
	}
	id := sessions[0].ID

	out.Reset()
	if err := runPrompt(t.Context(), &out, a, RunOptions{Format: OutputText, Continue: true}, "and the lint"); err != nil {
		t.Fatal(err)
	}
	sessions, err = a.Sessions().ListSessions(t.Context())
	if err != nil || len(sessions) != 1 || sessions[0].ID != id {
		t.Fatalf("sessions after --continue = %+v, %v; want %s alone", sessions, err, id)
	}
	msgs, err := a.Session(id).Messages()
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, m := range msgs {
		if m.Role == message.RoleUser {
			prompts = append(prompts, messageText(m.Parts))
		}
	}
	if got := strings.Join(prompts, "|"); got != "fix the tests|and the lint" {
		t.Errorf("prompts of %s = %q, want both runs' prompts", id, got)
	}
}

// TestRunPromptUnknownSession checks that --session refuses a session that
// is not stored, and stores none.
func TestRunPromptUnknownSession(t *testing.T) {
	a, gw := newRunTestApp(t, "hi")
	err := runPrompt(t.Context(), new(strings.Builder), a, RunOptions{Format: OutputText, Session: "ses_missing"}, "hi")
	if err == nil || !strings.Contains(err.Error(), "ses_missing") {
		t.Errorf("run on a missing session: err = %v, want it to name the session", err)
	}
	if len(gw.StreamCalls) != 0 {
		t.Errorf("gateway saw %d requests, want none", len(gw.StreamCalls))
	}
}

// TestRunPromptFailure checks that a run whose model fails reports the
// failure and writes no answer file.
func TestRunPromptFailure(t *testing.T) {
	a, gw := newRunTestApp(t, "")
	gw.StreamFunc = func() (agent.StreamResponse, error) { return nil, errors.New("upstream down") }
	answer := filepath.Join(t.TempDir(), "answer.txt")
	err := runPrompt(t.Context(), new(strings.Builder), a, RunOptions{Format: OutputText, Output: answer}, "hi")
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Errorf("failed run: err = %v, want the model's failure", err)
	}
	if _, err := os.Stat(answer); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("answer file after a failed run: %v, want none", err)
	}
}

// TestRunExclusiveSessionFlags runs the run command with session flags that
// conflict: two that choose a session, --no-session with one that chooses
// a stored session, and --dir with --session, which works in the session's
// own folder. The command fails before Run, so no app starts.
func TestRunExclusiveSessionFlags(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "--session", "ses_1"}, "--continue and --session"},
		{[]string{"--no-session", "-c"}, "--no-session and --continue"},
		{[]string{"--no-session", "--session", "ses_1"}, "--no-session and --session"},
		{[]string{"--dir", "..", "--session", "ses_1"}, "--dir and --session"},
	} {
		args := append(append([]string{"run"}, tt.args...), "hi")
		want := "flow run: use only one of " + tt.want
		if err := rootCmd.Execute(t.Context(), args); err == nil || err.Error() != want {
			t.Errorf("execute %q: err = %v, want %q", args, err, want)
		}
	}
}

// TestRunPromptNoSession checks that a run with NoSession prints the answer
// and writes its file, and keeps no session, whether it finishes or fails.
func TestRunPromptNoSession(t *testing.T) {
	a, gw := newRunTestApp(t, "All green.")
	answer := filepath.Join(t.TempDir(), "answer.txt")
	var out strings.Builder
	if err := runPrompt(t.Context(), &out, a, RunOptions{Format: OutputText, Output: answer, NoSession: true}, "fix the tests"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "All green.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(answer); err != nil || string(data) != "All green." {
		t.Errorf("answer file = %q, %v; want %q", data, err, "All green.")
	}
	if sessions, err := a.Sessions().ListSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Errorf("sessions after the run = %+v, %v; want none", sessions, err)
	}

	gw.StreamFunc = func() (agent.StreamResponse, error) { return nil, errors.New("upstream down") }
	err := runPrompt(t.Context(), new(strings.Builder), a, RunOptions{Format: OutputText, NoSession: true}, "hi")
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Errorf("failed run: err = %v, want the model's failure", err)
	}
	if sessions, err := a.Sessions().ListSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Errorf("sessions after the failed run = %+v, %v; want none", sessions, err)
	}
}

// TestEnterDir checks that --dir makes its folder the working directory,
// that no --dir leaves it, and that a folder that does not exist fails.
func TestEnterDir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("sub")
	if err != nil {
		t.Fatal(err)
	}
	if err := enterDir(""); err != nil {
		t.Fatal(err)
	}
	if err := enterDir("sub"); err != nil {
		t.Fatal(err)
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if g, w := evalPath(t, got), evalPath(t, want); g != w {
		t.Errorf("working directory = %q, want %q", g, w)
	}
	if err := enterDir("missing"); err == nil || !strings.HasPrefix(err.Error(), "--dir: ") {
		t.Errorf("enterDir(missing) = %v, want an error about --dir", err)
	}
}

// evalPath returns path with its symbolic links resolved, as a temporary
// folder has them on some systems.
func evalPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
