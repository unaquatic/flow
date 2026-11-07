package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

// streamOf returns a stream that yields parts in order.
func streamOf(parts ...StreamPart) StreamResponse {
	return func(yield func(StreamPart, error) bool) {
		for _, p := range parts {
			if !yield(p, nil) {
				return
			}
		}
	}
}

// TestCollect_AssemblesFromParts checks that Collect builds the message from
// the stream's parts alone, in the order its blocks and calls start: each
// reasoning block with its signature, also one with no text, each text
// block, and every call with its own input, from interleaved deltas or from
// its end, which replaces them, or an empty object if none arrived.
func TestCollect_AssemblesFromParts(t *testing.T) {
	resp, err := streamOf(
		ReasoningDelta{Block: 0, Text: "let me "},
		ReasoningDelta{Block: 0, Text: "look"},
		ReasoningSignature{Block: 0, Signature: "sig-0"},
		TextDelta{Block: 1, Text: "Looking."},
		ToolUseStart{ID: "call_a", Name: "bash"},
		ToolUseStart{ID: "call_b", Name: "bash"},
		ToolUseStart{ID: "call_c", Name: "bash"},
		ToolUseStart{ID: "call_d", Name: "bash"},
		ToolUseDelta{ID: "call_b", JSON: `{"command":`},
		ToolUseDelta{ID: "call_a", JSON: `{"command":"pwd"}`},
		ToolUseDelta{ID: "call_b", JSON: `"ls"}`},
		ToolUseDelta{ID: "call_c", JSON: `{"comm`},
		ToolUseEnd{ID: "call_c", Input: json.RawMessage(`{"command":"cat go.mod"}`)},
		TextDelta{Block: 6, Text: "Then more."},
		ReasoningSignature{Block: 7, Signature: "sig-7"},
		Usage{InputTokens: 9},
		Stop{Reason: StopToolUse},
	).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := []message.ContentPart{
		message.ReasoningPart{Text: "let me look", Signature: "sig-0"},
		message.TextPart{Text: "Looking."},
		message.ToolUsePart{CallID: "call_a", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
		message.ToolUsePart{CallID: "call_b", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
		message.ToolUsePart{CallID: "call_c", Name: "bash", Input: json.RawMessage(`{"command":"cat go.mod"}`)},
		message.ToolUsePart{CallID: "call_d", Name: "bash", Input: json.RawMessage(`{}`)},
		message.TextPart{Text: "Then more."},
		message.ReasoningPart{Signature: "sig-7"},
	}
	if !reflect.DeepEqual(resp.Message.Content, want) {
		t.Errorf("content = %+v\nwant      %+v", resp.Message.Content, want)
	}
	if resp.StopReason != StopToolUse || resp.Usage.InputTokens != 9 {
		t.Errorf("stop reason, usage = %q, %+v, want %q and the usage", resp.StopReason, resp.Usage, StopToolUse)
	}
}
