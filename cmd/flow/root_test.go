package main

import (
	"errors"
	"flag"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/fluid/style"
)

// TestPrintVersion checks that the version is the name and the version of
// the application and the platform it runs on, on one line, with no logo.
func TestPrintVersion(t *testing.T) {
	var out strings.Builder
	printVersion(&out)
	if got, want := out.String(), product.Name+" "+product.Version()+" ["+product.Platform()+"]\n"; got != want {
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

// TestRootResumePrompt checks that the root command refuses a prompt with
// --resume, whose session list has not picked the session it would go to.
// It fails before the app starts.
func TestRootResumePrompt(t *testing.T) {
	for _, args := range [][]string{{"-r", "fix", "the", "tests"}, {"--resume", "--", "fix the tests"}} {
		if err := rootCmd.Execute(t.Context(), args); !errors.Is(err, errResumePrompt) {
			t.Errorf("execute %q: err = %v, want %v", args, err, errResumePrompt)
		}
	}
}

// TestRootPrintFlags checks that --print takes the root command's model and
// session flags, and the format and output file of a printed run, which
// the run command takes too.
func TestRootPrintFlags(t *testing.T) {
	var o rootOptions
	fs := flag.NewFlagSet("flow", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootCmd.Flags(fs, &o)
	args := []string{
		"-p", "-m", "model-a", "--provider", "provider-a", "--format", "json", "-o", "answer.md", "--no-session", "--dir", "sub",
		"--no-context-files", "--instructions", "Be brief.", "--max-steps", "4", "--tools", "read,grep", "--exclude-tools", "grep",
		"-f", "a.png", "--file", "b, c.txt", "fix it",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	want := RunOptions{
		modelOptions: modelOptions{provider: "provider-a", model: "model-a"}, Format: OutputJSON, Output: "answer.md", NoSession: true, Dir: "sub",
		NoContextFiles: true, Instructions: "Be brief.", MaxSteps: 4, Tools: []string{"read", "grep"}, ExcludeTools: []string{"grep"},
		Files: []string{"a.png", "b, c.txt"},
	}
	if !o.print || !reflect.DeepEqual(o.runOptions(), want) || !slices.Equal(fs.Args(), []string{"fix it"}) {
		t.Errorf("parse %q = print %v, %+v, args %q; want print, %+v, args [fix it]", args, o.print, o.runOptions(), fs.Args(), want)
	}
}

// TestRootPrintMisuse checks that the root command refuses --format,
// --output and --no-session without --print, --print with --resume,
// --no-session with a flag that chooses a stored session, and --dir with
// --session. It fails before the app starts.
func TestRootPrintMisuse(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json"}, {"-o", "answer.md", "hi"}, {"--no-session", "hi"}, {"--tools", "", "hi"},
		{"--exclude-tools", "bash"}, {"-f", "a.png"}, {"--max-steps", "2"}, {"--instructions", "x"}, {"--no-context-files"},
	} {
		if err := rootCmd.Execute(t.Context(), args); !errors.Is(err, errNeedsPrint) {
			t.Errorf("execute %q: err = %v, want %v", args, err, errNeedsPrint)
		}
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-p", "-r", "hi"}, "--print and --resume"},
		{[]string{"-p", "--no-session", "-c", "hi"}, "--no-session and --continue"},
		{[]string{"-p", "--no-session", "--session", "ses_1", "hi"}, "--no-session and --session"},
		{[]string{"--dir", "..", "--session", "ses_1"}, "--dir and --session"},
		{[]string{"-p", "--instructions", "x", "-c", "hi"}, "--instructions and --continue"},
		{[]string{"-p", "--no-context-files", "--session", "ses_1", "hi"}, "--no-context-files and --session"},
	} {
		want := "flow: use only one of " + tt.want
		if err := rootCmd.Execute(t.Context(), tt.args); err == nil || err.Error() != want {
			t.Errorf("execute %q: err = %v, want %q", tt.args, err, want)
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
