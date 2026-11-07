package app

import (
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"
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
// publishes the session once the prompt has stored it, after the prompt
// ends, and that later prompts do not; that a title publishes the session
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
	if ended, ok := first[i-1].(EventMessageEnded); !ok || ended.Message.ID != "msg_first" {
		t.Errorf("event before EventSessionUpdated = %+v, want the end of the first prompt", first[i-1])
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
