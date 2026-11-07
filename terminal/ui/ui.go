// Package ui is flow's interactive terminal UI: the widget tree that the
// renderer draws, the controller that turns the app's events and the keys
// into UI events, and the golden frames that test how they draw. The
// terminal package runs it with Run.
package ui

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// UI owns the terminal renderer and its widget tree.
type UI struct {
	renderer *terminal.Renderer
	service  *Service

	// Widgets resolve colors from theme during construction.
	theme *style.Theme

	messageBox        *widget.ScrollBox
	previewMessageBox *widget.ScrollBox
	input             *widget.Textarea
	statusBar         *widget.Text
	inputFooter       *widget.Box   // the row right under the input box
	footerHints       *KeyHintsView // the open panel's keys, in the footer while KeyHintsMode is KeyHintsFooter
	slashMenu         *widget.Select
	slashMenuRow      *widget.Box  // slashMenu with its header and dividers, at the top of the input box
	slashStatus       *widget.Text // the status line shown while the slash menu is open, as StatusLine gives it
	slashStatusBox    *widget.Box  // slashStatus in a box of its own above the input box, with a gap under it
	statusHeader      *PanelHeader // the Usage box's title, with the key that pins it
	statusBounded     bool         // reports that a short terminal leaves slashStatusBox no room
	statusModel       string       // the model the status row names, in "provider/model:option:effort" format
	statusUsage       SessionUsage // the token use the status row shows
	slashHeader       *PanelHeader // the slash menu's title, which counts no rows, unlike other lists' titles
	inputMiddleCol    *widget.Box  // middleCol of input StreamableMessageBox
	inputContentRow   *widget.Box  // input content row used as the queue insert anchor
	queueBox          *widget.Box  // a box for each queued prompt, above the input box
	queuePrompts      []string     // the queued prompts, the first sent next
	queueLimit        int          // the most queued prompts shown, or zero for all
	sessionMenu       *widget.Select
	sessionPanel      *widget.Box // session list with its header and dividers, at the top of the input box
	sessionText       *widget.Text
	sessionTextRow    *widget.Box
	sessionHeader     *PanelHeader   // the session list's title, with the selected row's place after it while its rows do not all fit
	sessionCount      *PanelStatus   // under the session list while CountPlace is CountBelow and its rows do not all fit
	sessionPrompts    *PromptPreview // the selected session's prompts, under the session list in inline mode
	bannerPanel       *widget.Box    // banner rows and their dividers, at the top of the input box
	bannerHeader      *PanelHeader   // the banner's title, with the selected row's place after it while its menu's rows do not all fit
	bannerCount       *PanelStatus   // under the banner menu while CountPlace is CountBelow and its rows do not all fit
	bannerText        *widget.Text
	bannerTextRow     *widget.Box
	bannerDividerRow  *widget.Box // between the header and the text and menu, shown when both are
	bannerTailRow     *widget.Box // under the banner, shown when a row of the input box follows
	bannerMenu        *widget.Select
	bannerSearch      bool         // the input searches the open banner's menu
	bannerHints       []KeyHint    // the open banner's keys
	treePanel         *widget.Box  // session tree rows with their header and status, at the top of the input box
	treeHeader        *PanelHeader // the session tree's title, with the selected row's place after it while its rows do not all fit
	treeRows          *widget.Text
	treeRowsBox       *widget.Box  // around treeRows, across the box's whole row, where FillTreeSelection fills the selected row
	treeSelected      int          // the line of treeRows that holds the selected row, or -1 when there is none
	treeStatus        *PanelStatus // under the session tree's rows: its filter, or that nothing matches
	inputWrapper      *widget.Box
	indicatorBox      *widget.Box  // progress or error line between the transcript and the input
	indicatorLabel    *widget.Text // text inside indicatorBox

	// messageWidgets indexes live transcript bubbles by message ID.
	messageWidgets map[message.ID]*ChatMessageBox

	// inline reports whether the transcript renders into terminal scrollback.
	inline bool
	// transcriptRows are the bubbles and notices in display order. Inline mode
	// parents them to the root, which owns unrelated children too, so the
	// transcript tracks its own rows.
	transcriptRows []*widget.Box
	// committed reports that inline rows of the transcript were committed to
	// the terminal since the transcript was last built, as
	// CommitScrolledRows commits them.
	committed bool
	// trimmed reports that the transcript was built without the rows of the
	// earliest messages, which the terminal never showed, as TrimTranscript
	// leaves them out.
	trimmed bool

	previewWidgets map[message.ID]*ChatMessageBox

	// extensions add the widgets of what extensions add to messages.
	extensions []Extension

	// appEvents moves the app's events onto the event loop.
	appEvents *AppEvents

	// fitHeight is the terminal height the input box was last fitted to.
	fitHeight int
	// liveReserve is the rows FitInputBox last kept for the live rows of
	// the transcript, as ReserveLiveRows keeps them.
	liveReserve int

	// shimmer is where the band that sweeps across the time of a run in
	// progress is, as DrawShimmer draws it, which the rows of the tool calls
	// the run executes share.
	shimmer Shimmer

	// resumeShown reports that the final frame ends with the notice that
	// tells how to resume the session. Run reads it once the event loop may
	// still be running.
	resumeShown atomic.Bool
}

