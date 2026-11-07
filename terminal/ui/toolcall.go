package ui

import (
	"bytes"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// This file holds what the box of any tool's call is made of: the tool's
// label, the summary of its input and the notes on its params, its status,
// and its output, as a ToolBox shows them.

// UnnamedToolLabel is the label of a tool whose name has no words, such as
// "" or "__", so that a call's header never starts with a blank.
const UnnamedToolLabel = "Tool"

// labelAcronyms are the words that ToolLabel writes in capitals wherever a
// tool's name has them, such as "url" in "get_url".
var labelAcronyms = []string{
	"api", "cli", "css", "csv", "html", "http", "id", "json",
	"pdf", "sql", "ssh", "uri", "url", "xml", "yaml",
}

// ToolLabel returns the name of a tool as a call's header shows it, unless a
// ToolFormatter labels the tool: its words, as NameWords splits them, in
// title case, such as "Bash" for "bash" and "Web Search" for "web_search" or
// "webSearch". Words in labelAcronyms, and
// words the name writes in capitals while it is not all in capitals, are in
// capitals, such as "Get URL" for "get_url" and "HTTP Request" for
// "HTTPRequest". No label keeps a separator NameWords splits at, such as the
// underscores of "github__create_issue", bytes of name that are not
// valid UTF-8 show as U+FFFD, and a name with no words is UnnamedToolLabel.
func ToolLabel(name string) string {
	name = strings.ToValidUTF8(name, "\uFFFD")
	shouting := name == strings.ToUpper(name)
	var words []string
	for word := range NameWords(name) {
		lower := strings.ToLower(word)
		capitals := word == strings.ToUpper(word) && word != lower && utf8.RuneCountInString(word) > 1
		if slices.Contains(labelAcronyms, lower) || capitals && !shouting {
			words = append(words, strings.ToUpper(word))
			continue
		}
		words = append(words, Capitalize(lower))
	}
	if len(words) == 0 {
		return UnnamedToolLabel
	}
	return strings.Join(words, " ")
}

// NameWords yields the words of an identifier, such as a tool's name: the
// runs between underscores, hyphens, dots, and spaces, split again where
// camel case starts a word, at an upper-case letter after a lower-case letter
// or a digit, as in "webSearch", or at the last of a run of upper-case
// letters before a lower-case one, as in "HTTPRequest".
func NameWords(name string) iter.Seq[string] {
	separator := func(r rune) bool { return r == '_' || r == '-' || r == '.' || unicode.IsSpace(r) }
	wordStart := func(rs []rune, i int) bool {
		if i == 0 || !unicode.IsUpper(rs[i]) {
			return false
		}
		prev := rs[i-1]
		if unicode.IsLower(prev) || unicode.IsDigit(prev) {
			return true
		}
		return unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
	}
	return func(yield func(string) bool) {
		for part := range strings.FieldsFuncSeq(name, separator) {
			rs := []rune(part)
			start := 0
			for i := range len(rs) {
				if wordStart(rs, i) {
					if !yield(string(rs[start:i])) {
						return
					}
					start = i
				}
			}
			if !yield(string(rs[start:])) {
				return
			}
		}
	}
}

// Capitalize returns s with its first letter in upper case and the rest as it
// is, such as "Cache usage" for "cache usage", as a menu's rows show words.
func Capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// ToolFence wraps content in a Markdown code fence. It returns an empty string
// for empty content and lengthens the fence when content contains backticks.
func ToolFence(filetype, content string) string {
	if content == "" {
		return ""
	}
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	return fence + filetype + "\n" + strings.TrimRight(content, "\n") + "\n" + fence
}

// ToolNameChunk returns label, the name of a call's tool as ToolView.NameLabel
// gives it, in fg and not bold, for the start of a call's header.
func ToolNameChunk(label string, fg style.Color) style.TextChunk {
	return style.TextChunk{Text: label, Fg: fg}
}

// ToolHeaderOutput returns the call header, its status, and any output: the
// output so far of a running call, or the output of a completed one, as view
// shows the call. The header is the tool's name as view.NameLabel gives it,
// in nameFg and not bold, such as "Edit", and then, in inputFg, a one-line
// summary of the input, such as its path, a search's pattern, or a command,
// in every state of the call, and then view.HeaderNote. A transparent color
// leaves its part in the header's color. The status is view.Status when it is
// set, and otherwise "[queued]" or "[executing]" before the call ends, the
// reason a failed call failed as ErrorStatus gives it, and "" for a completed
// call.
func ToolHeaderOutput(tool message.ToolPart, view ToolView, nameFg, inputFg style.Color) (header style.StyledText, status, output string) {
	switch state := tool.State.(type) {
	case message.ToolStatePending:
		status = "[queued]"
	case message.ToolStateRunning:
		status = "[executing]"
		output = strings.TrimRight(state.Output, "\n")
	case message.ToolStateCompleted:
		output = strings.TrimRight(state.Output, "\n")
	case message.ToolStateError:
		status, output = ErrorStatus(state.Error)
	}
	if view.Status != "" {
		status = view.Status
	}
	header = style.NewStyledText(ToolNameChunk(view.NameLabel(tool.Name), nameFg))
	input := slices.DeleteFunc([]string{FirstLine(ToolInputSummary(string(tool.Input()))), view.HeaderNote},
		func(s string) bool { return s == "" })
	if len(input) > 0 {
		header.Chunks = append(header.Chunks, style.Plain(" "), style.TextChunk{Text: strings.Join(input, " "), Fg: inputFg})
	}
	return header, status, output
}

// ErrorStatus splits the error of a failed call into a status that names the
// reason in brackets and the output shown under it. A failed run or a failed
// tool ends its error with such a marker, as in "out\n\n[exit 1]" or
// "not found: a.go\n\n[tool execution failed]": the marker is the status and
// the text before it the output. message.InterruptedError reads
// "[interrupted]". Any other one-line error is put in brackets whole, and any
// other multi-line error is output under "[error]", as is an empty one.
func ErrorStatus(errText string) (status, output string) {
	text := strings.TrimRight(errText, "\n")
	if text == message.InterruptedError {
		return "[interrupted]", ""
	}
	output, last := "", text
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		output, last = strings.TrimRight(text[:i], "\n"), text[i+1:]
	}
	switch {
	case IsStatusMarker(last):
		return last, output
	case text == "":
		return "[error]", ""
	case output == "":
		return "[" + text + "]", ""
	}
	return "[error]", text
}

