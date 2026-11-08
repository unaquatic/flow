package ui

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/testutil"
	"github.com/unaquatic/fluid/widget"
)

// renderFrames draws rows stacked in a column w cells wide and h rows tall.
func renderFrames[R render.Renderable](w, h int, rows ...R) *buffer.Buffer {
	root := terminal.NewRootRenderable(w, h)
	for _, row := range rows {
		root.Add(row)
	}
	buf := buffer.New(w, h)
	root.Render(buf, 0)
	return buf
}

// detachTranscript removes ui's transcript rows from their parent and returns
// them in display order.
func detachTranscript(ui *UI) []*widget.Box {
	rows := ui.transcriptRows
	ui.ClearTranscriptRows()
	return rows
}

// surfaceNames names the backgrounds that terminal widgets fill, for golden
// frame legends. A color that several surfaces share gets all their names.
func surfaceNames(ui *UI) map[style.Color]string {
	th := ui.theme
	names := make(map[style.Color]string)
	for _, s := range []struct {
		color style.Color
		name  string
	}{
		{ui.BoxBg(), "BoxBg"},
		{th.Bg, "Bg"},
		{th.SurfaceRaised, "SurfaceRaised"},
		{th.SuccessSurface, "SuccessSurface"},
		{th.WarningSurface, "WarningSurface"},
		{th.DangerSurface, "DangerSurface"},
		{th.InfoSurface, "InfoSurface"},
	} {
		if prev, ok := names[s.color]; ok {
			names[s.color] = prev + "/" + s.name
		} else {
			names[s.color] = s.name
		}
	}
	return names
}

// userText returns a user message holding text.
func userText(id message.ID, text string) ChatMessage {
	return ChatMessage{ID: id, ParentID: id, Role: message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{ID: message.PartID(id + "-text"), Text: text}}}
}

// agentParts returns an agent message holding parts.
func agentParts(id message.ID, parts ...message.ContentPart) ChatMessage {
	return ChatMessage{ID: id, ParentID: id, Role: message.RoleAgent, Parts: parts}
}

// agentText returns an agent message holding text.
func agentText(id message.ID, text string) ChatMessage {
	return agentParts(id, message.TextPart{ID: message.PartID(id + "-text"), Text: text})
}

// expandTools expands the details of every tool call and step change in the
// transcript of ui, as Ctrl+O does, without the input box that ui, built
// without one, does not have.
func expandTools(ui *UI) {
	ui.service.SetToolsExpanded(true)
	ui.SetToolsExpanded(true)
}

// toolRowsTurn adds a turn of three replies: a1, which says what it does and
// makes two calls, a2, the next step, which makes one, and a3, which sums up.
func toolRowsTurn(ui *UI) {
	call := func(id message.PartID, name, input string, state func(json.RawMessage) message.ToolState) message.ToolPart {
		return message.ToolPart{ID: id, CallID: string(id), Name: name, State: state(json.RawMessage(input))}
	}
	failed := func(in json.RawMessage) message.ToolState {
		return message.ToolStateError{Input: in, Error: "FAIL\n\n[exit 1]"}
	}
	read := func(in json.RawMessage) message.ToolState {
		return message.ToolStateCompleted{Input: in, Output: "package calc"}
	}
	grep := func(in json.RawMessage) message.ToolState {
		return message.ToolStateCompleted{Input: in, Output: "calc.go:3:func Divide"}
	}
	first := agentParts("a1",
		message.TextPart{ID: "a1-text", Text: "Running the tests."},
		call("a1-bash", "bash", `{"command":"go test ./..."}`, failed),
		call("a1-read", "read", `{"path":"calc.go"}`, read),
	)
	second := agentParts("a2", call("a2-grep", "grep", `{"pattern":"Divide"}`, grep))
	last := agentText("a3", "Fixed the zero check.")
	second.ParentID, last.ParentID = "a1", "a1"
	for _, msg := range []ChatMessage{first, second, last} {
		ui.AddMessage(msg)
	}
}

// clickTool clicks the header of the i-th tool call of the bubble that holds
// message id, as a click that expands or collapses it does.
func clickTool(ui *UI, id message.ID, i int) {
	box := slices.Collect(ui.messageWidgets[id].ToolBoxes())[i]
	box.HeaderRow.OnMouseDownHandler(&input.MouseEvent{Type: input.MouseDown, Button: input.ButtonLeft})
}

// movedFiles returns seven changed files, one more than a diff notice lists.
// Their paths lie outside the working directory, so the notice shows them
// unchanged on every machine.
func movedFiles() []app.FileDiff {
	names := []string{"calc.go", "calc_test.go", "divide.go", "divide_test.go", "doc.go", "errors.go", "go.mod"}
	files := make([]app.FileDiff, len(names))
	for i, name := range names {
		files[i] = app.FileDiff{File: "/work/calc/" + name, Additions: i + 1, Deletions: i % 3, Status: "modified"}
	}
	return files
}

// statusLines returns the rows of a /status notice with fixed values.
func statusLines() []style.StyledText {
	return StatusLines(statusInfo(20_000), nil)
}

// statusInfo returns the session info of a session whose latest request
// used context tokens of a 400K window.
func statusInfo(context int) StatusInfo {
	return StatusInfo{
		Version:   product.Name + " v0.1.0",
		Session:   "01a0e33d-4571-7ed4-b06b-4229c726c313",
		Messages:  4,
		Model:     "provider/model",
		Usage:     SessionUsage{Context: context, Window: 400_000},
		Directory: "/work/project",
	}
}

