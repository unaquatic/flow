package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/demo"
	demoui "github.com/unaquatic/flow/extension/demo/ui"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/update"
)

// demoCmd shows the output of flow's commands, as the terminal's /demo
// shows its UI: a command below it for each of flow's, which shows what that
// command prints in each of its cases, without touching the user's install,
// accounts or network. flow's help does not list it.
var demoCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "demo",
	Short: "Show demos of what flow's commands print",
	Commands: []cmdline.Node{
		demoCommand("version", "Show what flow version prints", versionDemos),
		demoCommand("update", "Show what flow update prints in each case, without checking or installing", updateDemos()),
		demoCommand("login", "Show what flow login prints in each case, with demo providers", loginDemos),
		demoCommand("logout", "Show what flow logout prints in each case, with demo providers", logoutDemos),
		demoCommand("account", "Show what flow account prints in each case, with demo providers", accountDemos),
		demoCommand("serve", "Show what flow serve prints in each case, without listening", serveDemos),
	},
	Hidden: true,
}

// demoCase is a case of one of flow's commands that flow demo shows.
type demoCase struct {
	// name selects the case on the command line.
	name string
	// desc says what the case is.
	desc string
	// line is the command line of the case, after flow's name.
	line string
	// run writes to w what the command prints in the case, in colors when
	// color is set. The error it returns shows as the command line reports
	// it.
	run func(ctx context.Context, w io.Writer, color bool) error
}

// demoCommand returns the command of flow demo that shows cases, the cases
// of flow's command name, as runDemo shows them.
func demoCommand(name, short string, cases []demoCase) *cmdline.Command[cmdline.NoOptions] {
	return &cmdline.Command[cmdline.NoOptions]{
		Name:  name,
		Usage: "[" + strings.Join(demoNames(cases), "|") + "]",
		Short: short,
		Args:  func(args []string) error { return demoArgs(cases, args) },
		Run: func(ctx context.Context, _ cmdline.NoOptions, args []string) error {
			// The demo apps' logs would show among the output.
			slog.SetDefault(slog.New(slog.DiscardHandler))
			return runDemo(ctx, os.Stdout, cmdline.UseColor(os.Stdout), cases, args)
		},
	}
}

// demoNames returns the names of cases, in order.
func demoNames(cases []demoCase) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.name)
	}
	return names
}

// demoArgs accepts no argument, for every case, or the name of one of
// cases.
func demoArgs(cases []demoCase, args []string) error {
	names := demoNames(cases)
	switch {
	case len(args) > 1:
		return fmt.Errorf("takes at most one case, got %d", len(args))
	case len(args) == 1 && !slices.Contains(names, args[0]):
		return fmt.Errorf("no case %q, want one of %s", args[0], strings.Join(names, ", "))
	}
	return nil
}

// runDemo writes to w the cases of cases that names holds, or each when
// names is empty, a blank line apart: a comment that says what the case is,
// the command line, and then what the command prints, an error as the
// command line reports it, in colors when color is set.
func runDemo(ctx context.Context, w io.Writer, color bool, cases []demoCase, names []string) error {
	first := true
	for _, c := range cases {
		if len(names) > 0 && !slices.Contains(names, c.name) {
			continue
		}
		if !first {
			fmt.Fprintln(w)
		}
		first = false
		fmt.Fprintf(w, "# %s\n$ %s %s\n", c.desc, product.Name, c.line)
		if err := c.run(ctx, w, color); err != nil {
			cmdline.Report(w, err, color)
		}
	}
	return nil
}

// versionDemos are the cases of flow version.
var versionDemos = []demoCase{{
	name: "version",
	desc: "The banner and the version of the running flow",
	line: "version",
	run: func(_ context.Context, w io.Writer, color bool) error {
		printVersion(w, color, colorMode)
		return nil
	},
}}

// updateDemoInstall is where the flow of an update demo is installed.
type updateDemoInstall int

const (
	// demoGlobal is npm's global folder, which the user can write to.
	demoGlobal updateDemoInstall = iota
	// demoReadOnly is npm's global folder, which the user cannot write to.
	demoReadOnly
	// demoUnmanaged is a folder of no package manager's.
	demoUnmanaged
)

// demoGlobalRoot is the global folder of npm in the update demos.
const demoGlobalRoot = "/usr/local/lib/node_modules"

// updateDemo is a case of flow update that flow demo update shows.
type updateDemo struct {
	// name selects the case on the command line.
	name string
	// desc says what the case is.
	desc string
	// current is the version of the running flow.
	current string
	// opts are the flags of flow update.
	opts updateOptions
	// install is where the running flow is installed.
	install updateDemoInstall
}

