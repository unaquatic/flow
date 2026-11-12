package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/tools"
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
	// Name is the title the run gives its session, or empty to leave the
	// title to the session's titling.
	Name string
	// NoContextFiles leaves the AGENTS.md files out of the system prompt
	// of the run's new session.
	NoContextFiles bool
	// Instructions is the text of the instructions section of the system
	// prompt of the run's new session, in place of the configuration's
	// agent.instructions, or empty to keep it.
	Instructions string
	// MaxSteps limits the model responses of the run, or is zero for no
	// limit.
	MaxSteps int
	// Tools names the built-in tools the run offers the model, or patterns
	// of them, in place of the default ones, as tools.Selection's Only
	// does: nil offers the default ones, and an empty list none.
	Tools []string
	// ExcludeTools names the tools, or patterns of them, the run leaves
	// out of those it offers.
	ExcludeTools []string
	// Files are the paths of the files the run attaches to its prompt, as
	// attachFiles reads them.
	Files []string
}

// PrintOnly reports whether o sets a flag that shapes a printed run alone,
// which flow takes only with --print.
func (o RunOptions) PrintOnly() bool {
	return o.Format != OutputText || o.Output != "" || o.NoSession || o.Name != "" ||
		o.NoContextFiles || o.Instructions != "" || o.MaxSteps != 0 ||
		o.Tools != nil || o.ExcludeTools != nil || o.Files != nil
}

// AppOptions returns the options of the app the run starts on: in the
// folder of the session that Session names, with the instructions, step
// limit and tools that o's flags choose.
func (o RunOptions) AppOptions() app.Options {
	sel := tools.Selection{Only: o.Tools, Exclude: o.ExcludeTools}
	return app.Options{
		Session:            message.SessionID(o.Session),
		Extensions:         extensionsWith(sel.Load),
		NoInstructionFiles: o.NoContextFiles,
		Instructions:       o.Instructions,
		MaxSteps:           o.MaxSteps,
	}
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

// OutputFormat is how a command writes what it prints: OutputText or
// OutputJSON.
type OutputFormat string

const (
	// OutputText writes text for people: a run's final answer, once the
	// run has finished, and nothing else, or a list a row per item.
	OutputText OutputFormat = "text"
	// OutputJSON writes JSON for scripts: each event of a run on a line of
	// its own, as the HTTP API streams it, with its type first, or a list
	// as the HTTP API writes it.
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
		return fmt.Errorf("unknown format: %s; use text or json", text)
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
	Exclusive: append([][]string{{"continue", "session"}, {"dir", "session"}}, printExclusive...),
	Args:      cmdline.AnyArgs,
	Run:       printRun,
}

// printExclusive are the groups of flags of a printed run, as printFlags
// binds them, of which at most one may be given with the flags that choose
// a stored session. --no-session deletes the run's session, which must so
// be new and needs no title. The system prompt is that of a session's
// start, which a stored session has had already. The history of a stored
// session may hold calls of tools that --tools or --exclude-tools would
// leave out, which some providers refuse to take without them.
var printExclusive = [][]string{
	{"no-session", "continue"}, {"no-session", "session"}, {"no-session", "name"},
	{"instructions", "continue"}, {"instructions", "session"},
	{"no-context-files", "continue"}, {"no-context-files", "session"},
	{"tools", "continue"}, {"tools", "session"},
	{"exclude-tools", "continue"}, {"exclude-tools", "session"},
}

// printFlags binds the flags that shape what a run prints, which the run
// command and flow --print take, to o.
func printFlags(fs *flag.FlagSet, o *RunOptions) {
	fs.TextVar(&o.Format, "format", OutputText, "Output `format` of a printed run: text, the final answer, or json, an event per line")
	const outputUsage = "Also write the final answer of a printed run to this `file`"
	fs.StringVar(&o.Output, "output", "", outputUsage)
	fs.StringVar(&o.Output, "o", "", outputUsage)
	fs.BoolVar(&o.NoSession, "no-session", false, "Keep no session of a printed run: delete it once the run ends")
	const nameUsage = "Title the session of a printed run"
	fs.StringVar(&o.Name, "name", "", nameUsage)
	fs.StringVar(&o.Name, "n", "", nameUsage)
	fs.BoolVar(&o.NoContextFiles, "no-context-files", false, "Leave the AGENTS.md files out of the system prompt of a printed run")
	fs.StringVar(&o.Instructions, "instructions", "", "Instructions `text` of a printed run, in place of the configuration's agent.instructions")
	fs.Var(positiveValue{n: &o.MaxSteps}, "max-steps", "Most model responses a printed run makes, `n` (default no limit)")
	fs.Var(listValue{list: &o.Tools}, "tools",
		"Built-in tools a printed run offers, in place of the default ones: comma-separated `names` or patterns, such as read,grep, or \"\" for none")
	fs.Var(listValue{list: &o.ExcludeTools}, "exclude-tools", "Tools a printed run leaves out: comma-separated `names` or patterns, such as bash,write")
	const fileUsage = "Attach this `file` to the prompt of a printed run: an image, a PDF or a text file (repeatable)"
	fs.Var(pathsValue{paths: &o.Files}, "file", fileUsage)
	fs.Var(pathsValue{paths: &o.Files}, "f", fileUsage)
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
// promptText reads it, with the files that o attaches, as attachFiles reads
// them, to the session that o chooses, in the folder that o names, on an
// app with the options o's AppOptions gives, and prints the run on standard
// output, as runPrompt does, until the run ends or a signal stops it. It
// fails before the app starts when --tools or --exclude-tools names no
// built-in tool, as checkTools tells, there is no prompt, a file cannot be
// attached, or there is no such folder. The paths of the files are those
// of the folder flow started in, as a shell completes them.
func printRun(ctx context.Context, o RunOptions, args []string) error {
	if err := checkTools(o); err != nil {
		return err
	}
	prompt, err := promptText(args, pipedInput(os.Stdin))
	if err != nil {
		return err
	}
	files, err := attachFiles(o.Files)
	if err != nil {
		return err
	}
	if err := enterDir(o.Dir); err != nil {
		return err
	}
	// A signal stops the run, as the app's work in progress.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return withAppOptions(RunOptions.AppOptions, func(ctx context.Context, w io.Writer, a *app.App, o RunOptions, _ []string) error {
		return runPrompt(ctx, w, a, o, prompt, files...)
	})(ctx, o, args)
}

