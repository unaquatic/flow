package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// The sessions that storeDemoSessions stores.
const (
	// demoUntitledSession is the session with no title, stored first.
	demoUntitledSession message.SessionID = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e0a"
	// demoSession is the session titled demoSessionTitle, stored second,
	// whose ID sorts after demoUntitledSession's, as a later version 7 UUID
	// does.
	demoSession message.SessionID = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e0b"
	// demoSessionTitle is demoSession's title.
	demoSessionTitle = "Fix the failing test"
)

// sessionDemos are the cases of flow session list, each on a demo app of
// its own, which holds the sessions storeDemoSessions stores unless the
// case is of none.
var sessionDemos = []demoCase{
	{name: "list", desc: "This project's sessions, newest first", line: "session list",
		run: storedDemo(true, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runSessionList(ctx, w, a, SessionListOptions{Format: OutputText}, nil)
		})},
	{name: "all", desc: "The sessions of every folder, each with its folder", line: "session list --all",
		run: storedDemo(true, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runSessionList(ctx, w, a, SessionListOptions{All: true, Format: OutputText}, nil)
		})},
	{name: "json", desc: "The sessions, as JSON", line: "session list --format json",
		run: storedDemo(true, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runSessionList(ctx, w, a, SessionListOptions{Format: OutputJSON}, nil)
		})},
	{name: "empty", desc: "A project with no sessions", line: "session list",
		run: storedDemo(false, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runSessionList(ctx, w, a, SessionListOptions{Format: OutputText}, nil)
		})},
}

// modelsDemos are the cases of flow models, each on a demo app of its own.
var modelsDemos = []demoCase{
	{name: "list", desc: "The models of the signed-in providers, with their reasoning efforts", line: "models",
		run: storedDemo(false, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runModels(ctx, w, a, ModelsOptions{Format: OutputText}, nil)
		})},
	{name: "not-signed-in", desc: "A provider that no account signs in to", line: "models " + string(demoKeyProvider),
		run: storedDemo(false, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runModels(ctx, w, a, ModelsOptions{Format: OutputText}, []string{string(demoKeyProvider)})
		})},
}

// exportDemos are the cases of flow export, each on a demo app of its own.
var exportDemos = []demoCase{
	{name: "markdown", desc: "A session, as /export saves it", line: "export " + string(demoSession),
		run: storedDemo(true, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runExport(ctx, w, a, ExportOptions{Format: ExportMarkdown}, []string{string(demoSession)})
		})},
	{name: "missing", desc: "A session that is not stored", line: "export " + demoMissingSession,
		run: storedDemo(true, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runExport(ctx, w, a, ExportOptions{Format: ExportMarkdown}, []string{demoMissingSession})
		})},
	{name: "none", desc: "A project with no sessions", line: "export",
		run: storedDemo(false, func(ctx context.Context, w io.Writer, a *app.App) error {
			return runExport(ctx, w, a, ExportOptions{Format: ExportMarkdown}, nil)
		})},
}

// storedDemo returns the run of a demo case that passes run a demo app, as
// withRunDemoApp makes it, which holds the sessions storeDemoSessions
// stores when stored is set, and none otherwise.
func storedDemo(stored bool, run func(context.Context, io.Writer, *app.App) error) func(context.Context, io.Writer, bool) error {
	return func(ctx context.Context, w io.Writer, _ bool) error {
		reply := func() (agent.StreamResponse, error) { return demoReply(agent.Stop{Reason: agent.StopEndTurn}), nil }
		return withRunDemoApp(ctx, reply, func(a *app.App) error {
			if stored {
				if err := storeDemoSessions(ctx, a); err != nil {
					return err
				}
			}
			return run(ctx, w, a)
		})
	}
}

// storeDemoSessions stores two sessions in a's project: demoUntitledSession,
// which said hi, and then demoSession, which asked why TestAdd fails, read
// calc.go, and answered with demoRunAnswer. The list shows demoSession
// first: it is updated last, or, within the same millisecond, created last,
// as its ID tells.
func storeDemoSessions(ctx context.Context, a *app.App) error {
	if err := storeDemoSession(ctx, a, demoUntitledSession, "",
		demoMessage(message.RoleUser, message.TextPart{Text: "say hi"}),
		demoMessage(message.RoleAgent, message.TextPart{Text: "Hi!"}),
	); err != nil {
		return err
	}
	read := message.ToolPart{CallID: "call_1", Name: "read", State: message.ToolStateCompleted{
		Input:  json.RawMessage(`{"path":"calc.go"}`),
		Output: "package calc\n\nfunc Add(a, b int) int { return a - b }",
	}}
	return storeDemoSession(ctx, a, demoSession, demoSessionTitle,
		demoMessage(message.RoleUser, message.TextPart{Text: "why does TestAdd fail?"}),
		demoMessage(message.RoleAgent, message.TextPart{Text: "Let me read calc.go."}, read),
		demoMessage(message.RoleAgent, message.TextPart{Text: demoRunAnswer}),
	)
}

// storeDemoSession stores session id in a's project, holding msgs, in
// order, titled title unless it is empty.
func storeDemoSession(ctx context.Context, a *app.App, id message.SessionID, title string, msgs ...message.Content) error {
	for _, m := range msgs {
		if err := a.Sessions().AppendMessage(ctx, id, &m); err != nil {
			return err
		}
	}
	if title == "" {
		return nil
	}
	_, err := a.Session(id).SetTitle(title)
	return err
}

// demoMessage returns a new message of role that holds parts, each with an
// ID of its own.
func demoMessage(role message.Role, parts ...message.ContentPart) message.Content {
	for i, p := range parts {
		switch p := p.(type) {
		case message.TextPart:
			p.ID = message.NewPartID()
			parts[i] = p
		case message.ToolPart:
			p.ID = message.NewPartID()
			parts[i] = p
		}
	}
	return message.Content{ID: message.NewID(), Role: role, Parts: parts}
}
