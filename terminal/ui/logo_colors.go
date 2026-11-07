package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/unaquatic/fluid/style"
)

// A ColorStep changes a color in HSV: its hue by Hue degrees, and its
// saturation and value, each from 0 through 1, by Saturation and Value. The
// zero ColorStep keeps the color as it is.
type ColorStep struct {
	Hue, Saturation, Value float64
}

// Apply returns c changed by s, its saturation and value kept from 0
// through 1.
func (s ColorStep) Apply(c style.Color) style.Color {
	if s == (ColorStep{}) {
		return c
	}
	h, sat, v := style.ColorRGBToHSV(c)
	return style.ColorHSVToRGB(h+s.Hue, min(max(sat+s.Saturation, 0), 1), min(max(v+s.Value, 0), 1))
}

// LogoColorSteps are the rows of changes that LogoColorRows makes to a color:
// none first, then pairs of opposite steps. Of each pair, the left one is
// redder or darker and the right one yellower or lighter, a step further from
// the color than the pair above it. Two logos fit in 80 columns.
var LogoColorSteps = [][]ColorStep{
	{{}},
	{{Hue: -4}, {Hue: 4}},
	{{Hue: -8}, {Hue: 8}},
	{{Hue: -12}, {Hue: 12}},
	{{Value: -0.1}, {Saturation: -0.15}},
	{{Value: -0.2}, {Saturation: -0.3}},
	{{Value: -0.3}, {Saturation: -0.45}},
}

// LogoColorRows returns the hex colors that LogoColorSteps make of the color
// hex, row by row, to compare oranges close to it. The first row holds hex
// alone.
func LogoColorRows(hex string) [][]string {
	base := style.ColorFromHex(hex)
	rows := make([][]string, len(LogoColorSteps))
	for i, steps := range LogoColorSteps {
		for _, s := range steps {
			rows[i] = append(rows[i], s.Apply(base).ToHex())
		}
	}
	return rows
}

// LogoColorGap is the number of blank columns between two logos in a row of
// LogoColorLines.
const LogoColorGap = 4

// LogoColorLines returns LogoColorTile of each color of rows, the tiles of a
// row side by side, with a blank line between rows.
func LogoColorLines(rows [][]string) []style.StyledText {
	var lines []style.StyledText
	for _, row := range rows {
		if len(lines) > 0 {
			lines = append(lines, style.StyledText{})
		}
		tiles := make([][]style.StyledText, len(row))
		for i, hex := range row {
			tiles[i] = LogoColorTile(hex)
		}
		lines = append(lines, JoinColumns(tiles, LogoColorGap)...)
	}
	return lines
}

// LogoColorTile returns the lines of the product logo with its "f" in the
// color hex, and under them the line of Go that sets product.LogoInitialColor
// to hex, its const keyword in hex too.
func LogoColorTile(hex string) []style.StyledText {
	c := style.ColorFromHex(hex)
	keyword := style.New().WithFg(c).Func()
	return append(slices.Collect(LogoLinesIn(c)),
		style.NewStyledText(keyword("const"), style.Plain(fmt.Sprintf(" LogoInitialColor = %q", hex))))
}

// JoinColumns returns blocks of lines set side by side: each line of a block
// is padded on the right to the widest line of its block, then by gap more
// columns, except in the last block. A block shorter than the others ends in
// blank lines. Every character of a line must be one column wide.
func JoinColumns(blocks [][]style.StyledText, gap int) []style.StyledText {
	height := 0
	widths := make([]int, len(blocks))
	for i, b := range blocks {
		height = max(height, len(b))
		for _, line := range b {
			widths[i] = max(widths[i], line.Len())
		}
	}
	lines := make([]style.StyledText, height)
	for y := range height {
		var chunks []style.TextChunk
		for i, b := range blocks {
			var line style.StyledText
			if y < len(b) {
				line = b[y]
			}
			chunks = append(chunks, line.Chunks...)
			if i < len(blocks)-1 {
				chunks = append(chunks, style.Plain(strings.Repeat(" ", widths[i]-line.Len()+gap)))
			}
		}
		lines[y] = style.NewStyledText(chunks...)
	}
	return lines
}
