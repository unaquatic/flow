package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/style"
)

// TestFormatContentPart_AllPartsRenderNonEmpty checks that each listed content
// type produces visible text. Add a case when a content type is introduced.
func TestFormatContentPart_AllPartsRenderNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		part message.ContentPart
		want string // required output substring
	}{
		{
			name: "TextPart",
			part: message.TextPart{Text: "hello"},
			want: "hello",
		},
		{
			name: "TextPart_empty",
			part: message.TextPart{Text: ""},
			want: "[text]",
		},
		{
			name: "ReasoningPart",
			part: message.ReasoningPart{Text: "thinking..."},
			want: "[reasoning]",
		},
		{
			name: "ToolUsePart_raw",
			part: message.ToolUsePart{Name: "write", Input: json.RawMessage(`{"path":"x"}`)},
			want: "[tool_use]",
		},
		{
			name: "ToolResultPart",
			part: message.ToolResultPart{Name: "write", Text: "wrote 100 bytes"},
			want: "[tool_result]",
		},
		{
			name: "ToolResultPart_error",
			part: message.ToolResultPart{Name: "write", Text: "boom", IsError: true},
			want: "[tool_result error]",
		},
		{
			name: "ImagePart",
			part: message.ImagePart{Source: &message.ImageSource{MediaType: "image/png"}},
			want: "[image]",
		},
		{
			name: "FilePart",
			part: message.FilePart{Filename: "a.pdf", MediaType: "application/pdf"},
			want: "[file]",
		},
		{
			name: "UsagePart",
			part: message.UsagePart{Usage: message.Usage{InputTokens: 1200, CachedInputTokens: 1024, OutputTokens: 300}},
			want: "[usage] in 0.2K out 0.3K cache 1.0K write 0K",
		},
		{
			name: "ToolPart_pending",
			part: message.ToolPart{
				Name:  "write",
				State: message.ToolStatePending{Input: json.RawMessage(`{"path":"x"}`)},
			},
			want: "Write x [queued]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatContentPart(tc.part)
			if got == "" {
				t.Fatalf("FormatContentPart(%T) returned empty string", tc.part)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("FormatContentPart(%T) = %q, want substring %q",
					tc.part, got, tc.want)
			}
		})
	}
}

// TestUsageBox_GoneUntilShown checks that a usage block is gone as it is
// made, stays gone as its part changes, and keeps its counts up to date, so
// that it shows the latest once shown.
func TestUsageBox_GoneUntilShown(t *testing.T) {
	box := NewUsageBox(message.UsagePart{ID: "u", Usage: message.Usage{InputTokens: 10}}, nil, style.Color{})
	if box.Visible() {
		t.Fatal("a new usage block is visible, want it gone")
	}
	later := message.UsagePart{ID: "u", Usage: message.Usage{InputTokens: 9_000, CachedInputTokens: 7_700, OutputTokens: 900}}
	UpdateContentPart(box, later)
	if box.Visible() {
		t.Error("updating the usage block showed it, want it still gone")
	}
	if got, want := box.Header.PlainText(), "[usage] in 1.3K out 0.9K cache 7.7K write 0K"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	if box.Part != later {
		t.Errorf("part = %+v, want %+v", box.Part, later)
	}
}

func TestUsageDetails(t *testing.T) {
	got := UsageDetails(message.Usage{
		InputTokens: 1200, CachedInputTokens: 1024, CacheCreationInputTokens: 176,
		OutputTokens: 300, ReasoningOutputTokens: 20, TotalTokens: 1676,
	})
	want := "input            1200\n" +
		"cached input     1024\n" +
		"cache creation    176\n" +
		"output            300\n" +
		"reasoning output   20\n" +
		"total            1676"
	if got != want {
		t.Errorf("details =\n%s\nwant\n%s", got, want)
	}
}

