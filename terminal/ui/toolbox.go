package ui

import (
	"reflect"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// ToolBox is the PartBox of a tool call. It writes the call's header and
// status, and shows in its details the call's params, a body, such as an
// edit's diff or the file a read reads, and its output, as the View that its
// Formats give the call says, as ShowTool describes. The PartBox decides how
// the call shows: as a row or a block, expanded or not, with a marker, and
// shimmering while the call executes.
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
	Highlighter widget.SyntaxHighlighter
	// Params displays a call's input as ParamsText gives it, such as a
	// command line or an edit call's path, wrapped to the box width.
	Params *widget.Text
	// Body displays tool-specific input or read output.
	Body *widget.Markdown
	// Output displays progress or completed output.
	Output *widget.Markdown
}

var _ BoxContent = (*ToolBox)(nil)

// NewToolBox creates the box of a tool call, collapsed in ToolCallsCompact,
// a row on the canvas, whose block, once it expands or is boxed, fills the
// theme's success surface. Its status is in PanelDimFg, as a panel colors its
// keys after its title, and notes on its input in the theme's muted text
// color. Its header is a single row: the call summary and then the status,
// with the summary cut off with an ellipsis when the row is full, after
// ToolMarker, in the box's left padding, once SetMarker shows it. The marker
// and the tool's name are in PanelDimFg, as the input is. Clicking the header
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
			widget.TextWithBg(bg),
			widget.TextWithWrapMode(textbuf.WrapWord),
		),
		Body:   NewToolBody(theme, fg, bg),
		Output: NewToolBody(theme, fg, bg),
	}
	b.Content = b
	b.Details.Add(b.Params)
	b.Details.Add(b.Body)
	b.Details.Add(b.Output)
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

// SetTool shows tool, unless it equals the call already shown.
func (b *ToolBox) SetTool(tool message.ToolPart) {
	if reflect.DeepEqual(b.Tool, tool) {
		return
	}
	b.ShowTool(tool)
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
// ParamNotes gives them, but for those the View leaves unnoted. Input that is
// not a JSON object yet, as while it streams, is shown as it is in the body
// instead. A call whose params show nothing, and that has nothing else to
// show, shows its input in the body, as InputJSON gives it, so that
// expanding it shows something. The box is busy while the call executes.
func (b *ToolBox) ShowTool(tool message.ToolPart) {
	b.Tool = tool
	b.View = b.Format(tool)
	view := b.View
	_, status, output := ToolHeaderOutput(tool, view, b.NameFg, b.Status.Fg())
	args := DecodeToolArgs(tool.Input())
	key, param := SummaryField(args)
	if view.ParamKey != "" {
		key, param = view.ParamKey, view.Param
	}
	param = strings.TrimRight(param, "\n")
	content, filetype := view.Body, view.BodyLang
	if view.OutputInBody {
		content, output = output, ""
	}
	notes := slices.Clone(view.Notes)
	if args != nil {
		notes = slices.AppendSeq(notes, ParamNotes(tool.Input(), append([]string{key}, view.Unnoted...)...))
	} else if len(tool.Input()) > 0 {
		content, filetype = string(tool.Input()), "json"
	}
	hasParams := param != "" || len(notes) > 0
	if !hasParams && content == "" && output == "" && len(args) > 0 {
		content, filetype = InputJSON(tool.Input()), "json"
	}
	b.Params.SetStyledContent(b.ParamsText(param, view.Shell, notes))
	b.Params.SetGone(!hasParams)
	b.ShowHeader()
	b.Body.SetContent(ToolFence(filetype, content))
	b.Body.SetGone(content == "")
	b.Output.SetContent(ToolFence("text", output))
	b.Output.SetGone(output == "")
	b.SetStatus(status)
	// Only an executing call shimmers.
	_, executing := tool.State.(message.ToolStateRunning)
	b.SetBusy(executing)
}

// ShowHeader writes the call's header: the tool's name and the summary of its
// input, as ToolHeaderOutput gives them, with the input in the color of the
// status, or the name alone while the box is Open and Params shows the
// input, which the summary repeats.
func (b *ToolBox) ShowHeader() {
	if b.Open() && b.Params.Visible() {
		b.Header.SetStyledContent(style.NewStyledText(ToolNameChunk(b.View.NameLabel(b.Tool.Name), b.NameFg)))
		return
	}
	// The input after the tool's name reads dim, as the status after it does.
	header, _, _ := ToolHeaderOutput(b.Tool, b.View, b.NameFg, b.Status.Fg())
	b.Header.SetStyledContent(header)
}

// ParamsText returns the params of a call: param, the main one, and then
// each of notes in MutedFg, a space apart. A shell param is a command, shown
// as a prompt line, "$ command", with the command highlighted as bash. It
// returns empty text when there is neither param nor a note.
func (b *ToolBox) ParamsText(param string, shell bool, notes []string) style.StyledText {
	fg, bg := b.Params.Fg(), b.Params.Bg()
	var chunks []style.TextChunk
	switch {
	case param == "":
	case shell:
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

// HighlightCommand returns the lines of command highlighted as bash, at least
// one, with fg and bg for the text the highlighter leaves uncolored. A command
// the highlighter fails on is returned in fg on bg.
func (b *ToolBox) HighlightCommand(command string, fg, bg style.Color) []textbuf.Line {
	lines, err := b.Highlighter.Highlight(command, "bash", fg, bg)
	if err == nil && len(lines) > 0 {
		return lines
	}
	// Show the command unstyled rather than not at all.
	lines = nil
	for text := range strings.SplitSeq(command, "\n") {
		lines = append(lines, textbuf.Line{Spans: []textbuf.Span{{Text: text, Fg: fg, Bg: bg}}})
	}
	return lines
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
