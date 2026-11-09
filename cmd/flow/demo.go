package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/unaquatic/flow/extension/demo"
	demoui "github.com/unaquatic/flow/extension/demo/ui"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/update"
)

// demoCmd shows the output of flow's commands, as the terminal's /demo
// shows its UI. flow's help does not list it.
var demoCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:     "demo",
	Short:    "Show demos of what flow's commands print",
	Commands: []cmdline.Node{demoUpdateCmd},
	Hidden:   true,
}

var demoUpdateCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "update",
	Usage: "[" + strings.Join(updateDemoNames(), "|") + "]",
	Short: "Show what flow update prints in each case, without checking or installing",
	Args:  updateDemoArgs,
	Run: func(ctx context.Context, _ cmdline.NoOptions, args []string) error {
		return runDemoUpdate(ctx, os.Stdout, cmdline.UseColor(os.Stdout), args)
	},
}

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

// updateDemos are the cases of flow update, in the order flow demo update
// shows them.
var updateDemos = []updateDemo{
	{name: "latest", desc: "Up to date", current: demoui.UpdateVersion},
	{name: "check", desc: "A newer release, only checked for", current: demoOlder, opts: updateOptions{check: true}},
	{name: "install", desc: "A newer release, installed with npm", current: demoOlder},
	{name: "readonly", desc: "A newer release, into a global folder the user cannot write to", current: demoOlder, install: demoReadOnly},
	{name: "unmanaged", desc: "A newer release, for a flow no package manager installed", current: demoOlder, install: demoUnmanaged},
	{name: "devel", desc: "A development build", current: "(devel)"},
}

// updateDemoNames returns the names of updateDemos, in order.
func updateDemoNames() []string {
	names := make([]string, 0, len(updateDemos))
	for _, d := range updateDemos {
		names = append(names, d.name)
	}
	return names
}

// updateDemoArgs accepts no argument, for every case, or the name of one
// of updateDemos.
func updateDemoArgs(args []string) error {
	switch {
	case len(args) > 1:
		return fmt.Errorf("takes at most one case, got %d", len(args))
	case len(args) == 1 && !slices.Contains(updateDemoNames(), args[0]):
		return fmt.Errorf("no case %q, want one of %s", args[0], strings.Join(updateDemoNames(), ", "))
	}
	return nil
}

// runDemoUpdate writes to w what flow update prints in the case of
// updateDemos that names holds, or in each when names is empty: a comment
// that says what the case is, the command line, and then the output of the
// command itself, run against a canned registry, which names
// demoui.UpdateVersion the latest release, and a package manager that
// installs nothing, so that nothing is checked or installed. An error shows
// as the command line reports it, in colors when color is set.
func runDemoUpdate(ctx context.Context, w io.Writer, color bool, names []string) error {
	first := true
	for _, d := range updateDemos {
		if len(names) > 0 && !slices.Contains(names, d.name) {
			continue
		}
		if !first {
			fmt.Fprintln(w)
		}
		first = false
		if err := d.run(ctx, w, color); err != nil {
			return err
		}
	}
	return nil
}

// run writes the case d to w, as runDemoUpdate does.
func (d updateDemo) run(ctx context.Context, w io.Writer, color bool) error {
	line := product.Name + " update"
	switch {
	case d.opts.check:
		line += " --check"
	case d.opts.force:
		line += " --force"
	}
	fmt.Fprintf(w, "# %s\n$ %s\n", d.desc, line)

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
	if err := runUpdate(ctx, w, u, d.opts, demoInstall); err != nil {
		cmdline.Report(w, err, color)
	}
	return nil
}

// demoInstall stands in for the package manager that cmd runs, and writes
// what npm writes once it has updated a global package.
func demoInstall(cmd *exec.Cmd) error {
	_, err := fmt.Fprint(cmd.Stdout, "\nchanged 1 package in 2s\n")
	return err
}