// TestToolBox_ExpandedHeaderDropsParams checks that expanding a call leaves
// its bold name alone in the header, since the params under it show its
// input, through later updates too, and that collapsing it brings the input
// back. Calls without params, such as one whose input still streams, keep
// their header.
func TestToolBox_ExpandedHeaderDropsParams(t *testing.T) {
	input := json.RawMessage(`{"command":"go test ./..."}`)
	box := newTestToolBox(t, message.ToolPart{Name: "bash", State: message.ToolStateRunning{Input: input}}, DefaultTheme(), style.ColorWhite)

	box.SetExpanded(true)
	if got := box.Header.PlainText(); got != "Bash" {
		t.Errorf("expanded header = %q; want %q", got, "Bash")
	}
	if label := box.Header.Content().Chunks[0]; label.Attributes&style.AttrBold == 0 {
		t.Errorf("expanded label = %+v; want it bold", label)
	}
	box.SetTool(message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: input, Output: "ok"}})
	if got := box.Header.PlainText(); got != "Bash" {
		t.Errorf("expanded header after an update = %q; want %q", got, "Bash")
	}
	box.SetExpanded(false)
	if got := box.Header.PlainText(); got != "Bash go test ./..." {
		t.Errorf("collapsed header = %q; want %q", got, "Bash go test ./...")
	}

	read := newTestToolBox(t, message.ToolPart{Name: "read", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"path":"calc.go","offset":8,"limit":7}`), Output: "func Divide() {}\n\n[showing lines 8-14 of 29]",
	}}, DefaultTheme(), style.ColorWhite)
	read.SetExpanded(true)
	if got := read.Header.PlainText(); got != "Read" {
		t.Errorf("expanded read header = %q; want %q", got, "Read")
	}
	if got, want := read.Params.PlainText(), "calc.go [8-14]"; got != want {
		t.Errorf("expanded read params = %q; want %q", got, want)
	}
	read.SetExpanded(false)
	if got, want := read.Header.PlainText(), "Read calc.go [8-14]"; got != want {
		t.Errorf("collapsed read header = %q; want %q", got, want)
	}

	partial := newTestToolBox(t, message.ToolPart{Name: "bash", State: message.ToolStatePending{Input: json.RawMessage(`{"command":"l`)}},
		DefaultTheme(), style.ColorWhite)
	empty := newTestToolBox(t, message.ToolPart{Name: "ls", State: message.ToolStatePending{Input: json.RawMessage(`{}`)}},
		DefaultTheme(), style.ColorWhite)
	for _, b := range []*ToolBox{partial, empty} {
		want := b.Header.PlainText()
		b.SetExpanded(true)
		if got := b.Header.PlainText(); got != want || b.Params.Visible() {
			t.Errorf("expanded %s header = %q, params shown %v; want it kept as %q, without params",
				b.Tool.Name, got, b.Params.Visible(), want)
		}
	}
}

// TestToolBox_ParamsNoteOtherParams checks that the params of an expanded
// call show its main param, the one its header shows, and then a note on
// each other param, in the order the call gave them and in the muted text
// color, but for those the box shows apart, such as an edit's diff.
func TestToolBox_ParamsNoteOtherParams(t *testing.T) {
	tests := map[string]struct {
		name, input, want string
	}{
		"edit":       {"edit", `{"path":"calc.go","old_string":"a","new_string":"b"}`, "calc.go"},
		"write":      {"write", `{"path":"calc.go","content":"package calc"}`, "calc.go"},
		"read":       {"read", `{"path":"calc.go"}`, "calc.go"},
		"read lines": {"read", `{"path":"calc.go","offset":8}`, "calc.go [8-]"},
		"grep":       {"grep", `{"pattern":"TODO","path":"internal","include":"*.go","limit":10}`, "TODO [path internal] [include *.go] [limit 10]"},
		"glob":       {"glob", `{"path":"internal","pattern":"**/*.go"}`, "**/*.go [path internal]"},
		"ls":         {"ls", `{"path":"calc","limit":5}`, "calc [limit 5]"},
		"url":        {"webfetch", `{"url":"https://pkg.go.dev/errors","format":"markdown"}`, "https://pkg.go.dev/errors [format markdown]"},
		// Without a main param, the notes alone are shown.
		"no main param": {"todo", `{"id":3,"done":true}`, "[id 3] [done true]"},
		// A value that a note cannot hold on one line is shown as JSON, and
		// an empty one not at all.
		"other values": {"search", `{"query":"q","tags":["a", "b"],"filter":{"x": 1},"body":"one\ntwo","note":"","next":null}`,
			`q [tags ["a","b"]] [filter {"x":1}] [body "one\ntwo"]`},
	}
	muted := DefaultTheme().TextMuted
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			box := newTestToolBox(t, message.ToolPart{Name: tt.name, State: message.ToolStatePending{Input: json.RawMessage(tt.input)}},
				DefaultTheme(), style.ColorWhite)
			box.SetExpanded(true)
			if got := box.Params.PlainText(); got != tt.want || !box.Params.Visible() {
				t.Errorf("params = %q, shown %v; want %q shown", got, box.Params.Visible(), tt.want)
			}
			if got, want := box.Header.PlainText(), box.View.NameLabel(tt.name); got != want {
				t.Errorf("header = %q; want %q", got, want)
			}
			for _, c := range box.Params.Content().Chunks {
				if strings.HasPrefix(c.Text, "[") && (c.Fg != muted) {
					t.Errorf("note %q fg = %v; want the muted text color", c.Text, c.Fg)
				}
			}
			if strings.Contains(box.Body.Content(), "```json") {
				t.Errorf("body = %q; want no input, which the params show", box.Body.Content())
			}
		})
	}
}

