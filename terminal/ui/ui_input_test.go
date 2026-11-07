package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// ClipboardOutput captures clipboard output or returns a configured failure.
type ClipboardOutput struct {
	// Buffer holds bytes accepted by Write.
	Buffer bytes.Buffer
	// Err makes Write fail when set.
	Err error
}

// Write captures data unless Err is set.
func (w *ClipboardOutput) Write(p []byte) (int, error) {
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Buffer.Write(p)
}

func TestLoginCopyURL(t *testing.T) {
	for _, failure := range []error{nil, io.ErrClosedPipe} {
		name := "success"
		if failure != nil {
			name = "write failure"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := newTestService(t)
			output := &ClipboardOutput{Err: failure}
			r, err := terminal.NewRenderer(terminal.Config{InlineMode: true, Stdout: output})
			if err != nil {
				t.Fatal(err)
			}
			ui := &UI{renderer: r, service: s, theme: DefaultTheme(), extensions: builtinExtensions(t), messageWidgets: make(map[message.ID]*ChatMessageBox)}
			ui.BuildUI()
			url := "https://login.test/authorize?state=nonce&code_challenge=challenge"
			ui.Apply(s.ApplyAgentEvent(app.EventLoginPrompt{
				Provider: "provider-b", Account: agent.NewAccountID(), URL: url,
				State: &agent.LoginState{Verifier: "hidden-proof", State: "nonce"},
			}))
			ui.SetInputText("partially-pasted-code")
			key := &input.KeyEvent{Name: "l", Mods: input.ModCtrl}
			ui.HandleKey(key)
			if !key.DefaultPrevented() || s.state.Login == nil || s.state.Banner != LoginBanner {
				t.Fatal("copy was not handled or closed the login panel")
			}
			if ui.input.Text() != "partially-pasted-code" || s.state.LoginCompleting || len(s.state.Messages) != 0 {
				t.Fatal("copy changed the input, submitted the code, or added a chat message")
			}
			want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(url)) + "\x1b\\"
			status := "URL sent to terminal clipboard."
			if failure != nil {
				want = ""
				status = "Could not send URL to terminal clipboard: " + failure.Error()
			}
			if output.Buffer.String() != want || !strings.Contains(ui.bannerText.PlainText(), status) {
				t.Fatalf("clipboard output = %q, banner = %q", output.Buffer.String(), ui.bannerText.PlainText())
			}
			ui.HandleKey(&input.KeyEvent{Name: "c", Mods: input.ModCtrl})
			if s.state.Login != nil || s.state.Banner != NoBanner {
				t.Fatal("Ctrl+C no longer cancels login")
			}
			key = &input.KeyEvent{Name: "l", Mods: input.ModCtrl}
			ui.HandleKey(key)
			if key.DefaultPrevented() || output.Buffer.String() != want {
				t.Fatal("copy shortcut remained active outside login")
			}
		})
	}
}

// cursorHost records the cursor that the widgets drawn under it placed
// last.
type cursorHost struct {
	render.NoopHost
	visible bool
}

func (c *cursorHost) SetCursorPosition(_, _ int, visible bool) { c.visible = visible }

// TestHideCoveringCursor checks that the input hides its cursor where its
// block would cover a character the input colors: on the first letter of a
// selection made leftward, whose fill stays the primary color, and on the
// first letter of a hint. After a selection made rightward, and in plain
// text, the cursor shows.
func TestHideCoveringCursor(t *testing.T) {
	shiftArrow := func(name input.KeyName) func(ui *UI) {
		return func(ui *UI) {
			for range 3 {
				ui.input.HandleKeyPress(&input.KeyEvent{Name: name, Mods: input.ModShift, Type: input.KeyPress})
			}
		}
	}
	tests := []struct {
		name    string
		edit    func(ui *UI)
		visible bool
	}{
		{name: "text", edit: func(*UI) {}, visible: true},
		{name: "selected leftward", edit: shiftArrow(input.KeyLeft), visible: false},
		{name: "selected rightward", edit: func(ui *UI) {
			ui.input.SetCursor(0, 0)
			shiftArrow(input.KeyRight)(ui)
		}, visible: true},
		{name: "hint", edit: func(ui *UI) {
			ui.ClearInput()
			ui.SetInputHint(QuitHint)
		}, visible: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.SetInputText("hello")
			ui.input.Focus()
			tt.edit(ui)

			root := terminal.NewRootRenderable(60, 5)
			root.Add(ui.inputWrapper)
			host := &cursorHost{}
			root.SetHost(host)
			root.Render(buffer.New(60, 5), 0)

			if host.visible != tt.visible {
				t.Errorf("cursor visible = %v, want %v", host.visible, tt.visible)
			}
		})
	}
}

