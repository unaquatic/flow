package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/fluid/style"
)

// TestPrintVersion checks that the logo follows a blank line, and the version
// follows the logo after another. With color, the logo is terminal.LogoBanner
// in the mode given; without, the mode is ignored.
func TestPrintVersion(t *testing.T) {
	version := "\n\n" + product.Name + " " + product.Version() + "\n"
	for _, tt := range []struct {
		name  string
		color bool
		want  string
	}{
		{name: "plain", want: "\n" + product.Logo + version},
		{name: "color", color: true, want: terminal.ANSI(terminal.LogoBanner(), style.Color256) + version},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			printVersion(&out, tt.color, style.Color256)
			if out.String() != tt.want {
				t.Errorf("version =\n%q\nwant\n%q", out.String(), tt.want)
			}
		})
	}
}

func TestRootOptionsStart(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want terminal.Start
	}{
		{name: "no flags"},
		{name: "resume", args: []string{"-resume"}, want: terminal.Start{Mode: terminal.StartPick}},
		{name: "resume alias", args: []string{"-r"}, want: terminal.Start{Mode: terminal.StartPick}},
		{name: "continue", args: []string{"--continue"}, want: terminal.Start{Mode: terminal.StartLatest}},
		{name: "continue alias", args: []string{"-c"}, want: terminal.Start{Mode: terminal.StartLatest}},
		{name: "session", args: []string{"--session", "ses_1"},
			want: terminal.Start{Mode: terminal.StartID, ID: "ses_1"}},
		{name: "repeated alias", args: []string{"-r", "-resume"}, want: terminal.Start{Mode: terminal.StartPick}},
		{name: "set to false", args: []string{"-r=false"}},
		{name: "last value wins", args: []string{"-r", "-resume=false"}},
		{name: "last session wins", args: []string{"--session", "ses_1", "--session", "ses_2"},
			want: terminal.Start{Mode: terminal.StartID, ID: "ses_2"}},
		{name: "empty session", args: []string{"--session="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o rootOptions
			fs := flag.NewFlagSet("flow", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			sessionFlags(fs, &o)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse %q: %v", tt.args, err)
			}
			if got := o.start(); got != tt.want {
				t.Errorf("start of %q = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

// TestRootExclusiveSessionFlags runs the root command with session flags that
// conflict. The command fails before Run, so no app starts.
func TestRootExclusiveSessionFlags(t *testing.T) {
	const want = "flow: use only one of --resume, --continue and --session"
	for _, args := range [][]string{
		{"-r", "-c"},
		{"-resume", "--session", "ses_1"},
		{"-c", "-r", "-r=false", "--session", "ses_1"},
	} {
		if err := rootCmd.Execute(t.Context(), args); err == nil || err.Error() != want {
			t.Errorf("execute %q: err = %v, want %q", args, err, want)
		}
	}
}
