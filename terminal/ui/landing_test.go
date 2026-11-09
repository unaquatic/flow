package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/testutil"
)

// LandingWidth is the width, in cells, of the terminal that flow's landing
// page, cmd/landing in aquatic next to this repository, replays the demo turn
// in.
const LandingWidth = 62

// NewLandingUI returns a test UI drawn as the landing page's frames are: with
// the default settings, as a terminal starts with no config file.
func NewLandingUI(t *testing.T) *UI {
	t.Helper()
	ui := newInputTestUI(t)
	ui.service.state.ApplyConfig(config.Terminal{}, false)
	ui.SetInputStyle(ui.service.state.InputStyle)
	ui.ShowStatusMarker()
	return ui
}

// LandingPrompt is the prompt of the landing page's turn.
const LandingPrompt = "Add a Divide method that rejects a zero divisor"

// LandingSteps are the steps of demo.Segments that the landing page's turn
// takes, in order: its replies find and read the calculator, add the method,
// write tests, see one fail, fix it, and run them again.
var LandingSteps = []int{1, 3, 4, 5, 8, 9, 11, 12, 13, 14, 15}

// LandingSummary is the text of the last reply of the landing page's turn. Its
// table fits in LandingWidth inside the transcript's padding.
const LandingSummary = "The tests pass.\n\n" +
	"| File | Change |\n" +
	"|------|--------|\n" +
	"| `calculator/calc.go` | Divide and its zero check |\n" +
	"| `calculator/divide_test.go` | Success and error tests |"

// LandingTurn returns the messages of the landing page's turn: the prompt,
// a reply for each of LandingSteps holding its text and its call in the
// state the call ends in, and the summary, the replies stored 3s apart.
func LandingTurn() []ChatMessage {
	msgs := []ChatMessage{userText("u1", LandingPrompt)}
	for i, step := range LandingSteps {
		seg := demo.Segments[step]
		id := message.ID(fmt.Sprintf("a%d", i+1))
		var parts []message.ContentPart
		if seg.Text != "" {
			parts = append(parts, message.TextPart{ID: message.PartID(id + "-text"), Text: seg.Text})
		}
		parts = append(parts, message.ToolPart{ID: message.PartID(id + "-tool"), CallID: string(id),
			Name: seg.Tool.Name, State: seg.Tool.State(time.Time{}, time.Time{})})
		msgs = append(msgs, agentParts(id, parts...))
	}
	msgs = append(msgs, agentText(message.ID(fmt.Sprintf("a%d", len(LandingSteps)+1)), LandingSummary))
	for i := range msgs {
		msgs[i].CreatedAt = turnT0.Add(time.Duration(i) * 3 * time.Second)
	}
	AssignParents(msgs)
	AssignWorked(msgs)
	return msgs
}

// LastInkRow returns the last row of buf that shows a character or an opaque
// background, or -1 when none does.
func LastInkRow(buf *buffer.Buffer) int {
	for y := buf.Height() - 1; y >= 0; y-- {
		for x := range buf.Width() {
			if c := buf.Cell(x, y); (c.Char != 0 && c.Char != ' ') || !c.Bg.IsTransparent() {
				return y
			}
		}
	}
	return -1
}

// CropRows returns the first h rows of buf.
func CropRows(buf *buffer.Buffer, h int) *buffer.Buffer {
	out := buffer.New(buf.Width(), h)
	for y := range h {
		for x := range buf.Width() {
			c := buf.Cell(x, y)
			out.SetCell(x, y, c.Char, c.Fg, c.Bg, c.Attributes)
		}
	}
	return out
}

// TestLandingDemoFrames draws what the landing page replays, LandingWidth
// cells wide, as a sequence of steps:
//
//   - transcript: the inline transcript of the turn once it ends, down to the
//     footer that times it, which the page shows a block at a time;
//   - typed: the input box holding the prompt, before it is sent;
//   - running: the indicator of the run in progress over the empty input box;
//   - idle: the empty input box once the turn ends.
//
// The page embeds the sequence; go test ./cmd/landing -update in aquatic
// copies it in.
func TestLandingDemoFrames(t *testing.T) {
	seq := testutil.NewGoldenSequence()

	ui := NewLandingUI(t)
	ui.inline = true
	ui.LoadTranscript(LandingTurn())
	tall := renderFrames(LandingWidth, 400, detachTranscript(ui)...)
	seq.AddFrames("transcript", CropRows(tall, LastInkRow(tall)+1), surfaceNames(ui))

	ui = NewLandingUI(t)
	ui.SetInputText(LandingPrompt)
	seq.AddFrames("typed", renderFrames(LandingWidth, 5, ui.inputWrapper), surfaceNames(ui))

	ui = NewLandingUI(t)
	ui.inline = true
	ui.SetInputText("")
	ui.service.state.Working = true
	ui.ShowIndicator(WorkingIndicator(12*time.Second, false))
	seq.AddFrames("running", renderFrames(LandingWidth, 7, detachFromRoot(ui, ui.indicatorBox), ui.inputWrapper), surfaceNames(ui))

	ui = NewLandingUI(t)
	ui.SetInputText("")
	seq.AddFrames("idle", renderFrames(LandingWidth, 5, ui.inputWrapper), surfaceNames(ui))

	testutil.AssertGoldenSequence(t, "landing_demo", seq)
}
