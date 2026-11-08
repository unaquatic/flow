package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// nextEvent returns the next event of events, failing on timeout.
func nextEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
		return nil
	}
}

// TestSession_PublishesItsLifecycle checks that a session's first prompt
// publishes the session once the system message it opens with has stored
// it, after that message ends, and that later prompts do not; that a title
// publishes the session
// with it; and that deleting the session publishes its deletion. A title for
// a session that is not stored yet fails and publishes nothing.
func TestSession_PublishesItsLifecycle(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	const id = "ses_life"
	events := a.Subscribe(t.Context())

	if _, err := a.Session(id).SetTitle("too early"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SetTitle of a session not stored = %v, want fs.ErrNotExist", err)
	}

	send(t, a, id, SendInput{Text: "hi", MessageID: "msg_first"})
	first := collectUntilIdle(t, events)
	if _, ok := first[0].(EventAgentStarted); !ok {
		t.Errorf("first event = %T, want the run's start, with nothing from the failed title before it", first[0])
	}
	i := slices.IndexFunc(first, func(ev Event) bool { _, ok := ev.(EventSessionUpdated); return ok })
	if n := len(eventsOfType[EventSessionUpdated](first)); n != 1 || i < 1 {
		t.Fatalf("first run published %d EventSessionUpdated, want 1", n)
	}
	if ended, ok := first[i-1].(EventMessageEnded); !ok || ended.Message.Role != message.RoleSystem {
		t.Errorf("event before EventSessionUpdated = %+v, want the end of the system message", first[i-1])
	}
	if started, ok := first[i+1].(EventMessageStarted); !ok || started.Message.ID != "msg_first" {
		t.Errorf("event after EventSessionUpdated = %+v, want the start of the first prompt", first[i+1])
	}
	stored, err := a.Sessions().Session(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u := first[i].(EventSessionUpdated); u.SessionID != id || u.Title != "" || !u.CreatedAt.Equal(stored.CreatedAt) {
		t.Errorf("EventSessionUpdated = %+v, want session %s, untitled, created %v", u, id, stored.CreatedAt)
	}

	send(t, a, id, SendInput{Text: "again"})
	if n := len(eventsOfType[EventSessionUpdated](collectUntilIdle(t, events))); n != 0 {
		t.Errorf("second run published %d EventSessionUpdated, want none", n)
	}

	titled, err := a.Session(id).SetTitle("greetings")
	if err != nil {
		t.Fatal(err)
	}
	u, ok := nextEvent(t, events).(EventSessionUpdated)
	if !ok || u.SessionID != id || u.Title != "greetings" || !u.CreatedAt.Equal(titled.CreatedAt) || !u.UpdatedAt.Equal(titled.UpdatedAt) {
		t.Errorf("event after SetTitle = %+v, want session %s titled greetings, as stored: %+v", u, id, titled)
	}

	if err := a.DeleteSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if d, ok := nextEvent(t, events).(EventSessionDeleted); !ok || d.SessionID != id {
		t.Errorf("event after DeleteSession = %+v, want EventSessionDeleted of %s", d, id)
	}
}

// storeSessionIn stores a session of one message in the database of the
// data directory home, in the project whose worktree is dir.
func storeSessionIn(t *testing.T, home, dir string, id message.SessionID) {
	t.Helper()
	db, err := database.Open(filepath.Join(home, database.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project, err := database.Project(t.Context(), db, dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(t.Context(), db, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(t.Context(), id, &message.Content{ID: "msg_1", Role: message.RoleUser}); err != nil {
		t.Fatal(err)
	}
}

// sameDir reports whether a and b name the same folder, whatever symbolic
// links lead to it, as the temporary directories of macOS do.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// TestNew_EntersTheSessionsFolder checks that an app opened on a stored
// session works in that session's folder, wherever the process started:
// the working directory moves there and the session is one of the app's
// own; that a session the database does not hold leaves the working
// directory as it is; and that a session whose folder is gone fails.
func TestNew_EntersTheSessionsFolder(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"catalog_source":"http://127.0.0.1:1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, providersCatalog))
	there, here, gone := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gone")
	storeSessionIn(t, home, there, "ses_there")
	storeSessionIn(t, home, gone, "ses_gone")
	t.Chdir(here)

	open := func(id message.SessionID) (*App, error) {
		t.Helper()
		a, err := New(t.Context(), config.NewService(home), Options{Session: id})
		if err == nil {
			t.Cleanup(a.Close)
		}
		return a, err
	}

	a, err := open("ses_there")
	if err != nil {
		t.Fatalf("open on a session elsewhere: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if a.WorkingDir() != there || !sameDir(t, wd, there) {
		t.Errorf("working directory = %s, app's %s; want the session's folder %s", wd, a.WorkingDir(), there)
	}
	if ok, err := a.Sessions().Stored(t.Context(), "ses_there"); err != nil || !ok {
		t.Errorf("session of the app's project = %v, %v; want it its own", ok, err)
	}

	t.Chdir(here)
	if a, err = open("ses_missing"); err != nil {
		t.Fatalf("open on a session not stored: %v", err)
	}
	if wd, _ := os.Getwd(); !sameDir(t, wd, here) || !sameDir(t, a.WorkingDir(), here) {
		t.Errorf("working directory = %s, app's %s; want it left in %s", wd, a.WorkingDir(), here)
	}

	if _, err := open("ses_gone"); err == nil || !strings.Contains(err.Error(), gone) {
		t.Errorf("open on a session whose folder is gone = %v, want an error naming %s", err, gone)
	}
	if wd, _ := os.Getwd(); !sameDir(t, wd, here) {
		t.Errorf("working directory after the failure = %s, want it left in %s", wd, here)
	}
}
