package terminal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// newTestApp returns an app over a new data directory, with snapshots off,
// no catalog server and a catalog of one provider, provider-a, whose API key
// no environment variable holds, in a new working directory. The test ends
// by closing it.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"model":"provider-a/model","snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, `{"provider-a":{"id":"provider-a","format":"responses","models":{}}}`))
	conf := config.NewService(home)
	a, err := app.New(t.Context(), conf, app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// storeMessage appends a user message to session id of a, which stores the
// session when it is new.
func storeMessage(t *testing.T, a *app.App, id message.SessionID) {
	t.Helper()
	err := a.Sessions().AppendMessage(t.Context(), id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartFirstSession(t *testing.T) {
	a := newTestApp(t)
	older, newer := message.NewSessionID(), message.NewSessionID()
	storeMessage(t, a, older)
	storeMessage(t, a, newer)
	// A later message makes the older session the most recently updated one.
	storeMessage(t, a, older)

	tests := []struct {
		name    string
		start   Start
		want    message.SessionID
		wantErr string
	}{
		{name: "new"},
		{name: "pick", start: Start{Mode: StartPick}},
		{name: "latest", start: Start{Mode: StartLatest}, want: older},
		{name: "by ID", start: Start{Mode: StartID, ID: newer}, want: newer},
		{name: "unknown ID", start: Start{Mode: StartID, ID: "ses_missing"},
			wantErr: `no stored session matches "ses_missing"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.start.FirstSession(t.Context(), a.Sessions())
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("first session of %+v: err = %v, want %q", tt.start, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("first session of %+v = %q, %v; want %q", tt.start, got, err, tt.want)
			}
		})
	}
}

func TestStartFirstSessionLatestWithoutSessions(t *testing.T) {
	a := newTestApp(t)
	start := Start{Mode: StartLatest}
	if got, err := start.FirstSession(t.Context(), a.Sessions()); err != nil || got != "" {
		t.Errorf("first session = %q, %v; want a new session", got, err)
	}
}

func TestProgramResumable(t *testing.T) {
	a := newTestApp(t)
	stored := message.NewSessionID()
	storeMessage(t, a, stored)

	tests := []struct {
		name   string
		closed message.SessionID
		want   message.SessionID
	}{
		{name: "stored", closed: stored, want: stored},
		{name: "new", closed: message.NewSessionID()},
		{name: "before Run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(a, Start{})
			p.closed = tt.closed
			if got := p.Resumable(t.Context()); got != tt.want {
				t.Errorf("resumable after closing on %q = %q, want %q", tt.closed, got, tt.want)
			}
		})
	}
}
