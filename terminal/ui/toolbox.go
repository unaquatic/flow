package ui

import (
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/internal/shell"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/syntax"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// ToolBox is the PartBox of a tool call. It writes the call's header and
// status, and shows in its details the call's params, a body, such as its
// input as JSON, and its output, as the View that its Formats give the call
// says, as ShowTool describes. The PartBox decides how the call shows: as a
// row or a block, expanded or not, with a marker, which blinks while the
// call executes.
type ToolBox struct {
	*PartBox
	// Tool contains the latest call state and input.
	Tool message.ToolPart
	// Formats describe the calls of the tools they know, in order: the
	// first that describes the call's tool says how it shows, as Format
	// gives it.
	Formats []ToolFormatter
	// View is how the box shows Tool, as Format gave it.
	View ToolView
	// MutedFg colors notes on a call's input, such as a command's timeout
	// and the lines a read call reads after its path.
	MutedFg style.Color
	// Highlighter colors a command line as Body colors code.
	Highlighter *widget.Highlighter
	// Params displays a call's input as ParamsText gives it, such as a
	// command line or an edit call's path, wrapped to the box width.
	Params *widget.Text
	// Body displays tool-specific input or read output.
	Body *widget.Markdown
	// Lines displays the View's Lines in place of Body, wrapped to the box
	// width.
	Lines *widget.Text
	// Output displays progress or completed output.
	Output *widget.Markdown
	// Truncated shows TruncatedNote under the details, in MutedFg, while
	// KeepRows cuts them short.
	Truncated *widget.Text
	// KeepRows, when above zero, is the most rows of the call's tallest
	// block that the details show, as KeepRows the function cuts text to:
	// its output, its body, or its Lines, whichever takes the most rows at
	// WrapWidth. Zero shows them whole. UI.FitToolBoxes sets it, so
	// that an expanded call fits the screen.
	KeepRows int
	// WrapWidth is the width, in columns, that KeepRows counts rows at.
	WrapWidth int
	// FitLimit and FitWidth are the rows and the width FitCalls last fitted
	// the call to, and Fitted reports that it did, since the call last
	// changed.
	FitLimit, FitWidth int
	Fitted             bool
}

// TruncatedNote ends the details of a call that KeepLines cuts short.
const TruncatedNote = "[truncated]"

var _ BoxContent = (*ToolBox)(nil)

// NewToolBox creates the box of a tool call, collapsed to a row on the
// canvas, whose block, once it expands, fills the theme's success surface in
// ExpandedBoxed. Its status and the notes on its input are in the theme's
// muted text color. Its header is a single row: the call summary and then
// the status, with the summary cut off with an ellipsis when the row is
// full, after ToolMarker, in the box's left padding, once SetMarker shows
// it. The marker and the tool's name are in the muted text color too, as
// the input is. Clicking the header
// expands the block, and the header of an expanded call drops the summary of
// its input, which the details show, and the marker. The call shows as the
// first of formats that describes it says.
func NewToolBox(tool message.ToolPart, theme *style.Theme, fg style.Color, formats ...ToolFormatter) *ToolBox {
	theme = themeOrDefault(theme)
	bg := theme.SuccessSurface
	b := &ToolBox{
		PartBox:     NewPartBox(theme, fg, bg),
		Formats:     formats,
		MutedFg:     theme.TextMuted,
		Highlighter: widget.NewHighlighter(theme),
		Params: widget.NewText("",
			widget.TextWithFg(theme.CodeFg),
			widget.TextWithBg(style.ColorTransparent),
			widget.TextWithWrapMode(textbuf.WrapWord),
		),
		Body: NewToolBody(theme, fg, style.ColorTransparent),
		Lines: widget.NewText("",
			widget.TextWithFg(fg),
			widget.TextWithBg(style.ColorTransparent),
			widget.TextWithWrapMode(textbuf.WrapWord),
		),
		Output: NewToolBody(theme, fg, style.ColorTransparent),
		Truncated: widget.NewText(TruncatedNote,
			widget.TextWithFg(theme.TextMuted),
			widget.TextWithBg(style.ColorTransparent),
		),
	}
	b.Content = b
	b.Details.Add(b.Params)
	b.Details.Add(b.Body)
	b.Details.Add(b.Lines)
	b.Details.Add(b.Output)
	b.Details.Add(b.Truncated)
	b.ShowTool(tool)
	b.ShowShape()
	return b
}

// NewToolBody creates a Markdown view for tool input or output.
func NewToolBody(theme *style.Theme, fg, bg style.Color) *widget.Markdown {
	return widget.NewMarkdown("",
		widget.MarkdownWithTheme(theme),
		widget.MarkdownWithFg(fg),
		widget.MarkdownWithBg(bg),
		widget.MarkdownWithCodeBg(bg),
	)
}

// Span returns when the call started executing and when it ended, either
// zero when not known, as before it runs or while it does.
func (b *ToolBox) Span() (start, end time.Time) {
	switch s := b.Tool.State.(type) {
	case message.ToolStateRunning:
		return s.StartedAt, time.Time{}
	case message.ToolStateCompleted:
		return s.StartedAt, s.EndedAt
	case message.ToolStateError:
		return s.StartedAt, s.EndedAt
	}
	return time.Time{}, time.Time{}
}

// Format returns how tool shows: as the first of Formats that describes its
// calls says, or as any tool's call.
func (b *ToolBox) Format(tool message.ToolPart) ToolView {
	for _, f := range b.Formats {
		if view, ok := f.FormatTool(tool); ok {
			return view
		}
	}
	return ToolView{}
}

// ShowTool updates the tool header, status, params, body, and output to show
// tool, as its View, which Format gives, says. The params show its main
// param, the one its header sums its input up by unless the View names
// another, then the View's notes, and then a note on each other param, as
// ParamNotes gives them, but for those the View leaves unnoted, and the
// View's Lines, when it has any, show in place of the body. Input that is
// not a JSON object yet, as while it streams, is shown as it is in the body
// instead. A call whose params show nothing, and that has nothing else to
// show, shows its input in the body, as InputJSON gives it, so that
// expanding it shows something. A View that is HeaderOnly shows none of that,
// but the output of a failed call. A call left with nothing to show is
// Empty, and shows as its header alone. The box is busy while the call
// executes, and KeepExpanded as the View says.
func (b *ToolBox) ShowTool(tool message.ToolPart) {
	b.Tool = tool
	b.View = b.Format(tool)
	view := b.View
	b.KeepExpanded = view.KeepExpanded
	_, status, output := ToolHeaderOutput(tool, view, b.NameFg, b.Status.Fg())
	args := DecodeToolArgs(tool.Input())
	key, param := SummaryField(args)
	if view.ParamKey != "" {
		key, param = view.ParamKey, view.Param
	}
	param = strings.TrimRight(param, "\n")
	// The header shows the input, so the details need not, as they need not
	// fall back to the input's JSON for it.
	inHeader := view.ParamInHeader && param != ""
	if inHeader {
		param = ""
	}
	content, filetype := view.Body, view.BodyLang
	if view.OutputInBody {
		content, output = output, ""
	}
	hasLines := len(view.Lines) > 0
	if hasLines {
		content = ""
	}
	notes := slices.Clone(view.Notes)
	if args != nil {
		notes = slices.AppendSeq(notes, ParamNotes(tool.Input(), append([]string{key}, view.Unnoted...)...))
	} else if len(tool.Input()) > 0 {
		content, filetype = string(tool.Input()), "json"
	}
	hasParams := param != "" || len(notes) > 0
	if !hasParams && !inHeader && !hasLines && content == "" && output == "" && len(args) > 0 {
		content, filetype = InputJSON(tool.Input()), "json"
	}
	lines := view.Lines
	if view.HeaderOnly {
		// Only why a failed call failed shows under the header.
		param, notes, hasParams, content, lines, hasLines = "", nil, false, "", nil, false
		if _, failed := tool.State.(message.ToolStateError); !failed {
			output = ""
		}
	}
	cut := false
	if b.KeepRows > 0 {
		switch TallestBlock(output, content, lines, b.WrapWidth) {
		case BlockOutput:
			output, cut = KeepRows(output, b.KeepRows, b.WrapWidth)
		case BlockBody:
			content, cut = KeepRows(content, b.KeepRows, b.WrapWidth)
		case BlockLines:
			lines, cut = KeepLineRows(lines, b.KeepRows, b.WrapWidth)
		}
	}
	b.Truncated.SetGone(!cut)
	b.Params.SetStyledContent(b.ParamsText(param, view.Shell, notes))
	b.Params.SetGone(!hasParams)
	b.ShowHeader()
	b.Body.SetContent(ToolFence(filetype, content))
	b.Body.SetGone(content == "")
	b.Lines.SetStyledContent(b.LinesText(lines))
	b.Lines.SetGone(!hasLines)
	b.Output.SetContent(ToolFence("text", output))
	b.Output.SetGone(output == "")
	b.SetEmpty(!hasParams && content == "" && !hasLines && output == "")
	b.SetStatus(status)
	// Only an executing call's marker blinks.
	_, executing := tool.State.(message.ToolStateRunning)
	b.SetBusy(executing)
}

// SetTool shows tool, unless it equals the call already shown, and has
// FitCalls fit it again, as its content may have grown.
func (b *ToolBox) SetTool(tool message.ToolPart) {
	if reflect.DeepEqual(b.Tool, tool) {
		return
	}
	b.Fitted = false
	b.ShowTool(tool)
}

// SetKeepRows shows at most n rows of the call's tallest block, as KeepRows
// says, or all of them when n is zero.
func (b *ToolBox) SetKeepRows(n int) {
	if n == b.KeepRows {
		return
	}
	b.KeepRows = n
	b.ShowTool(b.Tool)
}

// Block names a block of a call's details that KeepRows may cut.
type Block int

const (
	// BlockNone is no block: the call has nothing to cut.
	BlockNone Block = iota
	// BlockOutput is the call's output, shown under its body.
	BlockOutput
	// BlockBody is the call's body, such as its input as JSON.
	BlockBody
	// BlockLines is the view's Lines, such as a todo list.
	BlockLines
)

// TallestBlock returns which of output, body, and lines takes the most rows
// at width, as Rows counts them, or BlockNone when all are empty. Ties go to
// the output, then the body.
func TallestBlock(output, body string, lines []ToolLine, width int) Block {
	best, rows := BlockNone, 0
	for _, c := range []struct {
		block Block
		rows  int
	}{
		{BlockOutput, Rows(output, width)},
		{BlockBody, Rows(body, width)},
		{BlockLines, LineRows(lines, width)},
	} {
		if c.rows > rows {
			best, rows = c.block, c.rows
		}
	}
	return best
}

// BlockRows returns the rows the call's tallest block takes whole at
// WrapWidth, as TallestBlock picks it.
func (b *ToolBox) BlockRows() int {
	_, _, output := ToolHeaderOutput(b.Tool, b.View, b.NameFg, b.Status.Fg())
	body := b.View.Body
	if b.View.OutputInBody {
		body, output = output, ""
	}
	return max(Rows(output, b.WrapWidth), Rows(body, b.WrapWidth), LineRows(b.View.Lines, b.WrapWidth))
}

// Rows returns the rows text takes at width: each of its lines one row for
// every width columns it holds, or one row when it is blank. Any width below
// one counts each line as a row. It counts characters, not the columns a wide
// character takes, which is near enough for KeepRows, whose cut FitCalls
// measures as laid out.
func Rows(text string, width int) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 0
	}
	n := 0
	for line := range strings.SplitSeq(text, "\n") {
		n += LineRowCount(line, width)
	}
	return n
}