// TestToolBox_StatusNamesNoKey checks that a call's status names no key,
// collapsed or expanded: the indicator names Ctrl+O during a run.
func TestToolBox_StatusNamesNoKey(t *testing.T) {
	input := json.RawMessage(`{"command":"go test ./..."}`)
	box := newTestToolBox(t, message.ToolPart{Name: "bash", State: message.ToolStateError{Input: input, Error: "[cancelled]"}},
		DefaultTheme(), style.ColorWhite)
	for _, expanded := range []bool{false, true, false} {
		box.SetExpanded(expanded)
		if got := box.Status.PlainText(); got != "[cancelled]" {
			t.Errorf("expanded %v: status = %q; want [cancelled] alone", expanded, got)
		}
	}
}

// TestToolBox_ShowsInputWithoutOutput checks that a call interrupted before
// its output shows its input in its params, and that a call whose params
// show nothing, and that has nothing else to show, shows its input, indented,
// in the body, until its output takes the input's place. A call without
// input arguments has nothing to show.
func TestToolBox_ShowsInputWithoutOutput(t *testing.T) {
	grep := newTestToolBox(t, message.ToolPart{Name: "grep", State: message.ToolStateError{
		Input: json.RawMessage(`{"pattern":"TODO","path":"calc"}`), Error: message.InterruptedError,
	}}, DefaultTheme(), style.ColorWhite)
	if got, want := grep.Status.PlainText(), "[interrupted]"; got != want {
		t.Errorf("interrupted status = %q; want %q", got, want)
	}
	if got, want := grep.Params.PlainText(), "TODO [path calc]"; got != want || grep.Body.Visible() {
		t.Errorf("interrupted params = %q, body shown %v; want %q and no body", got, grep.Body.Visible(), want)
	}

	// A bash call's description is never shown, but in its input.
	input := json.RawMessage(`{"description":"List files"}`)
	const indented = "```json\n{\n  \"description\": \"List files\"\n}\n```"
	bash := newTestToolBox(t, message.ToolPart{Name: "bash", State: message.ToolStateError{Input: input, Error: message.InterruptedError}},
		DefaultTheme(), style.ColorWhite)
	if got := bash.Body.Content(); got != indented || !bash.Body.Visible() || bash.Params.Visible() {
		t.Errorf("interrupted body = %q, shown %v, params shown %v; want the input %q shown without params",
			got, bash.Body.Visible(), bash.Params.Visible(), indented)
	}
	bash.SetTool(message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: input, Output: "calc.go"}})
	if bash.Body.Visible() || !bash.Output.Visible() {
		t.Errorf("completed: body shown %v, output shown %v; want the output in the input's place",
			bash.Body.Visible(), bash.Output.Visible())
	}

	ls := newTestToolBox(t, message.ToolPart{Name: "ls", State: message.ToolStateError{Input: json.RawMessage(`{}`), Error: message.InterruptedError}},
		DefaultTheme(), style.ColorWhite)
	if got := ls.Status.PlainText(); got != "[interrupted]" || ls.Body.Visible() {
		t.Errorf("call without arguments: status = %q, body shown %v; want [interrupted] and no body", got, ls.Body.Visible())
	}
}

