package ui

import (
	"slices"

	"github.com/unaquatic/flow/agent/message"
)

// BuiltScreens is how many screens of rows, at least, the fullscreen
// transcript holds once built from a session's messages, as BuildTranscript
// builds it: those of its last messages, or every row when they add up to
// fewer. It is more than the screen it shows at the bottom and the
// EarlierScreens it keeps above that, so that a transcript just built does
// not build more until the user scrolls up or the terminal grows taller.
const BuiltScreens = 4

// EarlierScreens is how many screens of rows the fullscreen transcript keeps
// built above the top of its viewport while the user scrolls up, as
// BuildEarlierInView builds them, and how many it builds each time it falls
// short.
const EarlierScreens = 2

// earlierBatch is the number of messages BuildEarlier builds the rows of at
// once, before it measures how many rows they took.
const earlierBatch = 8

// ScreenRows returns the rows the root lays the fullscreen transcript out in,
// those of the terminal, at least one, which the transcript counts the rows it
// builds in.
func (ui *UI) ScreenRows() int { return max(ui.renderer.Root().Height(), 1) }

// TurnStart returns where in msgs a transcript of their last messages from i
// on starts: where BubbleStart starts it, unless the bubble there is a reply
// that follows its prompt, past the notices between them, at the bubble of
// that prompt. The fullscreen transcript so never holds a reply without its
// prompt, which pins to the top while the reply scrolls under it.
func TurnStart(msgs []ChatMessage, i int) int {
	start := BubbleStart(msgs, i)
	first := slices.IndexFunc(msgs[start:], func(m ChatMessage) bool { return m.Notice == nil })
	if first < 0 || msgs[start+first].Role != message.RoleAgent {
		return start
	}
	for k, m := range slices.Backward(msgs[:start]) {
		if m.Notice != nil {
			continue
		}
		if m.Role == message.RoleUser {
			return BubbleStart(msgs, k)
		}
		break
	}
	return start
}

// BuildEarlier builds the rows of the last of the earlier messages, those
// whose rows the fullscreen transcript does not hold yet, above its first
// row: earlierBatch messages at a time, as few as take rows rows at least, or
// all of them. Each batch starts with a message that starts a turn, as
// TurnStart picks it, so that a bubble shows all its messages, and a reply
// shows under its prompt. It returns
// the rows the transcript gained, as TranscriptHeight measures it, and
// records that the transcript lacks the rows of its first messages, as
// trimmed does, while any are left.
//
// The measures lay the root out, which marks its layout clean. It marks the
// root dirty again, so that the next frame still sees that its layout
// changed, as KeepInputBoxInView looks for.
func (ui *UI) BuildEarlier(rows int) int {
	if len(ui.earlier) == 0 {
		return 0
	}
	defer ui.renderer.Root().LayoutNode().MarkDirty()
	before := ui.TranscriptHeight()
	height := before
	for len(ui.earlier) > 0 && height-before < rows {
		start := TurnStart(ui.earlier, len(ui.earlier)-earlierBatch)
		ui.PrependMessages(ui.earlier[start:])
		ui.earlier = ui.earlier[:start]
		height = ui.TranscriptHeight()
	}
	ui.trimmed = len(ui.earlier) > 0
	return height - before
}

// PrependMessages adds the rows of msgs, messages and the notices among them,
// as AddMessage adds them, above the first row of the transcript, which shows
// the messages after them. msgs start with a message that starts a bubble,
// so the bubbles they make hold none of the messages after them.
func (ui *UI) PrependMessages(msgs []ChatMessage) {
	if len(ui.transcriptRows) > 0 {
		ui.prependBefore = ui.transcriptRows[0]
		defer func() { ui.prependBefore = nil }()
	}
	for _, msg := range msgs {
		ui.AddMessage(msg)
	}
}

// BuildEarlierInView builds the rows of earlier messages above the fullscreen
// transcript, EarlierScreens screens of them, as BuildEarlier builds them,
// once the top of its viewport is fewer than EarlierScreens screens under its
// first row, as the last frame laid it out, and keeps the viewport over the
// rows it showed. That happens as the user scrolls up, or as the terminal
// grows taller than the rows built leave room for. A transcript just built
// has BuiltScreens screens of rows, so one that follows its bottom builds
// nothing more until then. It runs before every frame is laid out.
//
// The first frame after BuildTranscript builds nothing more: the scroll box
// has not laid the new rows out yet, so its offset is still that of the
// transcript before them, and the rows built fill BuiltScreens screens of the
// terminal as it is.
func (ui *UI) BuildEarlierInView() {
	fresh := ui.freshBuild
	ui.freshBuild = false
	if ui.inline || fresh || len(ui.earlier) == 0 {
		return
	}
	screens := EarlierScreens * ui.ScreenRows()
	y := ui.messageBox.ScrollY()
	if y >= screens {
		return
	}
	added := ui.BuildEarlier(screens)
	if added == 0 {
		return
	}
	// The rows went in above the viewport. The scroll box keeps the
	// position of the row at its top by that row's index, which the rows
	// added moved on, so the offset moves down by the rows they took
	// instead, which also records the row that is now at the top.
	ui.messageBox.SetContentHeight(ui.messageBox.ContentHeight() + added)
	ui.messageBox.SetScrollY(y + added)
}