// LineRowCount returns the rows one line takes at width, as Rows counts them.
func LineRowCount(line string, width int) int {
	if width < 1 {
		return 1
	}
	return max((utf8.RuneCountInString(line)+width-1)/width, 1)
}

// LineRows returns the rows lines take at width, as Rows counts them.
func LineRows(lines []ToolLine, width int) int {
	n := 0
	for _, l := range lines {
		n += LineRowCount(l.Text, width)
	}
	return n
}

// KeepRows returns the first n rows of text at width, as Rows counts them,
// cutting a line that wraps past them, and whether it left any text out.
func KeepRows(text string, n, width int) (string, bool) {
	text = strings.TrimRight(text, "\n")
	var out []string
	left := n
	for line := range strings.SplitSeq(text, "\n") {
		if left == 0 {
			return strings.Join(out, "\n"), true
		}
		rows := LineRowCount(line, width)
		if rows > left {
			out = append(out, string([]rune(line)[:left*width]))
			return strings.Join(out, "\n"), true
		}
		out = append(out, line)
		left -= rows
	}
	return text, false
}

// KeepLineRows returns the first n rows of lines at width, as KeepRows cuts
// text, and whether it left any out.
func KeepLineRows(lines []ToolLine, n, width int) ([]ToolLine, bool) {
	var out []ToolLine
	left := n
	for _, l := range lines {
		if left == 0 {
			return out, true
		}
		rows := LineRowCount(l.Text, width)
		if rows > left {
			l.Text = string([]rune(l.Text)[:left*width])
			return append(out, l), true
		}
		out = append(out, l)
		left -= rows
	}
	return lines, false
}

