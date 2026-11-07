package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
)

// HandleUndo takes back the prompt queued last, as UnqueueLast does, while
// any is queued. Otherwise it stops the active run and requests a move back
// to the state before the latest turn.
//
// The move itself is not performed here, because it waits for the stopped run
// to release the session, and a run releases it only after the UI drains its
// events. The UI runs it off the event loop and reports the outcome through
// CompleteUndo.
func (s *Service) HandleUndo() []UIEvent {
	if len(s.state.Queue) > 0 {
		return s.UnqueueLast()
	}
	s.Stop()
	return []UIEvent{EventUndoRequested{}}
}

// HandleRedo stops the active run and requests a move forward one turn,
// deferred for the reason given on HandleUndo.
func (s *Service) HandleRedo() []UIEvent {
	s.Stop()
	return []UIEvent{EventRedoRequested{}}
}

// CompleteUndo reports the files a finished undo changed.
func (s *Service) CompleteUndo(files []app.FileDiff, err error) []UIEvent {
	if err != nil {
		return s.ShowToast(UndoErrorMessage(err))
	}
	return s.ShowDiffToast(files)
}

// CompleteRedo reports the files a finished redo changed.
func (s *Service) CompleteRedo(files []app.FileDiff, err error) []UIEvent {
	if err != nil {
		return s.ShowToast(RedoErrorMessage(err))
	}
	return s.ShowDiffToast(files)
}

// UndoErrorMessage returns the toast text of a failed undo.
func UndoErrorMessage(err error) string {
	return checkoutErrorMessage("undo", err)
}

// RedoErrorMessage returns the toast text of a failed redo.
func RedoErrorMessage(err error) string {
	return checkoutErrorMessage("redo", err)
}

// checkoutErrorMessage returns the toast text of a failed history move: the
// lowercase verb naming the move, then the error.
func checkoutErrorMessage(verb string, err error) string {
	return fmt.Sprintf("%s failed: %v", verb, err)
}

// AddNotice adds notices to the transcript, under its messages, each in a
// ChatMessage of its own, and returns the events that show them.
func (s *Service) AddNotice(notices ...Notice) []UIEvent {
	var out []UIEvent
	for _, n := range notices {
		msg := ChatMessage{ID: message.NewID(), Notice: &n}
		s.state.Messages = append(s.state.Messages, msg)
		out = append(out, EventMessageAdded{Msg: msg})
	}
	return out
}

// ShowToast adds a one-line notice to the transcript.
func (s *Service) ShowToast(text string) []UIEvent {
	return s.AddNotice(Notice{Text: text})
}

// ShowKeywordToast returns an event that appends a one-line transcript notice
// reading before, keyword and after, with keyword, such as the model the
// notice names, in productFg, the product's primary color.
func (s *Service) ShowKeywordToast(before, keyword, after string) []UIEvent {
	fg := productFg
	line := style.NewStyledText(style.Plain(before), style.TextChunk{Text: keyword, Fg: fg}, style.Plain(after))
	return s.AddNotice(Notice{Lines: []style.StyledText{line}})
}

// NoFileChangesText is the notice of a history move that changed no file.
const NoFileChangesText = "No file changes"

// ChangesTitle heads the summary of the files a history move changed while
// prompts show in PromptBar.
const ChangesTitle = "Changes"

// ShowDiffToast returns a transcript notice summarizing the files a history
// move changed, titled ChangesTitle, or saying that it changed none.
func (s *Service) ShowDiffToast(files []app.FileDiff) []UIEvent {
	if len(files) == 0 {
		return s.ShowToast(NoFileChangesText)
	}
	return s.AddNotice(Notice{Lines: BuildDiffToastLines(files), Title: ChangesTitle})
}

// DiffAddFg and DiffDelFg color the counts of added and deleted lines in a
// summary of changed files.
var (
	DiffAddFg = style.ColorFromInts(95, 200, 130, 255)
	DiffDelFg = style.ColorFromInts(220, 100, 110, 255)
)

// DiffCounts returns the counts of added and deleted lines, "+add -del", in
// DiffAddFg and DiffDelFg.
func DiffCounts(add, del int) []style.TextChunk {
	return []style.TextChunk{
		style.New().WithFg(DiffAddFg).Func()(fmt.Sprintf("+%d", add)),
		style.Plain(" "),
		style.New().WithFg(DiffDelFg).Func()(fmt.Sprintf("-%d", del)),
	}
}

// DiffSummary returns a summary of a change to files files that adds add
// lines and deletes del, such as "2 files  +15 -5", with the counts as
// DiffCounts colors them.
func DiffSummary(files, add, del int) []style.TextChunk {
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	return append([]style.TextChunk{style.Plain(fmt.Sprintf("%d %s  ", files, noun))}, DiffCounts(add, del)...)
}

