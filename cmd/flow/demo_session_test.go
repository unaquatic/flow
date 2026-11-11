package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// TestSessionDemos checks what flow demo session, flow demo models and flow
// demo export show, case by case, on the demo sessions and catalog.
func TestSessionDemos(t *testing.T) {
	const updated = `\d{4}-\d\d-\d\d \d\d:\d\d:\d\d`
	q := regexp.QuoteMeta
	titled, untitled := q(string(demoSession)), q(string(demoUntitledSession))
	for _, tt := range []struct {
		command string
		cases   []demoCase
		// want holds, for each case by name, the pattern its output, after
		// its comment and command line, matches in whole.
		want map[string]string
	}{
		{command: "session", cases: sessionDemos, want: map[string]string{
			"list": q(demoSessionTitle) + `  ` + updated + `  ` + titled + "\n" +
				`Untitled              ` + updated + `  ` + untitled + "\n",
			"all": q(demoSessionTitle) + `  \S+  ` + updated + `  ` + titled + "\n" +
				`Untitled              \S+  ` + updated + `  ` + untitled + "\n",
			"json":  `(?s)\[\n  \{\n    "id": "` + titled + `",\n    "title": "` + q(demoSessionTitle) + `",.*"id": "` + untitled + `",.*\]\n`,
			"empty": "No sessions found\n",
		}},
		{command: "session-delete", cases: sessionDeleteDemos, want: map[string]string{
			"delete":         `Deleted:\n  ` + q(demoSessionTitle) + `  \S+  ` + updated + `  ` + titled + "\n",
			"delete-json":    `(?s)\[\n  \{\n    "id": "` + titled + `",\n    "title": "` + q(demoSessionTitle) + `",\n.*"worktree": "[^"]+"\n  \}\n\]\n`,
			"delete-missing": q("error: no session: " + demoMissingSession + "\n"),
		}},
		{command: "models", cases: modelsDemos, want: map[string]string{
			"list":          q("provider-a  lab-a/model-a  low, medium, high\nprovider-a  lab-a/model-b\n"),
			"not-signed-in": q("error: " + errNoModels.Error() + "\n"),
		}},
		{command: "export", cases: exportDemos, want: map[string]string{
			"markdown": q("# " + demoSessionTitle + "\n\n## User\n\nwhy does TestAdd fail?\n\n## Agent\n\nLet me read calc.go.\n\n### Read\n\n" +
				"```json\n{\"path\":\"calc.go\"}\n```\n\n```\npackage calc\n\nfunc Add(a, b int) int { return a - b }\n```\n\n" + demoRunAnswer + "\n"),
			"jsonl": `\{"type":"session","id":"` + titled + `","title":"` + q(demoSessionTitle) + `",[^\n]*\}\n` +
				`\{"type":"message",[^\n]*"role":"user"[^\n]*\}\n` +
				`\{"type":"message",[^\n]*"name":"read"[^\n]*\}\n` +
				`\{"type":"message",[^\n]*"text":"` + q(strings.ReplaceAll(demoRunAnswer, "\n", `\n`)) + `"[^\n]*\}\n`,
			"missing": q("error: no session: " + demoMissingSession + "\n"),
			"none":    q("error: " + errNoSessions.Error() + "\n"),
		}},
	} {
		for _, c := range tt.cases {
			t.Run(tt.command+"/"+c.name, func(t *testing.T) {
				want, ok := tt.want[c.name]
				if !ok {
					t.Fatalf("no output given for case %q", c.name)
				}
				var w bytes.Buffer
				if err := runDemo(t.Context(), &w, false, tt.cases, []string{c.name}); err != nil {
					t.Fatal(err)
				}
				head := "# " + c.desc + "\n$ flow " + c.line + "\n"
				re := regexp.MustCompile(`\A` + q(head) + want + `\z`)
				if !re.MatchString(w.String()) {
					t.Errorf("output =\n%s\nwant it to match\n%s", w.String(), re)
				}
			})
		}
		if len(tt.want) != len(tt.cases) {
			t.Errorf("%s: %d outputs given for %d cases", tt.command, len(tt.want), len(tt.cases))
		}
	}
}
