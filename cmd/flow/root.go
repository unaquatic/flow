package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/fluid/style"
	fluidterm "github.com/unaquatic/fluid/terminal"
)

// rootOptions are the flags of the root command itself.
type rootOptions struct {
	modelOptions
	version bool
	// color is the color mode the terminal UI, the version and the resume
	// command draw in.
	color colorFlag

	// resume, cont and session choose the session the terminal opens with.
	// The root command allows at most one of them.
	resume  bool
	cont    bool
	session string
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
	const resumeUsage = "Pick a session of this project to resume"
	fs.BoolVar(&o.resume, "resume", false, resumeUsage)
	fs.BoolVar(&o.resume, "r", false, resumeUsage)
	const continueUsage = "Continue the most recent session of this project"
	fs.BoolVar(&o.cont, "continue", false, continueUsage)
	fs.BoolVar(&o.cont, "c", false, continueUsage)
	fs.StringVar(&o.session, "session", "", "Resume the session with this `id`")
}

// rootCmd is named after the application.
var rootCmd = &cmdline.Command[rootOptions]{
	Name:        product.Name,
	Banner:      banner,
	ColorBanner: colorBanner,
	Env:         envVars(),
	Flags: func(fs *flag.FlagSet, o *rootOptions) {
		modelFlags(fs, &o.modelOptions)
		fs.BoolVar(&o.version, "version", false, "Show the version")
		colorFlags(fs, &o.color)
		sessionFlags(fs, o)
	},
	Exclusive: [][]string{{"resume", "continue", "session"}},
	Run: func(ctx context.Context, o rootOptions, args []string) error {
		// The version needs no app.
		if o.version {
			printVersion(os.Stdout, cmdline.UseColor(os.Stdout), o.color.mode())
			return nil
		}
		return withApp(runTerminal)(ctx, o, args)
	},
	Commands: []cmdline.Node{
		versionCmd,
		loginCmd,
		logoutCmd,
		accountCmd,
		serveCmd,
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

// runTerminal runs the terminal UI of a on the session that o chooses, with
// the model and in the color mode of o. The UI draws on the terminal itself,
// not on w, and leaves the command that resumes the session it closes on.
func runTerminal(ctx context.Context, _ io.Writer, a *app.App, o rootOptions, _ []string) error {
	start := o.start()
	model, err := selectModel(ctx, a, o.modelOptions)
	if err != nil {
		return err
	}
	start.Model = model
	mode := o.color.mode()
	start.ColorMode = &mode
	start.NoColor = !cmdline.UseColor(os.Stdout)
	return terminal.New(a, start, views(ctx, a)...).Run(ctx)
}

// colorMode is the color mode of the terminal, as the terminal UI detects it,
// in which the command line draws its oranges as near as it can unless
// --color names another.
var colorMode = fluidterm.DetectColorMode()

// banner is terminal.LogoBanner as plain text, which help shows where colors
// are not shown.
var banner = terminal.LogoBanner().PlainText()

// colorBanner is terminal.LogoBanner in colorMode, which help shows.
var colorBanner = terminal.ANSI(terminal.LogoBanner(), colorMode)

// printVersion writes the banner to w, then a blank line and the name and
// version of the application. When color is true, the banner is
// terminal.LogoBanner in mode.
func printVersion(w io.Writer, color bool, mode style.ColorMode) {
	b := banner
	if color {
		b = terminal.ANSI(terminal.LogoBanner(), mode)
	}
	fmt.Fprintf(w, "%s\n\n%s %s\n", b, product.Name, product.Version())
}
