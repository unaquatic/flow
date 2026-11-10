package main

import (
	"flag"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/fluid/style"
)

// TestPrintVersion checks that the version is the name and the version of
// the application alone, on one line, with no logo.
func TestPrintVersion(t *testing.T) {
	var out strings.Builder
	printVersion(&out)
	if got, want := out.String(), product.Name+" "+product.Version()+"\n"; got != want {
		t.Errorf("version = %q, want %q", got, want)
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

// TestRelaunchArgs checks the arguments that start flow again on a session
// of another folder: the session, which --session opens in its folder, and
// the color mode only when --color chose one, not the session flags that
// chose the session left, and that the root command reads them back.
func TestRelaunchArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "plain", want: []string{"--session", "ses_2"}},
		{name: "auto color", args: []string{"--color", "auto"}, want: []string{"--session", "ses_2"}},
		{name: "chosen color", args: []string{"--color", "256"}, want: []string{"--session", "ses_2", "--color", "256"}},
		{name: "from the list", args: []string{"-r"}, want: []string{"--session", "ses_2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o rootOptions
			fs := flag.NewFlagSet("flow", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			colorFlags(fs, &o.color)
			sessionFlags(fs, &o)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse %q: %v", tt.args, err)
			}
			got := relaunchArgs(o, "ses_2")
			if !slices.Equal(got, tt.want) {
				t.Fatalf("relaunch args = %q, want %q", got, tt.want)
			}

			var again rootOptions
			fs = flag.NewFlagSet("flow", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			colorFlags(fs, &again.color)
			sessionFlags(fs, &again)
			if err := fs.Parse(got); err != nil {
				t.Fatalf("parse the relaunch args %q: %v", got, err)
			}
			if start := again.start(); start.Mode != terminal.StartID || start.ID != "ses_2" {
				t.Errorf("relaunched start = %+v, want session ses_2", start)
			}
			if rootAppOptions(again).Session != "ses_2" {
				t.Errorf("relaunched app options = %+v, want them on session ses_2", rootAppOptions(again))
			}
		})
	}
}

// TestHighlight checks that a value shows in the primary color where colors
// are shown, as it is elsewhere, and that a writer that is no terminal gets
// it as it is.
func TestHighlight(t *testing.T) {
	const id = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e01"
	if got := highlight(id, false); got != id {
		t.Errorf("highlight without color = %q, want %q", got, id)
	}
	primary := style.New().WithFg(style.ColorFromHex(product.PrimaryColor)).Func()
	if got, want := highlight(id, true), terminal.ANSI(style.NewStyledText(primary(id)), colorMode); got != want || !strings.Contains(got, "\x1b[") {
		t.Errorf("highlight with color = %q, want %q", got, want)
	}
	if got := highlight("", true); got != "" {
		t.Errorf("highlight of nothing = %q, want it empty", got)
	}
	if got := highlighter(io.Discard)(id); got != id {
		t.Errorf("highlighter of a writer that is no terminal = %q, want %q", got, id)
	}
}

// TestWriteField checks that a field is its label on a line, bold where
// colors are shown, and its value under it, indented as the resume command
// is, and that a writer that is no terminal gets them plain.
func TestWriteField(t *testing.T) {
	var out strings.Builder
	writeField(&out, "Open this URL in your browser:", "https://login.example/device")
	if got, want := out.String(), "Open this URL in your browser:\n  https://login.example/device\n"; got != want {
		t.Errorf("field = %q, want %q", got, want)
	}
	if got := introLine("Waiting for authorization...", false); got != "Waiting for authorization..." {
		t.Errorf("intro line without color = %q, want it plain", got)
	}
	if got := introLine("Waiting for authorization...", true); !strings.Contains(got, "\x1b[1m") || !strings.Contains(got, "Waiting for authorization...") {
		t.Errorf("intro line with color = %q, want it bold", got)
	}
}
