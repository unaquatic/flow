package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/terminal/ui"
)

// ExportOptions are the flags of flow export.
type ExportOptions struct {
	// Format is how the transcript is written.
	Format ExportFormat
	// Output is the file the transcript is written to, or empty for
	// standard output.
	Output string
}

// ExportFormat is how flow export writes a transcript: ExportMarkdown or
// ExportJSON.
type ExportFormat string

const (
	// ExportMarkdown writes the transcript as the terminal's /export saves
	// it.
	ExportMarkdown ExportFormat = "markdown"
	// ExportJSON writes the session and its messages as the HTTP API
	// writes them.
	ExportJSON ExportFormat = "json"
)

// MarshalText returns f as text.
func (f ExportFormat) MarshalText() ([]byte, error) {
	return []byte(f), nil
}

// UnmarshalText sets f to the format text names, in any case.
func (f *ExportFormat) UnmarshalText(text []byte) error {
	name := ExportFormat(strings.ToLower(strings.TrimSpace(string(text))))
	if name != ExportMarkdown && name != ExportJSON {
		return fmt.Errorf("unknown format %q; use markdown or json", text)
	}
	*f = name
	return nil
}

// errNoSessions is the error of flow export, named no session, in a project
// that has none.
var errNoSessions = errors.New("no sessions in this project: name one, as flow session list --all lists them")

// exportCmd writes a session's transcript.
var exportCmd = &cmdline.Command[ExportOptions]{
	Name:  "export",
	Usage: "[session-id]",
	Short: "Write a session's transcript, the latest of this project's by default",
	Flags: func(fs *flag.FlagSet, o *ExportOptions) {
		fs.TextVar(&o.Format, "format", ExportMarkdown, "Output `format`: markdown, as /export saves it, or json, as the HTTP API writes it")
		const outputUsage = "Write the transcript to this `file`, not to standard output"
		fs.StringVar(&o.Output, "output", "", outputUsage)
		fs.StringVar(&o.Output, "o", "", outputUsage)
	},
	Args: cmdline.MaxArgs(1),
	Run:  withApp(runExport),
}

// SessionExport is a session as flow export writes it in ExportJSON.
type SessionExport struct {
	// Session is the session as the HTTP API writes it.
	Session *session.Session `json:"session"`
	// Messages are the messages of its active branch, from its root.
	Messages []app.Message `json:"messages"`
}

// runExport writes the transcript of the session args names, of any folder,
// or else of the project's latest session, in o's format, to o's output
// file, saying so on w, or else to w.
func runExport(ctx context.Context, w io.Writer, a *app.App, o ExportOptions, args []string) error {
	id, err := exportID(ctx, a, args)
	if err != nil {
		return err
	}
	stored, err := a.Sessions().Session(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no stored session matches %q", id)
	}
	if err != nil {
		return err
	}
	msgs, err := a.Session(id).Messages()
	if err != nil {
		return err
	}
	data, err := exportData(o.Format, stored, msgs)
	if err != nil {
		return err
	}
	if o.Output == "" {
		_, err := w.Write(data)
		return err
	}
	if err := os.WriteFile(o.Output, data, 0o644); err != nil {
		return err
	}
	writeField(w, "Exported the session to:", o.Output)
	return nil
}

// exportID returns the session that args names, or else the latest of a's
// project, or errNoSessions when it has none.
func exportID(ctx context.Context, a *app.App, args []string) (message.SessionID, error) {
	if len(args) == 1 {
		return message.SessionID(args[0]), nil
	}
	sessions, err := a.Sessions().ListSessions(ctx)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", errNoSessions
	}
	// Sessions are listed newest first.
	return sessions[0].ID, nil
}

// exportData returns the transcript of sess, whose active branch msgs holds,
// in format: Markdown, as ui.TranscriptMarkdown writes it, or the JSON of a
// SessionExport.
func exportData(format ExportFormat, sess *session.Session, msgs []app.Message) ([]byte, error) {
	if format == ExportJSON {
		data, err := json.MarshalIndent(SessionExport{Session: sess, Messages: msgs}, "", "  ")
		return append(data, '\n'), err
	}
	chat := make([]ui.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		chat = append(chat, ui.ChatMessageOf(m.Content))
	}
	return []byte(ui.TranscriptMarkdown(sess.Title, chat)), nil
}