func TestTranscriptFrames(t *testing.T) {
	tests := []struct {
		name string
		w, h int // w is 40 when zero
		add  func(ui *UI)
	}{
		{name: "notice_single", h: 4, add: func(ui *UI) {
			ui.AddNotice(Notice{Text: "Chat cleared"})
		}},
		// A notice that names a model draws it in the primary color.
		{name: "notice_keyword", h: 4, add: func(ui *UI) {
			ui.Apply(ui.service.ShowKeywordToast("Switched to ", "provider-a/model-a:high", ""))
		}},
		{name: "notice_pair", h: 8, add: func(ui *UI) {
			ui.AddNotice(Notice{Text: "Chat cleared"})
			ui.AddNotice(Notice{Text: "Nothing to undo"})
		}},
		{name: "user_message", h: 4, add: func(ui *UI) {
			ui.AddMessage(userText("u1", "Run the tests"))
		}},
		{name: "user_then_notice", h: 7, add: func(ui *UI) {
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddNotice(Notice{Text: "Chat cleared"})
		}},
		{name: "transcript_surfaces", h: 20, add: func(ui *UI) {
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddNotice(Notice{Text: "Chat cleared"})
			ui.AddMessage(ChatMessage{ID: "a1", ParentID: "a1", Role: message.RoleAgent,
				Parts: []message.ContentPart{
					message.TextPart{ID: "a1-text", Text: "```go\nx := 1\n```"},
					message.ToolPart{CallID: "c1", Name: "bash", State: message.ToolStateCompleted{
						Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok",
					}},
				}})
		}},
		{name: "notice_multiline", h: 7, add: func(ui *UI) {
			ui.AddNotice(Notice{Lines: statusLines()})
		}},
		// /status is a system notice under the logo, which sits on the canvas
		// from the left edge, a blank row above the notice.
		{name: "status_notice", h: 18, add: func(ui *UI) {
			ui.AddNotice(StatusNotice(statusLines())...)
		}},
		// /status shows the session info alone, without the logo a new
		// session opens under.
		{name: "status_command", h: 9, add: func(ui *UI) {
			ui.AddNotice(LogoNotice(nil, statusLines())...)
		}},
		// A context that fills most of the window shows in the warning
		// color, and past 90% in the danger color, as the status row's does.
		{name: "status_context_warn", h: 9, add: func(ui *UI) {
			ui.AddNotice(LogoNotice(nil, StatusLines(statusInfo(300_000), nil))...)
		}},
		{name: "status_context_danger", h: 9, add: func(ui *UI) {
			ui.AddNotice(LogoNotice(nil, StatusLines(statusInfo(380_000), nil))...)
		}},
		// /new heads the emptied transcript with the new session's /status.
		{name: "new_chat", h: 22, add: func(ui *UI) {
			ui.AddNotice(StatusNotice(statusLines())...)
			ui.AddNotice(Notice{Text: "Chat cleared"})
		}},
		{name: "notice_diff_summary", h: 11, add: func(ui *UI) {
			ui.AddNotice(Notice{Lines: BuildDiffToastLines(movedFiles())})
		}},
		// A code block shows its lines alone, under the prose, and a
		// line too wide for it wraps.
		{name: "agent_code_block", h: 12, add: func(ui *UI) {
			ui.AddMessage(agentText("a1", "Run this:\n\n```\ngo test ./calculator/... -run TestDivide -count=1\n```\n\nThen commit."))
		}},
		// A code block in a list item or a quote starts under its text.
		// In a quote, the quote's border runs beside it.
		{name: "agent_code_nested", h: 20, add: func(ui *UI) {
			ui.AddMessage(agentText("a1", "1. Run it:\n\n   ```\n   go test ./...\n   ```\n\n- Or this:\n\n  ```\n  go vet ./...\n  ```\n\n> Quoted:\n>\n> ```\n> x := 1\n> ```"))
		}},
		{name: "agent_markdown", h: 8, add: func(ui *UI) {
			ui.AddMessage(agentText("a1", "## Plan\n\n1. Read `calc.go`.\n2. Add **Divide** with a zero check.\n\n- Keep `Add` as it is.\n- Run the tests."))
		}},
		{name: "agent_reasoning", h: 6, add: func(ui *UI) {
			ui.AddMessage(agentParts("a1",
				message.ReasoningPart{ID: "a1-think", Text: "The divisor can be zero, so Divide needs a guard."},
				message.TextPart{ID: "a1-text", Text: "I'll add the guard."},
			))
		}},
		// Inline code in reasoning is as bright as the reasoning around it,
		// not as bright as the same code in the reply under it.
		{name: "agent_reasoning_code", h: 7, add: func(ui *UI) {
			ui.AddMessage(agentParts("a1",
				message.ReasoningPart{ID: "a1-think", Text: "Divide needs a guard in `calc.go`."},
				message.TextPart{ID: "a1-text", Text: "I'll add the guard in `calc.go`."},
			))
		}},
		// Per message, a reply shows its cache use in a row under its text
		// whether or not it ends a turn.
		{name: "agent_usage", h: 4, add: func(ui *UI) {
			ui.Apply(ui.service.SetCacheMode(CachePerMessage))
			msg := agentText("a1", "Done.")
			msg.Model = "provider-b/model-d"
			msg.Usage = message.Usage{InputTokens: 1200, OutputTokens: 300, CachedInputTokens: 1024, CacheCreationInputTokens: 176}
			ui.AddMessage(msg)
		}},
		{name: "agent_error", h: 5, add: func(ui *UI) {
			msg := agentText("a1", "Reading the file.")
			ui.AddMessage(msg)
			msg.Error = errors.New("connection reset by peer")
			ui.UpdateMessage(msg)
		}},
		// Tool calls start collapsed to their header, and Ctrl+O expands them.
		{name: "agent_tool_streamed", h: 12, add: streamToolTurn},
		{name: "agent_tool_expanded", h: 12, add: func(ui *UI) {
			streamToolTurn(ui)
			expandTools(ui)
		}},
		// Collapsed calls are rows on the canvas, with no blank row between
		// two of them, whether one step makes both or the next step does.
		{name: "agent_tool_rows", h: 10, add: toolRowsTurn},
		// A click expands a call into its block, a blank row apart from the
		// rows above and below it.
		{name: "agent_tool_rows_clicked", h: 18, add: func(ui *UI) {
			toolRowsTurn(ui)
			clickTool(ui, "a1", 1)
		}},
		// Showing the tool marker in /settings puts it in the padding before
		// each row, leaving the rows' text where it was.
		{name: "agent_tool_rows_marker", h: 10, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetToolMarkers(true))
		}},
		// The white tool name color shows the names as a panel shows its
		// title, and the input after them gray.
		{name: "agent_tool_rows_white", h: 10, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetToolColor(ToolNameWhite))
		}},
		// Expanded plain, as /settings sets them, the calls show their
		// details on the canvas, with no surface and no padding.
		{name: "agent_tool_expanded_plain", h: 24, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetExpandStyle(ExpandedPlain))
			expandTools(ui)
		}},
		// With the prompt style a bar, a prompt sits on the canvas with the
		// marker shape in the primary color at the left edge of each of its
		// rows, ■ by default.
		{name: "prompt_bar", h: 10, add: func(ui *UI) {
			ui.Apply(ui.service.SetPromptStyle(PromptBar))
			ui.AddMessage(userText("u1", "fix the divide test, then run the calculator tests again"))
			ui.AddMessage(agentText("a1", "Fixed the divide-by-zero check."))
		}},
		// The block shape fills the bar's cells, and a shape set later
		// reaches a prompt already shown.
		{name: "prompt_bar_block", h: 10, add: func(ui *UI) {
			ui.Apply(ui.service.SetPromptStyle(PromptBar))
			ui.AddMessage(userText("u1", "fix the divide test, then run the calculator tests again"))
			ui.AddMessage(agentText("a1", "Fixed the divide-by-zero check."))
			ui.Apply(ui.service.SetMarkerShape(MarkerBlock))
		}},
		// With the prompt style a bar, a notice that would fill a box sits on
		// the canvas as a prompt does, its first line marked in the tool name
		// color as its header and the lines under it unmarked. A notice
		// with a title is headed by the title instead, as the files a history
		// move changed are by the snapshot it checked out.
		{name: "notice_bar", h: 14, add: func(ui *UI) {
			ui.Apply(ui.service.SetPromptStyle(PromptBar))
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddNotice(Notice{Text: "Chat cleared"})
			ui.Apply(ui.service.ShowDiffToast(app.MoveResult{Files: movedFiles()[:2], Checkout: "c1d2e3f4a5b6c7d8"}))
		}},
		// With the prompt style a bar, the session info under the logo is
		// headed by a marked StatusTitle, its lines unmarked under it.
		{name: "status_notice_bar", h: 18, add: func(ui *UI) {
			ui.Apply(ui.service.SetPromptStyle(PromptBar))
			ui.AddNotice(StatusNotice(statusLines())...)
		}},
		// A marker shape and a tool name color set later reach a notice
		// already shown.
		{name: "notice_bar_restyled", h: 4, add: func(ui *UI) {
			ui.Apply(ui.service.SetPromptStyle(PromptBar))
			ui.AddNotice(Notice{Text: "Chat cleared"})
			ui.Apply(ui.service.SetMarkerShape(MarkerCircle))
			ui.Apply(ui.service.SetToolColor(ToolNameWhite))
		}},
		// A reply's text starts with the marker on its first row alone, and
		// a prompt shows none.
		{name: "agent_text_marker", h: 10, add: func(ui *UI) {
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.AddMessage(userText("u1", "fix the divide test"))
			ui.AddMessage(agentText("a1", "Fixed the divide-by-zero check.\n\nThe tests pass now."))
		}},
		// The marker shape set in /settings marks the calls, the cache rows,
		// and the footer alike: here an unfilled circle.
		{name: "agent_tool_rows_circle", h: 10, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.Apply(ui.service.SetMarkerShape(MarkerCircleOutline))
		}},
		// Block fills a space with the marker's color.
		{name: "agent_tool_rows_block", h: 10, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.Apply(ui.service.SetMarkerShape(MarkerBlock))
		}},
		// Expanded plain, the calls keep the marker, as rows do.
		{name: "agent_tool_expanded_plain_marker", h: 24, add: func(ui *UI) {
			toolRowsTurn(ui)
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.Apply(ui.service.SetExpandStyle(ExpandedPlain))
			expandTools(ui)
		}},
		// Inline, text that streams shows its blocks once each is whole, and
		// a row in place of the last until then, so that the rows above it
		// never change.
		{name: "agent_text_streaming", h: 4, add: func(ui *UI) {
			ui.inline = true
			msg := agentText("a1", "Line counts:\n\n| file | lines |\n|---|---|\n| calc.go | 12 |")
			msg.Live = []message.PartID{"a1-text"}
			ui.AddMessage(msg)
		}},
		// Inline, a long session loads the rows of its last messages alone:
		// from the first row that leaves TailRows rows under it, though it is
		// a reply's step, without the prompt above it.
		{name: "transcript_tail", w: 80, h: 8, add: func(ui *UI) {
			ui.inline = true
			ui.LoadTranscript(longSession(200, 4))
		}},
		// Inline, a call that runs shows as its row though Ctrl+O expanded
		// the calls, and so does the call under it that has ended, until the
		// call above it ends too.
		{name: "agent_tool_running_held", h: 4, add: func(ui *UI) {
			ui.inline = true
			expandTools(ui)
			msg := agentParts("a1",
				message.ToolPart{ID: "a1-test", CallID: "c1", Name: "bash", State: message.ToolStateRunning{
					Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok  \tcalc"}},
				message.ToolPart{ID: "a1-vet", CallID: "c2", Name: "bash", State: message.ToolStateCompleted{
					Input: json.RawMessage(`{"command":"go vet ./..."}`), Output: "ok"}},
			)
			msg.Live = []message.PartID{"a1-test"}
			ui.AddMessage(msg)
		}},
		// While a run is in progress, the marker of a call that executes
		// blinks, here hidden, and the call that has ended keeps its own.
		{name: "agent_tool_marker_blink_off", h: 4, add: func(ui *UI) {
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.blink.On.Store(true)
			ui.blink.Off = true
			msg := agentParts("a1",
				message.ToolPart{ID: "a1-test", CallID: "c1", Name: "bash", State: message.ToolStateRunning{
					Input: json.RawMessage(`{"command":"go test ./..."}`)}},
				message.ToolPart{ID: "a1-vet", CallID: "c2", Name: "bash", State: message.ToolStateCompleted{
					Input: json.RawMessage(`{"command":"go vet ./..."}`), Output: "ok"}},
			)
			ui.AddMessage(msg)
		}},
		// Inline, the marker of the text a reply writes blinks, here hidden,
		// while its first row is the one that stands for the block it holds
		// back.
		{name: "agent_text_marker_blink_off", h: 4, add: func(ui *UI) {
			ui.inline = true
			ui.Apply(ui.service.SetToolMarkers(true))
			ui.blink.On.Store(true)
			ui.blink.Off = true
			msg := agentText("a1", "Running the tests")
			msg.Live = []message.PartID{"a1-text"}
			ui.AddMessage(msg)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInlineTestUI(t)
			tt.add(ui)
			buf := renderFrames(cmp.Or(tt.w, 40), tt.h, detachTranscript(ui)...)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

func TestToolBoxFrames(t *testing.T) {
	bashInput := json.RawMessage(`{"command":"go test ./calculator/...","description":"Run calculator tests"}`)
	tests := []struct {
		name string
		h    int
		tool message.ToolPart
		// collapsed leaves the box collapsed to its header.
		collapsed bool
		// style is how the expanded box shows.
		style ExpandedStyle
	}{
		// An expanded call shows its params under the header, which then holds
		// the tool name alone: a bash call its command.
		{name: "toolbox_bash_queued", h: 6, tool: message.ToolPart{Name: "bash",
			State: message.ToolStatePending{Input: bashInput}}},
		{name: "toolbox_bash_completed", h: 8, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateCompleted{Input: bashInput, Output: "ok\texample/calculator\t0.003s"}}},
		// A timeout other than the default follows the command.
		{name: "toolbox_bash_timeout", h: 6, tool: message.ToolPart{Name: "bash",
			State: message.ToolStatePending{Input: json.RawMessage(
				`{"command":"go test ./calculator/...","description":"Run calculator tests","timeout_ms":120000}`)}}},
		// A command wider than the box wraps on the command row, and in a
		// collapsed box, a row, is cut off in the header, before the status.
		{name: "toolbox_bash_wrapped", h: 7, tool: message.ToolPart{Name: "bash",
			State: message.ToolStatePending{Input: json.RawMessage(
				`{"command":"go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v","description":"Run the divide tests","timeout_ms":120000}`)}}},
		// A call collapses to its header alone, on the canvas.
		{name: "toolbox_bash_wrapped_row", h: 2, collapsed: true, tool: message.ToolPart{Name: "bash",
			State: message.ToolStatePending{Input: json.RawMessage(
				`{"command":"go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v","description":"Run the divide tests","timeout_ms":120000}`)}}},
		{name: "toolbox_bash_wrapped_timeout", h: 7, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateError{Input: json.RawMessage(
				`{"command":"go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v","description":"Run the divide tests","timeout_ms":120000}`),
				Error: "[timeout after 120s]"}}},
		// Only the first line of a multi-line command is in the header.
		{name: "toolbox_bash_multiline", h: 7, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateRunning{Input: json.RawMessage(
				`{"command":"cd calculator\ngo test ./...","description":"Run the tests"}`)}}},
		// A failed run's marker is its status, and its output is shown under
		// the command.
		{name: "toolbox_bash_error", h: 9, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateError{Input: bashInput, Error: "--- FAIL: TestDivide (0.00s)\nFAIL\n\n[exit 1]"}}},
		{name: "toolbox_bash_interrupted", h: 6, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateError{Input: bashInput, Error: message.InterruptedError}}},
		// Expanded plain, the call shows on the canvas, with no surface and
		// no padding.
		{name: "toolbox_bash_error_plain", h: 7, style: ExpandedPlain, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateError{Input: bashInput, Error: "--- FAIL: TestDivide (0.00s)\nFAIL\n\n[exit 1]"}}},
		// A call interrupted before its output shows its input in its params,
		// each param after the main one as a note in the muted text color.
		{name: "toolbox_grep_interrupted", h: 6, tool: message.ToolPart{Name: "grep",
			State: message.ToolStateError{Input: json.RawMessage(`{"pattern":"TODO","path":"calculator"}`),
				Error: message.InterruptedError}}},
		{name: "toolbox_grep_interrupted_row", h: 2, collapsed: true, tool: message.ToolPart{Name: "grep",
			State: message.ToolStateError{Input: json.RawMessage(`{"pattern":"TODO","path":"calculator"}`),
				Error: message.InterruptedError}}},
		// A tool that fails shows why under its "[tool execution failed]".
		{name: "toolbox_read_failed", h: 8, tool: message.ToolPart{Name: "read",
			State: message.ToolStateError{Input: json.RawMessage(`{"path":"calc.go"}`),
				Error: "not found: calc.go\n\n[tool execution failed]"}}},
		// A failed edit shows its path, then its diff, then why it failed.
		{name: "toolbox_edit_failed", h: 19, tool: message.ToolPart{Name: "edit",
			State: message.ToolStateError{Input: json.RawMessage(
				`{"path":"calculator/calc.go","old_string":"package calculator\n\nimport \"fmt\"","new_string":"package calculator\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)"}`),
				Error: "old_string not found\n\n[tool execution failed]"}}},
		{name: "toolbox_bash_running_output", h: 8, tool: message.ToolPart{Name: "bash",
			State: message.ToolStateRunning{Input: bashInput, Output: "=== RUN   TestAdd\n--- PASS: TestAdd"}}},
		{name: "toolbox_edit_running", h: 10, tool: message.ToolPart{Name: "edit",
			State: message.ToolStateRunning{
				Input: json.RawMessage(`{"path":"calc.go","old_string":"old line","new_string":"new line"}`),
			}}},
		{name: "toolbox_read_completed", h: 10, tool: message.ToolPart{Name: "read",
			State: message.ToolStateCompleted{
				Input:  json.RawMessage(`{"path":"calc.go"}`),
				Output: "package calc\n\nfunc Add(a, b int) int { return a + b }",
			}}},
		// A read of part of a file notes the lines it reads after its path.
		{name: "toolbox_read_lines", h: 10, tool: message.ToolPart{Name: "read",
			State: message.ToolStateCompleted{
				Input:  json.RawMessage(`{"path":"calc.go","offset":8,"limit":2}`),
				Output: "// Divide returns the quotient.\nfunc (c *Calculator) Divide(a, b float64) float64 {\n\n[showing lines 8-9 of 29]",
			}}},
		// A search notes where it looks after what it looks for.
		{name: "toolbox_grep_completed", h: 9, tool: message.ToolPart{Name: "grep",
			State: message.ToolStateCompleted{
				Input:  json.RawMessage(`{"pattern":"TODO","path":"calculator","include":"*.go"}`),
				Output: "calculator/calc.go:3:// TODO: handle overflow\ncalculator/calc_test.go:9:// TODO: test Divide",
			}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInlineTestUI(t)
			box := newTestToolBox(t, tt.tool, ui.theme, ui.theme.ResolveTextBase())
			box.SetStyle(tt.style)
			box.SetExpanded(!tt.collapsed)
			buf := renderFrames(60, tt.h, box)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestToolBoxHeaderFrames draws the collapsed headers of completed calls that
// carry a result title, each a row: the tool name and then its path,
// pattern, or command, never the title.
func TestToolBoxHeaderFrames(t *testing.T) {
	ui := newInlineTestUI(t)
	completed := func(name, input, title string) *ToolBox {
		box := newTestToolBox(t, message.ToolPart{Name: name, State: message.ToolStateCompleted{
			Input: json.RawMessage(input), Output: "ok", Title: title,
		}}, ui.theme, ui.theme.ResolveTextBase())
		return box
	}
	buf := renderFrames(60, 5,
		completed("glob", `{"pattern":"calculator/*.go"}`, "Find calculator files"),
		completed("read", `{"path":"calculator/calc_test.go"}`, "Read calculator tests"),
		completed("edit", `{"path":"calculator/calc.go","old_string":"a","new_string":"b"}`, "Add Calculator.Divide"),
		completed("write", `{"path":"calculator/divide_test.go","content":"package calculator"}`, "Write the tests"),
		completed("bash", `{"command":"go test ./calculator/... -v","description":"Run calculator tests"}`, "Run calculator tests"),
	)
	testutil.AssertGoldenFrames(t, "toolbox_headers_completed", buf, surfaceNames(ui))
}

// TestUsageBoxFrames draws a usage box once shown, which it is not by
// default, as /settings shows tool calls: collapsed to its header, a row,
// with the marker when it is on, and expanded to every count the provider
// reported, in a block on the surface, or on the canvas when plain.
func TestUsageBoxFrames(t *testing.T) {
	part := message.UsagePart{ID: "a1-usage", Usage: message.Usage{
		InputTokens: 1200, CachedInputTokens: 1024, CacheCreationInputTokens: 176,
		OutputTokens: 300, ReasoningOutputTokens: 20, TotalTokens: 1676,
	}}
	tests := []struct {
		name     string
		h        int
		style    ExpandedStyle
		marker   bool
		expanded bool
	}{
		{name: "usagebox_collapsed", h: 1},
		{name: "usagebox_marker", h: 1, marker: true},
		{name: "usagebox_plain", h: 8, style: ExpandedPlain, expanded: true},
		{name: "usagebox_expanded", h: 10, marker: true, expanded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInlineTestUI(t)
			box := NewUsageBox(part, ui.theme, ui.theme.ResolveTextBase())
			box.SetGone(false)
			box.SetStyle(tt.style)
			box.SetMarker(tt.marker)
			box.SetExpanded(tt.expanded)
			buf := renderFrames(60, tt.h, box)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestDemoToolFrames draws the collapsed header of every call in the demo,
// boxed, in its last state, so that each way a call ends shows in one frame.
func TestDemoToolFrames(t *testing.T) {
	ui := newInlineTestUI(t)
	var boxes []*ToolBox
	for _, seg := range demo.Segments {
		if seg.Tool == nil {
			continue
		}
		box := newTestToolBox(t, message.ToolPart{Name: seg.Tool.Name,
			State: seg.Tool.State(time.Time{}, time.Time{})}, ui.theme, ui.theme.ResolveTextBase())
		boxes = append(boxes, box)
	}
	// Each collapsed box is its header alone, a row.
	buf := renderFrames(60, len(boxes)+1, boxes...)
	testutil.AssertGoldenFrames(t, "demo_tool_calls", buf, surfaceNames(ui))
}

// newInputTestUI returns a UI built by BuildUI whose footer label holds fixed
// text, so that its input box renders the same on every run. It returns to the
// package directory after building the app, which newTestService leaves, so
// that golden files resolve against this package's testdata.
func newInputTestUI(t *testing.T, loads ...extension.Load) *UI {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newTestService(t, loads...)
	s.Commands = builtinCommands(t)
	t.Chdir(wd)
	// What the renderer writes, such as the progress on the terminal tab,
	// would otherwise reach the test's output.
	r, err := terminal.NewRenderer(terminal.Config{InlineMode: true, Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ui := &UI{
		renderer:       r,
		theme:          DefaultTheme(),
		service:        s,
		extensions:     builtinExtensions(t),
		messageWidgets: make(map[message.ID]*ChatMessageBox),
	}
	ui.BuildUI()
	ui.SetModelLabel("provider/model")
	r.Root().Remove(ui.inputWrapper.ID())
	return ui
}

// findText returns the Text widget with ID id in root's subtree, or nil.
func findText(root render.Renderable, id string) *widget.Text {
	for _, child := range root.Children() {
		if text, ok := child.(*widget.Text); ok && text.ID() == id {
			return text
		}
		if text := findText(child, id); text != nil {
			return text
		}
	}
	return nil
}

func TestInputFrames(t *testing.T) {
	tests := []struct {
		name   string
		golden string // the golden file name; empty means name
		h      int
		open   func(ui *UI)
	}{
		{name: "input_closed", h: 5, open: func(*UI) {}},
		// After an idle Ctrl+C, which empties the input, the input names the
		// key that quits while a second press does.
		{name: "input_quit_hint", h: 5, open: func(ui *UI) {
			ui.ClearInput()
			ui.service.state.LastCtrlCTime = time.Now()
			ui.SyncInputHint()
		}},
		// Tab in /model asks for a model typed in, which the empty input
		// names with CustomModelPlaceholder, dim, as it names the quit key.
		{name: "input_custom_model", h: 5, open: func(ui *UI) {
			ui.Apply(ui.service.AskCustomModel(""))
		}},
		// The menu opens under its header, like every panel in the box.
		{name: "input_slash_menu", h: 15, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
		}},
		// Under the fill selection style, the selected command's row is
		// filled a cell in from either edge, its text where it always is.
		{name: "input_slash_fill", h: 19, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionFill))
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
		}},
		// Under the arrow style, an arrow in the left border cell marks the
		// selected command, whose row is drawn in the primary color.
		{name: "input_slash_arrow", h: 19, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionArrow))
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
		}},
		// In the filled input style, the input sits in a box on the box
		// background, with no marker.
		{name: "input_filled", h: 5, open: func(ui *UI) {
			ui.Apply(ui.service.SetInputStyle(InputFilled))
		}},
		// The panels that open in the box fill it too, their titles unmarked.
		{name: "input_filled_slash_menu", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.SetInputStyle(InputFilled))
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
		}},
		// In the bar input style, the default, the marker follows the shape
		// /settings sets: in the primary color before the input, and in the
		// color of a panel's title before it.
		{name: "input_bar_circle", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.SetMarkerShape(MarkerCircle))
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
		}},
		// Back in the bar style, the box shows as it did before it was filled.
		{name: "input_bar_again", golden: "input_slash_menu", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.SetInputStyle(InputFilled))
			ui.Apply(ui.service.SetInputStyle(InputBar))
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
		}},
		// Closing the menu leaves the box as it was.
		{name: "input_slash_closed", golden: "input_closed", h: 5, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
			ui.HideSlashMenu()
		}},
		// A command found by an alias shows as it always does: its name, and
		// the description that holds the alias.
		{name: "input_slash_alias", h: 14, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems("hist"))
		}},
		// The menu shows five rows, and the selected command's place after
		// its title.
		{name: "input_slash_all", h: 19, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
		}},
		// Moving past the fifth row scrolls the menu, and the place follows
		// the selection.
		{name: "input_slash_scrolled", h: 19, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
			// The menu scrolls by the height it was last drawn at, and the
			// app draws it before a key reaches it.
			renderFrames(60, 19, ui.inputWrapper)
			ui.inputWrapper.Parent().Remove(ui.inputWrapper.ID())
			for range 5 {
				ui.slashMenu.MoveDown()
			}
		}},
		// Text selected with shift and the arrows is the input box's fill
		// on the primary color.
		{name: "input_selected", h: 5, open: func(ui *UI) {
			for range 3 {
				ui.input.HandleKeyPress(&input.KeyEvent{Name: input.KeyLeft, Mods: input.ModShift, Type: input.KeyPress})
			}
		}},
		{name: "input_multiline", h: 7, open: func(ui *UI) {
			ui.SetInputText("Add a Divide method to the calculator that returns an error " +
				"when the divisor is zero, then run the calculator tests and report the result.")
		}},
		// Each queued prompt shows in a box of its own above the input box,
		// the first with the key that sends it now.
		{name: "input_queued", h: 13, open: func(ui *UI) {
			ui.ShowQueue([]string{
				"also update the README",
				"then run go vet on every package in the repository and report the result\nthen fix what it finds",
			})
		}},
		// The queue's boxes sit between the Usage box and the input box, in
		// which the menu opens.
		{name: "input_slash_queued", h: 19, open: func(ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
			ui.ShowQueue([]string{"also update the README"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.SetInputText("hello")
			tt.open(ui)
			buf := renderFrames(60, tt.h, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, cmp.Or(tt.golden, tt.name), buf, surfaceNames(ui))
		})
	}
}

