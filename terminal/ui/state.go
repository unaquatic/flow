package ui

import (
	"slices"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
)

// ChatMessage contains the data needed to render one chat message.
type ChatMessage struct {
	// ID identifies the message.
	ID message.ID
	// ParentID identifies the first message in a shared bubble.
	ParentID message.ID
	// Role identifies the message author.
	Role message.Role
	// Parts contains the message content in display order.
	Parts []message.ContentPart
	// Live holds the IDs of the parts that have not taken their final form
	// yet: those a part event started and none ended, such as text that
	// still streams or a tool call that still runs. A message that ended,
	// or that a session loaded, has none.
	Live []message.PartID
	// Prev holds the parts of the message before this one in the
	// transcript, which the views of an extension may show this one
	// against, as a reply's changes are those from the workspace the message
	// before it ended with. It is nil for the first message.
	Prev []message.ContentPart
	// Error is an optional failure rendered with the message.
	Error error
	// Provider is the provider of the model variant that produced the
	// message, or that a prompt was sent to, as [message.Content.Provider]
	// names it.
	Provider agent.ProviderID
	// Model is the model variant that produced the message, or that a
	// prompt was sent to, without its provider, in "model:option:effort"
	// form, as [message.Content.Model] names it. Its variant prices the
	// usage.
	Model string
	// Usage contains the token counts of the message's request, which its
	// usage part records. It stays zero while the message streams, though
	// the part arrives before its tools run, so that the counts show once the
	// message ends.
	Usage message.Usage
	// CreatedAt is when the message was stored, or zero when it is not
	// known, as for a message still streaming.
	CreatedAt time.Time
	// Worked is how long the turn that this reply ends took, from its prompt
	// to the end of the reply. It is zero for a message that does not end a
	// turn, and for a turn whose time is not known.
	Worked time.Duration
	// Window is the most tokens a prompt of the model that produced this
	// reply holds, as the catalog lists it, which its footer shows its
	// context as a share of. It is zero for a reply without usage, and when
	// the catalog does not list the model.
	Window int
	// Notice, when set, makes the entry a notice of the transcript, as
	// UI.NewNoticeRow shows it, rather than a message: it has no role, parts,
	// or usage, and takes no part in the conversation, as LastMessage skips
	// it.
	Notice *Notice
}

// Notice is a notice of the transcript, such as the session info under the
// logo or the files a history move changed, as UI.NewNoticeRow shows it. The
// transcript holds it among its messages, in a ChatMessage of its own, and
// unlike a message it never changes once shown.
type Notice struct {
	// Text is an unstyled single-line notice.
	Text string
	// Lines are styled rows, which take precedence over Text.
	Lines []style.StyledText
	// Surface is what the notice shows on.
	Surface NoticeSurface
	// Title, when set, heads the notice while prompts show in PromptBar,
	// as UI.NewNoticeBarRow shows it, such as "Status" over the session
	// info. Without one, the notice's first line heads it.
	Title string
}

// NoticeSurface is what a notice shows on.
type NoticeSurface int

const (
	// NoticeRaised shows a notice in a box on the raised surface. It is the
	// default.
	NoticeRaised NoticeSurface = iota
	// NoticeSystem shows a notice in a box on the system surface, as the
	// session info is.
	NoticeSystem
	// NoticeCanvas shows a notice's rows on the canvas, from the transcript's
	// left edge, with no box around them, as the logo is.
	NoticeCanvas
)

// HasParent reports whether the message belongs to another message's bubble.
func (m ChatMessage) HasParent() bool {
	return m.ParentID != "" && m.ParentID != m.ID
}

// Variant returns the model variant that produced the message, or that a
// prompt was sent to, as agent.ParseVariantAt reads it against cat. It is
// the zero Variant when the model is not known.
func (m ChatMessage) Variant(cat *gateway.Catalog) agent.Variant {
	return agent.ParseVariantAt(m.Provider, m.Model, cat)
}

