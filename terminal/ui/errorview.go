package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// TypeError identifies an ErrorPart.
const TypeError message.ContentType = "error"

// ErrorPartID is the ID of every ErrorPart: a message shows one error at
// most, which each update of the message replaces.
const ErrorPartID message.PartID = "error"

// ErrorPart is the error a message failed with, which UpdateMessage shows
// after the message's parts, in an ErrorView. It is not stored with the
// message.
type ErrorPart struct {
	// Err is the error the message failed with.
	Err error
}

// PartType returns TypeError.
func (ErrorPart) PartType() message.ContentType { return TypeError }

// PartID returns ErrorPartID.
func (ErrorPart) PartID() message.PartID { return ErrorPartID }

// ErrorView shows an ErrorPart as the indicator shows the error a run ended
// with: a marker in the left padding, in the theme's Danger, then the line
// IndicatorText gives ErrorIndicator of the error, and right under it, with
// no row between them, the body of an error response, as HighlightDetail
// colors it. It is a block among the rows of its bubble, as prose is.
type ErrorView struct {
	*widget.Box
	// Theme colors the marker and IndicatorErrorTitle.
	Theme *style.Theme
	// Marker starts the line, in the left padding.
	Marker *widget.Text
	// Label is the line: IndicatorErrorTitle and the error's headline.
	Label *widget.Text
	// Detail is the body under the line, or is gone when there is none.
	Detail *widget.Text
	// Highlighter colors a JSON body.
	Highlighter widget.SyntaxHighlighter
}

var (
	_ PartView   = (*ErrorView)(nil)
	_ StyledView = (*ErrorView)(nil)
)

// NewErrorView returns the view of p, its marker in shape, colored by theme.
func NewErrorView(p ErrorPart, theme *style.Theme, shape MarkerShape) *ErrorView {
	theme = themeOrDefault(theme)
	v := &ErrorView{
		Theme:  theme,
		Marker: NewMarker(shape, theme.Danger),
		Label: widget.NewText("",
			widget.TextWithFg(theme.TextMuted),
			widget.TextWithWrapMode(textbuf.WrapWord),
			widget.TextWithLayout(layout.WithFlexGrow(1), layout.WithFlexShrink(1)),
		),
		Detail:      widget.NewText("", widget.TextWithFg(textFg), widget.TextWithWrapMode(textbuf.WrapWord)),
		Highlighter: widget.NewHighlighter(theme),
	}
	// The marker sits in the left padding, as the indicator's does, so that
	// the line's text lines up with the body under it and with the prose.
	row := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithGap(1),
			layout.WithMarginLeft(layout.Points(-PaddingX)),
		),
		widget.Children(v.Marker, v.Label),
	)
	v.Box = widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
		),
		widget.Children(row, v.Detail),
	)
	v.SetPart(p)
	return v
}

// SetPart shows part, when it is an ErrorPart.
func (v *ErrorView) SetPart(part message.ContentPart) {
	p, ok := part.(ErrorPart)
	if !ok || p.Err == nil {
		return
	}
	ind := ErrorIndicator(app.ErrorOf(p.Err))
	v.Label.SetStyledContent(IndicatorText(ind, v.Theme.Danger))
	v.Detail.SetGone(ind.Detail == "")
	if ind.Detail != "" {
		v.Detail.SetStyledContent(HighlightDetail(v.Highlighter, ind.Detail, ind.JSON))
	}
}

// SetViewStyle shows the marker in the shape st names.
func (v *ErrorView) SetViewStyle(st ViewStyle) {
	StyleMarker(v.Marker, st.MarkerShape, v.Theme.Danger)
}

// HighlightDetail returns detail, the body of an error response, as it shows
// under the error's line: in textFg, and, when isJSON reports it is JSON,
// highlighted by h as JSON code is. JSON that h fails on, or a nil h, shows
// in textFg.
func HighlightDetail(h widget.SyntaxHighlighter, detail string, isJSON bool) style.StyledText {
	if isJSON && h != nil {
		lines, err := h.Highlight(detail, "json", textFg, style.ColorTransparent)
		if err == nil && len(lines) > 0 {
			var chunks []style.TextChunk
			for i, line := range lines {
				if i > 0 {
					chunks = append(chunks, style.Plain("\n"))
				}
				chunks = AppendSpans(chunks, line)
			}
			return style.NewStyledText(chunks...)
		}
	}
	return style.NewStyledText(style.TextChunk{Text: detail, Fg: textFg})
}
