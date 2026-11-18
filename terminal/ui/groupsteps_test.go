package ui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// TestGroupStepsSummary checks that the steps that stack in a reply show,
// once done, as their last row alone, which reads "Worked for" the time from
// the first one's start to the last one's end, that a click on it shows the
// steps again, and that while the reply is at work the last step shows as it
// is, as it does once Ctrl+O expands the steps.
func TestGroupStepsSummary(t *testing.T) {
	t0 := turnT0
	bash := func(id string, from, to time.Duration) message.ToolPart {
		return message.ToolPart{ID: message.PartID(id), CallID: id, Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"ls"}`), Output: "ok", StartedAt: t0.Add(from), EndedAt: t0.Add(to)}}
	}
	msg := agentParts("a1",
		message.ReasoningPart{ID: "a1-think", Text: "Look.", StartedAt: t0, EndedAt: t0.Add(time.Second)},
		bash("c1", 2*time.Second, 14*time.Second),
		bash("c2", 20*time.Second, 32*time.Second),
	)
	cm := NewChatMessageBox(msg, ChatMessageWithGroupSteps(true))
	boxes := partBoxes(cm)
	shown := func() (n int) {
		for _, b := range boxes {
			if !b.Grouped {
				n++
			}
		}
		return n
	}
	last := boxes[len(boxes)-1]
	if shown() != 1 || last.SummaryText != "Worked for 32s" || !last.Summary.Visible() || last.Header.Visible() {
		t.Fatalf("done: %d rows shown, summary %q; want the last alone, reading Worked for 32s", shown(), last.SummaryText)
	}

	cm.SetWorking(true)
	if shown() != 1 || last.SummaryText != "" {
		t.Errorf("working: %d rows shown, summary %q; want the last alone, as it is", shown(), last.SummaryText)
	}
	cm.SetWorking(false)

	cm.SetToolsExpanded(true)
	if shown() != len(boxes) || last.SummaryText != "" {
		t.Errorf("expanded: %d rows shown, summary %q; want all, as they are", shown(), last.SummaryText)
	}
	cm.SetToolsExpanded(false)

	last.OnSummaryClick()
	if shown() != len(boxes) || last.SummaryText != "" {
		t.Errorf("clicked: %d rows shown, summary %q; want all, as they are", shown(), last.SummaryText)
	}
}

// partBoxes returns the boxes of cm's parts, in order.
func partBoxes(cm *ChatMessageBox) []*PartBox {
	var out []*PartBox
	for _, r := range cm.segments[0].ContentBox.Head.ChildrenRaw() {
		if f, ok := r.(Framed); ok {
			out = append(out, f.Frame())
		}
	}
	return out
}