// AssignPrev records on each message in msgs the parts of the one before
// it, as Prev describes them.
func AssignPrev(msgs []ChatMessage) {
	for i := range msgs {
		msgs[i].Prev = nil
		if i > 0 {
			msgs[i].Prev = slices.Clone(msgs[i-1].Parts)
		}
	}
}

// AssignParents groups consecutive messages with the same role. The first
// message in each group uses its own ID as ParentID.
func AssignParents(msgs []ChatMessage) {
	for i := range msgs {
		if i > 0 && msgs[i].Role == msgs[i-1].Role {
			msgs[i].ParentID = msgs[i-1].ParentID
		} else {
			msgs[i].ParentID = msgs[i].ID
		}
	}
}

// AssignWorked records on the last reply of each turn in msgs, a prompt and
// the replies that follow it, how long the turn took: from when the prompt
// was stored to when the reply was. A turn whose prompt
// or last reply has no creation time records nothing. A run that continues a
// turn without a new prompt, as /continue does, adds to the turn's time.
func AssignWorked(msgs []ChatMessage) {
	var prompt time.Time
	for i := range msgs {
		m := &msgs[i]
		if m.Role == message.RoleUser {
			prompt = m.CreatedAt
			continue
		}
		last := i+1 == len(msgs) || msgs[i+1].Role != message.RoleAgent
		if m.Role == message.RoleAgent && last && !prompt.IsZero() && !m.CreatedAt.IsZero() {
			m.Worked = m.CreatedAt.Sub(prompt)
		}
	}
}

// Banner is the kind of modal banner that opens at the top of the input box.
type Banner int

const (
	// NoBanner means no banner is open.
	NoBanner Banner = iota
	// MenuBanner shows a SearchMenu.
	MenuBanner
	// ModelBanner shows that the model list is loading.
	ModelBanner
	// LoginBanner shows the progress of a login.
	LoginBanner
	// GraphemeBanner shows the grapheme width report.
	GraphemeBanner
	// TreeBanner shows the session tree.
	TreeBanner
)

// CacheMode is where replies show the cache use of their requests, as
// CacheText gives it, which /settings sets.
type CacheMode int

const (
	// CacheOff shows no cache use. It is the default.
	CacheOff CacheMode = iota
	// CachePerTurn shows the cache use of a turn's last request in the
	// footer under the turn, after the turn's time.
	CachePerTurn
	// CachePerMessage shows the cache use of each reply in a row of its own
	// under the reply, as CacheBox draws it.
	CachePerMessage
)

// CacheModes lists the cache modes in the order /settings offers them.
var CacheModes = [...]CacheMode{CacheOff, CachePerTurn, CachePerMessage}

// Next returns the mode step places after m in CacheModes, or before it for
// a negative step, going round from the last to the first and back.
func (m CacheMode) Next(step int) CacheMode {
	return Cycle(CacheModes[:], m, step)
}

// Cycle returns the value step places after v in values, or before it for a
// negative step, going round from the last to the first and back, as a
// setting's value moves in /settings.
func Cycle[T comparable](values []T, v T, step int) T {
	n := len(values)
	i := slices.Index(values, v)
	return values[((i+step)%n+n)%n]
}

// String returns the name /settings shows for m, such as "Per turn".
func (m CacheMode) String() string {
	switch m {
	case CachePerTurn:
		return "Per turn"
	case CachePerMessage:
		return "Per message"
	default:
		return "Off"
	}
}

// ExpandedStyle is how the transcript shows an expanded tool call, which
// /settings sets. A collapsed call shows as its header alone, one row on the
// canvas, in every style, and the rows of consecutive collapsed calls stack
// with no blank row between them.
type ExpandedStyle int

const (
	// ExpandedPlain shows an expanded call on the canvas, with no
	// background and no padding. It is the default.
	ExpandedPlain ExpandedStyle = iota
	// ExpandedBoxed shows an expanded call as a block on the success
	// surface, its content between two rows of padding.
	ExpandedBoxed
)

// ExpandedStyles lists the expanded styles in the order /settings offers
// them.
var ExpandedStyles = [...]ExpandedStyle{ExpandedPlain, ExpandedBoxed}

