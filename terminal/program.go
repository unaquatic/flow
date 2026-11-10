// Package terminal runs flow's interactive terminal UI on an app, on the
// session that a Start selects, and prints the text the command line shows
// around it, such as the logo and the command that resumes a session. The UI
// itself is in terminal/ui.
package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/terminal/ui"
)

// Program runs the interactive terminal UI of an app. When the UI closes on
// a stored session, Run leaves the notice that tells how to resume it in the
// terminal: the last frame of inline mode shows it, and Run prints it after
// fullscreen mode. After Run returns, Resumable reports that session, and
// Relaunch the session of another folder that the UI closed to reopen there,
// which needs no notice.
type Program struct {
	app   *app.App
	start Start
	// views add to the UI the widgets of what the app's extensions add to
	// messages.
	views []ui.Extension
	// closed is the session the UI closed on, once Run has returned.
	closed message.SessionID
	// relaunch is the session of another folder the UI closed to reopen
	// there, once Run has returned, or "".
	relaunch message.SessionID
}

// New returns a Program that runs the terminal UI of a on the first session
// that start selects, showing the widgets of the app's extensions that views
// add, in order.
func New(a *app.App, start Start, views ...ui.Extension) *Program {
	return &Program{app: a, start: start, views: views}
}

// Run starts the interactive terminal UI and blocks until it closes, and
// then leaves the resume notice, as Program describes. Standard error goes
// to the application log meanwhile.
func (p *Program) Run(ctx context.Context) error {
	first, err := p.start.FirstSession(ctx, p.app.Sessions())
	if err != nil {
		return err
	}
	res, err := ui.Run(ctx, p.app, ui.Options{
		First:       first,
		Prompt:      p.start.Prompt,
		Model:       p.start.Model,
		ColorMode:   p.start.ColorMode,
		PickSession: p.start.Mode == StartPick,
		Extensions:  p.views,
		Updated:     p.start.Updated,
	})
	p.closed, p.relaunch = res.Closed, res.Relaunch
	if err != nil || res.ResumeShown || p.relaunch != "" {
		return err
	}
	// Fullscreen mode took its screen along, and a notice on it.
	if id := p.Resumable(ctx); id != "" {
		text := ResumeText(id)
		out := text.PlainText()
		if !p.start.NoColor {
			out = ANSI(text, res.ColorMode)
		}
		fmt.Fprintln(os.Stdout, out)
	}
	return nil
}

// Resumable returns the session the UI closed on when that session is stored,
// so that it can be resumed, and "" otherwise or before Run has returned.
func (p *Program) Resumable(ctx context.Context) message.SessionID {
	ok, err := p.app.Sessions().Stored(ctx, p.closed)
	if err != nil {
		slog.Warn("resumable session", "session", p.closed, "err", err)
	}
	if !ok {
		return ""
	}
	return p.closed
}

// Relaunch returns the session of another folder that was picked from the
// session list, which the UI closed for the caller to start flow again on in
// that folder, and "" otherwise or before Run has returned.
func (p *Program) Relaunch() message.SessionID { return p.relaunch }