// AppEvents moves the app's events onto the event loop in the order the app
// published them, and runs functions there behind the events published before
// them.
type AppEvents struct {
	// Events receives the app's events. Run is its only reader.
	Events <-chan app.Event
	// Apply applies one event. It runs on the event loop.
	Apply func(app.Event)
	// Enqueue runs a function on the event loop, after those enqueued
	// before it.
	Enqueue func(func())
	// Done ends Run, and makes After drop its function once Run has ended.
	Done <-chan struct{}
	// Later takes the functions After runs.
	Later chan func()
}

// NewAppEvents returns an AppEvents that applies the events it receives from
// events with apply, on the event loop that enqueue runs functions on, until
// done is closed.
func NewAppEvents(events <-chan app.Event, apply func(app.Event), enqueue func(func()), done <-chan struct{}) *AppEvents {
	return &AppEvents{Events: events, Apply: apply, Enqueue: enqueue, Done: done, Later: make(chan func())}
}

// Run enqueues each event as it arrives, until Done is closed. A function
// After takes goes behind the events still buffered when it arrives, which
// include every event published before After was called: the app's broker
// returns from publishing an event once the event is buffered.
func (q *AppEvents) Run() {
	forward := func(ev app.Event) { q.Enqueue(func() { q.Apply(ev) }) }
	for {
		select {
		case <-q.Done:
			return
		case ev := <-q.Events:
			forward(ev)
		case fn := <-q.Later:
			for range len(q.Events) {
				forward(<-q.Events)
			}
			q.Enqueue(fn)
		}
	}
}

// After runs fn on the event loop once every event the app published before
// the call has been applied. It runs nothing once Run has ended.
func (q *AppEvents) After(fn func()) {
	select {
	case q.Later <- fn:
	case <-q.Done:
	}
}

// Options are how Run starts the terminal UI.
type Options struct {
	// First is the stored session the UI opens first, or "" for a new one.
	First message.SessionID
	// Model is the model variant the UI starts with, in
	// "provider/model:option:effort" format, which a stored first session
	// keeps. Empty takes the app's default model, or the model the first
	// session last ran.
	Model string
	// ColorMode is the color mode the UI draws in. Nil detects it from the
	// terminal.
	ColorMode *style.ColorMode
	// PickSession opens the session list over the first session.
	PickSession bool
	// Extensions add to the transcript the widgets of what the app's
	// extensions add to messages, in order.
	Extensions []Extension
}

// Result is how the terminal UI closed.
type Result struct {
	// Closed is the session the UI closed on.
	Closed message.SessionID
	// ResumeShown reports that the UI closed in inline mode on a stored
	// session, and that its last frame ends with the notice that tells how
	// to resume it.
	ResumeShown bool
	// ColorMode is the color mode the UI drew in.
	ColorMode style.ColorMode
}

