package ui

import (
	"encoding/json"
	"strings"
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
	if got, want := last.Summary.PlainText(), "Worked 32s [ctrl+o expand]"; got != want {
		t.Errorf("summary row = %q, want %q", got, want)
	}
	if shown() != 1 || last.SummaryText != "Worked 32s" || !last.Summary.Visible() || last.Header.Visible() {
		t.Fatalf("done: %d rows shown, summary %q; want the last alone, reading Worked 32s", shown(), last.SummaryText)
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
	footer := cm.segments[0].Footer
	if got, want := footer.Text.PlainText(), "Worked 40s [ctrl+o expand]"; shown() != 0 || got != want {
		t.Errorf("ended: %d rows shown, footer %q; want none above the footer, %q", shown(), got, want)
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
// and Reasoned once it ends.
func TestReasoningLabel(t *testing.T) {
	b := NewReasoningBox(message.ReasoningPart{Text: "Look."}, DefaultTheme(), mutedFg)
	if got := b.Header.PlainText(); got != ReasonedLabel {
		t.Errorf("ended: header %q, want %q", got, ReasonedLabel)
	}
	b.SetBusy(true)
	if got := b.Header.PlainText(); got != ThinkingLabel {
		t.Errorf("streaming: header %q, want %q", got, ThinkingLabel)
	}
	b.SetBusy(false)
	if got := b.Header.PlainText(); got != ReasonedLabel {
		t.Errorf("ended again: header %q, want %q", got, ReasonedLabel)
	}
}

// TestWorkingTextStepTime checks that the indicator times the step at work
// from its own start, not the turn's.
func TestWorkingTextStepTime(t *testing.T) {
	ui := newInputTestUI(t)
	ui.AddMessage(agentParts("a1", message.ReasoningPart{ID: "r1", Text: "Look.",
		StartedAt: time.Now().Add(-4 * time.Second), EndedAt: time.Now()}))
	cm := ui.messageWidgets["a1"]
	cm.SetWorking(true)
	if got := ui.WorkingText(26 * time.Second); !strings.HasSuffix(got, " [4s]") {
		t.Errorf("step at work: %q, want its own 4s", got)
	}
}

// TestWorkingStepHiddenExpanded checks that, while Ctrl+O expands the steps,
// a step at work shown as a row, as inline holds a live one, still hides
// under the indicator, which names it.
func TestWorkingStepHiddenExpanded(t *testing.T) {
	ui := newInputTestUI(t)
	ui.AddMessage(agentParts("a1", message.ToolPart{ID: "c1", CallID: "c1", Name: "bash", State: message.ToolStateRunning{
		Input: json.RawMessage(`{"command":"go test ./...","description":"Run the tests"}`)}}))
	cm := ui.messageWidgets["a1"]
	cm.SetToolsExpanded(true)
	b := partBoxes(cm)[0]
	b.SetHeld(true)
	cm.SetWorking(true)
	if !b.Grouped || cm.StepLabel() != "Run the tests" {
		t.Errorf("grouped %v, step %q; want it hidden and named", b.Grouped, cm.StepLabel())
	}
}

// TestWorkingStepStaysHidden checks that, inline with Ctrl+O expanding the
// steps, streaming reasoning stays held and hidden under the indicator frame
// after frame, rather than flipping between hidden and shown.
func TestWorkingStepStaysHidden(t *testing.T) {
	ui := newInputTestUI(t)
	ui.inline = true
	msg := agentParts("a1", message.ReasoningPart{ID: "r1", Text: "Look.", StartedAt: time.Now()})
	msg.Live = []message.PartID{"r1"}
	ui.AddMessage(msg)
	cm := ui.messageWidgets["a1"]
	cm.SetHolds(true)
	cm.SetToolsExpanded(true)
	cm.SetWorking(true)
	b := partBoxes(cm)[0]
	for i := range 4 {
		cm.SyncHeld()
		cm.SyncRows()
		if !b.Grouped || !b.Held || cm.StepLabel() != ThinkingLabel {
			t.Fatalf("frame %d: grouped %v, held %v, step %q; want it held and hidden as Thinking", i, b.Grouped, b.Held, cm.StepLabel())
		}
	}
}

// TestHiddenStepHoldsNoRows checks that a step at work, hidden under the
// indicator, counts as no live row, so that the input box keeps its rows,
// whatever position the step was last laid out at.
func TestHiddenStepHoldsNoRows(t *testing.T) {
	ui := newInputTestUI(t)
	ui.inline = true
	msg := agentParts("a1", message.ToolPart{ID: "c1", CallID: "c1", Name: "bash", State: message.ToolStateRunning{
		Input: json.RawMessage(`{"command":"sleep 60","description":"Wait"}`)}})
	msg.Live = []message.PartID{"c1"}
	ui.AddMessage(msg)
	cm := ui.messageWidgets["a1"]
	cm.SetHolds(true)
	cm.SetWorking(true)
	b := partBoxes(cm)[0]
	if !b.Grouped || cm.LivePart == nil {
		t.Fatalf("grouped %v, live part %v; want the step hidden and live", b.Grouped, cm.LivePart)
	}
	// The position a hidden box keeps is stale, here as far up as it goes.
	b.LayoutNode().Computed.Y = 0
	if row, ok := cm.LiveRow(); ok {
		t.Errorf("live row %d, want none for a hidden step", row)
	}
}
