// Package ui shows the calls of flow's built-in tools in the terminal UI, as
// the terminal side of the tools extension. The transcript draws every tool
// call in a ui.ToolBox; this package tells the boxes what the built-in tools'
// calls hold, as a ui.ToolFormatter:
//
//   - bash: the command as a command line, and its timeout when it is not
//     the default, never its description;
//   - read: the file as code in its language, and the lines it reads;
//   - write and edit: the change as a diff;
//   - grep: the number of matches once it completes;
//   - ls: the label "List".
//
// Any other tool's call, such as glob's, shows as the box shows every call.
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/tools/internal/builtin"
	"github.com/unaquatic/flow/terminal/ui"
)

// Extension describes how the calls of flow's built-in tools show. It
// implements ui.ToolFormatter, and shows no parts or views of its own.
type Extension struct{}

var _ ui.ToolFormatter = (*Extension)(nil)

// New returns the formats of the built-in tools' calls.
func New() *Extension { return &Extension{} }

// NewPartView returns nil: the extension adds no parts.
func (*Extension) NewPartView(message.ContentPart, ui.ViewHost) ui.PartView { return nil }

// NewSegmentView returns nil: the extension shows nothing under a message.
func (*Extension) NewSegmentView(ui.ChatMessage, ui.ViewHost) ui.SegmentView { return nil }

// FormatTool returns how a call of a built-in tool shows, as the package doc
// lists them, and reports false for the call of any other tool.
func (*Extension) FormatTool(call message.ToolPart) (ui.ToolView, bool) {
	args := ui.DecodeToolArgs(call.Input())
	switch call.Name {
	case "bash":
		command, timeout := BashCommand(args)
		return ui.ToolView{
			ParamKey: "command",
			Param:    command,
			Shell:    true,
			Notes:    nonEmpty(timeout),
			Unnoted:  []string{"description", "timeout_ms"},
		}, true
	case "read":
		lines := ReadLines(call)
		return ui.ToolView{
			HeaderNote:   lines,
			Notes:        nonEmpty(lines),
			Unnoted:      []string{"offset", "limit"},
			BodyLang:     LangForPath(ui.StringField(args, "path")),
			OutputInBody: true,
		}, true
	case "write":
		return ui.ToolView{
			Body:     Patch("", ui.StringField(args, "content")),
			BodyLang: "diff",
			Unnoted:  []string{"content"},
		}, true
	case "edit":
		return ui.ToolView{
			Body:     Patch(ui.StringField(args, "old_string"), ui.StringField(args, "new_string")),
			BodyLang: "diff",
			Unnoted:  []string{"old_string", "new_string"},
		}, true
	case "grep":
		var view ui.ToolView
		if state, ok := call.State.(message.ToolStateCompleted); ok {
			view.Status = GrepMatches(strings.TrimRight(state.Output, "\n"))
		}
		return view, true
	case "ls":
		return ui.ToolView{Label: "List"}, true
	}
	return ui.ToolView{}, false
}

// nonEmpty returns s as the one note of a list, or no notes when it is "".
func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// BashCommand returns the command of a bash call with args, without the
// newlines that end it, and a note, "[timeout 120s]", when the call runs under
// a timeout other than the default. Both are "" when args hold no command.
func BashCommand(args map[string]any) (command, note string) {
	command = strings.TrimRight(ui.StringField(args, "command"), "\n")
	if command == "" {
		return "", ""
	}
	if timeout := builtin.BashTimeout(args); timeout != builtin.DefaultBashTimeout {
		note = "[timeout " + builtin.FormatSeconds(timeout) + "]"
	}
	return command, note
}

// ReadLines returns the lines a read call reads, as ReadRange gives them for
// its input and, once it has completed, its output. It returns "" for a call
// that reads the whole file and for any other tool's call.
func ReadLines(call message.ToolPart) string {
	if call.Name != "read" {
		return ""
	}
	state, completed := call.State.(message.ToolStateCompleted)
	var output string
	if completed {
		output = strings.TrimRight(state.Output, "\n")
	}
	return ReadRange(ui.DecodeToolArgs(call.Input()), output, completed)
}

// ReadRange returns the lines a read call with args reads, as "[8-14]", or ""
// when it reads the whole file. Once the call has completed with output, the
// output tells: the read tool ends it with a notice such as "[showing lines
// 8-14 of 29]" when it left out lines after them, and otherwise it holds the
// lines from offset to the end. Before that, the range follows from offset
// and limit, as "[8-]" without a limit.
func ReadRange(args map[string]any, output string, completed bool) string {
	offset, _ := builtin.NumberAsInt64(args["offset"])
	limit, _ := builtin.NumberAsInt64(args["limit"])
	first := max(offset, 1)
	if completed {
		var from, to int
		if _, err := fmt.Sscanf(output[strings.LastIndexByte(output, '\n')+1:], "[showing lines %d-%d of", &from, &to); err == nil {
			return fmt.Sprintf("[%d-%d]", from, to)
		}
		if first == 1 || output == "" {
			return ""
		}
		return fmt.Sprintf("[%d-%d]", first, first+int64(strings.Count(output, "\n")))
	}
	switch {
	case limit > 0:
		return fmt.Sprintf("[%d-%d]", first, first+limit-1)
	case first > 1:
		return fmt.Sprintf("[%d-]", first)
	}
	return ""
}

// GrepMatches returns the number of matches in output, the output of a
// completed grep call, as "[3 matches]", "[1 match]", or "[no matches]". A
// count the grep tool cut short, by its limit or its size limit, reads
// "[100+ matches]".
func GrepMatches(output string) string {
	if output == "" || strings.HasPrefix(output, "[no matches]") {
		return "[no matches]"
	}
	// The matches come first, one per line, and any notices after a blank
	// line.
	matches, notices, _ := strings.Cut(output, "\n\n")
	count := strconv.Itoa(strings.Count(matches, "\n") + 1)
	if strings.Contains(notices, "showing first") {
		count += "+"
	}
	if count == "1" {
		return "[1 match]"
	}
	return "[" + count + " matches]"
}

// Patch formats the old and new snippets of an edit, or the content of a
// write, as a unified diff hunk. It returns "" when both are empty.
func Patch(oldText, newText string) string {
	if oldText == "" && newText == "" {
		return ""
	}
	var body strings.Builder
	counts := [2]int{}
	for i, text := range []string{oldText, newText} {
		if text == "" {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		counts[i] = len(lines)
		for _, line := range lines {
			body.WriteByte("-+"[i])
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	// Positions describe the snippets, not their location in the full file.
	header := fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", min(counts[0], 1), counts[0], min(counts[1], 1), counts[1])
	return header + strings.TrimSuffix(body.String(), "\n")
}

// LangForPath returns the lowercase extension of path, as the Markdown
// language tag of the file's code. An extensionless base name is returned as
// the tag, and an empty path as "".
func LangForPath(path string) string {
	if path == "" {
		return ""
	}
	base := path
	if _, after, ok := strings.CutLast(path, "/"); ok {
		base = after
	}
	if _, ext, ok := strings.CutLast(base, "."); ok {
		return strings.ToLower(ext)
	}
	return strings.ToLower(base)
}