// demoOlder is the version of the flow that the update demos find
// demoui.UpdateVersion newer than.
const demoOlder = "v1.2.2"

// updateDemoCases are the cases of flow update, in the order flow demo
// update shows them.
var updateDemoCases = []updateDemo{
	{name: "latest", desc: "Up to date", current: demoui.UpdateVersion},
	{name: "check", desc: "A newer release, only checked for", current: demoOlder, opts: updateOptions{check: true}},
	{name: "install", desc: "A newer release, installed with npm", current: demoOlder},
	{name: "readonly", desc: "A newer release, into a global folder the user cannot write to", current: demoOlder, install: demoReadOnly},
	{name: "unmanaged", desc: "A newer release, for a flow no package manager installed", current: demoOlder, install: demoUnmanaged},
	{name: "devel", desc: "A development build", current: "(devel)"},
}

// updateDemos returns updateDemoCases as the cases of flow demo update.
func updateDemos() []demoCase {
	cases := make([]demoCase, 0, len(updateDemoCases))
	for _, d := range updateDemoCases {
		line := "update"
		switch {
		case d.opts.check:
			line += " --check"
		case d.opts.force:
			line += " --force"
		}
		cases = append(cases, demoCase{name: d.name, desc: d.desc, line: line, run: d.run})
	}
	return cases
}

// run writes what flow update prints in the case d to w: the output of the
// command itself, run against a canned registry, which names
// demoui.UpdateVersion the latest release, and a package manager that
// installs nothing, so that nothing is checked or installed.
func (d updateDemo) run(ctx context.Context, w io.Writer, _ bool) error {
	dir, err := os.MkdirTemp("", product.ID+"-demo-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// The executable is in the folder as the package manager's own report
	// of it resolves, where it is a symbolic link on this machine.
	root := demoGlobalRoot
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	u := &update.Updater{
		Dir:     dir,
		Current: d.current,
		Client: &http.Client{Transport: demo.CannedReply{
			Status: http.StatusOK,
			Body:   fmt.Sprintf(`{"name":%q,"version":%q}`, update.PackageName, strings.TrimPrefix(demoui.UpdateVersion, "v")),
		}},
		Executable: filepath.Join(root, update.PackageName, "bin", product.Name),
		Output: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(root), nil
		},
		Writable: func(string) bool { return d.install != demoReadOnly },
	}
	if d.install == demoUnmanaged {
		u.Executable = filepath.Join("/usr/local/bin", product.Name)
	}
	return runUpdate(ctx, w, u, d.opts, demoInstall)
}

// demoInstall stands in for the package manager that cmd runs, and writes
// what npm writes once it has updated a global package.
func demoInstall(cmd *exec.Cmd) error {
	_, err := fmt.Fprint(cmd.Stdout, "\nchanged 1 package in 2s\n")
	return err
}

// serveDemos are the cases of flow serve. Each serves a demo app on a
// listener that accepts no connection, at the address the command names,
// and stops once the server says where it listens.
var serveDemos = []demoCase{
	{name: "local", desc: "Listening on this machine alone", line: "serve", run: serveDemo(serveOptions{port: demoPort})},
	{name: "exposed", desc: "Reachable from other machines, with no password", line: "serve --hostname 0.0.0.0", run: serveDemo(serveOptions{hostname: "0.0.0.0", port: demoPort})},
}

// demoPort is the port of the serve demos, flow serve's default.
const demoPort = 4096

// serveDemo returns the run of a case of flow serve with options o, as
// serveDemos runs them.
func serveDemo(o serveOptions) func(context.Context, io.Writer, bool) error {
	return withDemoApp(func(ctx context.Context, w io.Writer, a *app.App) error {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return serve(ctx, w, a, o, func(_, address string) (net.Listener, error) {
			addr, err := net.ResolveTCPAddr("tcp", address)
			if err != nil {
				return nil, err
			}
			return &demoListener{addr: addr, closed: make(chan struct{})}, nil
		})
	})
}

// demoListener is a net.Listener at addr that accepts no connection: Accept
// waits until it is closed.
type demoListener struct {
	addr   net.Addr
	closed chan struct{}
	once   sync.Once
}

// Accept waits until l is closed, and then fails with net.ErrClosed.
func (l *demoListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

// Close makes Accept return.
func (l *demoListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

// Addr returns the address l stands at.
func (l *demoListener) Addr() net.Addr { return l.addr }