// BuildDiffToastLines returns a summary, such as "2 files  +15 -5", followed
// by up to six per-file lines. A final line reports any omitted files.
func BuildDiffToastLines(files []app.FileDiff) []style.StyledText {
	const maxFiles = 6

	dim := style.New().WithFg(style.ColorFromInts(140, 140, 140, 255)).Func()
	pathFn := style.New().WithFg(style.ColorFromInts(220, 220, 220, 255)).Func()

	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	lines := []style.StyledText{style.NewStyledText(DiffSummary(len(files), totalAdd, totalDel)...)}

	shown := files
	truncated := 0
	if len(shown) > maxFiles {
		truncated = len(shown) - maxFiles
		shown = shown[:maxFiles]
	}

	for _, f := range shown {
		display := RelPathForToast(f.File)
		chunks := append([]style.TextChunk{pathFn(display), style.Plain("  ")}, DiffCounts(f.Additions, f.Deletions)...)
		lines = append(lines, style.NewStyledText(chunks...))
	}
	if truncated > 0 {
		lines = append(lines, style.NewStyledText(
			dim(fmt.Sprintf("[%d more]", truncated)),
		))
	}
	return lines
}

// RelPathForToast returns a path relative to the working directory when the
// path is inside it. Otherwise, it returns absPath unchanged.
func RelPathForToast(absPath string) string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return absPath
	}
	rel, err := filepath.Rel(wd, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return absPath
	}
	return rel
}

// ExpandingToolsText and CollapsingToolsText are the indicator text while
// Ctrl+O lays the transcript out again with the details of every tool call,
// and of the changes of every step, shown or hidden.
const (
	ExpandingToolsText  = "Expanding"
	CollapsingToolsText = "Collapsing"
)

// ToolsToggleText returns the indicator text while Ctrl+O shows the details
// of the tool calls, as expanded says, or hides them: ExpandingToolsText or
// CollapsingToolsText, and then, while a turn is in progress, the time it has
// taken in brackets, as FormatWorking shows it, such as "Expanding [12s]".
func ToolsToggleText(expanded bool, elapsed time.Duration) string {
	text := CollapsingToolsText
	if expanded {
		text = ExpandingToolsText
	}
	if elapsed <= 0 {
		return text
	}
	return text + " [" + FormatDuration(elapsed) + "]"
}

// The keys that act on a run in progress, as the indicator names them after
// the run's time: CancelHint names the key that cancels the run, and
// ExpandHint and CollapseHint what Ctrl+O does next to the details of the
// tool calls.
const (
	CancelHint   = "[esc cancel]"
	ExpandHint   = "[ctrl+o expand]"
	CollapseHint = "[ctrl+o collapse]"
)

// WorkingIndicator returns the indicator of a run in progress whose turn has
// taken elapsed: FormatWorking of the time, such as "Working [12s]", and
// then CancelHint and ExpandHint, or CollapseHint once expanded is set.
func WorkingIndicator(elapsed time.Duration, expanded bool) EventIndicatorShow {
	toggle := ExpandHint
	if expanded {
		toggle = CollapseHint
	}
	return EventIndicatorShow{Text: FormatWorking(elapsed), Hint: CancelHint + " " + toggle}
}

// ShowIndicator displays text, or when text is empty the WorkingIndicator of
// the turn in progress, which TickIndicator then keeps current.
func (s *Service) ShowIndicator(text string) []UIEvent {
	s.state.Working = text == ""
	if s.state.Working {
		return []UIEvent{WorkingIndicator(s.TurnElapsed(), s.state.ToolsExpanded)}
	}
	return []UIEvent{EventIndicatorShow{Text: text}}
}

// TickIndicator returns the working indicator with the time the turn in
// progress has taken by now, while the indicator shows it, and nothing
// otherwise. The UI calls it every IndicatorTick.
func (s *Service) TickIndicator() []UIEvent {
	if !s.state.Working {
		return nil
	}
	return []UIEvent{WorkingIndicator(s.TurnElapsed(), s.state.ToolsExpanded)}
}

// TurnElapsed returns how long the turn in progress has taken, or zero while
// none is, as before the run of a prompt just sent has started.
func (s *Service) TurnElapsed() time.Duration {
	if s.state.TurnStart.IsZero() {
		return 0
	}
	return time.Since(s.state.TurnStart)
}

// SessionWorked returns how long the session's turns took: the time each
// ended turn's last reply records, and the time the turn in progress has
// taken so far.
func (s *Service) SessionWorked() time.Duration {
	worked := s.TurnElapsed()
	for _, m := range s.state.Messages {
		worked += m.Worked
	}
	return worked
}

// HideIndicator dismisses the indicator.
func (s *Service) HideIndicator() []UIEvent {
	s.state.Working = false
	return []UIEvent{EventIndicatorHide{}}
}

// ResetError dismisses any visible error indicator.
func (s *Service) ResetError() []UIEvent {
	return s.HideIndicator()
}

// SimulateError displays a representative request failure.
func (s *Service) SimulateError() []UIEvent {
	return s.ShowIndicator("Request Failed: Post \"https://api.example.com/v1/responses\": EOF")
}