// Run runs the interactive terminal UI of a, as opts start it, and blocks
// until it closes. Standard error goes to the application log meanwhile.
// The Result holds the session the UI closed on even when Run fails once
// the UI has started.
func Run(ctx context.Context, a *app.App, opts Options) (Result, error) {
	model := opts.Model
	if model == "" {
		var err error
		if model, err = a.Models().Default(); err != nil {
			return Result{}, err
		}
	}
	// Stray writes to standard error would draw over the UI.
	restore := a.CaptureStderr()
	defer restore()

	// Start with the settings /settings saved last.
	conf, err := a.Config()
	if err != nil {
		return Result{}, err
	}
	state := &UIState{Model: model, KeepModel: opts.Model != ""}
	state.ApplyConfig(conf.Terminal, conf.Debug)

	cfg := terminal.Config{
		Stdin:            os.Stdin,
		Stdout:           os.Stdout,
		UseMouse:         true,
		InlineMode:       state.Inline,
		InlineTrimBlanks: true,
		EnableAutoFocus:  true,
		HandleCtrlC:      true,
		DebugLog:         "logs/fluid.log",
	}

	// Width probing is opt-in because it delays renderer startup.
	if config.WidthProbe() {
		cfg.WidthProbe = terminal.WidthProbeAuto
		cfg.WidthProbeSamples = GraphemeProbeSamples()
	}

	r, err := terminal.NewRenderer(cfg)
	if err != nil {
		return Result{}, fmt.Errorf("creating renderer: %w", err)
	}
	if opts.ColorMode != nil {
		r.SetColorMode(*opts.ColorMode)
	}
	for _, mode := range []style.ColorMode{style.Color16, style.Color256} {
		r.SetIndexers(mode, ColorIndexers(mode))
	}
	theme := DefaultTheme()
	r.SetBackgroundColor(theme.Bg)

	ui := &UI{
		renderer:       r,
		theme:          theme,
		inline:         state.Inline,
		messageWidgets: make(map[message.ID]*ChatMessageBox),
		extensions:     opts.Extensions,
	}

	// Service returns events; UI applies them to the widget tree.
	ui.service = NewService(ctx, a, state)
	ui.service.Commands = CommandsWith(opts.Extensions)

	ui.BuildUI()
	ui.Apply(ui.service.InitSession(ctx, opts.First))

	// Apply app events serially on the renderer's event loop.
	ui.appEvents = NewAppEvents(a.Subscribe(ctx), func(ev app.Event) { ui.Apply(ui.service.ApplyAgentEvent(ev)) }, r.Enqueue, ctx.Done())
	go ui.appEvents.Run()
	go ui.TickIndicator(r.Enqueue, ctx.Done())
	go ui.AnimateShimmer(r.Enqueue, ctx.Done())

	// Route parsed keyboard events through the UI before the focused textarea.
	r.EventBus().On(input.EventKeyboard, func(evt input.TerminalEvent) {
		if evt.Key == nil || evt.Key.Type == input.KeyRelease {
			return
		}
		ui.HandleKey(evt.Key)
	})

	if err := r.Auto(); err != nil {
		return Result{}, fmt.Errorf("starting renderer: %w", err)
	}

	// Width measurements become available after renderer setup.
	state.ProbedWidths, state.ProbeErr = r.ProbedWidths()

	r.FocusRenderable(ui.input)
	r.RequestRender()

	// The input searches the list, so the list opens on the event loop once
	// the input is focused.
	if opts.PickSession {
		r.Enqueue(func() { ui.Apply(ui.service.HandleSessionList()) })
	}

	err = r.Wait(ctx)
	// The event loop may still be handling a key that switches sessions.
	return Result{
		Closed:      ui.service.ActiveSessionID(),
		ResumeShown: ui.resumeShown.Load(),
		ColorMode:   r.ColorMode(),
	}, err
}

// Apply processes UI events in order. Once the renderer is running, callers
// must invoke Apply on its event-loop goroutine.
func (ui *UI) Apply(events []UIEvent) {
	for _, evt := range events {
		ui.ProcessEvent(evt)
	}
}

// Destroy tears the renderer down, ending Run.
func (ui *UI) Destroy() {
	ui.renderer.Destroy()
}

// SetStatus replaces the hint in the status line.
func (ui *UI) SetStatus(content style.StyledText) {
	ui.statusBar.SetStyledContent(content)
}

