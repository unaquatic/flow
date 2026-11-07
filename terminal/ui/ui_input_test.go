package ui

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/terminal"
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