// Next returns the style step places after s in ExpandedStyles, going round.
func (s ExpandedStyle) Next(step int) ExpandedStyle {
	return Cycle(ExpandedStyles[:], s, step)
}

// String returns the name /settings shows for s: "Plain" or "Boxed".
func (s ExpandedStyle) String() string {
	if s == ExpandedBoxed {
		return "Boxed"
	}
	return "Plain"
}

// ToolNameColor is the color of the marker and the name of each tool call,
// which /settings sets.
type ToolNameColor int

const (
	// ToolNameGray shows the marker and the name in ToolFg, as the
	// input after the name is. It is the default.
	ToolNameGray ToolNameColor = iota
	// ToolNameWhite shows the marker and the name in titleFg, as a panel
	// shows its title.
	ToolNameWhite
)

// ToolNameColors lists the tool name colors in the order /settings offers
// them.
var ToolNameColors = [...]ToolNameColor{ToolNameGray, ToolNameWhite}

// Next returns the color step places after c in ToolNameColors, going round.
func (c ToolNameColor) Next(step int) ToolNameColor {
	return Cycle(ToolNameColors[:], c, step)
}

// String returns the name /settings shows for c: "Gray" or "White".
func (c ToolNameColor) String() string {
	if c == ToolNameWhite {
		return "White"
	}
	return "Gray"
}

// MarkerShape is the symbol of the marker before each tool call's name,
// ToolMarker, which /settings sets.
type MarkerShape int

const (
	// MarkerSquare is a filled square, ■. It is the default.
	MarkerSquare MarkerShape = iota
	// MarkerSquareOutline is an unfilled square, □.
	MarkerSquareOutline
	// MarkerRoundedOutline is an unfilled square with rounded corners, ▢.
	MarkerRoundedOutline
	// MarkerCircle is a filled circle, ●.
	MarkerCircle
	// MarkerCircleOutline is an unfilled circle, ○.
	MarkerCircleOutline
	// MarkerDiamond is a filled diamond, ◆.
	MarkerDiamond
	// MarkerDiamondOutline is an unfilled diamond, ◇.
	MarkerDiamondOutline
	// MarkerBlock is a space filled with the marker's color, a solid cell.
	MarkerBlock
)

// MarkerShapes lists the marker shapes in the order /settings offers them.
var MarkerShapes = [...]MarkerShape{
	MarkerSquare, MarkerSquareOutline, MarkerRoundedOutline, MarkerCircle,
	MarkerCircleOutline, MarkerDiamond, MarkerDiamondOutline, MarkerBlock,
}

// markerShapeInfo holds the symbol and the name of each MarkerShape.
var markerShapeInfo = [...]struct{ glyph, name string }{
	MarkerSquare:         {"■", "Square"},
	MarkerSquareOutline:  {"□", "Square outline"},
	MarkerRoundedOutline: {"▢", "Rounded outline"},
	MarkerCircle:         {"●", "Circle"},
	MarkerCircleOutline:  {"○", "Circle outline"},
	MarkerDiamond:        {"◆", "Diamond"},
	MarkerDiamondOutline: {"◇", "Diamond outline"},
	MarkerBlock:          {" ", "Block"},
}

// Next returns the shape step places after m in MarkerShapes, going round.
func (m MarkerShape) Next(step int) MarkerShape {
	return Cycle(MarkerShapes[:], m, step)
}

// Glyph returns the symbol m draws: a space for MarkerBlock, which the
// marker's color fills.
func (m MarkerShape) Glyph() string { return markerShapeInfo[m].glyph }

// Filled reports whether m is a space the marker's color fills, rather than
// a symbol drawn in it.
func (m MarkerShape) Filled() bool { return m == MarkerBlock }

// String returns the name /settings shows for m, after its symbol, such as
// "■ Square", or "Block" for MarkerBlock.
func (m MarkerShape) String() string {
	if m.Filled() {
		return markerShapeInfo[m].name
	}
	return markerShapeInfo[m].glyph + " " + markerShapeInfo[m].name
}

