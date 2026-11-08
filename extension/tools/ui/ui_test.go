package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/tools/internal/builtin"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
)

// newBox returns the box of call, in the formats of the built-in tools.
func newBox(call message.ToolPart) *ui.ToolBox {
	return ui.NewToolBox(call, ui.DefaultTheme(), style.ColorWhite, New())
}

// TestFormatTool_OnlyBuiltinTools checks that the extension describes the
// calls of the built-in tools it formats, and leaves any other tool's to the
// box, and that it labels ls as List.
func TestFormatTool_OnlyBuiltinTools(t *testing.T) {
	for _, name := range []string{"bash", "read", "write", "edit", "grep", "ls", "todo"} {
		if _, ok := New().FormatTool(message.ToolPart{Name: name}); !ok {
			t.Errorf("FormatTool(%s) does not describe the call", name)
		}
	}
	for _, name := range []string{"glob", "webfetch", "github__create_issue"} {
		if view, ok := New().FormatTool(message.ToolPart{Name: name}); ok {
			t.Errorf("FormatTool(%s) = %+v, want the call left to the box", name, view)
		}
	}
	box := newBox(message.ToolPart{Name: "ls", State: message.ToolStatePending{Input: json.RawMessage(`{"path":"calc"}`)}})
	if got, want := box.Header.PlainText(), "List calc"; got != want {
		t.Errorf("ls header = %q, want %q", got, want)
	}
}

// TestToolBox_WriteShowsAddedDiff checks write previews before execution.
func TestToolBox_WriteShowsAddedDiff(t *testing.T) {
	box := newBox(message.ToolPart{
		Name: "write",
		State: message.ToolStatePending{
			Input: json.RawMessage(`{"path":"foo.go","content":"package x\n\nfunc Y() {}\n"}`),
		},
	})
	got := box.Header.PlainText() + " " + box.Status.PlainText() + "\n" + box.Body.Content()
	for _, want := range []string{"Write foo.go", "[queued]", "```diff", "@@ -0,0 +1,3 @@", "+package x", "+func Y() {}"} {
		if !strings.Contains(got, want) {
			t.Fatalf("write toolpart missing %q in output:\n%s", want, got)
		}
	}
	if box.Body.LineCount() < 3 {
		t.Fatalf("diff did not render the additions: %d lines", box.Body.LineCount())
	}
}

// TestToolBox_EditShowsUnifiedDiff checks removed and added lines.
func TestToolBox_EditShowsUnifiedDiff(t *testing.T) {
	box := newBox(message.ToolPart{
		Name: "edit",
		State: message.ToolStateRunning{
			Input: json.RawMessage(`{"path":"foo.go","old_string":"old line","new_string":"new line"}`),
		},
	})
	got := box.Header.PlainText() + " " + box.Status.PlainText() + "\n" + box.Body.Content()
	for _, want := range []string{"Edit foo.go", "[executing]", "```diff", "-old line", "+new line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("edit toolpart missing %q in output:\n%s", want, got)
		}
	}
}

// TestToolBox_ReadShowsContentOnCompletion checks that read output updates in place.
func TestToolBox_ReadShowsContentOnCompletion(t *testing.T) {
	pending := message.ToolPart{
		Name: "read",
		State: message.ToolStatePending{
			Input: json.RawMessage(`{"path":"foo.go"}`),
		},
	}
	box := newBox(pending)
	code := box.Body
	// Before it reads anything, the call shows its path alone, after its
	// name in the header, whether expanded or not.
	box.SetExpanded(true)
	if got := box.Header.PlainText(); got != "Read foo.go" || box.Params.Visible() || code.Visible() {
		t.Fatalf("pending read: header %q, params shown %v, body shown %v; want \"Read foo.go\" and neither", got, box.Params.Visible(), code.Visible())
	}
	box.SetTool(message.ToolPart{Name: "read", State: message.ToolStatePending{
		Input: json.RawMessage(`{"path":"calc`),
	}})
	if got := code.Content(); got != "```json\n{\"path\":\"calc\n```" {
		t.Fatalf("partial input = %q", got)
	}
	box.SetTool(message.ToolPart{Name: "read", State: message.ToolStateRunning{Input: pending.Input(), Output: "package x\n"}})
	if got := code.Content(); got != "```go\npackage x\n```" {
		t.Fatalf("read progress = %q", got)
	}

	completed := message.ToolPart{
		Name: "read",
		State: message.ToolStateCompleted{
			Input:  json.RawMessage(`{"path":"foo.go"}`),
			Output: "package x\n\nfunc Y() {}\n",
		},
	}
	box.SetTool(completed)
	if box.Body != code || !strings.HasPrefix(code.Content(), "```go\n") {
		t.Fatal("read did not preserve the Markdown widget and filetype")
	}
	got := box.Header.PlainText() + "\n" + code.Content()
	for _, want := range []string{"Read foo.go", "package x", "func Y() {}"} {
		if !strings.Contains(got, want) {
			t.Fatalf("completed read missing %q in output:\n%s", want, got)
		}
	}
}