// TestToolBox_CollapsedBashHeaderPlain checks that a collapsed bash call's
// header shows the first line of its command, before an ellipsis, all in the
// color of its status, compact or boxed, while the command line under an
// expanded header highlights it.
func TestToolBox_CollapsedBashHeaderPlain(t *testing.T) {
	theme := DefaultTheme()
	tinted := func(c style.TextChunk) bool { return !c.Fg.IsTransparent() && c.Fg != theme.CodeFg }
	for _, ts := range ToolCallStyles {
		t.Run(ts.String(), func(t *testing.T) {
			box := newTestToolBox(t, message.ToolPart{Name: "bash", State: message.ToolStatePending{Input: json.RawMessage(
				`{"command":"go test -run 'TestDivide' -count=1\ngo vet ./..."}`)}}, theme, theme.CodeFg)
			box.SetStyle(ts)
			if got, want := box.Header.PlainText(), "Bash go test -run 'TestDivide' -count=1…"; got != want {
				t.Fatalf("header = %q; want %q", got, want)
			}
			// The header is the bold label, a space, and then the command.
			command := box.Header.Content().Chunks[2:]
			if len(command) != 1 || command[0].Fg != box.Status.Fg() {
				t.Errorf("header command = %+v; want it whole in the status's color %v", command, box.Status.Fg())
			}
			box.SetExpanded(true)
			if command := box.Params.Content().Chunks; !slices.ContainsFunc(command, tinted) {
				t.Errorf("expanded command line = %+v; want some of it highlighted", command)
			}
		})
	}
}

// TestToolBox_HeaderBoldNameAndInput checks that every tool's header leads
// with its name in bold, its first letter capitalized, followed by its input
// summary in the color of its status, in every state, even once a completed
// call carries a result title.
func TestToolBox_HeaderBoldNameAndInput(t *testing.T) {
	tests := map[string]struct {
		name, input, want string
	}{
		"glob":  {"glob", `{"pattern":"calculator/*.go"}`, "Glob calculator/*.go"},
		"read":  {"read", `{"path":"calculator/calc_test.go"}`, "Read calculator/calc_test.go"},
		"edit":  {"edit", `{"path":"calc.go","old_string":"a","new_string":"b"}`, "Edit calc.go"},
		"write": {"write", `{"path":"divide_test.go","content":"x"}`, "Write divide_test.go"},
		"bash":  {"bash", `{"command":"go test ./...","description":"Run tests"}`, "Bash go test ./..."},
		// A search is summed up by what it looks for, not where.
		"grep with path": {"grep", `{"pattern":"TODO","path":"internal","include":"*.go"}`, "Grep TODO"},
		"glob with path": {"glob", `{"pattern":"**/*.go","path":"internal"}`, "Glob **/*.go"},
	}
	for name, tt := range tests {
		input := json.RawMessage(tt.input)
		states := map[string]message.ToolState{
			"pending":   message.ToolStatePending{Input: input},
			"running":   message.ToolStateRunning{Input: input},
			"completed": message.ToolStateCompleted{Input: input, Output: "ok", Title: "Result title"},
			"error":     message.ToolStateError{Input: input, Error: "failed"},
		}
		for stateName, state := range states {
			t.Run(name+"/"+stateName, func(t *testing.T) {
				box := newTestToolBox(t, message.ToolPart{Name: tt.name, State: state}, DefaultTheme(), style.ColorWhite)
				if got := box.Header.PlainText(); got != tt.want {
					t.Errorf("header = %q; want %q", got, tt.want)
				}
				label, _, _ := strings.Cut(tt.want, " ")
				chunks := box.Header.Content().Chunks
				if chunks[0].Text != label || chunks[0].Attributes&style.AttrBold == 0 {
					t.Errorf("label = %+v; want a bold %s", chunks[0], label)
				}
				if input := chunks[len(chunks)-1]; input.Fg != box.Status.Fg() {
					t.Errorf("input = %+v; want it in the status's color %v", input, box.Status.Fg())
				}
			})
		}
	}
}

