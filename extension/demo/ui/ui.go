// Package ui is the terminal side of the demo extension: the /demo command,
// which offers the demos in a menu, and the label of webfetch, the tool the
// demo calls to show a call of a tool that is not found.
package ui

import (
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/flow/terminal/ui"
)

// Command is the slash command that offers the demos.
const Command ui.Command = "demo"

// StoppedToast is the toast /demo shows when it stops a run in progress.
const StoppedToast = "Stream stopped"

// Extension adds the demos to the terminal UI. It implements
// ui.CommandExtension and ui.ToolFormatter, and shows no parts or views of
// its own.
type Extension struct{}

var (
	_ ui.CommandExtension = (*Extension)(nil)
	_ ui.ToolFormatter    = (*Extension)(nil)
)

// New returns the demos.
func New() *Extension { return &Extension{} }

// NewPartView returns nil: the extension adds no parts.
func (*Extension) NewPartView(message.ContentPart, ui.ViewHost) ui.PartView { return nil }

// NewSegmentView returns nil: the extension shows nothing under a message.
func (*Extension) NewSegmentView(ui.ChatMessage, ui.ViewHost) ui.SegmentView { return nil }

// SlashCommands returns /demo, which runs as Run does, and which the menu
// offers only while debug is on.
func (*Extension) SlashCommands() []ui.SlashCommand {
	return []ui.SlashCommand{{Name: Command, Desc: "Run a demo", Debug: true, Run: Run}}
}

// FormatTool labels webfetch "Web Fetch", which its name does not read as
// once split, and leaves every other tool's call to the box.
func (*Extension) FormatTool(call message.ToolPart) (ui.ToolView, bool) {
	if call.Name == "webfetch" {
		return ui.ToolView{Label: "Web Fetch"}, true
	}
	return ui.ToolView{}, false
}

// Run stops the run in progress, as a second /demo stops the demo it
// started, or else offers the demos, as ShowMenu does.
func Run(s *ui.Service) []ui.UIEvent {
	if s.Streaming() {
		s.Stop()
		return s.ShowToast(StoppedToast)
	}
	return ShowMenu(s)
}

// ShowMenu offers the demos, searched from the input: a streamed demo
// session, the errors flow reports, which ShowErrorMenu offers, the notice
// of an update, as ShowUpdated adds it, the grapheme width report, and the
// 256-color palette.
func ShowMenu(s *ui.Service) []ui.UIEvent {
	item := func(name, desc string, run func() []ui.UIEvent) ui.MenuItem {
		return ui.MenuItem{Name: name, Desc: desc, Choose: s.Closing(run)}
	}
	items := []ui.MenuItem{
		item("Stream", "Run a streamed demo reply", func() []ui.UIEvent { return StartStream(s) }),
		// It opens a menu of its own in place of this one.
		{Name: "Error", Desc: "Show each kind of error flow reports", Choose: func() []ui.UIEvent { return ShowErrorMenu(s) }},
		item("Update", "Show the notice of the first start after an update", func() []ui.UIEvent { return ShowUpdated(s) }),
		item("Grapheme", "Run a grapheme width check on this terminal", s.ShowGraphemeReport),
		item("Palette", "Run a demo of the xterm 256-color palette", s.ShowPalette),
	}
	return s.ShowSearchMenu(&ui.SearchMenu{Title: "Demos", Items: func() []ui.MenuItem { return items }})
}

// ShowErrorMenu offers the errors of ErrorItems, searched from the input.
func ShowErrorMenu(s *ui.Service) []ui.UIEvent {
	items := ErrorItems(s)
	return s.ShowSearchMenu(&ui.SearchMenu{Title: "Errors", Items: func() []ui.MenuItem { return items }})
}

// ErrorItems returns a menu row for each kind of error flow reports, which,
// once chosen, closes the menu and shows its error where flow shows one of
// its kind, through the code that shows it. Each reads as ui.ErrorIndicator
// gives it:
//
//   - a provider's error response with a JSON body, and one with an HTML
//     page, in the indicator, headed by the provider and the status, over
//     the body;
//   - an error event in the middle of a stream, which has no status, and a
//     request that got no response, in the indicator, as their message;
//   - a login whose token endpoint answered with an error, in the
//     indicator, headed by the login and the status, over the body;
//   - and an error under a reply, which ui.ErrorView shows as the
//     indicator does.
func ErrorItems(s *ui.Service) []ui.MenuItem {
	item := func(name, desc string, run func() []ui.UIEvent) ui.MenuItem {
		return ui.MenuItem{Name: name, Desc: desc, Choose: s.Closing(run)}
	}
	indicate := func(err error) func() []ui.UIEvent {
		return func() []ui.UIEvent { return s.ShowError(app.ErrorOf(err)) }
	}
	return []ui.MenuItem{
		item("Response JSON", "A 401 response with a JSON error object", indicate(demo.KeyExpiredError())),
		item("Response HTML", "A 502 response with a gateway's HTML page", indicate(demo.BadGatewayError())),
		item("Stream error", "An error event mid-stream, without a status", indicate(demo.StreamError())),
		item("Request failed", "A connection closed before any response", indicate(demo.RequestError())),
		item("Login failed", "A token endpoint that answers 401", func() []ui.UIEvent { return ShowLoginError(s) }),
		item("Message error", "An error under a reply", func() []ui.UIEvent { return ShowMessageError(s) }),
	}
}

// UpdateVersion is the release of flow that the update demo's notices name.
const UpdateVersion = "v1.2.3"

// ShowUpdated adds to the transcript the notice under the session info at
// the first start of UpdateVersion, a release that an update installed,
// through ui.Service.ShowUpdated. Nothing is checked or installed.
func ShowUpdated(s *ui.Service) []ui.UIEvent {
	return s.ShowUpdated(UpdateVersion)
}

// LoginAccount is the account that the error demo's failed login names.
const LoginAccount agent.AccountID = "demo"

// ShowLoginError reports a login to LoginAccount of demo.Provider that
// failed with demo.TokenError, as the app reports a login that fails.
func ShowLoginError(s *ui.Service) []ui.UIEvent {
	return s.ApplyAgentEvent(app.EventLoginFailed{Provider: demo.Provider, Account: LoginAccount, Error: app.ErrorOf(demo.TokenError())})
}

// MessageErrorText is the text of the reply that ShowMessageError shows an
// error under.
const MessageErrorText = "Reading the file."

// ShowMessageError adds a reply of demo.ModelName that reads
// MessageErrorText and failed with demo.KeyExpiredError, which shows under
// its text. The reply is not stored in the session.
func ShowMessageError(s *ui.Service) []ui.UIEvent {
	id := message.NewID()
	msg := ui.ChatMessage{ID: id, Role: message.RoleAgent, Provider: demo.Provider, Model: demo.ModelName, Error: demo.KeyExpiredError()}
	// The error shows once the message updates, as its first part does.
	text := message.TextPart{ID: message.NewPartID(), Text: MessageErrorText}
	return append(s.AddMessage(msg), s.ApplyPart(id, text, false)...)
}

// StartStream plays the demo session, as demo.Script gives it, stored and
// shown like any other run.
func StartStream(s *ui.Service) []ui.UIEvent {
	return s.StartScript("Demo", demo.Script())
}