// Fg returns the color c names: ToolFg or titleFg.
func (c ToolNameColor) Fg() style.Color {
	if c == ToolNameWhite {
		return titleFg
	}
	return ToolFg
}

// KeyHintsMode is where the panels in the input box name their keys, which
// /settings sets.
type KeyHintsMode int

const (
	// KeyHintsTitle names each panel's keys after its title and count, as
	// TitleHints gives them, such as "Commands [↑/↓ move] [esc cancel]". It is
	// the default.
	KeyHintsTitle KeyHintsMode = iota
	// KeyHintsSubtitle names each panel's keys on a row under its title, as
	// the footer names them, such as "esc cancel  ↑/↓ move  tab complete".
	KeyHintsSubtitle
	// KeyHintsFooter names the open panel's keys in the footer under the
	// input box, such as "esc cancel  ↑/↓ move  tab complete".
	KeyHintsFooter
)

// KeyHintsModes lists the key hint modes in the order /settings offers them.
var KeyHintsModes = [...]KeyHintsMode{KeyHintsTitle, KeyHintsSubtitle, KeyHintsFooter}

// Next returns the mode step places after m in KeyHintsModes, going round.
func (m KeyHintsMode) Next(step int) KeyHintsMode {
	return Cycle(KeyHintsModes[:], m, step)
}

// String returns the name /settings shows for m: "Title", "Subtitle", or
// "Footer".
func (m KeyHintsMode) String() string {
	switch m {
	case KeyHintsSubtitle:
		return "Subtitle"
	case KeyHintsFooter:
		return "Footer"
	default:
		return "Title"
	}
}

// CountPlace is where a list whose rows do not all fit counts them, as
// TitleCount gives the count, such as "[3/20]", which /settings sets. The
// slash menu counts no rows in either place.
type CountPlace int

const (
	// CountBelow counts the rows on a line under them. It is the default.
	CountBelow CountPlace = iota
	// CountInTitle counts the rows after the list's title, such as
	// "Models [3/15]".
	CountInTitle
)

// CountPlaces lists the count places in the order /settings offers them.
var CountPlaces = [...]CountPlace{CountBelow, CountInTitle}

// Next returns the place step places after p in CountPlaces, going round.
func (p CountPlace) Next(step int) CountPlace {
	return Cycle(CountPlaces[:], p, step)
}

// String returns the name /settings shows for p: "Below" or "Title".
func (p CountPlace) String() string {
	if p == CountInTitle {
		return "Title"
	}
	return "Below"
}

// PromptStyle is how the transcript marks a prompt, which /settings sets.
type PromptStyle int

const (
	// PromptBar shows a prompt on the canvas, with the marker shape in the
	// product's primary color at the left edge of each of its rows, or for
	// the block shape a cell filled with it. It is the default.
	PromptBar PromptStyle = iota
	// PromptFilled shows a prompt as a block on the box background, padded
	// by a row above and below.
	PromptFilled
)

// PromptStyles lists the prompt styles in the order /settings offers them.
var PromptStyles = [...]PromptStyle{PromptBar, PromptFilled}

// Next returns the style step places after s in PromptStyles, going round.
func (s PromptStyle) Next(step int) PromptStyle {
	return Cycle(PromptStyles[:], s, step)
}

// String returns the name /settings shows for s: "Bar" or "Filled".
func (s PromptStyle) String() string {
	if s == PromptFilled {
		return "Filled"
	}
	return "Bar"
}

// InputStyle is how the input box, and the panels that open in it, such as
// the slash menu and /settings, show, which /settings sets.
type InputStyle int

const (
	// InputBar shows the input box and its panels on the canvas, as a tool
	// call's row is, with the marker shape at the left edge of the input's
	// first row, in the primary color, and of each panel's title, in the
	// color of the title. It is the default.
	InputBar InputStyle = iota
	// InputFilled shows the input box and its panels on the box background.
	InputFilled
)

// InputStyles lists the input styles in the order /settings offers them.
var InputStyles = [...]InputStyle{InputBar, InputFilled}

