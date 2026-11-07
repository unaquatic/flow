package ui

import "github.com/unaquatic/fluid/style"

// ColorChoice is the palette entries that a color the UI draws takes on
// terminals with 16 or 256 colors, in place of the entries nearest to it.
type ColorChoice struct {
	// Color is the color as the theme or the UI names it.
	Color style.Color
	// ANSI16 is the ANSI color, from style.ANSIBlack through
	// style.ANSIBrightWhite, that Color draws in in 16-color mode.
	ANSI16 style.ANSI
	// Xterm256 is the palette entry, from 0 through 255, that Color draws
	// in in 256-color mode.
	Xterm256 style.ANSI
}

// BackgroundChoices and TextChoices list the colors the UI draws behind text
// and as text, with the palette entries they take, one row for each color.
// Edit a row to change what a color looks like on terminals with 16 or 256
// colors. Roles that share a color share its row, which names them. A color
// can take one entry as a background and another as text. A color no row
// lists, such as one the UI blends of two others, draws in the entry nearest
// to it.
//
// In 16-color mode the canvas is the terminal's own background, so the input
// and the other cards draw in bright black, the one ANSI color that stands
// apart from it in nearly every theme. Text that cards hold therefore takes
// white or a bright color: bright white for text, white for dim text and the
// product's color, and the bright ANSI colors for the other colored text.
var (
	BackgroundChoices = backgroundChoices(DefaultTheme())
	TextChoices       = textChoices(DefaultTheme())
)

// boxBg is UI.BoxBg, the background of the input and of the cards that
// share its look.
var boxBg = style.ColorFromHex("#232323")

// backgroundChoices returns BackgroundChoices for the theme t.
func backgroundChoices(t *style.Theme) []ColorChoice {
	return []ColorChoice{
		// The canvas.
		{t.Bg, style.ANSIBlack, 234}, // Bg, Surface, CodeBg, inline code

		// Cards.
		{boxBg, style.ANSIBrightBlack, 235},           // the input, user messages, the queue, the session list, the banner
		{t.SurfaceRaised, style.ANSIBrightBlack, 234}, // notices, tool boxes: SurfaceRaised, SuccessSurface
		{t.SurfaceAlt, style.ANSIBrightBlack, 235},    // SurfaceAlt
		{t.WarningSurface, style.ANSIBrightBlack, 235},
		{t.DangerSurface, style.ANSIBrightBlack, 235},
		{t.InfoSurface, style.ANSIBrightBlack, 235},

		// Selections and fills.
		{productFg, style.ANSIWhite, 202},             // the bar of a selected row, selected input text
		{t.SelectionBg, style.ANSIBlue, 24},           // SelectionBg, BorderFocus
		{t.SelectionInactiveBg, style.ANSIBlack, 236}, // SelectionInactiveBg
		{t.DiffAddSurface, style.ANSIGreen, 239},      // DiffAddSurface
		{t.DiffRemoveSurface, style.ANSIRed, 237},     // DiffRemoveSurface
	}
}

// textChoices returns TextChoices for the theme t.
func textChoices(t *style.Theme) []ColorChoice {
	gray := func(v uint8) style.Color { return style.ColorFromInts(v, v, v, 255) }
	return []ColorChoice{
		// Text.
		{t.TextBase, style.ANSIBrightWhite, 254},       // TextBase, TextStrong, SelectionFg, CodeFg, markdown text, the shimmer, code attributes, objects, numbers, types, constants, and properties
		{logoFg, style.ANSIBrightWhite, 254},           // the logo after its "f"
		{gray(220), style.ANSIBrightWhite, 253},        // paths in the diff toast
		{style.ColorWhite, style.ANSIBrightWhite, 231}, // OnAccent, OnInteractive and the other On roles
		{panelTextFg, style.ANSIWhite, 249},            // session tree rows, footer keys
		{boxBg, style.ANSIBlack, 235},                  // text on the bar of a selected row, selected input text

		// Dim text, which cards hold too.
		{t.TextMuted, style.ANSIWhite, 245}, // TextMuted, Interactive, Info, comments, quotes, H5
		{gray(140), style.ANSIWhite, 245},   // the labels of /status and the diff toast, tool rows and reasoning (ToolFg)
		{PanelDimFg, style.ANSIWhite, 242},  // panel counts, menu descriptions, hints
		{gray(92), style.ANSIWhite, 59},     // the banner's text, the session list's no-match notice
		{gray(80), style.ANSIWhite, 239},    // the words between keys in the status hints

		// Dim text and lines on the canvas only.
		{indicatorFg, style.ANSIBrightBlack, 243},   // the indicator
		{t.BorderWeak, style.ANSIBrightBlack, 236},  // BorderWeak, markdown rules
		{t.TextWeak, style.ANSIBrightBlack, 236},    // TextWeak
		{t.Border, style.ANSIBrightBlack, 237},      // Border, quote borders
		{t.BorderStrong, style.ANSIBrightBlack, 59}, // BorderStrong

		// Oranges.
		{t.Accent, style.ANSIYellow, 202},       // Accent, TextLink, Warning, H1, H3, H4, H6, links, bold, bullets, keywords
		{t.AccentStrong, style.ANSIYellow, 172}, // AccentStrong, H2
		{productFg, style.ANSIWhite, 202},       // the product name, the logo's "f", selected rows

		// Reds.
		{t.Danger, style.ANSIBrightRed, 203},  // Danger, DiffRemoveFg, code errors
		{DiffDelFg, style.ANSIBrightRed, 167}, // deleted-line counts

		// Greens.
		{t.Success, style.ANSIBrightGreen, 84},     // Success, DiffAddFg, strings, regular expressions, inline code
		{DiffAddFg, style.ANSIBrightGreen, 78},     // added-line counts
		{ReasoningCodeFg(t), style.ANSIGreen, 101}, // inline code in reasoning, as dim as its text

		// Blues.
		{t.InteractiveStrong, style.ANSIBrightBlue, 68}, // InteractiveStrong, link labels

		// Yellows.
		{t.Code.Function.Fg, style.ANSIBrightYellow, 214}, // functions, tags, decorators, preprocessor directives
	}
}

// ColorIndexers returns the Indexers that convert colors to palette entries
// in mode: the entries TextChoices and BackgroundChoices pick, their ANSI16
// entries in style.Color16 and their Xterm256 entries in style.Color256, with
// mode's own Indexer, style.Nearest16 or style.Nearest256, as the fallback
// for the colors they do not list. In other modes it returns the zero
// Indexers, which convert colors as the mode does by default.
func ColorIndexers(mode style.ColorMode) style.Indexers {
	var entry func(ColorChoice) style.ANSI
	switch mode {
	case style.Color16:
		entry = func(c ColorChoice) style.ANSI { return c.ANSI16 }
	case style.Color256:
		entry = func(c ColorChoice) style.ANSI { return c.Xterm256 }
	default:
		return style.Indexers{}
	}
	indexMap := func(choices []ColorChoice) style.IndexMap {
		m := make(map[style.Color]style.ANSI, len(choices))
		for _, c := range choices {
			m[c.Color] = entry(c)
		}
		return style.IndexMap{
			Map:      m,
			Fallback: mode.Indexer(),
		}
	}
	return style.Indexers{Fg: indexMap(TextChoices), Bg: indexMap(BackgroundChoices)}
}