// TestNewlineKeys checks that Shift+Enter, as a terminal sends it through the
// Kitty keyboard protocol, and a line feed, as Ctrl+J types it, break the
// input's line at the cursor rather than send it, while Enter and Alt+Enter
// still send it, and that with a banner open Shift+Enter acts as Enter.
func TestNewlineKeys(t *testing.T) {
	parser := input.NewParser()
	parser.SetKittyEnabled(true)
	key := func(raw string) *input.KeyEvent {
		t.Helper()
		events := parser.Parse([]byte(raw))
		if len(events) != 1 || events[0].Key == nil {
			t.Fatalf("%q parsed as %+v, want one key", raw, events)
		}
		return events[0].Key
	}
	for _, tt := range []struct {
		name, raw string
		newline   bool
	}{
		{name: "shift+enter", raw: "\x1b[13;2u", newline: true},
		{name: "line feed", raw: "\n", newline: true},
		{name: "enter", raw: "\r"},
		{name: "alt+enter", raw: "\x1b[13;3u"},
		{name: "ctrl+shift+enter", raw: "\x1b[13;6u"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			k := key(tt.raw)
			if got := NewlineKey(k); got != tt.newline {
				t.Fatalf("NewlineKey(%+v) = %v, want %v", k, got, tt.newline)
			}
			if !tt.newline {
				return
			}
			ui := newInputTestUI(t)
			ui.SetInputText("first")
			ui.HandleKey(k)
			ui.input.InsertText("second")
			if got, want := ui.input.Text(), "first\nsecond"; got != want || !k.DefaultPrevented() {
				t.Errorf("input = %q, handled %v; want %q, handled", got, k.DefaultPrevented(), want)
			}
			if len(ui.service.state.Messages) != 0 {
				t.Error("the key sent the input")
			}
		})
	}

	ui := newInputTestUI(t)
	ui.Apply(ui.service.ShowGraphemeReport())
	ui.HandleKey(key("\x1b[13;2u"))
	if ui.service.state.Banner != NoBanner || strings.Contains(ui.input.Text(), "\n") {
		t.Errorf("shift+enter on a banner left banner %v and input %q, want it closed as Enter closes it", ui.service.state.Banner, ui.input.Text())
	}
}

// TestKittyKeysKeepTheirNames checks that the keys flow binds keep the names
// and modifiers HandleKey matches once the terminal reports them through the
// Kitty keyboard protocol, as it does with KittyFlagDisambiguate.
func TestKittyKeysKeepTheirNames(t *testing.T) {
	parser := input.NewParser()
	parser.SetKittyEnabled(true)
	for raw, want := range map[string]input.KeyEvent{
		"\x1b[99;5u":  {Name: "c", Mods: input.ModCtrl},
		"\x1b[111;5u": {Name: "o", Mods: input.ModCtrl},
		"\x1b[117;5u": {Name: "u", Mods: input.ModCtrl},
		"\x1b[27u":    {Name: input.KeyEscape},
		"\x1b[13;3u":  {Name: input.KeyEnter, Mods: input.ModAlt},
	} {
		events := parser.Parse([]byte(raw))
		if len(events) != 1 || events[0].Key == nil {
			t.Fatalf("%q parsed as %+v, want one key", raw, events)
		}
		if k := events[0].Key; k.Name != want.Name || k.Mods != want.Mods || k.IsRune() {
			t.Errorf("%q = %s %v (rune %v), want %s %v", raw, k.Name, k.Mods, k.IsRune(), want.Name, want.Mods)
		}
	}
}