// TestSlashStatusFrames draws the status box the slash menu shows above the
// input box, under its "Usage" header: the model without its provider, the
// context, the session's token totals and cost, and how long its turns took.
func TestSlashStatusFrames(t *testing.T) {
	usage := SessionUsage{Input: 120_000, Output: 8_400, CacheRead: 310_000, CacheWrite: 2_000,
		Cost: 0.4249, Context: 64_000, Window: 922_000}
	tests := []struct {
		name  string
		w     int
		usage SessionUsage
		// pinned pins the box and draws it with the slash menu closed.
		pinned bool
		// marker shows the tool marker, as /settings does.
		marker bool
		// expanded expands tool calls, as Ctrl+O does.
		expanded bool
	}{
		{name: "slash_status", w: 84, usage: usage},
		// A narrow terminal cuts the row off at its right end.
		{name: "slash_status_narrow", w: 60, usage: usage},
		// Pinned, the box stays with the menu closed, and names no key, which
		// the menu names while it is open.
		{name: "slash_status_pinned", w: 84, usage: usage, pinned: true},
		// Collapsed, the box is a row like a collapsed tool call's, with no
		// background, and the marker before it while the tool marker is on.
		{name: "slash_status_marker", w: 84, usage: usage, marker: true},
		// With tool calls expanded, the box stays a row, with the marker, as
		// a row with nothing to expand does.
		{name: "slash_status_expanded", w: 84, usage: usage, marker: true, expanded: true},
		// A nearly full context turns the danger color.
		{name: "slash_status_context_full", w: 84, usage: SessionUsage{Input: 180_000, Output: 2_000,
			Cost: 0.57, Context: 182_000, Window: 200_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.SetInputText("hello")
			ui.SetModelLabel("provider-a/model-h:medium")
			ui.SetUsage(tt.usage)
			ui.service.state.Messages = []ChatMessage{{ID: "a1", Role: message.RoleAgent, Worked: 3*time.Minute + 12*time.Second}}
			if tt.marker {
				ui.Apply(ui.service.SetToolMarkers(true))
			}
			if tt.expanded {
				ui.Apply(ui.service.SetToolsExpanded(true))
			}
			h := 15
			if tt.pinned {
				ui.Apply(ui.service.HandleCtrlU())
				h = 11
			} else {
				ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
			}
			buf := renderFrames(tt.w, h, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestResumeNoticeFrames draws the notice that quitting leaves in the
// terminal: on the canvas, without a box, under the product logo and a blank
// row, it tells how to resume the session, with the command right under the
// line that introduces it. A command wider than the notice wraps.
func TestResumeNoticeFrames(t *testing.T) {
	const id = "01a12e38-2689-73a1-a799-5db6b6a8bc44"
	for _, tt := range []struct {
		name string
		w, h int
	}{
		{name: "resume_notice", w: 60, h: 12},
		{name: "resume_notice_wrapped", w: 36, h: 13},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInlineTestUI(t)
			buf := renderFrames(tt.w, tt.h, NewResumeNotice(id, ui.theme))
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestQuitFrames draws the frame that quitting inline leaves in the terminal:
// the transcript, then the input box, the footer's row under it left blank,
// and the product logo over the notice that tells how to resume the session.
func TestQuitFrames(t *testing.T) {
	ui := newInputTestUI(t)
	ui.inline = true
	ui.AddMessage(userText("u1", "Run the tests"))
	ui.AddMessage(agentText("a1", "All tests pass."))
	// The second Ctrl+C quits while the input names the key that quits. The
	// frame leaves the input box out, and ends with the transcript.
	ui.service.state.LastCtrlCTime = time.Now()
	ui.SyncInputHint()
	ui.PrepareInlineQuit()
	// The test session is not stored, so the frame takes a notice with a
	// fixed ID in place of the one a stored session adds.
	rows := append(detachTranscript(ui), ui.inputWrapper, NewResumeNotice("01a12e38-2689-73a1-a799-5db6b6a8bc44", ui.theme))
	buf := renderFrames(72, 22, rows...)
	testutil.AssertGoldenFrames(t, "quit_inline", buf, surfaceNames(ui))
}

// detachFromRoot removes box from the renderer root so that a test can draw
// it on a root of its own, and returns it.
func detachFromRoot(ui *UI, box *widget.Box) *widget.Box {
	ui.renderer.Root().Remove(box.ID())
	return box
}

// replyStarted adds a prompt and the reply its run has just opened, which
// holds no parts yet.
func replyStarted(ui *UI) {
	ui.AddMessage(userText("u1", "ok"))
	ui.AddMessage(agentParts("a1"))
}

// TestIndicatorFrames draws the indicator between the inline transcript and
// the input box, as a line on the canvas with one blank row above it and one
// below it. While it shows the time of a run in progress, the footer under
// the input box shows the run's key hints.
func TestIndicatorFrames(t *testing.T) {
	tests := []struct {
		name string
		h    int
		add  func(ui *UI)
		show EventIndicatorShow
		// working shows the indicator as the time of a run in progress.
		working bool
	}{
		// A reply that shows nothing yet takes no rows, and the prompt's blank
		// row parts it from the indicator.
		{name: "indicator_reply_started", h: 11, add: replyStarted, show: WorkingIndicator(12*time.Second, false), working: true},
		// Once the reply shows text, a blank row parts it from the indicator.
		// Reasoning that streamed no text shows nothing.
		{name: "indicator_reply_streaming", h: 13, add: func(ui *UI) {
			replyStarted(ui)
			ui.UpdateMessage(agentParts("a1",
				message.ReasoningPart{ID: "a1-think", Signature: "sig"},
				message.TextPart{ID: "a1-text", Text: "Running them now."},
			))
		}, show: WorkingIndicator(12*time.Second, false), working: true},
		// A turn after a tool call is spaced from it once it shows something.
		{name: "indicator_next_turn", h: 19, add: func(ui *UI) {
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddMessage(agentParts("a1", message.ToolPart{CallID: "c1", Name: "bash",
				State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}))
			next := agentParts("a2")
			next.ParentID = "a1"
			ui.AddMessage(next)
		}, show: WorkingIndicator(12*time.Second, false), working: true},
		// An error starts with a marker and Error, in the danger color, as a
		// tool call's row starts with its marker and name.
		{name: "indicator_error", h: 11, add: replyStarted,
			show: ErrorIndicator(app.NewUnknownError(`Request Failed: Post "https://api.example.com/v1/messages": EOF`))},
		// A provider's error names the provider and status, over the body it
		// answered with, formatted and highlighted, in a box filled as a
		// tool's is.
		{name: "indicator_response_error", h: 20, add: replyStarted,
			show: ErrorIndicator(app.ErrorOf(&agent.ResponseError{Provider: "provider-b", StatusCode: 429,
				ResponseBody: `{"type":"error","error":{"type":"rate_limit_error","message":"Error"},"request_id":"req_011CX7Yq2M"}`}))},
		// A body that is not JSON shows as its text, wrapped.
		{name: "indicator_response_error_text", h: 13, add: replyStarted,
			show: ErrorIndicator(app.ErrorOf(&agent.ResponseError{Provider: "gw", StatusCode: 502,
				ResponseBody: "upstream connect error or disconnect/reset before headers. reset reason: connection termination"}))},
		// Ctrl+O says what it is doing while it lays out a long transcript
		// again with every tool call expanded.
		{name: "indicator_expanding_tools", h: 13, add: func(ui *UI) {
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddMessage(agentParts("a1", message.ToolPart{CallID: "c1", Name: "bash",
				State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}))
		}, show: EventIndicatorShow{Text: ExpandingToolsText}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			tt.add(ui)
			ui.service.state.Working = tt.working
			ui.ShowIndicator(tt.show)
			rows := slices.Concat(detachTranscript(ui), []*widget.Box{detachFromRoot(ui, ui.indicatorBox), ui.inputWrapper})
			buf := renderFrames(72, tt.h, rows...)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestIndicatorBlinkFrames draws the indicator of a run in progress, whose
// marker before the time blinks: shown, and hidden, while the time stays
// where it is. The time and the hint after it stay in their own colors.
func TestIndicatorBlinkFrames(t *testing.T) {
	for _, tt := range []struct {
		name string
		off  bool
	}{
		{name: "indicator_blink_on"},
		{name: "indicator_blink_off", off: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			ui.service.state.Working = true
			ui.ShowIndicator(WorkingIndicator(12*time.Second, false))
			ui.blink.Off = tt.off
			buf := renderFrames(56, 2, detachFromRoot(ui, ui.indicatorBox))
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestWorkedFrames draws the footer under a turn's last reply, inset like its
// text, before the next prompt: how long the turn took.
func TestWorkedFrames(t *testing.T) {
	bash := message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}
	usage := message.Usage{InputTokens: 1200, OutputTokens: 300, CachedInputTokens: 1024, CacheCreationInputTokens: 176}
	tests := []struct {
		name string
		h    int
		add  func(ui *UI)
	}{
		// A run's turn shows its footer once the run ends.
		{name: "worked_run_ended", h: 8, add: func(ui *UI) {
			prompt := message.Content{ID: "u1", Role: message.RoleUser,
				Parts: []message.ContentPart{message.TextPart{ID: "u1-text", Text: "Run the tests"}}}
			reply := message.Content{ID: "a1", Role: message.RoleAgent, Model: "provider/model"}
			done := reply
			done.Parts = []message.ContentPart{
				message.TextPart{ID: "a1-text", Text: "All tests pass."},
				message.UsagePart{ID: "a1-usage", Usage: usage},
			}
			for _, ev := range []app.Event{
				app.EventAgentStarted{EventBase: at(0)},
				app.EventMessageStarted{EventBase: at(0), Message: app.Message{Content: prompt}},
				app.EventMessageEnded{EventBase: at(0), Message: app.Message{Content: prompt}},
				app.EventMessageStarted{EventBase: at(time.Second), Message: app.Message{Content: reply}},
				app.EventMessageEnded{EventBase: at(65 * time.Second), Message: app.Message{Content: done}},
				app.EventAgentEnded{EventBase: at(65 * time.Second)},
			} {
				ui.Apply(ui.service.ApplyAgentEvent(ev))
			}
		}},
		// A reply that read and wrote no cache shows no cache use.
		{name: "worked_cache_miss", h: 5, add: func(ui *UI) {
			msg := agentText("a1", "Done.")
			msg.Model = "provider-a/model-b"
			msg.Usage = message.Usage{InputTokens: 1200, OutputTokens: 300}
			msg.Worked = 42 * time.Second
			ui.AddMessage(msg)
		}},
		// A last reply that shows nothing but metadata that no extension
		// shows, such as its snapshot part, and so counted no tokens, is parted
		// from the reply above it by one blank row, as a reply is.
		{name: "worked_alone", h: 7, add: func(ui *UI) {
			ui.AddMessage(agentParts("a1", bash))
			last := agentParts("a2", snapshot.SnapshotPart{ID: "a2-step"})
			last.ParentID, last.Worked = "a1", 3*time.Second
			ui.AddMessage(last)
		}},
		// A loaded transcript shows the footer of each of its turns, timed
		// until its last reply was stored, and per message the cache use of
		// each reply.
		{name: "worked_loaded", h: 20, add: func(ui *UI) {
			ui.Apply(ui.service.SetCacheMode(CachePerMessage))
			modelD := func(msg ChatMessage) ChatMessage {
				msg.Model = "provider-b/model-d:high"
				return msg
			}
			first := modelD(agentParts("a1", bash))
			first.Usage = message.Usage{InputTokens: 900, OutputTokens: 40}
			second := modelD(agentText("a2", "All tests pass."))
			second.Usage, second.Window = usage, 200_000
			msgs := []ChatMessage{
				userText("u1", "Run the tests"),
				first,
				second,
				userText("u2", "commit it"),
				modelD(agentText("a3", "Committed.")),
			}
			for i, d := range []time.Duration{0, 4 * time.Second, 42 * time.Second, time.Minute, time.Minute + 7*time.Second} {
				msgs[i].CreatedAt = turnT0.Add(d)
			}
			AssignParents(msgs)
			AssignWorked(msgs)
			ui.LoadTranscript(msgs)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			tt.add(ui)
			buf := renderFrames(80, tt.h, detachTranscript(ui)...)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// cachedReply returns reply id holding text, which read cached tokens from a
// prompt cache and wrote none to it.
func cachedReply(id message.ID, text string, cached int) ChatMessage {
	msg := agentText(id, text)
	msg.Usage = message.Usage{InputTokens: cached + 1_000, CachedInputTokens: cached}
	return msg
}

// cacheTurn adds a turn of two replies that read from a prompt cache: a1,
// whose bash call failed, and a2, whose text ends the turn.
func cacheTurn(ui *UI) {
	ui.AddMessage(userText("u1", "fix the divide test"))
	first := agentParts("a1",
		message.TextPart{ID: "a1-text", Text: "Running the tests."},
		message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash", State: message.ToolStateError{
			Input: json.RawMessage(`{"command":"go test ./..."}`), Error: "FAIL\n\n[exit 1]"}})
	first.Usage = message.Usage{InputTokens: 22_000, CachedInputTokens: 18_000, CacheCreationInputTokens: 4_000}
	second := cachedReply("a2", "Fixed the divide-by-zero check.", 22_000)
	second.ParentID, second.Worked = "a1", 39*time.Second
	ui.AddMessage(first)
	ui.AddMessage(second)
}

// TestCacheFrames draws the cache use of each reply as a row of its own under
// the reply, like a collapsed tool call's: the name and the counts in the
// muted text color. A reply's text and tool calls show as they do without it.
// The row stacks under a tool call above it, and is parted by a blank row
// from text, and the footer under the turn's last reply is parted from the row
// by a blank row, as it is from any row. In the per turn mode there are no
// rows, and the footer shows the cache use of the turn's last request after
// the turn's time.
func TestCacheFrames(t *testing.T) {
	tests := []struct {
		name string
		w, h int
		add  func(ui *UI)
	}{
		{name: "cache_tool_then_text", w: 60, h: 15, add: cacheTurn},
		{name: "cache_per_turn", w: 60, h: 13, add: func(ui *UI) {
			ui.Apply(ui.service.SetCacheMode(CachePerTurn))
			cacheTurn(ui)
		}},
		// The row starts with the marker, as tool calls do, and so does the
		// footer.
		{name: "cache_marker", w: 60, h: 15, add: func(ui *UI) {
			ui.Apply(ui.service.SetToolMarkers(true))
			cacheTurn(ui)
		}},
		// A reply that ends with a code block, a block of its own, is parted
		// from the row by a blank row.
		{name: "cache_code_block", w: 40, h: 9, add: func(ui *UI) {
			ui.AddMessage(cachedReply("a1", "Run this:\n\n```\ngo test ./...\n```", 7_700))
		}},
		// A reply that ends its turn with a row, such as a tool call or a
		// snapshot, shows the cache row stacked under it, and the footer
		// under that parted by a blank row.
		{name: "cache_row_then_footer", w: 40, h: 8, add: func(ui *UI) {
			msg := agentParts("a1", message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash",
				State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}})
			msg.Usage, msg.Worked = message.Usage{InputTokens: 1_200}, 3*time.Second
			ui.AddMessage(msg)
		}},
		// A reply that read and wrote no cache shows the counts as none.
		{name: "cache_miss", w: 40, h: 4, add: func(ui *UI) {
			msg := agentText("a1", "Done.")
			msg.Usage = message.Usage{InputTokens: 1_200, OutputTokens: 300}
			ui.AddMessage(msg)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			// The cache use shows in a row under each reply, unless a case
			// sets another mode.
			ui.Apply(ui.service.SetCacheMode(CachePerMessage))
			tt.add(ui)
			buf := renderFrames(tt.w, tt.h, detachTranscript(ui)...)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestKeyHintsModeFrames draws the panels in the key hint modes other than
// the default: KeyHintsFooter, which names the open panel's keys in the
// footer under the input box, and KeyHintsSubtitle, which names them on a row
// under the panel's title, one row taller. Both name each key before what it
// does, and none after the panel's title.
func TestKeyHintsModeFrames(t *testing.T) {
	sessions := threeSessionRows()
	tests := []struct {
		name string
		w, h int
		open func(ui *UI)
	}{
		{name: "keys_footer_slash_menu", w: 60, h: 15, open: func(ui *UI) {
			ui.SetInputText("hello")
			ui.ShowSlashMenu(ui.service.SlashMenuItems("/st"))
		}},
		{name: "keys_footer_session_menu", w: 72, h: 11, open: func(ui *UI) {
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessions})
		}},
		{name: "keys_footer_settings", w: 72, h: 16, open: func(ui *UI) {
			ui.Apply(ui.service.ShowSettingsMenu())
		}},
		{name: "keys_footer_tree", w: 72, h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
		}},
	}
	for _, mode := range []KeyHintsMode{KeyHintsFooter, KeyHintsSubtitle} {
		for _, tt := range tests {
			name, h := tt.name, tt.h
			if mode == KeyHintsSubtitle {
				name, h = strings.Replace(name, "keys_footer_", "keys_subtitle_", 1), h+1
			}
			t.Run(name, func(t *testing.T) {
				ui := newInputTestUI(t)
				ui.Apply(ui.service.SetKeyHintsMode(mode))
				tt.open(ui)
				buf := renderFrames(tt.w, h, ui.inputWrapper)
				testutil.AssertGoldenFrames(t, name, buf, surfaceNames(ui))
			})
		}
	}
}

// TestCountInTitleFrames draws the lists whose rows do not all fit with
// CountPlace set to CountInTitle: each counts its rows after its title, and
// not on a line under them, where the tree keeps its filter alone.
func TestCountInTitleFrames(t *testing.T) {
	tests := []struct {
		name string
		w, h int
		open func(ui *UI)
	}{
		// The slash menu counts no rows after its title either.
		{name: "count_title_slash", w: 60, h: 19, open: func(ui *UI) {
			ui.SetInputText("hello")
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
		}},
		{name: "count_title_session", w: 72, h: 17, open: func(ui *UI) {
			rows := make([]widget.SelectItem, 10)
			for i := range rows {
				rows[i] = widget.SelectItem{
					Name:  fmt.Sprintf("01a0e1b4-77a3-7028-9d5e-4e14a6e9b5%02d  2026-09-%02d 09:14:02", i+1, 28-i),
					Value: "s",
				}
			}
			ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		}},
		{name: "count_title_banner", w: 72, h: 21, open: func(ui *UI) {
			items := make([]MenuItem, 15)
			for i := range items {
				items[i] = MenuItem{Name: fmt.Sprintf("provider/model-%02d", i+1)}
			}
			ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: "Models", Items: func() []MenuItem { return items }}))
			ui.bannerMenu.MoveDown()
			ui.bannerMenu.MoveDown()
		}},
		{name: "count_title_tree", w: 72, h: 21, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(longTree(30)))
			ui.Apply(ui.service.CycleTreeFilter())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.Apply(ui.service.SetCountPlace(CountInTitle))
			tt.open(ui)
			buf := renderFrames(tt.w, tt.h, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// sessionRows returns two rows of the session list, which select the
// sessions first and second.
// threeSessionRows returns the rows of three sessions, as the session list
// names them, the first titled and active, with values "s1" to "s3".
func threeSessionRows() []widget.SelectItem {
	rows := sessionRows("s1", "s2")
	names := SessionRowNames([]session.Session{
		{ID: "01a0e1b4-77a3-7028-9d5e-4e14a6e9b5e7", Title: "Add Divide", Worktree: "/work/project", UpdatedAt: time.Date(2026, 9, 28, 9, 14, 2, 0, time.Local)},
		{ID: "01a0e33d-4571-7ed4-b06b-4229c726c313", Worktree: "/work/project", UpdatedAt: time.Date(2026, 9, 27, 21, 40, 55, 0, time.Local)},
		{ID: "019ff2c8-0b1e-7c55-a2d4-6c1f3e8a90b2", Worktree: "/work/other", UpdatedAt: time.Date(2026, 9, 26, 16, 3, 18, 0, time.Local)},
	})
	return append(rows, widget.SelectItem{Name: names[2], Value: "s3"})
}

// sessionRows returns the rows of two sessions, as the session list names
// them, the first titled and active, with values first and second.
func sessionRows(first, second any) []widget.SelectItem {
	names := SessionRowNames([]session.Session{
		{ID: "01a0e1b4-77a3-7028-9d5e-4e14a6e9b5e7", Title: "Add Divide", Worktree: "/work/project", UpdatedAt: time.Date(2026, 9, 28, 9, 14, 2, 0, time.Local)},
		{ID: "01a0e33d-4571-7ed4-b06b-4229c726c313", Worktree: "/work/project", UpdatedAt: time.Date(2026, 9, 27, 21, 40, 55, 0, time.Local)},
	})
	return []widget.SelectItem{
		{Name: names[0], Hint: CurrentHint, Value: first},
		{Name: names[1], Value: second},
	}
}

// TestInlineMenuFrames draws the inline transcript above the input box with a
// menu open in it, as inline mode lays them out.
func TestInlineMenuFrames(t *testing.T) {
	tests := []struct {
		name string
		h    int
		open func(t *testing.T, ui *UI)
	}{
		// The session list opens like every other menu, under the transcript,
		// which stays on screen.
		{name: "session_menu_inline", h: 16, open: func(_ *testing.T, ui *UI) {
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessionRows("s1", "s2")})
		}},
		// The selected session's prompts are listed under the rows.
		{name: "session_menu_prompts", h: 20, open: func(t *testing.T, ui *UI) {
			a := ui.service.app
			loaded := awaitPrompts(t, ui)
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessionRows(
				storePrompts(t, a, "add a Divide method to the calculator", "also add tests",
					"use table-driven tests instead"),
				storePrompts(t, a, "fix the flaky login test"),
			)})
			loaded()
		}},
		// Until the selected session's prompts load, a dim [loading] takes
		// their place.
		{name: "session_menu_prompts_loading", h: 16, open: func(t *testing.T, ui *UI) {
			awaitPrompts(t, ui)
			a := ui.service.app
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessionRows(
				storePrompts(t, a, "add a Divide method to the calculator"),
				storePrompts(t, a, "fix the flaky login test"),
			)})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.AddMessage(agentText("a1", "All tests pass."))
			tt.open(t, ui)
			rows := append(detachTranscript(ui), ui.inputWrapper)
			buf := renderFrames(72, tt.h, rows...)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestFullscreenSessionPromptsFrames checks that fullscreen mode lists the
// selected session's prompts under the session list, each after a "User"
// label, as inline mode and the session tree do.
func TestFullscreenSessionPromptsFrames(t *testing.T) {
	ui := newInputTestUI(t)
	ui.inline = false
	a := ui.service.app
	loaded := awaitPrompts(t, ui)
	ui.ShowSessionMenu(EventSessionMenuShow{Items: sessionRows(
		storePrompts(t, a, "add a Divide method to the calculator", "also add tests",
			"use table-driven tests instead"),
		storePrompts(t, a, "fix the flaky login test"),
	)})
	loaded()
	buf := renderFrames(72, 14, ui.inputWrapper)
	testutil.AssertGoldenFrames(t, "session_menu_prompts_fullscreen", buf, surfaceNames(ui))
}

// TestShortTerminalFrames draws the input box fitted to a terminal of the
// frame's height: it ends a row short of the bottom, and a list shows the
// rows that fit, counted in its title.
func TestShortTerminalFrames(t *testing.T) {
	tests := []struct {
		name   string
		w      int // the frame's width; zero means 60
		height int
		open   func(t *testing.T, ui *UI)
	}{
		{name: "input_slash_short", height: 12, open: func(_ *testing.T, ui *UI) {
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
		}},
		{name: "tree_panel_short", height: 14, open: func(_ *testing.T, ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
		}},
		// The input scrolls within the rows that fit, keeping its cursor.
		{name: "input_multiline_short", height: 8, open: func(_ *testing.T, ui *UI) {
			ui.SetInputText("first line\nsecond line\nthird line\nfourth line\nfifth line\nlast line")
		}},
		// The prompts under the session list give their rows up first, and
		// list the latest that fit after a count of the earlier ones.
		{name: "session_menu_prompts_short", w: 72, height: 16, open: func(t *testing.T, ui *UI) {
			ui.inline = true
			a := ui.service.app
			loaded := awaitPrompts(t, ui)
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessionRows(
				storePrompts(t, a, "add a Divide method to the calculator", "also add tests",
					"use table-driven tests instead", "run them", "add a benchmark for Divide",
					"document the zero check", "now run go vet", "commit it"),
				storePrompts(t, a, "fix the flaky login test"),
			)})
			loaded()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			w := cmp.Or(tt.w, 60)
			// The box is fitted at the width it is drawn at, where its key
			// hints may wrap.
			root := ui.renderer.Root()
			root.Resize(w, tt.height)
			root.Add(ui.inputWrapper)
			ui.SetInputText("hello")
			tt.open(t, ui)
			ui.FitInputBox(tt.height)
			root.Remove(ui.inputWrapper.ID())
			buf := renderFrames(w, tt.height, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestIndicatorErrorShortFrames draws an error whose body is taller than the
// terminal leaves it: the body gives its rows up first, down to a line, so
// that the input stays in view under it.
func TestIndicatorErrorShortFrames(t *testing.T) {
	var body strings.Builder
	body.WriteString("{")
	for i := range 30 {
		fmt.Fprintf(&body, `"k%d":%d,`, i, i)
	}
	body.WriteString(`"end":0}`)
	for _, tt := range []struct {
		name   string
		height int
	}{
		{name: "indicator_error_short", height: 16},
		{name: "indicator_error_shortest", height: 10},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			root := ui.renderer.Root()
			root.Resize(60, tt.height)
			root.Add(ui.inputWrapper)
			ui.SetInputText("hello")
			ui.ShowIndicator(ErrorIndicator(app.ErrorOf(&agent.ResponseError{Provider: "gw", StatusCode: 400, ResponseBody: body.String()})))
			ui.FitInputBox(tt.height)
			root.Remove(ui.inputWrapper.ID())
			buf := renderFrames(60, tt.height, detachFromRoot(ui, ui.indicatorBox), ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestFullscreenFrames draws the scrolling transcript above the input, as
// fullscreen mode lays them out.
func TestFullscreenFrames(t *testing.T) {
	tests := []struct {
		name string
		h    int
		add  func(ui *UI)
	}{
		// The transcript overflows and follows its bottom edge, which keeps the
		// blank row under the last notice above the input.
		{name: "fullscreen_notice_above_input", h: 10, add: func(ui *UI) {
			ui.AddNotice(Notice{Text: "Chat cleared"})
			ui.AddNotice(Notice{Text: "Nothing to undo"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			tt.add(ui)
			ui.renderer.Root().Remove(ui.messageBox.ID())
			buf := renderFrames[render.Renderable](72, tt.h, ui.messageBox, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

func TestMenuFrames(t *testing.T) {
	sessions := threeSessionRows()
	tests := []struct {
		name string
		h    int
		open func(ui *UI)
		// loads returns the extensions of the app the menu opens in, after
		// those of every test UI.
		loads func(t *testing.T) []extension.Load
	}{
		// Each menu opens at the top of the input box, which searches it,
		// under a header: its title, and its key hints. The footer keeps the
		// model.
		{name: "banner_demo_menu", h: 12, open: func(ui *UI) {
			ui.Apply(runCommand(t, ui.service, "demo"))
		}},
		{name: "banner_demo_search", h: 10, open: func(ui *UI) {
			ui.Apply(runCommand(t, ui.service, "demo"))
			ui.input.InsertText("err")
		}},
		// A divider parts the header from the text and menu below it. Without
		// a search the input row is hidden, since it takes no keys.
		{name: "banner_login_progress", h: 8, open: func(ui *UI) {
			ui.ShowBanner(EventBannerShow{
				Title: "Login with Provider A",
				Text:  "Logging in to Provider A account 01a0e1b4 [browser]...",
			})
		}},
		{name: "banner_login_menu", h: 9, open: func(ui *UI) {
			ui.Apply(ui.service.ShowLoginMenu())
		}},
		// A provider without a login flow offers the API key alone.
		{name: "banner_login_key_method", h: 6, open: func(ui *UI) {
			ui.Apply(ui.service.ShowLoginMethodMenu(keyBuiltIn))
		}, loads: func(t *testing.T) []extension.Load { return []extension.Load{loadKeyOnlyCatalog(t)} }},
		// With the input hidden, the banner is the last row of the input box,
		// under the queue's box.
		{name: "banner_login_queued", h: 12, open: func(ui *UI) {
			ui.ShowQueue([]string{"also update the README"})
			ui.ShowBanner(EventBannerShow{
				Title: "Login with Provider A",
			})
		}},
		// The text sits under the header's divider, where a menu would.
		{name: "banner_loading", h: 7, open: func(ui *UI) {
			ui.ShowBanner(EventBannerShow{Title: "Models", Text: "Loading models..."})
		}},
		{name: "banner_settings_menu", h: 13, open: func(ui *UI) {
			ui.Apply(ui.service.ShowSettingsMenu())
		}},
		// In the filled input style, /settings fills the box, its title
		// unmarked.
		{name: "banner_settings_filled", h: 13, open: func(ui *UI) {
			ui.Apply(ui.service.SetInputStyle(InputFilled))
			ui.Apply(ui.service.ShowSettingsMenu())
		}},
		// → moves the selected setting to its next value, and the setting
		// stays selected: cache usage goes from off to per turn.
		{name: "banner_settings_changed", h: 13, open: func(ui *UI) {
			ui.Apply(ui.service.ShowSettingsMenu())
			ui.HandleBannerKey(&input.KeyEvent{Name: input.KeyDown})
			ui.HandleBannerKey(&input.KeyEvent{Name: input.KeyRight})
		}},
		// The model menu names each model by the catalog's name of its
		// variant, such as "Model A Fast", and then, dim, in aligned
		// columns, its variant ID and its provider, such as
		// "provider-a/model-a:fast   [provider-a]", with the terminal's
		// model selected.
		{name: "banner_model_menu", h: 13, open: func(ui *UI) {
			ui.Apply(ui.service.SelectModel("provider-a/model-a:fast:high"))
			ui.service.OpenBanner(ModelBanner)
			cat := ui.service.app.Models().Catalog()
			ui.Apply(ui.service.ModelsListed([]app.ModelListing{
				listing(cat, providerA, "model-a", "model-a-pro", "model-b"),
				listing(cat, providerB, "model-g"),
			}))
		}},
		// The model menu's next page: the levels the chosen model takes,
		// with the default marked and the one it would run at selected.
		{name: "banner_model_effort", h: 14, open: func(ui *UI) {
			ui.Apply(ui.service.SelectModel("provider-a/model-a:high"))
			ui.Apply(ui.service.ShowEffortMenu(agent.Variant{Provider: providerA, Model: "provider-a/model-a", Option: "fast"}))
		}},
		// The default is marked on the selected row too, as /effort shows it
		// for a model that runs at its default.
		{name: "banner_effort_default_selected", h: 14, open: func(ui *UI) {
			ui.Apply(ui.service.SelectModel("provider-a/model-a:medium"))
			ui.Apply(ui.service.ShowEffortMenu(agent.Variant{Provider: providerA, Model: "provider-a/model-a"}))
		}},
		// Under the fill selection style, → on the Selection setting fills
		// the selected row in the primary color, a cell in from either edge,
		// with no arrow, its name and value in the box's fill.
		{name: "banner_settings_fill", h: 16, open: func(ui *UI) {
			out := ui.service.ShowSettingsMenu()
			ui.Apply(out)
			var names []string
			for _, ev := range out {
				if show, ok := ev.(EventBannerShow); ok {
					names = bannerNames(show)
				}
			}
			for range slices.Index(names, selectionSetting) {
				ui.HandleBannerKey(&input.KeyEvent{Name: input.KeyDown})
			}
			ui.HandleBannerKey(&input.KeyEvent{Name: input.KeyRight})
		}},
		{name: "session_menu", h: 11, open: func(ui *UI) {
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessions})
		}},
		// The fill takes the selected row's hint, such as "[current]", too.
		{name: "session_menu_fill", h: 11, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionFill))
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessions})
		}},
		// Under the arrow style, the selected row's hint stays dim.
		{name: "session_menu_arrow", h: 11, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionArrow))
			ui.ShowSessionMenu(EventSessionMenuShow{Items: sessions})
		}},
		// The notice takes the place of the rows, above the query.
		{name: "session_menu_no_match", h: 9, open: func(ui *UI) {
			ui.ShowSessionMenu(EventSessionMenuShow{Text: NoMatchesText, Query: "zzz"})
		}},
		// Rows that do not all fit are counted after the menu's title, and the
		// count follows the selection.
		{name: "banner_menu_overflow", h: 21, open: func(ui *UI) {
			items := make([]MenuItem, 15)
			for i := range items {
				items[i] = MenuItem{Name: fmt.Sprintf("provider/model-%02d", i+1)}
			}
			ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: "Models", Items: func() []MenuItem { return items }}))
			ui.bannerMenu.MoveDown()
			ui.bannerMenu.MoveDown()
		}},
		{name: "session_menu_overflow", h: 17, open: func(ui *UI) {
			rows := make([]widget.SelectItem, 10)
			for i := range rows {
				rows[i] = widget.SelectItem{
					Name:  fmt.Sprintf("01a0e1b4-77a3-7028-9d5e-4e14a6e9b5%02d  2026-09-%02d 09:14:02", i+1, 28-i),
					Value: "s",
				}
			}
			ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		}},
		{name: "tree_panel_overflow", h: 21, open: func(ui *UI) {
			msgs := make([]message.Content, 30)
			parent := message.ID("")
			for i := range msgs {
				id := message.ID(fmt.Sprintf("m%02d", i))
				role := message.RoleUser
				if i%2 == 1 {
					role = message.RoleAgent
				}
				msgs[i] = treeMsg(id, parent, role, fmt.Sprintf("message %d", i+1))
				parent = id
			}
			ui.Apply(ui.service.OpenTree(app.Tree{Messages: msgs, Head: "m29"}))
		}},
		// The session tree draws every branch, dims the rows off the
		// transcript, and selects its end.
		{name: "tree_panel", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
		}},
		// Under the arrow style, the selected row's arrow and text are in
		// the primary color.
		{name: "tree_panel_arrow", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionArrow))
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
		}},
		// Under the fill style, the fill starts a cell in from either edge,
		// as in a menu.
		{name: "tree_panel_fill", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.SetSelectionStyle(SelectionFill))
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
		}},
		// A selected row off the transcript is drawn in full.
		// Each tool call reads like a role: a colored name, then its input.
		{name: "tree_panel_tools_on_transcript", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a4")))
		}},
		{name: "tree_panel_tool_selected", h: 15, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
			ui.Apply(ui.service.MoveTree(2))
		}},
		// The input searches the tree, and a match hangs under its nearest
		// listed ancestor.
		{name: "tree_panel_search", h: 10, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
			ui.input.InsertText("table")
		}},
		{name: "tree_panel_no_match", h: 9, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
			ui.input.InsertText("zzz")
		}},
		// Ctrl+T shows the forks off the conversation, such as the exchange
		// that titled the session: off the transcript, dim, tagged with
		// their type, ahead of the branch they forked from, and named in the
		// status line.
		{name: "tree_panel_hidden", h: 17, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(titledTree("a2")))
			ui.Apply(ui.service.ToggleTreeHidden())
			ui.Apply(ui.service.MoveTree(-1))
		}},
		{name: "tree_panel_user_only", h: 12, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
			ui.Apply(ui.service.CycleTreeFilter())
			ui.Apply(ui.service.CycleTreeFilter())
		}},
		// Choosing a prompt closes the tree and, once the session has moved
		// before the prompt, leaves its text in the input to edit and send.
		{name: "tree_prompt_chosen", h: 5, open: func(ui *UI) {
			ui.Apply(ui.service.OpenTree(forkedTree("a2")))
			ui.Apply(ui.service.MoveTree(1))
			for _, ev := range ui.service.ChooseTreeRow() {
				if move, ok := ev.(EventTreeMoveRequested); ok {
					ui.Apply(ui.service.CompleteTreeMove(move, app.MoveResult{}, nil))
					continue
				}
				ui.ProcessEvent(ev)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loads []extension.Load
			if tt.loads != nil {
				loads = tt.loads(t)
			}
			ui := newInputTestUI(t, loads...)
			tt.open(ui)
			buf := renderFrames(72, tt.h, ui.inputWrapper)
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}

// TestManualLoginFrames draws a login whose code is pasted: the long
// authorization URL of an OAuth login, wrapped in full.
func TestManualLoginFrames(t *testing.T) {
	authorizeURL := "https://login.provider-b.test/oauth/authorize?" + url.Values{
		"code": {"true"}, "response_type": {"code"},
		"client_id":      {"0b5e4a3c-7d21-4f8e-9c6a-2e1f0d9b8a47"},
		"redirect_uri":   {"https://console.provider-b.test/oauth/code/callback"},
		"code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"},
		"scope": {"org:create_api_key user:profile user:inference user:sessions:agent"},
		"state": {strings.Repeat("b", 32)},
	}.Encode()
	for _, tt := range []struct {
		name string
		w, h int
	}{
		{"manual_login_code_narrow", 40, 34},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.inline = true
			ui.service.OpenBanner(LoginBanner)
			ui.ShowBanner(EventBannerShow{Title: "Login with Provider B"})
			ui.Apply(ui.service.ApplyAgentEvent(app.EventLoginPrompt{
				Provider: providerB, Account: "019ff2c8-0b1e-7c55-a2d4-6c1f3e8a90b2",
				URL: authorizeURL, State: &agent.LoginState{Verifier: "hidden-proof", State: strings.Repeat("b", 32)},
			}))
			if len(ui.service.state.Messages) != 0 || len(ui.transcriptRows) != 0 {
				t.Fatal("authorization instructions were added to the transcript")
			}
			buf := renderFrames(tt.w, tt.h, ui.inputWrapper)
			if strings.Contains(buf.String(), "hidden-proof") {
				t.Fatal("PKCE verifier was rendered")
			}
			if !strings.Contains(strings.Join(strings.Fields(buf.String()), ""), authorizeURL) {
				t.Fatalf("authorization URL was clipped or changed by wrapping:\n%s", buf.String())
			}
			testutil.AssertGoldenFrames(t, tt.name, buf, surfaceNames(ui))
		})
	}
}