// checkTools fails, naming the flag, when a name or pattern that o's
// --tools or --exclude-tools gives matches no built-in tool, or is
// malformed, as tools.Matcher tells.
func checkTools(o RunOptions) error {
	all := tools.All()
	if _, err := tools.Matcher(all, o.Tools); err != nil {
		return fmt.Errorf("--tools: %w", err)
	}
	if _, err := tools.Matcher(all, o.ExcludeTools); err != nil {
		return fmt.Errorf("--exclude-tools: %w", err)
	}
	return nil
}

// attachFiles returns the parts that attach the files at paths to a
// prompt, in order, each with a new part ID: an image, of a type every
// request format takes, or a PDF, as a message.FilePart of its media type
// and base name, and a text file, UTF-8 without NUL bytes, as a
// message.TextPart that holds its content in a <file> element naming its
// absolute path, which holds wherever the run works. It fails, naming the
// path, for a file that cannot be read or is none of these.
func attachFiles(paths []string) ([]message.ContentPart, error) {
	var parts []message.ContentPart
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--file: %w", err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("--file: %w", err)
		}
		switch mediaType, _, _ := strings.Cut(http.DetectContentType(data), ";"); {
		case slices.Contains(attachedTypes, mediaType):
			parts = append(parts, message.FilePart{
				ID:        message.NewPartID(),
				Data:      base64.StdEncoding.EncodeToString(data),
				MediaType: mediaType,
				Filename:  filepath.Base(path),
			})
		case utf8.Valid(data) && !bytes.ContainsRune(data, 0):
			// The path is written as it is, not Go-quoted, which would double
			// the backslashes of a Windows path.
			text := fmt.Sprintf("<file path=\"%s\">\n%s\n</file>", strings.ReplaceAll(abs, `"`, "&quot;"), strings.TrimSuffix(string(data), "\n"))
			parts = append(parts, message.TextPart{ID: message.NewPartID(), Text: text})
		default:
			return nil, fmt.Errorf("--file %s: unsupported file type: %s; attach an image, a PDF or a text file", path, mediaType)
		}
	}
	return parts, nil
}

// attachedTypes are the media types, as http.DetectContentType names them,
// of the files attachFiles attaches whole: the images every request format
// takes, and PDFs.
var attachedTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf"}

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

// runPrompt sends prompt, followed by parts, such as the files attachFiles
// attaches, to the session that o chooses, with the model that o picks, or
// else the one the session last ran, or else the default model, and writes
// the run to w in o's format: the final answer once the
// run has finished, or every event as it comes. It writes the final answer
// to o's output file too. It fails with the run's failure, and with
// errStopped when ctx ends first, and then writes no answer, but for a run
// that the step limit ended: it writes the answer the run got to, and then
// fails with the limit, telling of --max-steps. With Name, it
// titles the session once the run has ended, unless a signal stopped it, or
// it stored nothing. With NoSession, it deletes the run's new session once
// the run has ended, however it ended.
func runPrompt(ctx context.Context, w io.Writer, a *app.App, o RunOptions, prompt string, parts ...message.ContentPart) (err error) {
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
	for ev, err := range sess.Stream(app.SendInput{Text: prompt, Parts: parts, Model: model}) {
		if err != nil {
			runErr = err
			break
		}
		// Leaving the loop stops the run.
		if err := p.Print(ev); err != nil {
			return err
		}
	}
	if ctx.Err() == nil {
		if err := nameSession(sess, o.Name); err != nil {
			return errors.Join(runErr, err)
		}
	}
	_, limited := errors.AsType[*agent.MaxStepsError](runErr)
	switch {
	case runErr != nil && !limited:
		return runErr
	case ctx.Err() != nil:
		return errStopped
	case limited:
		runErr = fmt.Errorf("%w; --max-steps raises the limit", runErr)
	}
	if o.Format == OutputText {
		if err := p.WriteAnswer(); err != nil {
			return errors.Join(runErr, err)
		}
	}
	if o.Output != "" {
		if err := os.WriteFile(o.Output, []byte(p.Answer), 0o644); err != nil {
			return errors.Join(runErr, err)
		}
	}
	return runErr
}

// nameSession titles sess name, unless name is empty. A session that is not
// stored, as one whose run failed before it stored its prompt, is no error.
func nameSession(sess app.Session, name string) error {
	if name == "" {
		return nil
	}
	if _, err := sess.SetTitle(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("--name: %w", err)
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
	line, err := typedLine(string(ev.EventType()), ev)
	if err != nil {
		return err
	}
	_, err = p.W.Write(line)
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