// ShowHeader writes the call's header: the tool's name and the summary of its
// input, as ToolHeaderOutput gives them, with the input in the color of the
// status, or the name alone while the box is Open and Params shows the
// input, which the summary repeats, unless the view keeps the input in the
// header, as ParamInHeader says.
func (b *ToolBox) ShowHeader() {
	if b.Open() && b.Params.Visible() && !b.View.ParamInHeader && b.View.Title == "" {
		b.Header.SetStyledContent(style.NewStyledText(ToolNameChunk(b.View.NameLabel(b.Tool.Name), b.NameFg)))
		return
	}
	// The input after the tool's name reads dim, as the status after it does.
	header, _, _ := ToolHeaderOutput(b.Tool, b.View, b.NameFg, b.Status.Fg())
	b.Header.SetStyledContent(header)
}

// ParamsText returns the params of a call: param, the main one, and then
// each of notes in MutedFg, a space apart. When command is set, param is a
// command, shown as a prompt line, "$ command", with the command highlighted
// as HighlightCommand does. It returns empty text when there is neither
// param nor a note.
func (b *ToolBox) ParamsText(param string, command bool, notes []string) style.StyledText {
	fg, bg := b.Params.Fg(), b.Params.Bg()
	var chunks []style.TextChunk
	switch {
	case param == "":
	case command:
		chunks = append(chunks, style.TextChunk{Text: "$ ", Fg: fg, Bg: bg})
		for i, line := range b.HighlightCommand(param, fg, bg) {
			if i > 0 {
				chunks = append(chunks, style.Plain("\n"))
			}
			chunks = AppendSpans(chunks, line)
		}
	default:
		chunks = append(chunks, style.TextChunk{Text: param, Fg: fg, Bg: bg})
	}
	muted := b.MutedFg
	for _, note := range notes {
		if len(chunks) > 0 {
			chunks = append(chunks, style.Plain(" "))
		}
		chunks = append(chunks, style.TextChunk{Text: note, Fg: muted})
	}
	return style.NewStyledText(chunks...)
}

