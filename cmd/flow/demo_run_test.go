package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/unaquatic/flow/app"
)

// TestRunDemos checks what flow demo run shows, case by case: the final
// answer alone, though a step that read a file came before it, the run's
// events as JSON, and each error as the command line reports it.
func TestRunDemos(t *testing.T) {
	want := map[string]string{
		"answer":          demoRunAnswer + "\n",
		"failure":         "error: provider-a response error (401): invalid API key\n",
		"no-session":      "Hi!\n",
		"missing-session": `error: no stored session matches "` + demoMissingSession + `"` + "\n",
		"no-prompt":       "error: " + errNoPrompt.Error() + "\n",
	}
	for _, c := range runDemos {
		t.Run(c.name, func(t *testing.T) {
			var w bytes.Buffer
			if err := runDemo(t.Context(), &w, false, runDemos, []string{c.name}); err != nil {
				t.Fatal(err)
			}
			head := "# " + c.desc + "\n$ flow " + c.line + "\n"
			out, ok := strings.CutPrefix(w.String(), head)
			if !ok {
				t.Fatalf("output =\n%s\nwant it to start with\n%s", w.String(), head)
			}
			if c.name == "json" {
				checkRunDemoJSON(t, out)
				return
			}
			if out != want[c.name] {
				t.Errorf("output = %q, want %q", out, want[c.name])
			}
		})
	}
}

// checkRunDemoJSON checks that out, what the json case of flow demo run
// shows, is the events of a run that answers "say hi" with "Hi!", each on
// a line of its own, typed, and with no instructions of the project the
// test runs in.
func checkRunDemoJSON(t *testing.T, out string) {
	t.Helper()
	var types []string
	for line := range strings.Lines(out) {
		var ev struct {
			Type    app.EventType `json:"type"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
			Part struct {
				Text string `json:"text"`
			} `json:"part"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %q is no JSON object: %v", line, err)
		}
		if ev.Message.Role == "system" {
			t.Errorf("line %q holds a system message, want none in a demo", line)
		}
		if ev.Type == app.TypePartEnded && ev.Part.Text != "Hi!" {
			t.Errorf("part ended with %q, want Hi!", ev.Part.Text)
		}
		types = append(types, string(ev.Type))
	}
	wantTypes := []app.EventType{
		app.TypeAgentStarted,
		app.TypeMessageStarted, app.TypeMessageEnded, app.TypeSessionUpdated,
		app.TypeMessageStarted, app.TypePartStarted, app.TypePartEnded, app.TypeMessageEnded,
		app.TypeAgentEnded,
	}
	var want []string
	for _, ty := range wantTypes {
		want = append(want, string(ty))
	}
	if got, w := strings.Join(types, " "), strings.Join(want, " "); got != w {
		t.Errorf("event types = %s, want %s", got, w)
	}
}

// TestRunDemoKeepsWorkingDir checks that a run demo leaves the working
// directory as it found it.
func TestRunDemoKeepsWorkingDir(t *testing.T) {
	t.Chdir(t.TempDir())
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := runDemo(t.Context(), new(bytes.Buffer), false, runDemos, []string{"answer"}); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Getwd(); err != nil || after != before {
		t.Errorf("working directory after the demo = %q, %v; want %q", after, err, before)
	}
}
