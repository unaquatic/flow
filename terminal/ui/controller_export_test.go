package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// storeChat stores a session titled title that holds a prompt and its reply,
// and returns its ID.
func storeChat(t *testing.T, a *app.App, title string) message.SessionID {
	t.Helper()
	id := message.NewSessionID()
	for _, m := range []message.Content{
		{ID: message.NewID(), Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: "Run the tests"}}},
		{ID: message.NewID(), Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "All pass."}}},
	} {
		if err := a.Sessions().AppendMessage(t.Context(), id, &m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Sessions().SetTitle(t.Context(), id, title); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLastReply checks that /copy takes the text of the latest reply that
// holds any, skipping notices and replies of tool calls alone.
func TestLastReply(t *testing.T) {
	tool := message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: json.RawMessage(`{}`)}}
	tests := []struct {
		name string
		msgs []ChatMessage
		want string
		ok   bool
	}{
		{name: "empty"},
		{name: "prompt alone", msgs: []ChatMessage{userText("u1", "hi")}},
		{name: "latest reply", ok: true, want: "second",
			msgs: []ChatMessage{agentText("a1", "first"), agentText("a2", "second")}},
		{name: "after tool calls and notices", ok: true, want: "Done.\n\nAll pass.",
			msgs: []ChatMessage{
				agentParts("a1", message.TextPart{Text: " Done. "}, message.TextPart{Text: "All pass."}),
				agentParts("a2", tool),
				{ID: "n1", Notice: &Notice{Text: "Chat cleared"}},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := LastReply(tt.msgs); got != tt.want || ok != tt.ok {
				t.Errorf("LastReply = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestCopyCommand checks that /copy asks the UI to copy the last reply, or
// says there is none, and that the UI's report of the copy is a notice.
func TestCopyCommand(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")
	if got, want := toasts(s.HandleSubmit("/copy")), []string{"No reply to copy"}; !slices.Equal(got, want) {
		t.Errorf("notices = %q, want %q", got, want)
	}
	s.state.Messages = append(s.state.Messages, userText("u1", "hi"), agentText("a1", "hello"))
	if out := s.HandleSubmit("/copy"); !slices.Contains(out, UIEvent(EventClipboardCopy{Text: "hello"})) {
		t.Errorf("/copy = %#v, want the reply copied", out)
	}
	if got, want := toasts(s.CompleteCopy(nil)), []string{"Copied the last reply to the clipboard"}; !slices.Equal(got, want) {
		t.Errorf("notices = %q, want %q", got, want)
	}
}

// TestOSC52 checks the escape sequence that sets the clipboard.
func TestOSC52(t *testing.T) {
	if got, want := OSC52("hi"), "\x1b]52;c;aGk=\x1b\\"; got != want {
		t.Errorf("OSC52 = %q, want %q", got, want)
	}
}

// TestTranscriptMarkdown checks the Markdown /export writes: a heading for
// each run of messages of one role, their text, and each tool call fenced,
// leaving out reasoning and notices, with a fence longer than the backticks
// its text holds.
func TestTranscriptMarkdown(t *testing.T) {
	msgs := []ChatMessage{
		{ID: "n1", Notice: &Notice{Text: "Chat cleared"}},
		userText("u1", "Run the tests"),
		agentParts("a1",
			message.ReasoningPart{Text: "thinking"},
			message.TextPart{Text: "Running them."},
			message.ToolPart{Name: "bash", State: message.ToolStateCompleted{
				Input: json.RawMessage(`{"command":"go test"}`), Output: "ok ```"}},
		),
		agentParts("a2", message.ToolPart{Name: "read", State: message.ToolStateError{
			Input: json.RawMessage(`{"path":"x"}`), Error: "no such file"}}),
	}
	want := "# Tests\n" +
		"\n## User\n\nRun the tests\n" +
		"\n## Agent\n\nRunning them.\n" +
		"\n### bash\n\n```json\n{\"command\":\"go test\"}\n```\n\n````\nok ```\n````\n" +
		"\n### read\n\n```json\n{\"path\":\"x\"}\n```\n\n```\nError: no such file\n```\n"
	if got := TranscriptMarkdown("Tests", msgs); got != want {
		t.Errorf("TranscriptMarkdown =\n%s\nwant\n%s", got, want)
	}
	if got := TranscriptMarkdown("", nil); got != "# "+UntitledName+"\n" {
		t.Errorf("untitled = %q", got)
	}
}

// TestExportCommand checks that /export writes the chat to a Markdown file
// in the working directory and names it, and that an empty chat writes none.
func TestExportCommand(t *testing.T) {
	s, a := newTestService(t)
	s.InitSession(t.Context(), "")
	if got, want := toasts(s.HandleSubmit("/export")), []string{"Nothing to export"}; !slices.Equal(got, want) {
		t.Errorf("notices = %q, want %q", got, want)
	}
	s.HandleSessionSelect(storeChat(t, a, "Tests"))

	out := s.HandleSubmit("/export")
	name := ExportName(s.SessionID())
	if got, want := toasts(out), []string{"Exported the chat to " + name}; !slices.Equal(got, want) {
		t.Errorf("notices = %q, want %q", got, want)
	}
	data, err := os.ReadFile(filepath.Join(a.WorkingDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Tests\n\n## User\n\nRun the tests\n\n## Agent\n\nAll pass.\n"
	if string(data) != want {
		t.Errorf("export =\n%s\nwant\n%s", data, want)
	}
}