// TestInputStyleFills checks that the bar input style leaves no fill of
// BoxBg in the input box and the panels that open in it, those not open
// included, nor its blank edge rows, and that the filled style gives back
// each fill it took, and no other, and the rows.
func TestInputStyleFills(t *testing.T) {
	ui := newInputTestUI(t)
	ui.Apply(ui.service.SetInputStyle(InputFilled))
	count := func() int {
		n := 0
		for _, r := range []render.Renderable{ui.inputWrapper, ui.slashMenuRow, ui.sessionPanel, ui.bannerPanel, ui.treePanel} {
			for range FillsOf(r, ui.BoxBg()) {
				n++
			}
		}
		return n
	}
	filled := count()
	if filled == 0 {
		t.Fatal("the filled input has no fill of BoxBg")
	}
	ui.Apply(ui.service.SetInputStyle(InputBar))
	if got := count(); got != 0 || ui.inputTopBorder.Visible() || ui.inputBottomBorder.Visible() {
		t.Errorf("bar input keeps %d fills of BoxBg and its edge rows %v %v, want none", got, ui.inputTopBorder.Visible(), ui.inputBottomBorder.Visible())
	}
	if got := ui.InputBg(); got != style.ColorTransparent {
		t.Errorf("InputBg() = %v in the bar style, want none", got)
	}
	ui.Apply(ui.service.SetInputStyle(InputFilled))
	if got := count(); got != filled || !ui.inputTopBorder.Visible() || !ui.inputBottomBorder.Visible() {
		t.Errorf("filled input has %d fills of BoxBg again and its edge rows %v %v, want the %d it had and the rows", got, ui.inputTopBorder.Visible(), ui.inputBottomBorder.Visible(), filled)
	}
}

// TestSyncInputGap checks that in the bar input style the input box keeps a
// single blank row above it: none of its own under a reply, which ends with
// a blank row, or under the indicator, and one under a row that ends with
// none. The filled box starts with a blank row of its own, so it takes none.
func TestSyncInputGap(t *testing.T) {
	gap := func(ui *UI) int {
		ui.SyncInputGap()
		g, _ := ui.inputWrapper.LayoutNode().MarginTop.Resolve(0)
		return g
	}
	for _, inline := range []bool{false, true} {
		ui := newBuiltTestUI(t, inline)
		ui.AddMessage(userText("u1", "Run the tests"))
		reply := agentText("a1", "All tests pass.")
		reply.Worked = 3 * time.Second
		ui.AddMessage(reply)
		ui.renderer.Root().CalculateLayout()
		if got := gap(ui); got != 0 {
			t.Errorf("inline %v: under a reply the box takes %d rows of its own, want none", inline, got)
		}

		last := ui.transcriptRows[len(ui.transcriptRows)-1]
		if inline {
			last.LayoutNode().MarginBottom = layout.Points(0)
		} else {
			ui.messageBox.Add(widget.NewText("tail"))
		}
		if got := gap(ui); got != 1 {
			t.Errorf("inline %v: under a row with no blank row the box takes %d, want 1", inline, got)
		}
		ui.ShowIndicator(EventIndicatorShow{Text: "Request failed"})
		if got := gap(ui); got != 0 {
			t.Errorf("inline %v: under the indicator the box takes %d rows of its own, want none", inline, got)
		}
		ui.HideIndicator()
		ui.Apply(ui.service.SetInputStyle(InputFilled))
		if got := gap(ui); got != 0 {
			t.Errorf("inline %v: the filled box takes %d rows of its own, want none", inline, got)
		}
	}
}

// TestSyncInputGapScrolled checks that a fullscreen transcript scrolled off
// its bottom, whose last row then does not show, leaves the input box a
// blank row of its own, and that one scrolled to its bottom, which ends with
// its last reply's blank row, leaves it none.
func TestSyncInputGapScrolled(t *testing.T) {
	ui := newBuiltTestUI(t, false)
	for i := range 4 {
		ui.AddMessage(userText(message.ID(fmt.Sprintf("u%d", i)), "Run the tests"))
		ui.AddMessage(agentText(message.ID(fmt.Sprintf("a%d", i)), "one\n\ntwo\n\nthree"))
	}
	gap := func() int {
		ui.SyncInputGap()
		g, _ := ui.inputWrapper.LayoutNode().MarginTop.Resolve(0)
		return g
	}
	DrawFullscreen(ui, 40, 8, 0)
	if got := gap(); got != 1 {
		t.Errorf("scrolled to the top, the box takes %d rows of its own, want 1", got)
	}
	ui.messageBox.ScrollToBottom()
	if got := gap(); got != 0 {
		t.Errorf("scrolled to the bottom, the box takes %d rows of its own, want none", got)
	}
}
