package app

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
)

// toolReply returns a reply that called a tool, whose call ended in state.
func toolReply(id message.ID, state message.ToolState) message.Content {
	return message.Content{ID: id, Role: message.RoleAgent, Parts: []message.ContentPart{
		message.TextPart{Text: "Reading the file."},
		message.ToolPart{CallID: "call_" + string(id), Name: "read", State: state},
	}}
}

// readInput is the input of a read call.
var readInput = json.RawMessage(`{"path":"calc.go"}`)

// TestContinue_AnswersThePromptTheTranscriptEndsAt checks that continuing a
// transcript that ends at a prompt, as a failed run leaves it, answers the
// prompt without storing another.
func TestContinue_AnswersThePromptTheTranscriptEndsAt(t *testing.T) {
	a, gw := newActorTestApp(t, "answer")
	const id message.SessionID = "ses_continue_prompt"
	appendMessage(t, a.sessions, id, &message.Content{
		ID:    "msg_prompt",
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "fix the tests"}},
	})

	events := a.Subscribe(t.Context())
	if err := a.Session(id).Continue(""); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	got := collectUntilIdle(t, events)

	for _, ev := range eventsOfType[EventMessageStarted](got) {
		if ev.Message.Role == message.RoleUser {
			t.Errorf("continue published prompt %s, want no new prompt", ev.Message.ID)
		}
	}
	if len(gw.StreamCalls) != 1 || len(gw.StreamCalls[0]) != 1 {
		t.Fatalf("gateway saw %v, want one call with the stored prompt alone", gw.StreamCalls)
	}

	msgs := branchOf(t, a.Sessions(), id)
	if len(msgs) != 2 || msgs[0].ID != "msg_prompt" || msgs[1].Role != message.RoleAgent {
		t.Fatalf("transcript = %d messages, want the prompt and its answer", len(msgs))
	}
	if p := msgs[1].ParentID; p == nil || *p != "msg_prompt" {
		t.Errorf("answer parent = %v, want the prompt", p)
	}
}

// TestContinue_ForksFromToolCalls checks that continuing from tool calls that
// already have a reply sends their results and stores the new reply beside
// the old one, as a branch.
func TestContinue_ForksFromToolCalls(t *testing.T) {
	a, gw := newActorTestApp(t, "more")
	const id message.SessionID = "ses_continue_fork"
	tools := toolReply("msg_tools", message.ToolStateRunning{Input: readInput})
	for _, m := range []message.Content{
		{ID: "msg_prompt", Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: "hi"}}},
		tools,
		{ID: "msg_later", Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "two"}}},
	} {
		appendMessage(t, a.sessions, id, &m)
	}
	if err := a.sessions.SetHead(t.Context(), id, session.Head{ID: "msg_tools", Forward: "msg_later"}); err != nil {
		t.Fatal(err)
	}

	events := a.Subscribe(t.Context())
	if err := a.Session(id).Continue(""); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	collectUntilIdle(t, events)

	// The prompt, the call, and its interrupted result.
	sent := gw.StreamCalls[0]
	if len(sent) != 3 || sent[2].Role != message.RoleUser {
		t.Fatalf("history = %d messages, want the prompt, the call, and its result", len(sent))
	}
	if res, ok := sent[2].Content[0].(message.ToolResultPart); !ok || !res.IsError || res.Text != message.InterruptedError {
		t.Errorf("result = %+v, want the interrupted call's error", sent[2].Content[0])
	}
	children, err := a.sessions.Children(t.Context(), id, "msg_tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || children[0].ID != "msg_later" {
		t.Errorf("tool reply has %d children, want the later reply and the new one", len(children))
	}
}

// TestContinue_ContinuesAReply checks that continuing a transcript that ends
// at a reply sends it as it stands, ending at the reply, and stores the new
// reply below it.
func TestContinue_ContinuesAReply(t *testing.T) {
	a, gw := newActorTestApp(t, "more")
	const id message.SessionID = "ses_continue_reply"
	for _, m := range []message.Content{
		{ID: "msg_prompt", Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: "hi"}}},
		{ID: "msg_reply", Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "Half an ans"}}},
	} {
		appendMessage(t, a.sessions, id, &m)
	}

	events := a.Subscribe(t.Context())
	if err := a.Session(id).Continue(""); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	collectUntilIdle(t, events)

	sent := gw.StreamCalls[0]
	if len(sent) != 2 || sent[1].Role != message.RoleAgent {
		t.Fatalf("history = %d messages, want the prompt and the reply last", len(sent))
	}
	msgs := branchOf(t, a.Sessions(), id)
	if len(msgs) != 3 || msgs[2].Role != message.RoleAgent {
		t.Fatalf("transcript = %d messages, want the new reply after the old", len(msgs))
	}
	if p := msgs[2].ParentID; p == nil || *p != "msg_reply" {
		t.Errorf("new reply parent = %v, want the old reply", p)
	}
}

// TestContinue_NothingToContinue checks that an empty transcript is refused
// before any run starts.
func TestContinue_NothingToContinue(t *testing.T) {
	a, gw := newActorTestApp(t, "unused")
	const id message.SessionID = "ses_continue_empty"
	if err := a.Session(id).Continue(""); !errors.Is(err, ErrNothingToContinue) {
		t.Errorf("Continue = %v, want ErrNothingToContinue", err)
	}
	for _, err := range a.Session(id).ContinueStream("") {
		if !errors.Is(err, ErrNothingToContinue) {
			t.Errorf("ContinueStream = %v, want ErrNothingToContinue", err)
		}
	}
	if a.Session(id).Busy() || len(gw.StreamCalls) != 0 {
		t.Error("a refused continue started a run")
	}
}

// TestContinueStream checks that a streamed continue yields its run's events,
// from its start to its end, without a new prompt.
func TestContinueStream(t *testing.T) {
	a, _ := newActorTestApp(t, "answer")
	const id message.SessionID = "ses_continue_stream"
	appendMessage(t, a.sessions, id, &message.Content{
		ID: "msg_prompt", Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
	})

	var types []EventType
	for ev, err := range a.Session(id).ContinueStream("") {
		if err != nil {
			t.Fatalf("ContinueStream: %v", err)
		}
		if started, ok := ev.(EventMessageStarted); ok && started.Message.Role == message.RoleUser {
			t.Errorf("continue published prompt %s, want no new prompt", started.Message.ID)
		}
		types = append(types, ev.EventType())
	}
	if len(types) < 2 || types[0] != TypeAgentStarted || types[len(types)-1] != TypeAgentEnded {
		t.Fatalf("events = %v, want the run's start first and its end last", types)
	}
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 2 {
		t.Errorf("transcript = %d messages, want the prompt and its answer", len(msgs))
	}
}
