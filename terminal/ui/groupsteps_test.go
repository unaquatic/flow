package ui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// TestGroupStepsSummary checks that the steps that stack in a reply show,
// once done, as their last row alone, which reads "Worked" and the time from
// the first one's start to the last one's end, that a click on it shows the
// steps again, that while the reply is at work they show no row and the
// bubble names the last step for the indicator, that the turn's footer
// stands for them right under it, and that Ctrl+O shows them all.
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
	if shown() != 1 || last.SummaryText != "Worked [32s]" || !last.Summary.Visible() || last.Header.Visible() {
		t.Fatalf("done: %d rows shown, summary %q; want the last alone, reading Worked [32s]", shown(), last.SummaryText)
	}

	cm.SetWorking(true)
	if shown() != 0 || cm.StepLabel() != "Bash ls" {
		t.Errorf("working: %d rows shown, step %q; want none, and the step Bash ls", shown(), cm.StepLabel())
	}
	cm.SetWorking(false)
	if cm.StepLabel() != "" {
		t.Errorf("done: step %q, want none", cm.StepLabel())
	}

	cm.SetTurnEnd("a1", 40*time.Second, TurnDone)
	if shown() != 0 {
		t.Errorf("ended: %d rows shown, want none above the footer", shown())
	}
	cm.SetTurnEnd("a1", 0, TurnDone)

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

// TestWorkingTextNamesStep checks that the indicator names the step at work
// that the transcript hides, with the turn's time, and reads "Working"
// while none is hidden.
func TestWorkingTextNamesStep(t *testing.T) {
	ui := newInputTestUI(t)
	if got, want := ui.WorkingText(4*time.Second), "Working [4s]"; got != want {
		t.Errorf("no step: %q, want %q", got, want)
	}
	msg := agentParts("a1", message.ToolPart{ID: "c1", CallID: "c1", Name: "bash", State: message.ToolStateRunning{
		Input: json.RawMessage(`{"command":"go test ./...","description":"Run the tests"}`)}})
	ui.AddMessage(msg)
	ui.messageWidgets["a1"].SetWorking(true)
	if got, want := ui.WorkingText(4*time.Second), "Run the tests [4s]"; got != want {
		t.Errorf("step at work: %q, want %q", got, want)
	}
}

// TestReasoningLabel checks that reasoning reads Thinking while it streams,
// and Thought once it ends.
func TestReasoningLabel(t *testing.T) {
	b := NewReasoningBox(message.ReasoningPart{Text: "Look."}, DefaultTheme(), mutedFg)
	if got := b.Header.PlainText(); got != ThoughtLabel {
		t.Errorf("ended: header %q, want %q", got, ThoughtLabel)
	}
	b.SetBusy(true)
	if got := b.Header.PlainText(); got != ThinkingLabel {
		t.Errorf("streaming: header %q, want %q", got, ThinkingLabel)
	}
	b.SetBusy(false)
	if got := b.Header.PlainText(); got != ThoughtLabel {
		t.Errorf("ended again: header %q, want %q", got, ThoughtLabel)
	}
}