// LinesText returns lines one per row, each in the color of Lines or, when
// Muted, in MutedFg.
func (b *ToolBox) LinesText(lines []ToolLine) style.StyledText {
	fg, muted := b.Lines.Fg(), b.MutedFg
	var chunks []style.TextChunk
	for i, line := range lines {
		if i > 0 {
			chunks = append(chunks, style.Plain("\n"))
		}
		c := style.TextChunk{Text: line.Text, Fg: fg}
		if line.Muted {
			c.Fg = muted
		}
		chunks = append(chunks, c)
	}
	return style.NewStyledText(chunks...)
}

// HighlightCommand returns the lines of command highlighted as bash, one per
// line, with fg and bg for the text the highlighter leaves uncolored. The
// words that name the commands it runs, as shell.CommandWords finds them,
// such as go and test in "sudo go test" and make in "bash -c 'make'", show
// as functions do wherever they stand, as CommandTokens gives them.
func (b *ToolBox) HighlightCommand(command string, fg, bg style.Color) []textbuf.Line {
	lang := syntax.Lookup("bash")
	if lang == nil {
		lines, _ := b.Highlighter.Highlight(command, "bash", fg, bg)
		return lines
	}
	words, _ := shell.CommandWords(command)
	lines := make([]textbuf.Line, 0, strings.Count(command, "\n")+1)
	var (
		st   syntax.State
		toks []syntax.Token
	)
	at := 0
	for text := range strings.SplitSeq(command, "\n") {
		toks, st = lang.AppendLine(toks[:0], text, st)
		lines = append(lines, b.Highlighter.Line(text, CommandTokens(toks, at, words), fg, bg))
		at += len(text) + 1
	}
	return lines
}

// CommandTokens returns toks, the tokens the bash lexer gives a line that
// starts at offset at of its source, with the parts of them within words,
// words of that source ordered by offset, as functions, in a new slice. The
// tokens the lexer already shows as builtins or keywords, as cd and exit,
// keep their kind.
func CommandTokens(toks []syntax.Token, at int, words []shell.Word) []syntax.Token {
	out := make([]syntax.Token, 0, len(toks))
	for _, t := range toks {
		for text := t.Text; text != ""; {
			for len(words) > 0 && words[0].Offset+len(words[0].Raw) <= at {
				words = words[1:]
			}
			n, kind := len(text), t.Kind
			switch {
			case len(words) == 0:
			case words[0].Offset > at:
				n = min(n, words[0].Offset-at)
			default:
				n = min(n, words[0].Offset+len(words[0].Raw)-at)
				if kind != syntax.BuiltinFunction && kind != syntax.Keyword {
					kind = syntax.Function
				}
			}
			out = append(out, syntax.Token{Kind: kind, Text: text[:n]})
			text, at = text[n:], at+n
		}
	}
	return out
}

// AppendSpans appends the spans of line to chunks, each as a chunk in its
// colors and attributes, and returns the extended slice.
func AppendSpans(chunks []style.TextChunk, line textbuf.Line) []style.TextChunk {
	for _, span := range line.Spans {
		fg, bg := span.Fg, span.Bg
		chunks = append(chunks, style.TextChunk{Text: span.Text, Fg: fg, Bg: bg, Attributes: span.Attributes})
	}
	return chunks
}
