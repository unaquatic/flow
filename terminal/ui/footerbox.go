package ui

import (
	"fmt"
	"time"

	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// FooterBox displays the footer of the reply that ends a turn as a single
// row, cut short with an ellipsis when it does not fit: how long the turn
// took, such as "Worked for 42s", and then, in the CachePerTurn mode, the
// cache use of its last request. It is hidden under the other replies, and
// until the turn has ended.
type FooterBox struct {
	*widget.Box
	// Text is the row's text.
	Text *widget.Text
	// Worked is how long the turn that the reply ends took, or zero when the
	// reply does not end one.
	Worked time.Duration
	// Cache is the cache use shown after the time, as CacheText gives it, or
	// "".
	Cache string
}

// NewFooterBox creates the footer row of a reply that ends a turn that took
// worked. The row is transparent and carries the prose inset so that it
// lines up with the reply's text. It draws the time, and the cache use that
// SetCache sets after it, in fg.
func NewFooterBox(worked time.Duration, fg style.Color) *FooterBox {
	text := widget.NewText("",
		widget.TextWithFg(fg),
		widget.TextWithBg(style.ColorTransparent),
		widget.TextWithLayout(layout.WithHeight(layout.Points(1))),
		widget.TextWithTruncate(true),
	)
	// A leaf Text draws at its own origin, so a padded box insets it. Margins
	// on the text would widen the bubble by their width instead of keeping
	// the row inside it.
	b := &FooterBox{Box: GutterRow(text), Text: text, Worked: worked}
	b.Sync()
	return b
}

// SetTurnEnd updates how long the turn that the reply ends took.
func (b *FooterBox) SetTurnEnd(worked time.Duration) {
	b.Worked = worked
	b.Sync()
}

// SetCache shows cache, a cache use as CacheText gives it, after the time.
// An empty cache shows none.
func (b *FooterBox) SetCache(cache string) {
	if cache == b.Cache {
		return
	}
	b.Cache = cache
	b.Sync()
}

// Sync shows FooterText of the recorded turn and cache use, and hides the
// row when that is empty.
func (b *FooterBox) Sync() {
	text := FooterText(b.Worked, b.Cache)
	if len(text.Chunks) == 0 {
		b.SetGone(true)
		return
	}
	b.SetGone(false)
	b.Text.SetStyledContent(text)
}

// FooterText returns the footer of a reply that ends a turn that took worked:
// FormatWorked of the time, and then, when cache is not empty, a space and
// cache, all in the row's color. A time that rounds to zero seconds, as that
// of a reply that does not end a turn or of a run that failed at once, has no
// footer.
func FooterText(worked time.Duration, cache string) style.StyledText {
	if worked.Round(time.Second) <= 0 {
		return style.StyledText{}
	}
	text := style.NewStyledText(style.Plain(FormatWorked(worked)))
	if cache != "" {
		text.Chunks = append(text.Chunks, style.Plain(" "+cache))
	}
	return text
}

// FormatWorked returns the footer text for a turn that took d, as
// FormatDuration gives it: "Worked for 42s", "Worked for 1m 05s", or "Worked
// for 1h 02m 05s".
func FormatWorked(d time.Duration) string {
	return "Worked for " + FormatDuration(d)
}

// FormatDuration returns d rounded to the second, such as "42s", "1m 05s",
// or "1h 02m 05s". A negative d shows as "0s".
func FormatDuration(d time.Duration) string {
	secs := int(max(d.Round(time.Second), 0) / time.Second)
	h, m, s := secs/3600, secs/60%60, secs%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