// IsStatusMarker reports whether line is a reason in brackets, such as
// "[exit 1]", that ends the error of a failed call.
func IsStatusMarker(line string) bool {
	return len(line) > 2 && line[0] == '[' && line[len(line)-1] == ']' && !strings.Contains(line, "\n")
}

// FirstLine returns the first line of s, followed by an ellipsis when more
// lines follow it.
func FirstLine(s string) string {
	first, _, more := strings.Cut(strings.TrimRight(s, "\n"), "\n")
	if more {
		return first + "…"
	}
	return first
}

// InputJSON returns tool input indented two spaces a level, its fields in the
// order the call gave them, or input as it is when it is not valid JSON.
func InputJSON(input json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, input, "", "  "); err != nil {
		return string(input)
	}
	return buf.String()
}

// DecodeToolArgs parses tool input as a JSON object. It returns nil for empty
// or malformed input.
func DecodeToolArgs(input json.RawMessage) map[string]any {
	if len(input) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return nil
	}
	return m
}

// StringField returns the string value for key in m, or "" when the
// key is missing or holds a non-string.
func StringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// SummaryKeys are the params that sum up a call's input, in the order
// SummaryField tries them. What a call looks for, such as a search's
// pattern, comes before where it looks, so that a grep or glob call with a
// path is summed up by its pattern.
var SummaryKeys = []string{"command", "pattern", "query", "url", "path", "file_path", "filePath"}

// SummaryField returns the first of SummaryKeys that holds a non-empty
// string in args, and that string, or "", "" when none does.
func SummaryField(args map[string]any) (key, value string) {
	for _, key := range SummaryKeys {
		if s := StringField(args, key); s != "" {
			return key, s
		}
	}
	return "", ""
}

// ToolInputSummary returns the string SummaryField finds in tool input, or
// "" for input that is not a JSON object.
func ToolInputSummary(input string) string {
	_, value := SummaryField(DecodeToolArgs(json.RawMessage(input)))
	return value
}

// ParamNotes yields a note on each param of input, a JSON object, as
// ParamNote gives it, in the order the call gave them, but for the params
// skip names and those ParamNote leaves out.
func ParamNotes(input json.RawMessage, skip ...string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for key, value := range InputFields(input) {
			if slices.Contains(skip, key) {
				continue
			}
			if note := ParamNote(key, value); note != "" && !yield(note) {
				return
			}
		}
	}
}

// ParamNote returns a note on the param key that holds value, as
// "[path calculator]": a string as it is when it is one line, and any other
// value as compact JSON. It returns "" for a value that is null, "", or
// malformed.
func ParamNote(key string, value json.RawMessage) string {
	var s string
	if err := json.Unmarshal(value, &s); err == nil && !strings.Contains(s, "\n") {
		if s == "" {
			return ""
		}
		return "[" + key + " " + s + "]"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, value); err != nil || buf.String() == "null" {
		return ""
	}
	return "[" + key + " " + buf.String() + "]"
}

// InputFields yields the fields of input, a JSON object, in the order the
// call gave them, each with its value as it is in input. It yields nothing
// for input that is not an object, and stops at a malformed field, as in
// input still being streamed.
func InputFields(input json.RawMessage) iter.Seq2[string, json.RawMessage] {
	return func(yield func(string, json.RawMessage) bool) {
		dec := json.NewDecoder(bytes.NewReader(input))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return
		}
		for dec.More() {
			tok, err := dec.Token()
			key, ok := tok.(string)
			if err != nil || !ok {
				return
			}
			var value json.RawMessage
			if err := dec.Decode(&value); err != nil || !yield(key, value) {
				return
			}
		}
	}
}
