package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
	fluidterm "github.com/unaquatic/fluid/terminal"
	"golang.org/x/term"
)

// rootOptions are the flags of the root command itself.
type rootOptions struct {
	modelOptions
	version bool
	// color is the color mode the terminal UI and the resume command draw
	// in.
	color colorFlag

	// resume, cont and session choose the session the terminal opens with.
	// The root command allows at most one of them.
	resume  bool
	cont    bool
	session string
	// dir is the folder flow works in, in place of the one it started in,
	// or empty.
	dir string

	// print sends the prompt without the terminal UI, prints the final
	// answer and exits, as the run command does, with the flags of a
	// printed run that printed holds, as printFlags binds them.
	print   bool
	printed RunOptions
}

// runOptions returns the options of the run that --print starts: o's
// model, session and folder flags, and the flags of a printed run that
// printed holds.
func (o rootOptions) runOptions() RunOptions {
	r := o.printed
	r.modelOptions = o.modelOptions
	r.Continue = o.cont
	r.Session = o.session
	r.Dir = o.dir
	return r
}

// start returns the session the flags in o choose for the terminal to open
// with.
func (o rootOptions) start() terminal.Start {
	switch {
	case o.resume:
		return terminal.Start{Mode: terminal.StartPick}
	case o.cont:
		return terminal.Start{Mode: terminal.StartLatest}
	case o.session != "":
		return terminal.Start{Mode: terminal.StartID, ID: message.SessionID(o.session)}
	}
	return terminal.Start{}
}

// sessionFlags binds the flags that choose the session the terminal opens
// with.
func sessionFlags(fs *flag.FlagSet, o *rootOptions) {
	const resumeUsage = "Pick a session to resume, from any folder"
	fs.BoolVar(&o.resume, "resume", false, resumeUsage)
	fs.BoolVar(&o.resume, "r", false, resumeUsage)
	const continueUsage = "Continue the most recent session of this project"
	fs.BoolVar(&o.cont, "continue", false, continueUsage)
	fs.BoolVar(&o.cont, "c", false, continueUsage)
	fs.StringVar(&o.session, "session", "", "Resume the session with this `id`, in its own folder")
}

// errNeedsPrint is the error of a flag that shapes a printed run, given to
// the root command without --print.
var errNeedsPrint = errors.New("the flags of a printed run, such as --format, --output, --tools and --file, need --print")

// errResumePrompt is the error of a prompt given to the root command with
// --resume, whose session list chooses the session the prompt would go to.
var errResumePrompt = errors.New("--resume takes no prompt: pick the session, then send it")

// rootCmd is named after the application.
var rootCmd = &cmdline.Command[rootOptions]{
	Name:        product.Name,
	Usage:       "[prompt...]",
	Banner:      banner,
	ColorBanner: colorBanner,
	Env:         envVars(),
	Flags: func(fs *flag.FlagSet, o *rootOptions) {
		modelFlags(fs, &o.modelOptions)
		fs.BoolVar(&o.version, "version", false, "Show the version")
		colorFlags(fs, &o.color)
		sessionFlags(fs, o)
		dirFlag(fs, &o.dir)
		const printUsage = "Send the prompt, print the final answer and exit, without the terminal UI"
		fs.BoolVar(&o.print, "print", false, printUsage)
		fs.BoolVar(&o.print, "p", false, printUsage)
		printFlags(fs, &o.printed)
	},
	Exclusive: append([][]string{{"resume", "continue", "session"}, {"print", "resume"}, {"dir", "session"}}, printExclusive...),
	Args:      cmdline.AnyArgs,
	Run: func(ctx context.Context, o rootOptions, args []string) error {
		// The version needs no app.
		if o.version {
			printVersion(os.Stdout)
			return nil
		}
		if o.print {
			return printRun(ctx, o.runOptions(), args)
		}
		if o.printed.PrintOnly() {
			return errNeedsPrint
		}
		prompt := strings.TrimSpace(strings.Join(args, " "))
		if o.resume && prompt != "" {
			return errResumePrompt
		}
		if err := enterDir(o.dir); err != nil {
			return err
		}
		// The UI closes on a session of another folder to have flow
		// started again on it, in that folder, once this app has closed.
		var next message.SessionID
		err := withAppOptions(rootAppOptions, func(ctx context.Context, _ io.Writer, a *app.App, o rootOptions, _ []string) error {
			var err error
			next, err = runTerminal(ctx, a, o, prompt)
			return err
		})(ctx, o, args)
		if err != nil || next == "" {
			return err
		}
		return relaunch(relaunchArgs(o, next))
	},
	Commands: []cmdline.Node{
		runCmd,
		sessionCmd,
		modelsCmd,
		exportCmd,
		versionCmd,
		updateCmd,
		loginCmd,
		logoutCmd,
		accountCmd,
		serveCmd,
		demoCmd,
		debugCmd,
	},
}

// envVars returns config.EnvVars as help lists them.
func envVars() []cmdline.EnvVar {
	var vars []cmdline.EnvVar
	for _, v := range config.EnvVars() {
		vars = append(vars, cmdline.EnvVar(v))
	}
	return vars
}

// rootAppOptions returns the app options of the root command's flags: a
// session that --session names makes the app work in that session's folder.
func rootAppOptions(o rootOptions) app.Options {
	return app.Options{Session: message.SessionID(o.session)}
}

