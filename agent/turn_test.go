package agent_test

import (
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// TestTurnState_TimesReasoning checks that a reasoning part records when its
// first chunk streamed and when its last did, and that a block that starts
// with metadata alone is timed from its first chunk.
func TestTurnState_TimesReasoning(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	s := agent.NewTurnState("msg_1", agent.Variant{Provider: "test", Model: "test-model"}, func(agent.Event) {})
	s.Now = func() time.Time { return now }

	s.Handle(agent.ReasoningMetadata{Block: 1, Metadata: message.TextMetadata("ns", "sig", "x")})
	now = t0.Add(time.Second)
	s.Handle(agent.ReasoningDelta{Block: 1, Text: "Think"})
	now = t0.Add(12 * time.Second)
	s.Handle(agent.ReasoningDelta{Block: 1, Text: "ing."})
	now = t0.Add(20 * time.Second)
	s.Handle(agent.TextDelta{Block: 2, Text: "Done."})

	rp, ok := s.Parts[0].(message.ReasoningPart)
	if !ok {
		t.Fatalf("part 0 = %T, want a reasoning part", s.Parts[0])
	}
	if want := t0.Add(time.Second); !rp.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", rp.StartedAt, want)
	}
	if want := t0.Add(12 * time.Second); !rp.EndedAt.Equal(want) {
		t.Errorf("EndedAt = %v, want %v", rp.EndedAt, want)
	}
	if got, want := rp.Duration(), 11*time.Second; got != want {
		t.Errorf("Duration = %v, want %v", got, want)
	}
}
