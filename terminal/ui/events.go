package ui

import (
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// UIEvent is implemented by events that update the terminal UI.
type UIEvent interface {
	uiEvent()
}

// EventQuit requests that the terminal close.
type EventQuit struct{}

// EventQuitArmed shows QuitHint in the empty input after an idle Ctrl+C
// armed the quit, until QuitWindow has passed and a second press no longer
// quits.
type EventQuitArmed struct{}

// EventInputClear requests that the input be emptied.
type EventInputClear struct{}

// EventInputSet requests that the input's text be replaced with Text, with
// the cursor at its end.
type EventInputSet struct {
	// Text is the input's new text.
	Text string
}

// EventClearMessages requests that the transcript be emptied.
type EventClearMessages struct{}

// EventSlashMenuHide requests that the slash-command menu close.
type EventSlashMenuHide struct{}

// EventMessageUpdated updates the rendered content of an existing message.
type EventMessageUpdated struct {
	// Msg contains the message ID and latest content.
	Msg ChatMessage
}

// EventMessageAdded appends a message to the transcript.
type EventMessageAdded struct {
	// Msg is the message to append.
	Msg ChatMessage
}

// EventTurnEnded shows how long a turn took under its last reply.
type EventTurnEnded struct {
	// MessageID identifies the reply that ends the turn.
	MessageID message.ID
	// Worked is how long the turn took.
	Worked time.Duration
}

// EventStatusChanged replaces the status-line content.
type EventStatusChanged struct {
	// Content is the new styled status text.
	Content style.StyledText
}

// EventModelLabelChanged replaces the model the slash menu's status row
// names.
type EventModelLabelChanged struct {
	// Label is the new model label.
	Label string
}

// EventUsageChanged replaces the token use the slash menu's status row
// shows.
type EventUsageChanged struct {
	// Usage is the transcript's token use.
	Usage SessionUsage
}

// EventSlashMenuShow displays the slash-command menu.
type EventSlashMenuShow struct {
	// Items contains the commands to display.
	Items []widget.SelectItem
}

// EventSessionMenuShow displays stored sessions at the top of the input box,
// whose text searches them.
type EventSessionMenuShow struct {
	// Items contains one row per session.
	Items []widget.SelectItem
	// Text is shown in place of the rows when there are none.
	Text string
	// Query is the search the rows match, which the input shows.
	Query string
}

// EventSessionMenuHide closes the session menu.
type EventSessionMenuHide struct{}

// EventSessionPreview replaces the session preview transcript.
type EventSessionPreview struct {
	// Messages contains the preview transcript.
	Messages []ChatMessage
}

// EventSessionPrompts replaces the prompts that inline mode previews under the
// session list, in place of the transcript preview of fullscreen mode.
type EventSessionPrompts struct {
	// Prompts are the selected session's prompts on its active branch, oldest
	// first, each on one line. None hides the preview.
	Prompts []string
}

// EventSessionLoad replaces the active transcript, as UI.LoadTranscript
// replaces it: inline, with the rows of its last messages alone.
type EventSessionLoad struct {
	// Messages contains the active session transcript, its notices among
	// its messages.
	Messages []ChatMessage
}

// EventBannerShow displays a modal banner at the top of the input box.
type EventBannerShow struct {
	// Title is the banner heading.
	Title string
	// Keys are the banner's keys. When nil, Hints derives them from the
	// items and the search.
	Keys []KeyHint
	// Text is the optional banner body, shown above the items.
	Text string
	// Items contains selectable banner entries. Their descriptions line up
	// in a column after the names.
	Items []widget.SelectItem
	// SelectedIdx is the initially selected item. Invalid values select zero.
	SelectedIdx int
	// NameWidth is the least width of the name column that the descriptions
	// follow. The column always fits the longest name.
	NameWidth int
	// Search makes the input the items' search field: its text goes to
	// the banner's search handler as it changes. Without it the input is
	// hidden and takes no keys.
	Search bool
	// Query is the search the items match, which the input shows while
	// Search is set.
	Query string
	// Change reports whether ←/→ change the value of items of the menu,
	// which Hints then names.
	Change bool
	// Custom reports whether Tab enters an entry the menu does not list, as
	// in the model menu, which Hints then names.
	Custom bool
}

// Hints returns the banner's keys: Keys when set, and otherwise esc to
// cancel, then the arrows to move when there are items to move through,
// the arrows that change an item's value when Change is set, tab to enter
// one the menu does not list when Custom is set, and typing to search when
// the input searches them. A search that matches nothing keeps the arrows,
// so the keys hold still as the user types.
func (e EventBannerShow) Hints() []KeyHint {
	if e.Keys != nil {
		return e.Keys
	}
	hints := []KeyHint{HintCancel}
	if e.Search || len(e.Items) > 0 {
		hints = append(hints, HintMove)
	}
	if e.Change {
		hints = append(hints, HintChange)
	}
	if e.Custom {
		hints = append(hints, HintCustom)
	}
	if e.Search {
		hints = append(hints, HintSearch)
	}
	return hints
}

// EventBannerUpdate replaces the banner body text.
type EventBannerUpdate struct{ Text string }

// EventBannerHide closes the active banner.
type EventBannerHide struct{}

// EventIndicatorShow displays the indicator between the transcript and input.
type EventIndicatorShow struct {
	// Text is the line displayed, such as the time a run has taken or an
	// error.
	Text string
	// Hint follows Text, dimmed, such as the key that cancels a run in
	// progress, or is empty.
	Hint string
}

// EventIndicatorHide dismisses the status indicator.
type EventIndicatorHide struct{}

// EventInlineModeSet switches the renderer between inline and alternate-screen
// rendering.
type EventInlineModeSet struct {
	// Inline requests rendering in the normal screen and scrollback.
	Inline bool
}

// EventQueueChanged shows the queued prompts at the top of the input box, and
// removes them when Prompts is empty.
type EventQueueChanged struct {
	// Prompts are the queued prompts, in the order they are sent.
	Prompts []string
}

// EventTitleSet replaces the terminal window and tab title.
type EventTitleSet struct {
	// Title is the new title.
	Title string
}

// EventProgressSet shows or clears the progress indicator on the terminal tab.
type EventProgressSet struct {
	// Active shows the indicator when true and clears it when false.
	Active bool
}

// EventUndoRequested asks the UI to move back one turn. The UI runs the move
// off the event loop, so the result arrives as a later notice.
type EventUndoRequested struct{}

// EventRedoRequested asks the UI to move forward one turn, with the same
// deferred result as EventUndoRequested.
type EventRedoRequested struct{}

// EventTreeShow draws the session tree at the top of the input box, whose
// text searches it.
type EventTreeShow struct {
	// Tree is what the tree panel draws.
	Tree TreeView
}

// EventTreeMoveRequested asks the UI to move the session to a message chosen
// in the session tree. The UI runs the move off the event loop, so the
// result arrives later, as for EventUndoRequested.
type EventTreeMoveRequested struct {
	// MessageID identifies the chosen message.
	MessageID message.ID
	// Edit reports that the chosen message is a prompt. The transcript then
	// ends before the prompt rather than at it, and Prompt goes in the input
	// once it does, so that sending it opens a branch beside the prompt.
	Edit bool
	// Prompt is the text of a chosen prompt.
	Prompt string
}

// EventToolsExpandedSet shows or hides the details of every tool call and of
// the changes of every step in the transcript and the session preview, and of
// those added later.
type EventToolsExpandedSet struct {
	// Expanded shows the details when true and hides them when false.
	Expanded bool
}

// EventToolStyleSet shows every collapsed tool call in the transcript and the
// session preview, and those added later, as Style says.
type EventToolStyleSet struct {
	// Style is how the collapsed calls show.
	Style ToolCallStyle
}

// EventToolMarkersSet shows or hides ToolMarker before the name of every tool
// call in the transcript and the session preview, and of those added later.
type EventToolMarkersSet struct {
	// Show shows the marker when true and hides it when false.
	Show bool
}

// EventToolColorSet shows the marker and the name of every tool call in the
// transcript and the session preview, and of those added later, in the color
// Color names.
type EventToolColorSet struct {
	// Color is the color of the markers and the names.
	Color ToolNameColor
}

// EventDebugSet shows what helps debug flow in the transcript and the session
// preview, and in what is added to them later, or hides it: the snapshot each
// prompt and reply ends with, by its hash, once taken.
type EventDebugSet struct {
	// On shows it when true and hides it when false.
	On bool
}

// EventCountPlaceSet counts the rows of every list where Place says.
type EventCountPlaceSet struct {
	// Place is where the lists count their rows.
	Place CountPlace
}

// EventSelectionStyleSet marks the selected row of every menu as Style says.
type EventSelectionStyleSet struct {
	// Style is how the menus mark their selected row.
	Style SelectionStyle
}

// EventUsagePinnedSet pins the Usage box above the input box, or unpins it.
type EventUsagePinnedSet struct {
	// Pinned shows the box while the slash menu is closed too.
	Pinned bool
}

// EventKeyHintsModeSet names the keys of every panel where Mode says.
type EventKeyHintsModeSet struct {
	// Mode is where the panels name their keys.
	Mode KeyHintsMode
}

// EventCacheModeSet shows the cache use of every reply in the transcript and
// the session preview, and of those added later, where Mode says.
type EventCacheModeSet struct {
	// Mode is where the cache use shows.
	Mode CacheMode
}

// EventInputHintChanged shows the hint the empty input now shows, as
// Service.InputHint gives it, such as the placeholder of a question that
// opened.
type EventInputHintChanged struct{}

func (EventQuit) uiEvent()              {}
func (EventQuitArmed) uiEvent()         {}
func (EventInputClear) uiEvent()        {}
func (EventInputSet) uiEvent()          {}
func (EventClearMessages) uiEvent()     {}
func (EventSlashMenuHide) uiEvent()     {}
func (EventMessageUpdated) uiEvent()    {}
func (EventMessageAdded) uiEvent()      {}
func (EventTurnEnded) uiEvent()         {}
func (EventStatusChanged) uiEvent()     {}
func (EventModelLabelChanged) uiEvent() {}
func (EventUsageChanged) uiEvent()      {}
func (EventSlashMenuShow) uiEvent()     {}
func (EventSessionMenuShow) uiEvent()   {}
func (EventSessionMenuHide) uiEvent()   {}
func (EventSessionPreview) uiEvent()    {}
func (EventSessionPrompts) uiEvent()    {}
func (EventSessionLoad) uiEvent()       {}
func (EventBannerShow) uiEvent()        {}
func (EventBannerUpdate) uiEvent()      {}
func (EventBannerHide) uiEvent()        {}
func (EventIndicatorShow) uiEvent()     {}
func (EventIndicatorHide) uiEvent()     {}
func (EventInlineModeSet) uiEvent()     {}
func (EventQueueChanged) uiEvent()      {}
func (EventTitleSet) uiEvent()          {}
func (EventProgressSet) uiEvent()       {}
func (EventUndoRequested) uiEvent()     {}
func (EventRedoRequested) uiEvent()     {}
func (EventTreeShow) uiEvent()          {}
func (EventTreeMoveRequested) uiEvent() {}
func (EventToolsExpandedSet) uiEvent()  {}
func (EventToolStyleSet) uiEvent()      {}
func (EventToolMarkersSet) uiEvent()    {}
func (EventToolColorSet) uiEvent()      {}
func (EventDebugSet) uiEvent()          {}
func (EventCacheModeSet) uiEvent()      {}
func (EventKeyHintsModeSet) uiEvent()   {}
func (EventUsagePinnedSet) uiEvent()    {}
func (EventCountPlaceSet) uiEvent()     {}
func (EventSelectionStyleSet) uiEvent() {}
func (EventInputHintChanged) uiEvent()  {}
