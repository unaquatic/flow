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
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/terminal"
)

// RunOptions are the flags of the run command.
type RunOptions struct {
	modelOptions
	// Continue sends the prompt to the project's most recent session.
	Continue bool
	// Session is the ID of the stored session to send the prompt to, in
	// its own folder, or empty.
	Session string
	// Format is how the run is written to standard output.
	Format OutputFormat
	// Output is the file the run's answer is written to, or empty.
	Output string
	// NoSession deletes the run's session once the run has ended, so that
	// none is kept. It takes a new session alone.
	NoSession bool
	// Dir is the folder the run works in, in place of the one flow started
	// in, or empty.
	Dir string
}

// Start returns the session the flags in o choose, as the terminal's Start
// picks it: the project's latest with Continue, the stored session that
// Session names, or else a new one.
func (o RunOptions) Start() terminal.Start {
	switch {
	case o.Continue:
		return terminal.Start{Mode: terminal.StartLatest}
	case o.Session != "":
		return terminal.Start{Mode: terminal.StartID, ID: message.SessionID(o.Session)}
	}
	return terminal.Start{}
}

// OutputFormat is how the run command writes a run: OutputText or
// OutputJSON.
type OutputFormat string

const (
	// OutputText writes the run's final answer once the run has finished,
	// and nothing else.
	OutputText OutputFormat = "text"
	// OutputJSON writes each event of the run on a line of its own, as the
	// JSON the HTTP API streams it in, with its type first.
	OutputJSON OutputFormat = "json"
)

// MarshalText returns f as text.
func (f OutputFormat) MarshalText() ([]byte, error) {
	return []byte(f), nil
}

// UnmarshalText sets f to the format text names, in any case.
func (f *OutputFormat) UnmarshalText(text []byte) error {
	name := OutputFormat(strings.ToLower(strings.TrimSpace(string(text))))
	if name != OutputText && name != OutputJSON {
		return fmt.Errorf("unknown format %q; use text or json", text)
	}
	*f = name
	return nil
}

// errNoPrompt is the error of a run given no prompt.
var errNoPrompt = errors.New("no prompt: pass it as arguments or on standard input")

// errStopped is the error of a run that a signal stopped.
var errStopped = errors.New("stopped")

// runCmd sends one prompt without the terminal UI, as flow --print does.
var runCmd = &cmdline.Command[RunOptions]{
	Name:  "run",
	Usage: "[prompt...]",
	Short: "Send a prompt, print the final answer and exit, as flow -p does",
	Flags: func(fs *flag.FlagSet, o *RunOptions) {
		modelFlags(fs, &o.modelOptions)
		const continueUsage = "Send the prompt to the most recent session of this project"
		fs.BoolVar(&o.Continue, "continue", false, continueUsage)
		fs.BoolVar(&o.Continue, "c", false, continueUsage)
		fs.StringVar(&o.Session, "session", "", "Send the prompt to the session with this `id`, in its own folder")
		dirFlag(fs, &o.Dir)
		printFlags(fs, o)
	},
	Exclusive: [][]string{{"continue", "session"}, {"no-session", "continue"}, {"no-session", "session"}, {"dir", "session"}},
	Args:      cmdline.AnyArgs,
	Run:       printRun,
}

// printFlags binds the flags that shape what a run prints, which the run
// command and flow --print take, to o.
func printFlags(fs *flag.FlagSet, o *RunOptions) {
	fs.TextVar(&o.Format, "format", OutputText, "Output `format` of a printed run: text, the final answer, or json, an event per line")
	const outputUsage = "Also write the final answer of a printed run to this `file`"
	fs.StringVar(&o.Output, "output", "", outputUsage)
	fs.StringVar(&o.Output, "o", "", outputUsage)
	fs.BoolVar(&o.NoSession, "no-session", false, "Keep no session of a printed run: delete it once the run ends")
}

// dirFlag binds --dir, the folder a command works in, to dir.
func dirFlag(fs *flag.FlagSet, dir *string) {
	fs.StringVar(dir, "dir", "", "Work in this `folder`, as though flow started there")
}

// enterDir makes dir, which --dir names, the working directory, so that the
// app works in its project. An empty dir leaves the working directory as it
// is.
func enterDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("--dir: %w", err)
	}
	return nil
}

// printRun sends the prompt that args and piped standard input hold, as
// promptText reads it, to the session that o chooses, in the folder that o
// names, and prints the run on standard output, as runPrompt does, until
// the run ends or a signal stops it. It fails before the app starts when
// there is no prompt or no such folder.
func printRun(ctx context.Context, o RunOptions, args []string) error {
	prompt, err := promptText(args, pipedInput(os.Stdin))
	if err != nil {
		return err
	}
	if err := enterDir(o.Dir); err != nil {
		return err
	}
	// A signal stops the run, as the app's work in progress.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	options := func(o RunOptions) app.Options { return app.Options{Session: message.SessionID(o.Session)} }
	return withAppOptions(options, func(ctx context.Context, w io.Writer, a *app.App, o RunOptions, _ []string) error {
		return runPrompt(ctx, w, a, o, prompt)
	})(ctx, o, args)
}

// pipedInput returns f when it is a pipe or a file, as standard input is
// when a command pipes into flow, and nil when it is a terminal or another
// device, such as /dev/null, which holds no prompt.
func pipedInput(f *os.File) io.Reader {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return f
}

