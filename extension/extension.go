// Package extension defines how a feature extends flow without the rest of
// flow naming it. An extension is loaded into the app as it starts, and plugs
// in at three points:
//
//   - Content: the parts it adds to messages, which its package registers
//     with message.RegisterPart, and marks as message.Metadata when no
//     provider is to see them.
//   - Behaviour: the hooks below: DialHook and LoginHook, whose providers
//     and logins the app runs the model and authorizes its requests with,
//     ToolHook, whose tools the app offers the model from the start, and the
//     hooks the app calls as a session runs and moves through its history.
//   - Terminal UI: the views that show its parts, which terminal/ui.Extension
//     describes.
//
// It keeps its settings in its own entry of the config file's extensions
// section, which it reads with config.Config.Extension, and its data in its
// own tables of the app's database, which it creates and upgrades with
// database.Migrate, under its name.
//
// An extension implements Extension and any of the hook interfaces, which
// the app finds by type assertion, as net/http finds an http.Flusher. The app
// calls the hooks of its extensions in the order it loaded them. Each hook
// that follows an event is given a struct that describes the event, such as
// Prompt or agent.Turn, so that the app can tell hooks more by adding fields,
// and a new hook is a new interface: neither changes the extensions written
// before.
//
// This package imports package agent, whose turns and tools its hooks share
// with the agent loop, and nothing of the app, so that an extension does not
// either, and the app's own tests can load one. Package agent imports no
// extension, not even this package.
package extension

import (
	"context"
	"database/sql"
	"errors"
	"iter"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
)

// Extension is a feature loaded into the app.
type Extension interface {
	// Name identifies the extension, as in logs, and the migrations of its
	// tables in the database, so it is snake_case.
	Name() string
}

// Host is what the app gives an extension as it loads it.
type Host struct {
	// DB is the app's database, which the extension may keep its own tables
	// in, created and upgraded with database.Migrate under its name. The app
	// closes it once its operations end.
	DB *sql.DB
	// Project identifies the working directory's project in DB.
	Project database.ProjectID
	// WorkingDir is the working directory, which is the project's worktree.
	WorkingDir string
	// DataDir is the data directory, which holds the config file, the
	// database, and the log.
	DataDir string
	// Config is the configuration as the app read it as it started. An
	// extension reads its own settings with Config.Extension.
	Config *config.Config
}

// Load starts an extension for the app that host describes. ctx is the
// context the app was created with. An error keeps the app from starting.
type Load func(ctx context.Context, host Host) (Extension, error)

// Prompt is a prompt being sent, before it is stored.
type Prompt struct {
	// SessionID is the session the prompt is sent to.
	SessionID message.SessionID
	// MessageID identifies the prompt.
	MessageID message.ID
	// Text is what the prompt says.
	Text string
	// Model is the model variant the prompt is sent to, in
	// "provider/model:option:effort" form.
	Model string
}

// Move is a move through a session's history.
type Move struct {
	// SessionID is the session that moves.
	SessionID message.SessionID
	// Branch is the branch the session moves onto, from its root to a leaf.
	Branch []message.Content
	// Head is the message of Branch the session's transcript ends at after
	// the move, or empty when it then shows none.
	Head message.ID
}

// MoveResult is what a MoveHook changed to follow a move.
type MoveResult struct {
	// Files are the files the hook changed.
	Files []FileDiff
}

// PromptHook adds parts to each prompt, after its text, before the prompt is
// stored and the run that answers it starts.
type PromptHook interface {
	Extension
	// OnPrompt adds parts to prompt p through sink. ctx is the session's,
	// which stopping the run does not end.
	OnPrompt(ctx context.Context, p Prompt, sink agent.PartSink)
}

// TurnHook adds parts to each agent message as its turn ends, however it
// ends, before the message is stored, as agent.TurnHook describes. The app
// gives the agent of every run the extensions that implement it.
type TurnHook interface {
	Extension
	agent.TurnHook
}

// MoveHook follows every move through a session's history: an undo, a redo,
// a checkout, a branch switch, and a navigation.
type MoveHook interface {
	Extension
	// OnMove brings what the extension keeps in step with the session to
	// where it stood after m.Head, or before the first message of m.Branch
	// when m.Head is empty, and reports what it changed. It runs before the
	// session records the move, and an error cancels the move.
	OnMove(ctx context.Context, m Move) (MoveResult, error)
}

// ToolHook adds tools that the model may call in every session. The app asks
// for them once, as it starts, after it loads every extension. It registers
// them in the order it loaded the extensions, so that a tool replaces one of
// the same name that an extension loaded before added.
type ToolHook interface {
	Extension
	// Tools returns the tools the extension adds.
	Tools() []agent.Tool
}

// DialHook speaks request formats of the catalog's endpoints: it makes the
// provider that sends a provider's requests to an endpoint. The app asks
// the extensions in the order it loaded them, and the first that speaks an
// endpoint makes its provider, so an extension can take the endpoints of a
// format that one loaded after it also speaks.
type DialHook interface {
	Extension
	// Dial returns the provider that sends provider t's requests to
	// endpoint ep, whose base URL is set, authorized by az, and reports
	// whether the extension speaks ep's request format and authentication,
	// as agent.Dialer does.
	Dial(t agent.ProviderID, ep gateway.Endpoint, az agent.Authorizer) (agent.Provider, bool)
}

// LoginHook adds the login flows of the providers whose accounts the
// extension can log in to. The app asks for them once, as it starts, after
// it loads every extension, in the order it loaded them, so that a flow
// replaces one for the same provider that an extension loaded before added.
type LoginHook interface {
	Extension
	// LoginFlows returns the login flow of each provider the extension
	// logs in to.
	LoginFlows() map[agent.ProviderID]agent.LoginFlow
}

// FileDiff is one file that a move changed. It encodes as the JSON of the
// HTTP API's history moves, which report the files a move changed.
type FileDiff struct {
	// File identifies the changed path.
	File string `json:"file"`
	// Additions is the number of added lines.
	Additions int `json:"additions"`
	// Deletions is the number of deleted lines.
	Deletions int `json:"deletions"`
	// Status is one of "added", "deleted", or "modified".
	Status string `json:"status"`
}

// ErrRefused is matched by every Refusal.
var ErrRefused = errors.New("refused by an extension")

// Refusal is an error with which a hook refuses what it was asked, such as a
// move to a point it recorded nothing for, because of what was asked rather
// than a failure. It matches ErrRefused, which the server reports as a bad
// request.
type Refusal string

// Error returns the reason for the refusal.
func (r Refusal) Error() string { return string(r) }

// Is reports whether target is ErrRefused.
func (Refusal) Is(target error) bool { return target == ErrRefused }

// Hooks yields the extensions among exts that implement hook H, in order.
func Hooks[H Extension](exts []Extension) iter.Seq[H] {
	return func(yield func(H) bool) {
		for _, e := range exts {
			if h, ok := e.(H); ok && !yield(h) {
				return
			}
		}
	}
}

// Find returns the first extension among exts of type T, and reports
// whether there is one.
func Find[T Extension](exts []Extension) (T, bool) {
	for h := range Hooks[T](exts) {
		return h, true
	}
	var zero T
	return zero, false
}