// Next returns the style step places after s in InputStyles, going round.
func (s InputStyle) Next(step int) InputStyle {
	return Cycle(InputStyles[:], s, step)
}

// String returns the name /settings shows for s: "Bar" or "Filled".
func (s InputStyle) String() string {
	if s == InputBar {
		return "Bar"
	}
	return "Filled"
}

// SelectionStyle is how the lists in the input box, the slash menu, the
// session list, a banner's menu, and the session tree, mark their selected
// row, which /settings sets.
type SelectionStyle int

const (
	// SelectionFullWidth fills the selected row in activeFg from one edge of
	// the box to the other, its border cells included, under the row's text
	// in the box's fill, with no arrow. It is the default.
	SelectionFullWidth SelectionStyle = iota
	// SelectionFill fills the selected row as SelectionFullWidth does, but a
	// cell in from either edge of the box.
	SelectionFill
	// SelectionArrow marks the selected row with an arrow in the box's left
	// border cell, and draws the row's text and the arrow in activeFg.
	SelectionArrow
)

// SelectionStyles lists the selection styles in the order /settings offers
// them.
var SelectionStyles = [...]SelectionStyle{SelectionFullWidth, SelectionFill, SelectionArrow}

// Next returns the style step places after s in SelectionStyles, going round.
func (s SelectionStyle) Next(step int) SelectionStyle {
	return Cycle(SelectionStyles[:], s, step)
}

// String returns the name /settings shows for s: "Full width", "Fill", or
// "Arrow".
func (s SelectionStyle) String() string {
	switch s {
	case SelectionFill:
		return "Fill"
	case SelectionArrow:
		return "Arrow"
	default:
		return "Full width"
	}
}

// FillInset returns how many cells in from either edge of the box the
// selected row's fill starts, and false for a style that marks the row with
// an arrow instead.
func (s SelectionStyle) FillInset() (int, bool) {
	switch s {
	case SelectionFill:
		return 1, true
	case SelectionArrow:
		return 0, false
	default:
		return 0, true
	}
}

