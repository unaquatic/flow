package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/flow/config"
)

// TestSend_WritesTheSessionLog checks that a run logs its start, its prompt,
// its reply, and its end to the session's log.
func TestSend_WritesTheSessionLog(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	dir := t.TempDir()
	InitLogging(dir, config.LogInfo)

	a, _ := newActorTestApp(t, "hello there")
	events := a.Subscribe(t.Context())
	send(t, a, "ses_log", SendInput{Text: "hi", MessageID: "msg_user1"})
	collectUntilIdle(t, events)

	data, err := os.ReadFile(filepath.Join(dir, "logs", "ses_log.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`msg="run started"`, `msg="prompt stored"`, `message=msg_user1`, `msg="message ended"`, `msg="run ended"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session log lacks %s:\n%s", want, data)
		}
	}
}
