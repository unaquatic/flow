package agent

import (
	"encoding/json"
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

func TestExpandToolParts_NoToolPartsPassesThrough(t *testing.T) {
	msgs := []Message{
		NewUserMessage("hello"),
		NewAgentMessage("hi"),
	}
	got := ExpandToolParts(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != message.RoleUser || got[1].Role != message.RoleAgent {
		t.Fatalf("roles changed: %q %q", got[0].Role, got[1].Role)
	}
}

func TestExpandToolParts_CompletedSplitsIntoUseAndResult(t *testing.T) {
	msgs := []Message{
		NewUserMessage("read the file"),
		{
			Role: message.RoleAgent,
			Content: []message.ContentPart{
				message.TextPart{Text: "sure"},
				message.ToolPart{
					CallID: "call_1",
					Name:   "read",
					State: message.ToolStateCompleted{
						Input:  json.RawMessage(`{"path":"main.go"}`),
						Output: "package main",
					},
				},
			},
		},
	}

	got := ExpandToolParts(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d (%+v)", len(got), got)
	}

	agentMsg := got[1]
	if agentMsg.Role != message.RoleAgent || len(agentMsg.Content) != 2 {
		t.Fatalf("unexpected agent message: %+v", agentMsg)
	}
	if _, ok := agentMsg.Content[0].(message.TextPart); !ok {
		t.Fatalf("expected TextPart, got %T", agentMsg.Content[0])
	}
	tu, ok := agentMsg.Content[1].(message.ToolUsePart)
	if !ok {
		t.Fatalf("expected ToolUsePart, got %T", agentMsg.Content[1])
	}
	if tu.CallID != "call_1" || tu.Name != "read" {
		t.Fatalf("tool_use mismatched: %+v", tu)
	}

	res := got[2]
	if res.Role != message.RoleUser || len(res.Content) != 1 {
		t.Fatalf("expected user tool_result message, got %+v", res)
	}
	tr, ok := res.Content[0].(message.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", res.Content[0])
	}
	if tr.ToolUseID != "call_1" || tr.Text != "package main" || tr.IsError {
		t.Fatalf("unexpected tool_result: %+v", tr)
	}
}

func TestExpandToolParts_PendingTreatedAsInterrupted(t *testing.T) {
	msgs := []Message{
		{
			Role: message.RoleAgent,
			Content: []message.ContentPart{
				message.ToolPart{
					CallID: "call_42",
					Name:   "bash",
					State:  message.ToolStatePending{Input: json.RawMessage(`{"cmd":"ls"}`)},
				},
			},
		},
	}

	got := ExpandToolParts(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	tr, ok := got[1].Content[0].(message.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", got[1].Content[0])
	}
	if !tr.IsError || tr.Text != message.InterruptedError {
		t.Fatalf("expected interrupted error result, got %+v", tr)
	}
}

// stepPart is metadata that an extension adds to a message, as the snapshot
// extension adds the workspace each message ends with.
type stepPart struct{}

func (stepPart) PartType() message.ContentType { return "step" }
func (stepPart) PartID() message.PartID        { return "" }
func (stepPart) Metadata()                     {}

// TestExpandToolParts_StripsInternalMetadataParts checks that the metadata
// an extension adds to a prompt and a reply, and the reply's usage, are not
// sent.
func TestExpandToolParts_StripsInternalMetadataParts(t *testing.T) {
	msgs := []Message{
		{
			Role:    message.RoleUser,
			Content: []message.ContentPart{message.TextPart{Text: "hello"}, stepPart{}},
		},
		{
			Role: message.RoleAgent,
			Content: []message.ContentPart{
				message.TextPart{Text: "hi"},
				message.UsagePart{Usage: message.Usage{InputTokens: 7, OutputTokens: 3}},
				stepPart{},
			},
		},
	}

	got := ExpandToolParts(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	for i, m := range got {
		if len(m.Content) != 1 {
			t.Fatalf("message %d: expected only the TextPart to survive, got %+v", i, m.Content)
		}
		if _, ok := m.Content[0].(message.TextPart); !ok {
			t.Fatalf("message %d: expected TextPart, got %T", i, m.Content[0])
		}
	}
}

func TestExpandToolParts_DropsMessageThatContainsOnlyMetadata(t *testing.T) {
	msgs := []Message{
		NewUserMessage("hi"),
		{
			Role: message.RoleAgent,
			Content: []message.ContentPart{
				message.UsagePart{Usage: message.Usage{InputTokens: 7}},
				stepPart{},
			},
		},
	}

	got := ExpandToolParts(msgs)
	if len(got) != 1 || got[0].Role != message.RoleUser {
		t.Fatalf("expected metadata-only message to be dropped, got %+v", got)
	}
}

func TestExpandToolParts_ErrorPropagatesIsError(t *testing.T) {
	msgs := []Message{
		{
			Role: message.RoleAgent,
			Content: []message.ContentPart{
				message.ToolPart{
					CallID: "call_3",
					Name:   "read",
					State: message.ToolStateError{
						Input: json.RawMessage(`{"path":"x"}`),
						Error: "permission denied",
					},
				},
			},
		},
	}

	got := ExpandToolParts(msgs)
	tr, ok := got[1].Content[0].(message.ToolResultPart)
	if !ok || !tr.IsError || tr.Text != "permission denied" {
		t.Fatalf("expected error tool_result, got %+v", got[1].Content[0])
	}
}

func TestExpandToolParts_InvalidInputBecomesEmptyObject(t *testing.T) {
	msgs := []Message{{
		Role: message.RoleAgent,
		Content: []message.ContentPart{message.ToolPart{
			CallID: "call_1",
			Name:   "bash",
			State:  message.ToolStateError{Input: json.RawMessage(`{"command":"go te`), Error: message.InterruptedError},
		}},
	}}

	got := ExpandToolParts(msgs)
	use, ok := got[0].Content[0].(message.ToolUsePart)
	if !ok || string(use.Input) != "{}" {
		t.Fatalf("tool use = %+v, want empty object input", got[0].Content[0])
	}
}
