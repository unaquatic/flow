package ui

import (
	"bytes"
	"encoding/json"
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

// CompleteUndo reports what a finished undo checked out and changed.
func (s *Service) CompleteUndo(moved app.MoveResult, err error) []UIEvent {
	if err != nil {
		return s.ShowToast(UndoErrorMessage(err))
	}
	return s.ShowDiffToast(moved)
}

// CompleteRedo reports what a finished redo checked out and changed.
func (s *Service) CompleteRedo(moved app.MoveResult, err error) []UIEvent {
	if err != nil {
		return s.ShowToast(RedoErrorMessage(err))
	}
	return s.ShowDiffToast(moved)
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
// prompts show in PromptBar, when the move checked nothing out.
const ChangesTitle = "Changes"

// ShortRefLen is how many characters of what a history move checked out,
// such as a snapshot's hash, CheckoutTitle shows.
const ShortRefLen = 8

// CheckoutTitle returns the title of the notice of a history move that
// checked out ref, the first ShortRefLen characters of it in brackets after
// "Checkout", such as "Checkout [c1d2e3f4]", or ChangesTitle when ref is
// empty.
func CheckoutTitle(ref string) string {
	if ref == "" {
		return ChangesTitle
	}
	return "Checkout [" + ref[:min(len(ref), ShortRefLen)] + "]"
}

// ShowDiffToast returns a transcript notice summarizing the files a history
// move changed, titled CheckoutTitle of what it checked out, or saying that
// it changed none: a single line when it checked nothing out either, and
// under the title otherwise.
func (s *Service) ShowDiffToast(moved app.MoveResult) []UIEvent {
	title := CheckoutTitle(moved.Checkout)
	switch {
	case len(moved.Files) > 0:
		return s.AddNotice(Notice{Lines: BuildDiffToastLines(moved.Files), Title: title})
	case moved.Checkout != "":
		return s.AddNotice(Notice{Text: NoFileChangesText, Title: title})
	}
	return s.ShowToast(NoFileChangesText)
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

// ShowError shows e, the error a run ended with, in the indicator, as
// ErrorIndicator shows it.
func (s *Service) ShowError(e app.UnknownError) []UIEvent {
	s.state.Working = false
	return []UIEvent{ErrorIndicator(e)}
}

// MaxErrorDetailLines is the most lines of a provider's error body that the
// indicator shows; a longer body ends in a line that counts the rest.
const MaxErrorDetailLines = 20

// MaxErrorDetailBytes is the most bytes of a provider's error body that the
// indicator shows, about MaxErrorDetailLines full lines, so that a body of
// few but long lines, as minified JSON or HTML is, is cut short too.
const MaxErrorDetailBytes = 2 << 10

// ErrorIndicator returns the indicator of e, the error a run ended with: for
// a provider's error response, its provider and status, such as
// "provider-a response error (400)", over the body it answered with, as
// ErrorBody formats it; and for another error, its message alone.
func ErrorIndicator(e app.UnknownError) EventIndicatorShow {
	if e.Status == 0 || strings.TrimSpace(e.Body) == "" {
		return EventIndicatorShow{Text: e.Message, Error: true}
	}
	detail, isJSON := ErrorBody(e.Body)
	return EventIndicatorShow{
		Text:   fmt.Sprintf("%s response error (%d)", e.Provider, e.Status),
		Error:  true,
		Detail: detail,
		JSON:   isJSON,
	}
}

// ErrorBody returns body, a provider's error response body, as the
// indicator shows it, and reports whether it is JSON: JSON indented by two
// spaces, other text without the white space around it, either cut to
// MaxErrorDetailLines lines, the last of which then counts the lines cut,
// and then to MaxErrorDetailBytes bytes, followed by an ellipsis.
func ErrorBody(body string) (string, bool) {
	body = strings.TrimSpace(body)
	var buf bytes.Buffer
	isJSON := json.Indent(&buf, []byte(body), "", "  ") == nil
	if isJSON {
		body = buf.String()
	}
	lines := strings.Split(body, "\n")
	if len(lines) > MaxErrorDetailLines {
		rest := len(lines) - (MaxErrorDetailLines - 1)
		lines = append(lines[:MaxErrorDetailLines-1], fmt.Sprintf("… %d more lines", rest))
	}
	return Truncate(strings.Join(lines, "\n"), MaxErrorDetailBytes), isJSON
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

// SimulateError displays a representative request failure, as ShowError
// shows the error a run ends with.
func (s *Service) SimulateError() []UIEvent {
	return s.ShowError(app.NewUnknownError("Request Failed: Post \"https://api.example.com/v1/responses\": EOF"))
}
