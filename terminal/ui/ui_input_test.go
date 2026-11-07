package ui

import (
	"testing"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/terminal"
)

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
