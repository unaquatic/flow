package product

import (
	"iter"
	"strings"
)

// Logo is the application's name drawn in half-block characters, 30 columns
// wide and 8 rows tall. Each column holds two square pixels, one above the
// other. The top pixel row is blank, which sets the name half a row below the
// line above it. Its lines carry no trailing spaces, and it ends without a
// newline. The README logos, docs/logo-*.svg, are drawn from a copy of it,
// cmd/logo/art/flow.txt in aquatic next to this repository: change both.
const Logo = "" +
	"    ▄▄▄ ▄▄▄\n" +
	"   ██▀▀ ▀██\n" +
	" ▄▄██▄▄  ██   ▄▄▄▄▄▄  ▄▄    ▄▄\n" +
	" ▀▀██▀▀  ██  ██▀▀▀▀██ ██    ██\n" +
	"   ██    ██  ██    ██ ██    ██\n" +
	"   ██    ██  ██    ██ ██ ▄▄ ██\n" +
	"▄▄ ██   ▄██▄ ██▄▄▄▄██ ████████\n" +
	"▀███▀   ▀▀▀▀  ▀▀▀▀▀▀   ▀▀  ▀▀"

// Icon is the application's icon drawn in half-block characters: an "ƒ" on a
// square, 16 columns wide and 8 rows tall, which hold 16 by 16 square pixels.
// It is the 16 by 16 icon of flow's landing page, cmd/landing in aquatic next
// to this repository, pixel for pixel, and the installer,
// .github/install/install.sh, prints a copy of it: change all three. Its top
// and bottom pixel rows are blank. Every line is 16 columns wide, as its
// spaces draw the square, and it ends without a newline. The terminal draws
// it in IconColor on IconBgColor.
const Icon = "" +
	"        ▄▄▄     \n" +
	"       ██▀▀     \n" +
	"     ▄▄██▄▄     \n" +
	"     ▀▀██▀▀     \n" +
	"       ██       \n" +
	"       ██       \n" +
	"    ██▄██       \n" +
	"     ▀▀▀        "

// IconColor is the color of the "f" of Icon, and IconBgColor the color of the
// square behind it, PrimaryColor, as the landing page's icon draws them.
const (
	IconColor   = "#111111"
	IconBgColor = PrimaryColor
)

// LogoInitialColor is the color that draws the "f" of Logo in the README
// logos: PrimaryColor. The terminal draws Icon in place of Logo.
const LogoInitialColor = PrimaryColor

// logoInitialWidth is the number of columns the "f" of Logo spans. The column
// after it is blank on every line, which sets the "f" apart from the "l".
const logoInitialWidth = 7

// LogoParts yields the lines of Logo in order, each cut in two: the part that
// draws the "f", and the rest of the line, which is empty when the line ends
// within the "f".
func LogoParts() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for line := range strings.SplitSeq(Logo, "\n") {
			runes := []rune(line)
			n := min(len(runes), logoInitialWidth)
			if !yield(string(runes[:n]), string(runes[n:])) {
				return
			}
		}
	}
}