// SetModelLabel replaces the model the slash menu's status row names, which
// label names in "provider/model:option:effort" format.
func (ui *UI) SetModelLabel(label string) {
	ui.statusModel = label
	ui.SyncSlashStatus()
}

// SetUsage replaces the token use the slash menu's status row shows.
func (ui *UI) SetUsage(u SessionUsage) {
	ui.statusUsage = u
	ui.SyncSlashStatus()
}

// BorderFg returns the foreground for border lines.
func (ui *UI) BorderFg() style.Color { return ui.theme.BorderStrong }

// BoxBg returns the solid fill of the input box, menus, and non-agent
// messages.
func (ui *UI) BoxBg() style.Color { return boxBg }

// SystemBg returns the fill of system notices, such as a mode switch and
// /status: the grey of other notices.
func (ui *UI) SystemBg() style.Color { return ui.theme.SurfaceRaised }

// BuildUI constructs the widget tree.
func (ui *UI) BuildUI() {
	root := ui.renderer.Root()

	root.LayoutNode().PaddingLeft = layout.Points(0)
	root.LayoutNode().PaddingRight = layout.Points(0)

	ui.messageBox = widget.NewScrollBox(
		widget.ScrollBoxWithID("messages"),
		widget.ScrollBoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithFlexGrow(1),
		),
		widget.ScrollBoxWithAutoScroll(widget.AutoScrollBottom),
		widget.ScrollBoxWithAnchorScroll(true),
		// A slow turn of the wheel moves one row per event, and a fast one
		// up to eight.
		widget.ScrollBoxWithScrollAccel(&widget.ScrollAccel{
			Window: 50 * time.Millisecond,
			Step:   0.5,
			Max:    8,
		}),
	)
	// Inline mode parents transcript rows to the root, leaving this unused.
	ui.messageBox.SetGone(ui.inline)

	ui.previewMessageBox = widget.NewScrollBox(
		widget.ScrollBoxWithID("preview-messages"),
		widget.ScrollBoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithFlexGrow(1),
		),
		widget.ScrollBoxWithAutoScroll(widget.AutoScrollBottom),
	)
	ui.previewMessageBox.SetGone(true)

	ui.input = widget.NewTextarea(
		widget.TextareaWithID("chat-input"),
		widget.TextareaWithFg(textFg),
		// Selected text, as shift and the arrows select it, is the input
		// box's fill on activeFg.
		widget.TextareaWithSelectionBg(activeFg),
		widget.TextareaWithSelectionFg(ui.BoxBg()),
		widget.TextareaWithLayout(layout.WithFlexGrow(1), layout.WithMinHeight(layout.Points(2))),
		widget.TextareaWithWrapMode(textbuf.WrapWord),
		widget.TextareaWithOnContentChange(func(text string) {
			ui.Apply(ui.service.HandleInputChange(text))
		}),
	)

	ui.statusBar = widget.NewStyledText(
		ui.service.state.StatusContent,
		widget.TextWithID("status-bar"),
		widget.TextWithLayout(layout.WithHeight(layout.Points(1))),
	)
	// The footer sits right under the input box, on the canvas, inset to line
	// up with the input text. It names the open panel's keys while
	// KeyHintsMode is KeyHintsFooter, as SyncFooter shows them, wrapping once
	// they do not fit the row, and is otherwise a blank row that parts the
	// box from the terminal's bottom edge.
	ui.footerHints = NewKeyHintsView("footer-hints", false)
	ui.footerHints.LayoutNode().FlexGrow = 1
	// A strut, no cells wide, keeps the row one row tall while it shows
	// nothing, so the input box stays in place as the hints come and go.
	strut := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithWidth(layout.Points(0)),
			layout.WithHeight(layout.Points(1)),
			layout.WithFlexShrink(0),
		),
	)
	ui.inputFooter = widget.NewBox(
		widget.BoxWithID("input-footer"),
		widget.BoxWithLayout(
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
			layout.WithFlexDirection(layout.DirectionRow),
		),
		widget.Children(ui.footerHints, strut),
	)

	inputBg := ui.BoxBg()
	inputBorderFg := ui.BorderFg()

	// Match the slash menu surface to the input container.
	ui.slashMenu = widget.NewSelect(
		ui.service.SlashMenuItems(""),
		widget.SelectWithID("slash-menu"),
		widget.SelectWithSelectionArrow(true),
		widget.SelectWithBg(inputBg),
		widget.SelectWithFocusedBg(inputBg),
		widget.SelectWithSelectedBg(inputBg),
		widget.SelectWithFg(titleFg),
		// The descriptions are dim, as the labels of the Usage box's values
		// are, and the commands bright, as its values are.
		widget.SelectWithDescriptionFg(PanelDimFg),
		widget.SelectWithFocusedBold(false),
		widget.SelectWithFocusedFg(activeFg),
		widget.SelectWithSelectedFg(activeFg),
		widget.SelectWithArrowColumn(true),
		widget.SelectWithShowDescription(false),
		widget.SelectWithInlineDescription(true),
		widget.SelectWithNameWidth(26),
		widget.SelectWithItemPaddingX(-1),
		widget.SelectWithWrapSelection(true),
		widget.SelectWithLayout(layout.WithFlexGrow(1)),
		widget.SelectWithOnItemSelected(func(_ int, item widget.SelectItem) {
			ui.Apply(ui.service.HandleSlashSelect(item.Value.(Command)))
		}),
	)
	// The menu opens above the input text, so the text stays at the bottom.
	ui.slashMenuRow = widget.NewBox(
		widget.BoxWithID("slash-menu-row"),
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
		),
	)
	// While the menu is open, a box of its own above the input box tells of
	// the session under a "Usage" header, as the menu's commands show under
	// theirs: the model, the context, the session's tokens, cost, and worked
	// time. A row of the canvas parts it from the input box. The line is cut
	// off at the right end on a narrow terminal, and the box is gone on a
	// short one, as StatusPart bounds it.
	ui.statusModel, ui.statusUsage = ui.service.state.ModelLabel, ui.service.state.Usage
	ui.slashStatus = widget.NewStyledText(style.StyledText{},
		widget.TextWithID("slash-status"),
		widget.TextWithLayout(layout.WithHeight(layout.Points(1))),
		widget.TextWithTruncate(true),
	)
	statusBox, statusCol := StreamableMessageBox(inputBorderFg, inputBg)
	ui.statusHeader = AddPanelHeader(statusCol, "status", "Usage", inputBorderFg, inputBg)
	AddDividerRow(statusCol, inputBorderFg, inputBg)
	AddContentRow(statusCol, ui.slashStatus, inputBorderFg, inputBg)
	statusGap := widget.NewBox(
		widget.BoxWithLayout(layout.WithHeight(layout.Points(1)), layout.WithFlexShrink(0)),
	)
	ui.slashStatusBox = widget.NewBox(
		widget.BoxWithID("slash-status-box"),
		widget.BoxWithLayout(layout.WithFlexShrink(0), layout.WithAlignItems(layout.AlignStretch)),
		widget.Children(statusBox, statusGap),
	)
	ui.slashStatusBox.SetGone(true)
	ui.slashHeader = AddPanelHeader(ui.slashMenuRow, "slash", "Commands", inputBorderFg, inputBg)
	AddDividerRow(ui.slashMenuRow, inputBorderFg, inputBg)
	// The arrow sits in the left border cell, so the commands line up with
	// the header above them.
	AddMenuRow(ui.slashMenuRow, ui.slashMenu)
	// Unlike other lists, the menu counts no rows, neither under them nor
	// after its title, whatever CountPlace says.
	AddDividerRow(ui.slashMenuRow, inputBorderFg, inputBg)

	// Keep the input column mutable so the slash menu can be inserted.
	inputRow := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithFlexGrow(1),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(ui.input),
	)
	inputOuterBox, inputMiddleCol := StreamableMessageBox(inputBorderFg, inputBg, inputRow)
	ui.inputMiddleCol = inputMiddleCol
	ui.inputContentRow = inputMiddleCol.Children()[0].(*widget.Box)

	// Queued prompts sit above the input box, each in a box of its own, as
	// DrawQueue draws them.
	ui.queueBox = widget.NewBox(
		widget.BoxWithID("queue"),
		widget.BoxWithLayout(layout.WithFlexShrink(0), layout.WithAlignItems(layout.AlignStretch)),
	)
	ui.queueBox.SetGone(true)

	ui.inputWrapper = widget.NewBox(
		widget.BoxWithID("input-wrapper"),
		widget.BoxWithLayout(layout.WithFlexShrink(0)),
		widget.Children(ui.slashStatusBox, ui.queueBox, inputOuterBox, ui.inputFooter),
	)

	// The session list and banners open at the top of the input box, like the
	// slash menu, and the input below them is their search.
	sessionBg := inputBg
	ui.sessionMenu = widget.NewSelect(
		nil,
		widget.SelectWithID("session-menu"),
		widget.SelectWithSelectionArrow(true),
		widget.SelectWithArrowColumn(true),
		widget.SelectWithItemPaddingX(-1),
		widget.SelectWithBg(sessionBg),
		widget.SelectWithFg(titleFg),
		widget.SelectWithFocusedBg(sessionBg),
		widget.SelectWithFocusedFg(activeFg),
		widget.SelectWithSelectedBg(sessionBg),
		widget.SelectWithSelectedFg(activeFg),
		widget.SelectWithDescriptionFg(PanelDimFg),
		// The active session's "[current]" stays dim on the selected row
		// under SelectionArrow, and takes the fill's text color under the
		// styles that fill it.
		widget.SelectWithHintFg(PanelDimFg),
		widget.SelectWithShowDescription(false),
		widget.SelectWithWrapSelection(true),
		widget.SelectWithOnItemSelected(func(_ int, item widget.SelectItem) {
			if item.Value != nil {
				ui.Apply(ui.service.HandleSessionSelect(item.Value))
			}
		}),
		widget.SelectWithOnSelectionChanged(func(_ int, item widget.SelectItem) {
			ui.SyncSessionCount()
			if item.Value != nil {
				ui.PreviewSession(item.Value)
			}
		}),
	)
	ui.sessionMenu.SetFocusable(false)
	ui.sessionText = widget.NewText("",
		widget.TextWithID("session-text"),
		widget.TextWithFg(style.ColorFromInts(92, 92, 92, 255)),
	)
	ui.sessionPanel = NewInputPanel("session-panel")
	ui.sessionHeader = AddPanelHeader(ui.sessionPanel, "session", "Sessions", inputBorderFg, sessionBg)
	AddDividerRow(ui.sessionPanel, inputBorderFg, sessionBg)
	// The no-match notice takes the place of the rows.
	ui.sessionTextRow = AddContentRow(ui.sessionPanel, ui.sessionText, inputBorderFg, sessionBg)
	ui.sessionTextRow.SetGone(true)
	// The arrow sits in the left border cell, as in the slash menu.
	AddMenuRow(ui.sessionPanel, ui.sessionMenu)
	ui.sessionCount = AddPanelStatus(ui.sessionPanel, "session-count", inputBorderFg, sessionBg)
	ui.sessionPrompts = NewPromptPreview(inputBorderFg, sessionBg, ui.theme)
	ui.sessionPanel.Add(ui.sessionPrompts.Box)
	AddDividerRow(ui.sessionPanel, inputBorderFg, sessionBg)
	ui.inputMiddleCol.InsertBefore(ui.sessionPanel, ui.inputContentRow)

	bannerBg := inputBg
	// The banner's text row collapses when its content is empty.
	ui.bannerText = widget.NewText("",
		widget.TextWithID("banner-text"),
		widget.TextWithWrapMode(textbuf.WrapWord),
		widget.TextWithFg(style.ColorFromInts(92, 92, 92, 255)),
	)
	ui.bannerMenu = widget.NewSelect(
		nil,
		widget.SelectWithID("banner-menu"),
		widget.SelectWithSelectionArrow(true),
		widget.SelectWithArrowColumn(true),
		widget.SelectWithInlineDescription(true),
		widget.SelectWithBg(bannerBg),
		widget.SelectWithFocusedBg(bannerBg),
		widget.SelectWithSelectedBg(bannerBg),
		widget.SelectWithFocusedFg(activeFg),
		widget.SelectWithSelectedFg(activeFg),
		widget.SelectWithFg(titleFg),
		// What follows a row's name, such as a setting's value or a demo's
		// description, is dim, as in the slash menu.
		widget.SelectWithDescriptionFg(PanelDimFg),
		// A row's hint, such as "[default]", stays dim on the selected row
		// under SelectionArrow, and takes the fill's text color under the
		// styles that fill it.
		widget.SelectWithHintFg(PanelDimFg),
		widget.SelectWithWrapSelection(true),
		widget.SelectWithShowScrollIndicator(false),
		widget.SelectWithShowDescription(false),
		widget.SelectWithItemPaddingX(-1),
		widget.SelectWithOnItemSelected(func(_ int, item widget.SelectItem) {
			ui.Apply(ui.service.HandleBannerSelect(item.Value))
		}),
		widget.SelectWithOnSelectionChanged(func(int, widget.SelectItem) { ui.SyncBannerCount() }),
	)
	// The header, then the text and the menu, which banner updates hide
	// independently.
	ui.bannerPanel = NewInputPanel("banner-panel")
	ui.bannerHeader = AddPanelHeader(ui.bannerPanel, "banner", "", inputBorderFg, bannerBg)
	ui.bannerDividerRow = BuildDividerRow(inputBorderFg, bannerBg)
	ui.bannerPanel.Add(ui.bannerDividerRow)
	ui.bannerTextRow = AddContentRow(ui.bannerPanel, ui.bannerText, inputBorderFg, bannerBg)
	// The arrow sits in the left border cell, as in the slash menu.
	AddMenuRow(ui.bannerPanel, ui.bannerMenu)
	ui.bannerCount = AddPanelStatus(ui.bannerPanel, "banner-count", inputBorderFg, bannerBg)
	ui.bannerTailRow = BuildDividerRow(inputBorderFg, bannerBg)
	ui.bannerPanel.Add(ui.bannerTailRow)
	ui.inputMiddleCol.InsertBefore(ui.bannerPanel, ui.inputContentRow)
	ui.BuildTreePanel(inputBorderFg, bannerBg)
	ui.SyncKeyHints()

	// Keep progress and errors visible below the scrolling transcript, as a
	// line on the canvas inset like the prose. The transcript ends in a blank
	// row, and a margin keeps one between the line and the input. A long
	// error, such as a provider's raw response body, wraps onto more lines.
	ui.indicatorLabel = widget.NewText("",
		widget.TextWithID("indicator-label"),
		widget.TextWithFg(indicatorFg),
		widget.TextWithWrapMode(textbuf.WrapWord),
	)
	ui.indicatorLabel.RenderAfterHook = ui.DrawShimmer
	ui.input.RenderAfterHook = ui.HideCoveringCursor
	for menu := range ui.Menus() {
		menu.RenderAfterHook = ui.MarkSelection(menu)
	}
	ui.indicatorBox = widget.NewBox(
		widget.BoxWithID("indicator-box"),
		widget.BoxWithLayout(
			layout.WithFlexShrink(0),
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
			layout.WithMarginBottom(layout.Points(1)),
		),
		widget.Children(ui.indicatorLabel),
	)
	ui.indicatorBox.SetGone(true)

	// Inline transcript rows are inserted before the indicator, so every fixed
	// child below it keeps its order.
	root.Add(ui.messageBox)
	root.Add(ui.indicatorBox)
	root.Add(ui.previewMessageBox)
	root.Add(ui.inputWrapper)

	// Inline, the rows that scrolled into the scrollback go to the terminal
	// before the frame is laid out, while they are where the last frame put
	// them.
	root.AddLifecyclePass(ui.CommitScrolledRows)
	// Every frame fits the input box to the terminal before it is laid out,
	// whatever changed it: a menu, the input text, the queue, or a resize.
	root.AddLifecyclePass(ui.KeepInputBoxInView)

	// Inline, what may still change stays out of the scrollback, and what
	// went to the terminal comes back when the transcript is written again.
	ui.renderer.SetInlineLiveTop(ui.LiveTop)
	ui.renderer.SetInlineReprint(ui.ReprintTranscript)
}