// runTerminal runs the terminal UI of a on the session that o chooses, with
// the model and in the color mode of o, and sends prompt there once it is
// open, unless prompt is empty. The UI draws on the terminal itself
// and leaves the command that resumes the session it closes on. It tells
// of the update that installed this release, at its first start, as
// justUpdated finds it, and installs a newer one meanwhile, as autoUpdate
// does. It returns the session of another folder that the UI closed to
// reopen there, or "".
func runTerminal(ctx context.Context, a *app.App, o rootOptions, prompt string) (message.SessionID, error) {
	start := o.start()
	start.Prompt = prompt
	model, err := selectModel(ctx, a, o.modelOptions)
	if err != nil {
		return "", err
	}
	start.Model = model
	cfg, err := a.Config()
	if err != nil {
		return "", err
	}
	start.Updated = justUpdated(cfg)
	autoUpdate(ctx, cfg)
	mode := o.color.mode()
	start.ColorMode = &mode
	start.NoColor = !cmdline.UseColor(os.Stdout)
	p := terminal.New(a, start, views(ctx, a)...)
	if err := p.Run(ctx); err != nil {
		return "", err
	}
	return p.Relaunch(), nil
}

// relaunchArgs returns the arguments that start flow again on session id,
// as --session opens it in its own folder, in the color mode that o's
// --color chose, if any.
func relaunchArgs(o rootOptions, id message.SessionID) []string {
	args := []string{"--session", string(id)}
	if o.color != "" && o.color != colorAuto {
		args = append(args, "--color", string(o.color))
	}
	return args
}

// stderr is where a command writes what is for people alone while it writes
// JSON to standard output, such as the steps of a login. Tests replace it.
var stderr io.Writer = os.Stderr

// colorMode is the color mode of the terminal, as the terminal UI detects it,
// in which the command line draws its oranges as near as it can unless
// --color names another.
var colorMode = fluidterm.DetectColorMode()

// banner is terminal.LogoBanner as plain text, which help shows where colors
// are not shown.
var banner = terminal.LogoBanner().PlainText()

// colorBanner is terminal.LogoBanner in colorMode, which help shows.
var colorBanner = terminal.ANSI(terminal.LogoBanner(), colorMode)

// highlight returns s, a value the user may copy, such as an account ID, a
// URL or a code, in product.PrimaryColor as terminal.ANSI draws it in
// colorMode when color is set, and as it is otherwise.
func highlight(s string, color bool) string {
	if !color || s == "" {
		return s
	}
	primary := style.New().WithFg(style.ColorFromHex(product.PrimaryColor)).Func()
	return terminal.ANSI(style.NewStyledText(primary(s)), colorMode)
}

// showsColor reports whether w shows colors: it is a terminal, as
// cmdline.UseColor tells.
func showsColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && cmdline.UseColor(f)
}

// highlighter returns the func that highlights a value as highlight does,
// in color where w shows colors, as showsColor tells.
func highlighter(w io.Writer) func(string) string {
	color := showsColor(w)
	return func(s string) string { return highlight(s, color) }
}

// introLine returns text, a line that introduces what follows it, bold as
// ui.IntroLine draws the line that introduces the resume command, when
// color is set, and as it is otherwise.
func introLine(text string, color bool) string {
	if !color {
		return text
	}
	return terminal.ANSI(ui.IntroLine(text, style.ColorTransparent), colorMode)
}

// liveLine is a line that a terminal shows while a step runs, such as
// "Checking for updates...", and rewrites in place as the step goes on,
// until what follows clears it or keeps it. Where w is no terminal, or one
// that cannot rewrite a line, it shows nothing, so that a log holds only
// what follows.
type liveLine struct {
	w     io.Writer
	live  bool
	shown bool
}

// newLiveLine returns a liveLine on w, live where w is a terminal whose
// TERM is not dumb.
func newLiveLine(w io.Writer) *liveLine {
	f, ok := w.(*os.File)
	return &liveLine{w: w, live: ok && term.IsTerminal(int(f.Fd())) && os.Getenv("TERM") != "dumb"}
}

// Show shows text on l, in place of what it showed.
func (l *liveLine) Show(text string) {
	if !l.live {
		return
	}
	fmt.Fprint(l.w, "\r\x1b[2K"+text)
	l.shown = true
}

// Clear removes what l shows, leaving the cursor where it began.
func (l *liveLine) Clear() {
	if l.shown {
		fmt.Fprint(l.w, "\r\x1b[2K")
		l.shown = false
	}
}

// Keep leaves what l shows on its line, and ends the line.
func (l *liveLine) Keep() {
	if l.shown {
		fmt.Fprintln(l.w)
		l.shown = false
	}
}

// writeField writes to w label, such as "Enter this code in your browser:",
// as introLine draws it, and under it value, indented by ui.ResumeIndent
// and highlighted, as the resume command is laid out, in colors where w
// shows them.
func writeField(w io.Writer, label, value string) {
	color := showsColor(w)
	writeFieldLine(w, introLine(label, color), highlight(value, color))
}

// writeURLField writes to w label, such as "Open this URL in your browser:",
// and under it url, as writeField does, but leaves url in the terminal's
// own color, as a terminal shows the links it finds.
func writeURLField(w io.Writer, label, url string) {
	writeFieldLine(w, introLine(label, showsColor(w)), url)
}

// writeFieldLine writes to w label, and under it value, indented by
// ui.ResumeIndent, both as they are drawn.
func writeFieldLine(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s\n%s%s\n", label, strings.Repeat(" ", ui.ResumeIndent), value)
}

// printVersion writes the name and version of the application to w, such
// as "flow v1.2.3".
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", product.Name, product.Version())
}