// promptText returns the prompt of the run command: args joined by spaces,
// followed, a blank line below, by all that stdin holds when it is not nil,
// such as a file or the output of another command. It fails with
// errNoPrompt when the prompt is blank.
func promptText(args []string, stdin io.Reader) (string, error) {
	pieces := []string{strings.Join(args, " ")}
	if stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read the prompt from standard input: %w", err)
		}
		pieces = append(pieces, string(data))
	}
	pieces = slices.DeleteFunc(pieces, func(p string) bool { return strings.TrimSpace(p) == "" })
	if len(pieces) == 0 {
		return "", errNoPrompt
	}
	return strings.Join(pieces, "\n\n"), nil
}

// runPrompt sends prompt to the session that o chooses, with the model
// that o picks, or else the one the session last ran, or else the default
// model, and writes the run to w in o's format: the final answer once the
// run has finished, or every event as it comes. It writes the final answer
// to o's output file too. It fails with the run's failure, and with
// errStopped when ctx ends first, and then writes no answer. With
// NoSession, it deletes the run's new session once the run has ended,
// however it ended.
func runPrompt(ctx context.Context, w io.Writer, a *app.App, o RunOptions, prompt string) (err error) {
	id, err := o.Start().FirstSession(ctx, a.Sessions())
	if err != nil {
		return err
	}
	if id == "" {
		id = message.NewSessionID()
	}
	if o.NoSession {
		defer func() { err = errors.Join(err, forgetSession(ctx, a, id)) }()
	}
	sess := a.Session(id)
	model, err := runModel(ctx, a, o.modelOptions, sess)
	if err != nil {
		return err
	}
	p := NewRunPrinter(w, o.Format)
	var runErr error
	for ev, err := range sess.Stream(app.SendInput{Text: prompt, Model: model}) {
		if err != nil {
			runErr = err
			break
		}
		// Leaving the loop stops the run.
		if err := p.Print(ev); err != nil {
			return err
		}
	}
	switch {
	case runErr != nil:
		return runErr
	case ctx.Err() != nil:
		return errStopped
	}
	if o.Format == OutputText {
		if err := p.WriteAnswer(); err != nil {
			return err
		}
	}
	if o.Output != "" {
		return os.WriteFile(o.Output, []byte(p.Answer), 0o644)
	}
	return nil
}

// forgetSession deletes session id of a, a new session that a run has ended
// on, with its messages, even once ctx has ended, as when a signal stopped
// the run. A session the run never stored is no error.
func forgetSession(ctx context.Context, a *app.App, id message.SessionID) error {
	err := a.Sessions().DeleteSession(context.WithoutCancel(ctx), id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// runModel returns the model variant that o picks, as selectModel picks it,
// or else the one that sess last ran, as its newest message that names a
// model records it, or else "", for the default model.
func runModel(ctx context.Context, a *app.App, o modelOptions, sess app.Session) (string, error) {
	model, err := selectModel(ctx, a, o)
	if err != nil || model != "" {
		return model, err
	}
	msgs, err := sess.Messages()
	if err != nil {
		return "", err
	}
	for _, m := range slices.Backward(msgs) {
		if m.Model != "" {
			return agent.ParseVariantAt(m.Provider, m.Model, a.Models().Catalog()).String(), nil
		}
	}
	return "", nil
}

// RunPrinter writes the events of a run to W in Format, as the run command
// shows them, and keeps the run's answer.
type RunPrinter struct {
	// W is where the run is written.
	W io.Writer
	// Format is how it is written.
	Format OutputFormat
	// Answer is the text of the run's last agent message that has any,
	// once that message has ended: the final answer, once the run has
	// finished.
	Answer string
}

// NewRunPrinter returns a RunPrinter that writes to w in format.
func NewRunPrinter(w io.Writer, format OutputFormat) *RunPrinter {
	return &RunPrinter{W: w, Format: format}
}

// Print records what the answer needs of ev, and, in OutputJSON, writes it
// as PrintJSON does. In OutputText it writes nothing: WriteAnswer writes
// the answer once the run has finished.
func (p *RunPrinter) Print(ev app.Event) error {
	if e, ok := ev.(app.EventMessageEnded); ok && e.Message.Role == message.RoleAgent {
		if text := messageText(e.Message.Parts); strings.TrimSpace(text) != "" {
			p.Answer = text
		}
	}
	if p.Format == OutputJSON {
		return p.PrintJSON(ev)
	}
	return nil
}

// WriteAnswer writes the answer, ending with a line break, or nothing when
// the run has none.
func (p *RunPrinter) WriteAnswer() error {
	if p.Answer == "" {
		return nil
	}
	text := p.Answer
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, err := io.WriteString(p.W, text)
	return err
}

// PrintJSON writes ev on a line of its own, as the JSON object that the
// HTTP API streams, with a "type" member, its EventType, first.
func (p *RunPrinter) PrintJSON(ev app.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	typ, err := json.Marshal(ev.EventType())
	if err != nil {
		return err
	}
	line := append([]byte(`{"type":`), typ...)
	if rest := data[1:]; len(rest) > 1 {
		line = append(append(line, ','), rest...)
	} else {
		line = append(line, '}')
	}
	_, err = p.W.Write(append(line, '\n'))
	return err
}

// messageText returns the text parts of parts, joined by blank lines.
func messageText(parts []message.ContentPart) string {
	var texts []string
	for _, part := range parts {
		if tp, ok := part.(message.TextPart); ok && tp.Text != "" {
			texts = append(texts, tp.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}
