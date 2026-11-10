package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/terminal/ui"
)

// sessionCmd groups the commands that read and delete the saved sessions.
var sessionCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:     "session",
	Short:    "List and delete saved sessions",
	Commands: []cmdline.Node{sessionListCmd, sessionDeleteCmd},
}

// SessionListOptions are the flags of flow session list.
type SessionListOptions struct {
	// All lists the sessions of every folder, not of this project alone.
	All bool
	// Format is how the list is written.
	Format OutputFormat
}

// sessionListCmd lists the saved sessions, newest first.
var sessionListCmd = &cmdline.Command[SessionListOptions]{
	Name:  "list",
	Short: "List this project's sessions, newest first",
	Flags: func(fs *flag.FlagSet, o *SessionListOptions) {
		const allUsage = "List the sessions of every folder"
		fs.BoolVar(&o.All, "all", false, allUsage)
		fs.BoolVar(&o.All, "a", false, allUsage)
		formatFlag(fs, &o.Format)
	},
	Run: withApp(runSessionList),
}

// runSessionList writes to w the sessions of a's project, or of every
// folder with o.All, newest first: in OutputText, a row each, as the
// terminal's session list shows them, without the folder when they are of
// this project alone; in OutputJSON, as the HTTP API lists them.
func runSessionList(ctx context.Context, w io.Writer, a *app.App, o SessionListOptions, _ []string) error {
	list := a.Sessions().ListSessions
	if o.All {
		list = a.Sessions().ListAllSessions
	}
	sessions, err := list(ctx)
	if err != nil {
		return err
	}
	if o.Format == OutputJSON {
		if sessions == nil {
			sessions = []session.Session{}
		}
		return writeJSON(w, sessions)
	}
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(w, "No sessions found")
		return err
	}
	if !o.All {
		// The project's sessions share its folder, which needs no column.
		for i := range sessions {
			sessions[i].Worktree = ""
		}
	}
	for _, row := range ui.SessionRowNames(sessions) {
		if _, err := fmt.Fprintln(w, strings.TrimRight(row, " ")); err != nil {
			return err
		}
	}
	return nil
}

// SessionDeleteOptions are the flags of flow session delete.
type SessionDeleteOptions struct {
	// Format is how the deleted sessions are written.
	Format OutputFormat
}

// sessionDeleteCmd deletes saved sessions.
var sessionDeleteCmd = &cmdline.Command[SessionDeleteOptions]{
	Name:  "delete",
	Usage: "<session-id>...",
	Short: "Delete saved sessions, of any folder, with their messages",
	Flags: func(fs *flag.FlagSet, o *SessionDeleteOptions) { formatFlag(fs, &o.Format) },
	Args:  cmdline.MinArgs(1),
	Run:   withApp(runSessionDelete),
}

// runSessionDelete deletes the sessions that args name, of any folder, with
// their messages, as app.App.DeleteSession does, which stops a run of this
// process in progress on one first, and writes to w the sessions it deleted:
// in OutputText, under "Deleted:", as introLine draws it, a row each, as
// the terminal's session list shows them; in OutputJSON, as the HTTP API
// lists them. It deletes none, and fails, when one is not stored; a session
// that another process is changing fails it there, after the sessions
// before it are deleted.
func runSessionDelete(ctx context.Context, w io.Writer, a *app.App, o SessionDeleteOptions, args []string) error {
	sessions := make([]session.Session, 0, len(args))
	for _, id := range args {
		sess, err := a.Sessions().Session(ctx, message.SessionID(id))
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("no stored session matches %q", id)
		}
		if err != nil {
			return err
		}
		sessions = append(sessions, *sess)
	}
	for _, sess := range sessions {
		if err := a.DeleteSession(ctx, sess.ID); err != nil {
			return fmt.Errorf("delete session %s: %w", sess.ID, err)
		}
	}
	if o.Format == OutputJSON {
		return writeJSON(w, sessions)
	}
	if _, err := fmt.Fprintln(w, introLine("Deleted:", showsColor(w))); err != nil {
		return err
	}
	for _, row := range ui.SessionRowNames(sessions) {
		if _, err := fmt.Fprintln(w, accountIndent+strings.TrimRight(row, " ")); err != nil {
			return err
		}
	}
	return nil
}

// formatFlag binds --format, how a command that lists things writes them,
// to f.
func formatFlag(fs *flag.FlagSet, f *OutputFormat) {
	fs.TextVar(f, "format", OutputText, "Output `format`: text, for people, or json, for scripts")
}

// writeJSON writes v to w as indented JSON, on lines of its own.
func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// typedLine returns v, which encodes as a JSON object, as one line of JSON
// Lines: the object with a "type" member, typ, first, and then v's own
// members, ending with a line break.
func typedLine(typ string, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(data) < 2 || data[0] != '{' {
		return nil, fmt.Errorf("%s: %T encodes as no JSON object", typ, v)
	}
	name, err := json.Marshal(typ)
	if err != nil {
		return nil, err
	}
	line := append([]byte(`{"type":`), name...)
	if rest := data[1:]; len(rest) > 1 {
		line = append(append(line, ','), rest...)
	} else {
		line = append(line, '}')
	}
	return append(line, '\n'), nil
}