// UIState contains the terminal state read and updated by Service.
type UIState struct {
	// Messages contains the active transcript: its messages, and the notices
	// shown among them, as ChatMessage.Notice marks them.
	Messages []ChatMessage
	// Session is the active persisted session.
	Session *session.Session
	// LastCtrlCTime records the idle Ctrl+C press that last armed the quit,
	// which a second press within QuitWindow carries out.
	LastCtrlCTime time.Time

	// SlashMenuVisible reports whether the slash-command menu is open.
	SlashMenuVisible bool
	// SessionMenuVisible reports whether the session list is open.
	SessionMenuVisible bool
	// SessionChoices are the sessions the open session list searches, in
	// the order the store lists them.
	SessionChoices []session.Session
	// Banner is the kind of banner open at the top of the input box.
	Banner Banner
	// Menu is the menu the banner shows while Banner is MenuBanner, and nil
	// otherwise.
	Menu *SearchMenu
	// Tree is the session tree the banner shows while Banner is TreeBanner,
	// and nil otherwise.
	Tree *SessionTree
	// MenuRows is the most rows a list in the input box shows, such as the
	// slash menu or the session tree, so that the box stays within the
	// terminal's height. Zero leaves each list its own maximum.
	MenuRows int
	// ProbedWidths contains measured widths for GraphemeProbeSamples.
	ProbedWidths []int
	// ProbeErr reports why terminal width measurement failed.
	ProbeErr error
	// Inline reports whether the UI renders inline instead of on the
	// alternate screen.
	Inline bool
	// Prompt is the open question that the next submitted input answers, or
	// nil when the input goes to the agent.
	Prompt *TextPrompt
	// Login holds temporary OAuth proof while the login input is open. It is
	// not a setting or a transcript message, and closing the banner discards it.
	Login *app.EventLoginPrompt
	// LoginCompleting prevents a second submission during the token exchange.
	LoginCompleting bool

	// Running reports whether a run the terminal started on the active
	// session has not ended yet: it is set when the run starts, and cleared
	// once the terminal applies the run's EventAgentEnded. The app frees the
	// session just before it publishes that event, so asking the app instead
	// would let a prompt start while the old run's end is still on its way.
	Running bool
	// Queue holds the submitted prompts not yet sent, in the order they are
	// sent, one each time the session becomes free.
	Queue []string
	// QueuePaused stops the queue after a prompt that failed or could not
	// start, until Enter on an empty input or Alt+Enter resumes it.
	QueuePaused bool
	// MovesPending counts the history moves in progress, which hold the
	// queue until they finish.
	MovesPending int
	// TurnStart is when the turn in progress began: when its run started,
	// or when the prompt that opened it did. It is zero while no turn of the
	// active session is in progress.
	TurnStart time.Time
	// TurnFrom is the index in Messages of the first message of the turn in
	// progress.
	TurnFrom int
	// Working reports whether the indicator shows how long the turn in
	// progress has taken, which Service.TickIndicator keeps current.
	Working bool

	// StatusContent is the text displayed in the status line.
	StatusContent style.StyledText
	// Model is the model variant the terminal sends its prompts to, in
	// "provider/model:option:effort" format, at the level it runs at. The
	// model menu and its effort page change it for this terminal alone.
	Model string
	// ReasoningEffort is the reasoning effort chosen for the terminal, or
	// empty for the default effort of the model's provider. Model runs at it
	// where the catalog lists it for the model, and a model switch keeps it
	// for the next model.
	ReasoningEffort agent.ReasoningEffort
	// KeepModel keeps Model when the first session opens, as when the
	// command line chose it, rather than taking the model the session's
	// transcript last ran.
	KeepModel bool
	// ModelLabel identifies the active model, which the slash menu's status
	// row names.
	ModelLabel string
	// Usage is what the slash menu's status row reports of the transcript's
	// token use.
	Usage SessionUsage
	// ToolsExpanded reports whether tool calls and the changes of each step
	// show their details, which Ctrl+O toggles. They start collapsed.
	ToolsExpanded bool
	// ExpandStyle is how expanded tool calls show, which /settings sets. It
	// is ExpandedPlain by default.
	ExpandStyle ExpandedStyle
	// HideToolMarkers reports that tool calls show no ToolMarker before
	// their name, which /settings sets. They show it by default.
	HideToolMarkers bool
	// MarkerShape is the symbol of each tool call's marker, which /settings
	// sets. It is MarkerSquare by default.
	MarkerShape MarkerShape
	// ToolColor is the color of each tool call's marker and name, which
	// /settings sets. It is ToolNameGray by default.
	ToolColor ToolNameColor
	// Debug shows what helps debug flow, which /settings sets, such as the
	// snapshot each prompt and reply ends with, by its hash, once taken. It
	// is off by default.
	Debug bool
	// CacheMode is where replies show the cache use of their requests, which
	// /settings sets. It is CacheOff by default.
	CacheMode CacheMode
	// KeyHints is where the panels name their keys, which /settings sets. It
	// is KeyHintsTitle by default.
	KeyHints KeyHintsMode
	// UsagePinned reports that the Usage box shows above the input box while
	// the slash menu is closed too, which Ctrl+U toggles.
	UsagePinned bool
	// Count is where a list whose rows do not all fit counts them, which
	// /settings sets. It is CountBelow by default.
	Count CountPlace
	// Selection is how the menus mark their selected row, which /settings
	// sets. It is SelectionFullWidth by default.
	Selection SelectionStyle
	// PromptStyle is how the transcript marks a prompt, which /settings
	// sets. It is PromptBar by default.
	PromptStyle PromptStyle
	// InputStyle is how the input box and its panels show, which /settings
	// sets. It is InputBar by default.
	InputStyle InputStyle
}

// StatusHintStyled returns the default keyboard hint.
func StatusHintStyled() style.StyledText {
	key := style.New().WithFg(textFg).Func()
	dim := style.New().WithFg(style.ColorFromInts(80, 80, 80, 255)).Func()
	return style.NewStyledText(key("slash"), dim(" commands "), key("esc"), dim(" interrupt"))
}
