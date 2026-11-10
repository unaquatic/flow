package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/snapshot"
)

// newTwoProjectServer returns a server of a fresh app, the ID of a session
// stored in its project, and the ID of one stored in another project of the
// same data directory.
func newTwoProjectServer(t *testing.T) (ts *httptest.Server, own, other message.SessionID) {
	t.Helper()
	var home string
	ts, a := newTestServerWith(t, func(cfg *config.Config) { home = cfg.HomeDir })
	own, other = "ses_own", "ses_other"
	appendMessages(t, a, own, promptMsg("u1", "here"))

	t.Chdir(t.TempDir())
	b, err := app.New(t.Context(), config.NewService(home), app.Options{Extensions: []extension.Load{protocol.Load, snapshot.Load}})
	if err != nil {
		t.Fatal(err)
	}
	appendMessages(t, b, other, promptMsg("u1", "there"))
	b.Close()
	return ts, own, other
}

// TestSessionListAll checks that GET /session lists the project's sessions,
// that with all=true it lists every project's, each with its own worktree,
// and that it refuses an all that is not a boolean.
func TestSessionListAll(t *testing.T) {
	ts, own, other := newTwoProjectServer(t)

	var listed []session.Session
	if status := doJSON(t, "GET", ts.URL+"/session", nil, &listed); status != http.StatusOK || len(listed) != 1 || listed[0].ID != own {
		t.Fatalf("GET /session = %d %+v, want %s alone", status, listed, own)
	}
	if listed[0].Worktree == "" {
		t.Errorf("GET /session: %s has no worktree", own)
	}

	listed = nil
	if status := doJSON(t, "GET", ts.URL+"/session?all=true", nil, &listed); status != http.StatusOK {
		t.Fatalf("GET /session?all=true: status %d", status)
	}
	ids := make([]message.SessionID, 0, len(listed))
	worktrees := map[string]bool{}
	for _, s := range listed {
		ids = append(ids, s.ID)
		worktrees[s.Worktree] = true
	}
	slices.Sort(ids)
	if want := []message.SessionID{other, own}; !slices.Equal(ids, want) || len(worktrees) != 2 || worktrees[""] {
		t.Errorf("GET /session?all=true = %+v, want %v with a worktree each", listed, want)
	}

	if status, got := doError(t, "GET", ts.URL+"/session?all=maybe", nil); status != http.StatusBadRequest || got.Code != app.CodeInvalidRequest {
		t.Errorf("GET /session?all=maybe = %d %+v, want 400 %s", status, got, app.CodeInvalidRequest)
	}
}