// TestToolBox_BashHeaderAndCommand checks that a bash call's header reads its
// tool name, not bold, and the first line of its command, never its description,
// with the status apart from it in the color the key hints give what a key
// does, and that its command line shows the timeout, in the muted text color,
// only when it is not the default, and notes its other params but for its
// description.
func TestToolBox_BashHeaderAndCommand(t *testing.T) {
	tests := map[string]struct {
		input       string
		wantHeader  string
		wantCommand string
		wantBody    string
	}{
		"description is hidden": {
			input:       `{"command":"ls","description":"List files"}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"no description": {
			input:       `{"command":"ls"}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"default timeout": {
			input:       `{"command":"ls","description":"List files","timeout_ms":60000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"custom timeout": {
			input:       `{"command":"ls","description":"List files","timeout_ms":120000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 120s]",
		},
		"fractional timeout": {
			input:       `{"command":"ls","description":"List files","timeout_ms":1500}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 1.5s]",
		},
		"capped timeout": {
			input:       `{"command":"ls","description":"List files","timeout_ms":7200000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 3600s]",
		},
		"multi-line command": {
			input:       `{"command":"cd calc\ngo test","timeout_ms":120000}`,
			wantHeader:  "Bash cd calc…",
			wantCommand: "$ cd calc\ngo test [timeout 120s]",
		},
		"trailing newline": {
			input:       `{"command":"ls\n"}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"shell": {
			input:       `{"command":"ls","description":"List files","shell":"bash","timeout_ms":120000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 120s] [shell bash]",
		},
		"partial input": {
			input:      `{"command":"l`,
			wantHeader: "Bash",
			wantBody:   "```json\n{\"command\":\"l\n```",
		},
	}
	muted := ui.DefaultTheme().TextMuted
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			box := newBox(message.ToolPart{Name: "bash", State: message.ToolStatePending{Input: json.RawMessage(tt.input)}})
			if got := box.Header.PlainText(); got != tt.wantHeader {
				t.Errorf("header = %q; want %q", got, tt.wantHeader)
			}
			if got, want := box.Status.PlainText(), "[queued]"; got != want {
				t.Errorf("status = %q; want %q", got, want)
			}
			if got := box.Status.Fg(); got != ui.ToolFg {
				t.Errorf("status fg = %v; want ToolFg, as tool rows show their status", got)
			}
			if got := box.Params.PlainText(); got != tt.wantCommand {
				t.Errorf("command = %q; want %q", got, tt.wantCommand)
			}
			if got := box.Body.Content(); got != tt.wantBody {
				t.Errorf("body = %q; want %q", got, tt.wantBody)
			}
			if label := box.Header.Content().Chunks[0]; label.Text != "Bash" || label.Attributes&style.AttrBold != 0 {
				t.Errorf("label = %+v; want an unbracketed Bash, not bold", label)
			}
			for _, c := range box.Params.Content().Chunks {
				if strings.HasPrefix(c.Text, "[") && (c.Fg != muted) {
					t.Errorf("%q fg = %v; want the muted text color", c.Text, c.Fg)
				}
			}
		})
	}
}

// TestReadRange checks the lines a read call shows it reads, before it runs
// and from the read tool's own output once it completes, for each window of
// a 30-line file.
func TestReadRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thirty.txt")
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		args, wantBefore, wantDone string
	}{
		"whole file":          {`{}`, "", ""},
		"limit":               {`{"limit":10}`, "[1-10]", "[1-10]"},
		"limit past the end":  {`{"limit":50}`, "[1-50]", ""},
		"offset":              {`{"offset":25}`, "[25-]", "[25-30]"},
		"offset and limit":    {`{"offset":8,"limit":7}`, "[8-14]", "[8-14]"},
		"window past the end": {`{"offset":25,"limit":10}`, "[25-34]", "[25-30]"},
		"numbers as text":     {`{"offset":"8","limit":"7"}`, "[8-14]", "[8-14]"},
		"offset of one":       {`{"offset":1}`, "", ""},
	}
	read := builtin.Read()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var args map[string]any
			if err := json.Unmarshal([]byte(tt.args), &args); err != nil {
				t.Fatal(err)
			}
			args["path"] = path
			if got := ReadRange(args, "", false); got != tt.wantBefore {
				t.Errorf("before it runs = %q; want %q", got, tt.wantBefore)
			}
			out, err := read.Execute(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			if got := ReadRange(args, strings.TrimRight(out.Text, "\n"), true); got != tt.wantDone {
				t.Errorf("completed = %q; want %q (output %q)", got, tt.wantDone, out.Text)
			}
		})
	}
}

// TestToolBox_ReadRangeInHeader checks that a read call of part of a file
// shows the lines after its path, both in the color of its status.
func TestToolBox_ReadRangeInHeader(t *testing.T) {
	box := newBox(message.ToolPart{Name: "read", State: message.ToolStateCompleted{
		Input:  json.RawMessage(`{"path":"calc.go","offset":8,"limit":7}`),
		Output: "func Divide() {}\n\n[showing lines 8-14 of 29]",
	}})
	if got := box.Header.PlainText(); got != "Read calc.go [8-14]" {
		t.Errorf("header = %q; want Read calc.go [8-14]", got)
	}
	chunks := box.Header.Content().Chunks
	if last := chunks[len(chunks)-1]; last.Text != "calc.go [8-14]" || last.Fg != box.Status.Fg() {
		t.Errorf("input = %+v; want calc.go [8-14] in the status's color %v", last, box.Status.Fg())
	}
}

