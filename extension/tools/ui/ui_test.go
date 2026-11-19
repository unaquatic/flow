package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestToolBox_HeaderOnly checks that a call of read, write, or edit shows as
// its header alone, expanded or not, in every state but failure, its input
// unshown even while it streams, and that a failed call shows why it failed.
func TestToolBox_HeaderOnly(t *testing.T) {
	editInput := json.RawMessage(`{"path":"foo.go","old_string":"old line","new_string":"new line"}`)
	tests := []struct {
		name   string
		call   message.ToolPart
		header string
	}{
		{name: "write queued", header: "Write foo.go", call: message.ToolPart{Name: "write",
			State: message.ToolStatePending{Input: json.RawMessage(`{"path":"foo.go","content":"package x\n"}`)}}},
		{name: "edit streaming", header: "Edit", call: message.ToolPart{Name: "edit",
			State: message.ToolStatePending{Input: json.RawMessage(`{"path":"foo.go","old_string":"old`)}}},
		{name: "edit running", header: "Edit foo.go", call: message.ToolPart{Name: "edit",
			State: message.ToolStateRunning{Input: editInput}}},
		{name: "edit completed", header: "Edit foo.go", call: message.ToolPart{Name: "edit",
			State: message.ToolStateCompleted{Input: editInput, Output: "edited"}}},
		{name: "read running", header: "Read foo.go", call: message.ToolPart{Name: "read",
			State: message.ToolStateRunning{Input: json.RawMessage(`{"path":"foo.go"}`), Output: "package x\n"}}},
		{name: "read completed", header: "Read foo.go [8-9]", call: message.ToolPart{Name: "read",
			State: message.ToolStateCompleted{Input: json.RawMessage(`{"path":"foo.go","offset":8,"limit":2}`),
				Output: "a\nb\n\n[showing lines 8-9 of 29]"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			box := newBox(tt.call)
			box.SetExpanded(true)
			if got := box.Header.PlainText(); got != tt.header {
				t.Errorf("header = %q, want %q", got, tt.header)
			}
			if !box.Empty || box.Open() {
				t.Errorf("empty %v, open %v; want the box empty, shown as its header alone", box.Empty, box.Open())
			}
			for name, w := range map[string]interface{ Visible() bool }{
				"params": box.Params, "body": box.Body, "lines": box.Lines, "output": box.Output,
			} {
				if w.Visible() {
					t.Errorf("%s shown, want only the header", name)
				}
			}
		})
	}

	// A failed call shows why it failed, under its header.
	box := newBox(message.ToolPart{Name: "edit", State: message.ToolStateError{Input: editInput,
		Error: "old_string not found\n\n[tool execution failed]"}})
	box.SetExpanded(true)
	if box.Empty || !box.Open() || box.Body.Visible() || !strings.Contains(box.Output.Content(), "old_string not found") {
		t.Errorf("failed edit: empty %v, open %v, body shown %v, output %q; want why it failed alone",
			box.Empty, box.Open(), box.Body.Visible(), box.Output.Content())
	}
}

// TestToolBox_BashDescription checks that a bash call with a description is
// headed by it alone, in the tool name's color, collapsed or expanded, its
// command still in the details, and that its status reads how long it ran
// once it ends, after the reason it failed, if it did.
func TestToolBox_BashDescription(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	input := json.RawMessage(`{"command":"go test ./...","description":"Runs the tests\nin full"}`)
	tests := map[string]struct {
		state      message.ToolState
		wantStatus string
	}{
		"queued":    {state: message.ToolStatePending{Input: input}, wantStatus: "[queued]"},
		"executing": {state: message.ToolStateRunning{Input: input, StartedAt: t0}, wantStatus: "[executing]"},
		"completed": {state: message.ToolStateCompleted{Input: input, Output: "ok", StartedAt: t0, EndedAt: t0.Add(12 * time.Second)}, wantStatus: "[12s]"},
		"failed": {state: message.ToolStateError{Input: input, Error: "FAIL\n\n[exit 1]", StartedAt: t0, EndedAt: t0.Add(65 * time.Second)},
			wantStatus: "[exit 1] [1m 05s]"},
		"interrupted": {state: message.ToolStateError{Input: input, Error: message.InterruptedError, StartedAt: t0, EndedAt: t0.Add(3 * time.Second)},
			wantStatus: "[interrupted] [3s]"},
		"untimed": {state: message.ToolStateCompleted{Input: input, Output: "ok"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			box := newBox(message.ToolPart{Name: "bash", State: tt.state})
			for _, expanded := range []bool{false, true} {
				box.SetExpanded(expanded)
				if got, want := box.Header.PlainText(), "Runs the tests…"; got != want {
					t.Errorf("expanded %v: header = %q; want %q", expanded, got, want)
				}
			}
			if got := box.Header.Content().Chunks[0].Fg; got != box.NameFg {
				t.Errorf("header fg = %v; want the name's %v", got, box.NameFg)
			}
			if got := box.Status.PlainText(); got != tt.wantStatus {
				t.Errorf("status = %q; want %q", got, tt.wantStatus)
			}
			if got, want := box.Params.PlainText(), "$ go test ./..."; got != want {
				t.Errorf("command = %q; want %q", got, want)
			}
		})
	}
}

// TestToolBox_BashHeaderAndCommand checks that a bash call without a
// description has a header that reads its tool name, not bold, and the names
// of the commands it runs with the operators between them, or the first line
// of a command bash would not run, with the status apart from it in the color the key hints give what a key
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
		"no description": {
			input:       `{"command":"ls"}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"default timeout": {
			input:       `{"command":"ls","timeout_ms":60000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"custom timeout": {
			input:       `{"command":"ls","timeout_ms":120000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 120s]",
		},
		"fractional timeout": {
			input:       `{"command":"ls","timeout_ms":1500}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 1.5s]",
		},
		"capped timeout": {
			input:       `{"command":"ls","timeout_ms":7200000}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls [timeout 3600s]",
		},
		"multi-line command": {
			input:       `{"command":"cd calc\ngo test","timeout_ms":120000}`,
			wantHeader:  "Bash cd; go test",
			wantCommand: "$ cd calc\ngo test [timeout 120s]",
		},
		"command names and operators": {
			input:       `{"command":"FOO=1 go test ./... -v 2>&1 | grep -v ok && echo \"$(date)\""}`,
			wantHeader:  "Bash go test | grep && echo",
			wantCommand: `$ FOO=1 go test ./... -v 2>&1 | grep -v ok && echo "$(date)"`,
		},
		"compound command": {
			input:       `{"command":"for f in *.go; do gofmt -l \"$f\"; done"}`,
			wantHeader:  "Bash for f in …; do gofmt; done",
			wantCommand: `$ for f in *.go; do gofmt -l "$f"; done`,
		},
		"subcommands": {
			input:       `{"command":"cd repo && git add a.go && git -c commit.gpgsign=false commit -q -F - <<'EOF'\nfix: a\nEOF\ngit log -1 --stat"}`,
			wantHeader:  "Bash cd && git add && git commit; git log",
			wantCommand: "$ cd repo && git add a.go && git -c commit.gpgsign=false commit -q -F - <<'EOF'\nfix: a\nEOF\ngit log -1 --stat",
		},
		"wrapper": {
			input:       `{"command":"sudo -u root rm -rf /tmp/x && bash -c 'cd app && make'"}`,
			wantHeader:  "Bash sudo rm && bash -c 'cd && make'",
			wantCommand: "$ sudo -u root rm -rf /tmp/x && bash -c 'cd app && make'",
		},
		"syntax error shows first line": {
			input:       `{"command":"echo 'x\nls"}`,
			wantHeader:  "Bash echo 'x…",
			wantCommand: "$ echo 'x\nls",
		},
		"trailing newline": {
			input:       `{"command":"ls\n"}`,
			wantHeader:  "Bash ls",
			wantCommand: "$ ls",
		},
		"shell": {
			input:       `{"command":"ls","shell":"bash","timeout_ms":120000}`,
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
			if got := box.Status.Fg(); got != muted {
				t.Errorf("status fg = %v; want TextMuted, as tool rows show their status", got)
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
