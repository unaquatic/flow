package ui

import (
	"fmt"

	"github.com/unaquatic/fluid/style"
)

// xtermCubeLevels are the channel values of the xterm 6×6×6 color cube.
var xtermCubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// PaletteLines returns the fixed entries of the xterm 256-color palette, 16
// through 255, as rows of color swatches with each index printed under its
// swatch. The color cube comes first as two bands of three 6×6 blocks, one
// block per red level, with green increasing down a block and blue across it.
// A blank line separates it from the gray ramp, which follows in two rows of
// twelve.
func PaletteLines() []style.StyledText {
	var lines []style.StyledText
	for band := range 2 {
		for g := range 6 {
			var row SwatchRow
			for r := range 3 {
				if r > 0 {
					row.Gap()
				}
				for b := range 6 {
					row.Add(CubeEntry(band*3+r, g, b))
				}
			}
			lines = append(lines, row.Colors, row.Labels)
		}
	}
	lines = append(lines, style.StyledText{})
	for half := range 2 {
		var row SwatchRow
		for n := range 12 {
			row.Add(GrayEntry(half*12 + n))
		}
		lines = append(lines, row.Colors, row.Labels)
	}
	return lines
}

// CubeEntry returns the palette index and color of the color-cube entry with
// red, green, and blue levels r, g, and b, each from 0 through 5.
func CubeEntry(r, g, b int) (int, style.Color) {
	c := style.ColorFromInts(xtermCubeLevels[r], xtermCubeLevels[g], xtermCubeLevels[b], 255)
	return 16 + 36*r + 6*g + b, c
}

// GrayEntry returns the palette index and color of gray-ramp entry n, from 0
// through 23.
func GrayEntry(n int) (int, style.Color) {
	v := uint8(8 + 10*n)
	return 232 + n, style.ColorFromInts(v, v, v, 255)
}

// SwatchRow is a line of color swatches above a line of their palette
// indexes.
type SwatchRow struct {
	// Colors holds the swatches, three columns each, separated by a space.
	Colors style.StyledText
	// Labels holds each swatch's index, right-aligned under it.
	Labels style.StyledText
}

// Add appends the swatch of palette entry index, whose color is c.
func (s *SwatchRow) Add(index int, c style.Color) {
	fill := style.New().WithBg(c).Func()
	s.Colors.Chunks = append(s.Colors.Chunks, fill("   "), style.Plain(" "))
	s.Labels.Chunks = append(s.Labels.Chunks, style.Plain(fmt.Sprintf("%3d ", index)))
}

// Gap appends a blank column to both lines.
func (s *SwatchRow) Gap() {
	s.Colors.Chunks = append(s.Colors.Chunks, style.Plain(" "))
	s.Labels.Chunks = append(s.Labels.Chunks, style.Plain(" "))
}