// TestToolBox_TodoShowsList checks that a todo call shows the list it
// writes, as the todo tool's own output lists it, in place of the output,
// the items done or not started in the muted text color and the one in
// progress in the box's, notes how many of its items are done in its
// header, and shows input that holds no list as any call's.
func TestToolBox_TodoShowsList(t *testing.T) {
	input := `{"todos":[{"content":"Find the tests","status":"completed"},{"content":"Add Divide","status":"in_progress"},{"content":"Run the tests","status":"pending"}]}`
	list := "[✓] Find the tests\n[–] Add Divide\n[ ] Run the tests"
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		t.Fatal(err)
	}
	out, err := builtin.Todo().Execute(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != list {
		t.Fatalf("todo output = %q; want %q", out.Text, list)
	}
	tests := map[string]struct {
		state                 message.ToolState
		wantHeader, wantLines string
		// wantMuted lists whether each line is muted.
		wantMuted []bool
	}{
		"queued":    {message.ToolStatePending{Input: json.RawMessage(input)}, "Todo [1/3]", list, []bool{true, false, true}},
		"completed": {message.ToolStateCompleted{Input: json.RawMessage(input), Output: out.Text}, "Todo [1/3]", list, []bool{true, false, true}},
		"cleared":   {message.ToolStateCompleted{Input: json.RawMessage(`{"todos":[]}`), Output: builtin.NoTodos}, "Todo", builtin.NoTodos, []bool{true}},
		"partial":   {message.ToolStatePending{Input: json.RawMessage(`{"todos":[{"con`)}, "Todo", "", nil},
	}
	muted := ui.DefaultTheme().TextMuted
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			box := newBox(message.ToolPart{Name: "todo", State: tt.state})
			box.SetExpanded(true)
			if got := box.Header.PlainText(); got != tt.wantHeader {
				t.Errorf("header = %q; want %q", got, tt.wantHeader)
			}
			if !box.KeepExpanded {
				t.Error("KeepExpanded = false; want the todo call kept expanded whatever Ctrl+O says")
			}
			if box.Params.Visible() || box.Output.Visible() {
				t.Errorf("params shown %v, output shown %v; want the list alone", box.Params.Visible(), box.Output.Visible())
			}
			if tt.wantLines == "" {
				if box.Lines.Visible() {
					t.Errorf("lines = %q; want none for input that holds no list", box.Lines.PlainText())
				}
				return
			}
			if box.Body.Visible() {
				t.Errorf("body = %q; want the lines in its place", box.Body.Content())
			}
			if got := box.Lines.PlainText(); got != tt.wantLines {
				t.Errorf("lines = %q; want %q", got, tt.wantLines)
			}
			var gotMuted []bool
			for _, c := range box.Lines.Content().Chunks {
				if c.Text != "\n" {
					gotMuted = append(gotMuted, c.Fg == muted)
				}
			}
			if !slices.Equal(gotMuted, tt.wantMuted) {
				t.Errorf("lines muted %v; want %v", gotMuted, tt.wantMuted)
			}
		})
	}
}

// TestGrepMatches checks the match count of a completed grep call against
// the grep tool's own output.
func TestGrepMatches(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": "// TODO one\n// TODO two\n// TODO three\n",
		"b.go": "// TODO four\n// FIXME " + strings.Repeat("x", builtin.MaxGrepLine) + "\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tests := map[string]struct {
		args, want string
	}{
		"matches":           {`{"pattern":"TODO"}`, "[4 matches]"},
		"one match":         {`{"pattern":"TODO f"}`, "[1 match]"},
		"no matches":        {`{"pattern":"XXX"}`, "[no matches]"},
		"cut by limit":      {`{"pattern":"TODO","limit":2}`, "[2+ matches]"},
		"long line cut":     {`{"pattern":"FIXME"}`, "[1 match]"},
		"limit not reached": {`{"pattern":"TODO","limit":10}`, "[4 matches]"},
	}
	grep := builtin.Grep()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var args map[string]any
			if err := json.Unmarshal([]byte(tt.args), &args); err != nil {
				t.Fatal(err)
			}
			args["path"] = dir
			out, err := grep.Execute(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			if got := GrepMatches(strings.TrimRight(out.Text, "\n")); got != tt.want {
				t.Errorf("GrepMatches = %q; want %q (output %q)", got, tt.want, out.Text)
			}
		})
	}
	box := newBox(message.ToolPart{Name: "grep", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"pattern":"TODO"}`), Output: "a.go:1:// TODO one\nb.go:1:// TODO four\n",
	}})
	if got, want := box.Status.PlainText(), "[2 matches]"; got != want {
		t.Errorf("completed grep status = %q; want %q", got, want)
	}
}
