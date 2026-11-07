package ui

import (
	"fmt"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// CacheLabel is the name a cache row shows, as ToolLabel names a tool.
const CacheLabel = "Cache"

// CacheBox is the PartBox of the cache use of a reply's request, which the
// CachePerMessage mode shows: a row like a collapsed tool call's, "Cache read
// 7.7K write 0K", under the reply's parts and views. It starts with ToolMarker
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

// ShowHeader writes CacheLabel in bold and NameFg, then the counts of Usage in
// PanelDimFg, as CacheCounts gives them.
func (b *CacheBox) ShowHeader() {
	b.Header.SetStyledContent(style.NewStyledText(
		ToolNameChunk(CacheLabel, b.NameFg),
		style.Plain(" "),
		style.TextChunk{Text: CacheCounts(b.Usage), Fg: PanelDimFg},
	))
}

// CacheCounts returns the cache use of a request that reported u as a cache
// row shows it after CacheLabel: the prompt tokens it read from a prompt
// cache and those it wrote to one, such as "read 7.7K write 0K".
func CacheCounts(u message.Usage) string {
	return fmt.Sprintf("read %s write %s", FormatTokens(u.CachedInputTokens), FormatTokens(u.CacheCreationInputTokens))
}
