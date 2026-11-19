package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// CacheLabel is the name a cache row shows, as ToolLabel names a tool.
const CacheLabel = "Cache"

// CacheBox is the PartBox of the cache use of a reply's request, which the
// CachePerMessage mode shows: a row like a collapsed tool call's, "Cache 7.7K
// Write 0K", under the reply's parts and views. It starts with ToolMarker
// as tool calls do, has nothing to expand, and is gone until shown.
type CacheBox struct {
	*PartBox
	// Usage is the token counts of the request, as SetUsage sets them.
	Usage message.Usage
}

var _ BoxContent = (*CacheBox)(nil)

// NewCacheBox creates a cache row, gone until SetShown shows it.
func NewCacheBox() *CacheBox {
	b := &CacheBox{PartBox: NewPartBox(nil, style.Color{}, style.ColorTransparent)}
	b.Content = b
	b.Shape = ShapeRow
	// The row is parted from the tool calls above it, so that it reads as
	// the request's own, not as one more call.
	b.Apart = true
	b.Pin()
	b.SetGone(true)
	b.ShowHeader()
	b.ShowShape()
	return b
}

// SetUsage shows the cache use of u, unless it is the one already shown.
func (b *CacheBox) SetUsage(u message.Usage) {
	if u == b.Usage {
		return
	}
	b.Usage = u
	b.ShowHeader()
}

// SetShown shows the row, or hides it, as show says. A row is gone while it
// has no usage to show, whatever show says.
func (b *CacheBox) SetShown(show bool) {
	show = show && !b.Usage.IsZero()
	if b.Visible() != show {
		b.SetGone(!show)
	}
}

// ShowHeader writes the cache use of Usage as the turn's footer shows it,
// CacheText of it: CacheLabel in NameFg, not bold, as a tool's name, the
// counts in textFg, as CacheChunks colors them, and the other labels in the
// color of Status.
func (b *CacheBox) ShowHeader() {
	chunks := CacheChunks(CacheText(b.Usage))
	for i := range chunks {
		switch {
		case i == 0:
			chunks[i] = ToolNameChunk(chunks[i].Text, b.NameFg)
		case chunks[i].Fg.IsTransparent():
			chunks[i].Fg = b.Status.Fg()
		}
	}
	b.Header.SetStyledContent(style.NewStyledText(chunks...))
}