// TestToolLabel checks that a tool's name reads as words in a call's header:
// known names by their label, and others split into words in title case,
// with acronyms in capitals.
func TestToolLabel(t *testing.T) {
	tests := map[string]string{
		// The built-in tools, but for ls, which the tools extension labels
		// List, and the demo's unknown one, which the demo labels Web Fetch.
		"bash":     "Bash",
		"read":     "Read",
		"write":    "Write",
		"edit":     "Edit",
		"grep":     "Grep",
		"glob":     "Glob",
		"ls":       "Ls",
		"webfetch": "Webfetch",
		// Names split at separators and camel case.
		"web_fetch":            "Web Fetch",
		"web-fetch":            "Web Fetch",
		"webFetch":             "Web Fetch",
		"WebFetch":             "Web Fetch",
		"todo.write":           "Todo Write",
		"github__create_issue": "Github Create Issue",
		// Acronyms, known or written in capitals, but not a name in capitals.
		"get_url":     "Get URL",
		"HTTPRequest": "HTTP Request",
		"readS3File":  "Read S3 File",
		"READ_FILE":   "Read File",
		"URL":         "URL",
		// Other letters, and names with no words.
		"Read":              "Read",
		"élan":              "Élan",
		"":                  UnnamedToolLabel,
		"__":                UnnamedToolLabel,
		" -. ":              UnnamedToolLabel,
		"___web___fetch___": "Web Fetch",
		"web_-_.fetch":      "Web Fetch",
		"\xffbroken":        "\uFFFDbroken",
		"\xffweb_fetch":     "\uFFFDweb Fetch",
	}
	for name, want := range tests {
		if got := ToolLabel(name); got != want {
			t.Errorf("ToolLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

// FuzzToolLabel checks that every label has words, and that none keeps a
// separator, such as an underscore, or the bytes of a name that are not valid
// UTF-8.
func FuzzToolLabel(f *testing.F) {
	for _, name := range []string{"github__create_issue", "", "_", "a-b.c d", "HTTPRequest", "\xffweb_fetch", "\t_x_\n"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		label := ToolLabel(name)
		if label == "" {
			t.Errorf("ToolLabel(%q) is empty", name)
		}
		if strings.ContainsAny(label, "_-.") || strings.ContainsFunc(label, func(r rune) bool { return unicode.IsSpace(r) && r != ' ' }) {
			t.Errorf("ToolLabel(%q) = %q keeps a separator", name, label)
		}
		if strings.HasPrefix(label, " ") || strings.HasSuffix(label, " ") || strings.Contains(label, "  ") {
			t.Errorf("ToolLabel(%q) = %q has stray spaces", name, label)
		}
		if !utf8.ValidString(label) {
			t.Errorf("ToolLabel(%q) = %q is not valid UTF-8", name, label)
		}
	})
}

// TestNameWords checks where an identifier splits into words: at separators,
// which no word keeps, and where camel case starts a word, keeping each
// word's letters as they are.
func TestNameWords(t *testing.T) {
	tests := map[string][]string{
		"bash":             {"bash"},
		"web_fetch":        {"web", "fetch"},
		"a-b.c d":          {"a", "b", "c", "d"},
		"webSearch":        {"web", "Search"},
		"HTTPRequest":      {"HTTP", "Request"},
		"readS3File":       {"read", "S3", "File"},
		"model5Mini":       {"model5", "Mini"},
		"github__x__do_it": {"github", "x", "do", "it"},
		"ALLCAPS":          {"ALLCAPS"},
		"_leading_":        {"leading"},
		"":                 nil,
		"élanVital_ñu":     {"élan", "Vital", "ñu"},
		"trailingUpperX":   {"trailing", "Upper", "X"},
	}
	for name, want := range tests {
		if got := slices.Collect(NameWords(name)); !slices.Equal(got, want) {
			t.Errorf("NameWords(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestFormatContentPart_ToolStatus checks that the plain-text form of a tool
// call keeps its status after the header, and its output below.
func TestFormatContentPart_ToolStatus(t *testing.T) {
	part := message.ToolPart{Name: "bash", State: message.ToolStateError{
		Input: json.RawMessage(`{"command":"go test ./...","description":"Run tests"}`), Error: "FAIL\n\n[exit 1]",
	}}
	if got, want := FormatContentPart(part), "Bash go test ./... [exit 1]\nFAIL"; got != want {
		t.Errorf("FormatContentPart = %q; want %q", got, want)
	}
}

// TestErrorStatus checks that a failed call's status names its reason in
// brackets, from the marker that ends its error where there is one, and that
// the text before the marker is its output.
func TestErrorStatus(t *testing.T) {
	tests := map[string]struct {
		err, wantStatus, wantOutput string
	}{
		"exit with output":        {"FAIL\n\n[exit 1]", "[exit 1]", "FAIL"},
		"exit without output":     {"[exit 1]", "[exit 1]", ""},
		"timeout":                 {"[timeout after 60s]", "[timeout after 60s]", ""},
		"timeout with output":     {"=== RUN   TestSlow\n\n[timeout after 60s]", "[timeout after 60s]", "=== RUN   TestSlow"},
		"cancelled":               {"[cancelled]", "[cancelled]", ""},
		"interrupted":             {message.InterruptedError, "[interrupted]", ""},
		"tool failure":            {"not found: a.go\n\n[tool execution failed]", "[tool execution failed]", "not found: a.go"},
		"multi-line tool failure": {"line 1\nline 2\n\n[tool execution failed]", "[tool execution failed]", "line 1\nline 2"},
		"trailing newline":        {"FAIL\n\n[exit 2]\n", "[exit 2]", "FAIL"},
		"one line, no marker":     {"permission denied", "[permission denied]", ""},
		"lines, no marker":        {"first\nsecond", "[error]", "first\nsecond"},
		"empty":                   {"", "[error]", ""},
		"empty brackets":          {"[]", "[[]]", ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			status, output := ErrorStatus(tt.err)
			if status != tt.wantStatus || output != tt.wantOutput {
				t.Errorf("ErrorStatus(%q) = %q, %q; want %q, %q", tt.err, status, output, tt.wantStatus, tt.wantOutput)
			}
		})
	}
}

// TestToolBox_OtherToolsShowOutput checks the plain-text output fallback.
func TestToolBox_OtherToolsShowOutput(t *testing.T) {
	tp := message.ToolPart{
		Name: "glob",
		State: message.ToolStatePending{
			Input: json.RawMessage(`{"pattern":"*.go"}`),
		},
	}
	box := newTestToolBox(t, tp, DefaultTheme(), style.ColorWhite)
	if got, want := box.Params.PlainText(), "*.go"; got != want || box.Body.Visible() || box.Output.Visible() {
		t.Fatalf("pending glob: params %q, body shown %v, output shown %v; want only its pattern %q",
			got, box.Body.Visible(), box.Output.Visible(), want)
	}
	box.SetTool(message.ToolPart{Name: "glob", State: message.ToolStateRunning{Input: tp.Input(), Output: "foo.go\n"}})
	if got := box.Output.Content(); got != "```text\nfoo.go\n```" || box.Body.Visible() {
		t.Fatalf("running output = %q, body shown %v; want the output under the params", got, box.Body.Visible())
	}
	if box.Expanded || !box.Details.Gone() {
		t.Fatal("tool details should stay collapsed while streaming")
	}
	box.Status.ProcessMouseEvent(&input.MouseEvent{Type: input.MouseDown, Button: input.ButtonLeft})
	if !box.Expanded || !box.Details.Visible() {
		t.Fatal("clicking the status should expand the tool")
	}
	tp.State = message.ToolStateCompleted{Input: tp.Input(), Output: "foo.go\n```literal```\n", Title: "Found files"}
	box.SetTool(tp)
	if got := box.Output.Content(); got != "````text\nfoo.go\n```literal```\n````" {
		t.Fatalf("output = %q, want safely fenced tool output", got)
	}
	// The result title does not replace the pattern, which the params show
	// while the header holds the tool name.
	if got := box.Header.PlainText() + " " + box.Params.PlainText(); got != "Glob *.go" {
		t.Fatalf("header and params = %q; want Glob *.go", got)
	}
	box.Header.ProcessMouseEvent(&input.MouseEvent{Type: input.MouseDown, Button: input.ButtonLeft})
	if box.Expanded || !box.Details.Gone() {
		t.Fatal("clicking the header again should collapse the tool")
	}
	if got := box.Header.PlainText(); got != "Glob *.go" {
		t.Fatalf("collapsed header = %q; want Glob *.go", got)
	}
}

func TestTruncate_RuneSafe(t *testing.T) {
	// The retained prefix must end at a UTF-8 boundary.
	s := strings.Repeat("é", 150)
	got := Truncate(s, 200)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected trailing ellipsis, got %q", got[len(got)-3:])
	}
	// TODO: Replace this no-op scan with a UTF-8 validity assertion.
	for i := 0; i < len(got); i++ {
		if got[i]&0xC0 == 0x80 && (i == 0 || got[i-1]&0xC0 != 0xC0 && got[i-1]&0xE0 != 0xC0) {
			break
		}
	}
}
