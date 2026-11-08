package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/snapshot"
)

// newTestServer returns an isolated HTTP server with the snapshot extension
// loaded but snapshots disabled, as flow serve loads it, and no API key in
// the environment.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := newTestServerApp(t)
	return ts
}

// newTestServerApp returns a server as newTestServer does, and the app it
// serves.
func newTestServerApp(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	return newTestServerWith(t, nil)
}

// newTestServerWith returns a server and its app as newTestServerApp does,
// with the configuration setup changes, if not nil, written to the config
// file before the app starts. The app reads the catalog of catalogDoc,
// unless setup caches one of its own or takes the cache's place.
func newTestServerWith(t *testing.T, setup func(*config.Config)) (*httptest.Server, *app.App) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv(keyEnvA, "")
	t.Setenv(keyEnvB, "")
	cfg := &config.Config{
		DefaultModel:    "provider-b/model-y",
		DefaultProvider: "provider-b",
		HomeDir:         t.TempDir(),
		Extensions:      map[string]json.RawMessage{snapshot.Name: json.RawMessage(`{"enabled":false}`)},
	}
	if setup != nil {
		setup(cfg)
	}
	if _, err := os.Stat(filepath.Join(cfg.HomeDir, gateway.StoreFile)); errors.Is(err, fs.ErrNotExist) {
		withCatalog(t, catalogDoc(t))(cfg)
	}
	if cfg.CatalogSource == "" {
		// No test downloads the catalog from a real catalog server.
		cfg.CatalogSource = "http://127.0.0.1:1"
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.HomeDir, config.FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	conf := config.NewService(cfg.HomeDir)
	a, err := app.New(t.Context(), conf, app.Options{Extensions: []extension.Load{protocol.Load, snapshot.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	ts := httptest.NewServer(New(a, Options{}).Handler())
	t.Cleanup(ts.Close)
	return ts, a
}

func doJSON(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s response: %v", method, url, err)
		}
	}
	return resp.StatusCode
}

// doError sends a request as doJSON does, and returns the status of its
// response and the error it holds. It fails t when the response is not a
// JSON ErrorResponse.
func doError(t *testing.T, method, url string, body any) (int, app.Error) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got ErrorResponse
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s %s: Content-Type %q, want application/json", method, url, ct)
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode %s %s error response: %v", method, url, err)
	}
	return resp.StatusCode, got.Error
}

func createSession(t *testing.T, ts *httptest.Server) session.Session {
	t.Helper()
	var sess session.Session
	if status := doJSON(t, "POST", ts.URL+"/session", nil, &sess); status != http.StatusCreated {
		t.Fatalf("create session: status %d", status)
	}
	if sess.ID == "" {
		t.Fatal("create session: empty id")
	}
	return sess
}

// TestSessionLifecycle checks that a session is stored with its first
// message, can be titled and deleted then, and that each of these changes is
// streamed to the session's events: session.updated as it is stored and as
// it is titled, and session.deleted.
func TestSessionLifecycle(t *testing.T) {
	ts := newTestServer(t)
	sess := createSession(t, ts)
	msgURL := fmt.Sprintf("%s/session/%s/message", ts.URL, sess.ID)
	sessURL := fmt.Sprintf("%s/session/%s", ts.URL, sess.ID)
	title := "Divide by zero"
	events := openEvents(t, ts.URL+"/event?session="+string(sess.ID))

	// A session is not stored until its first message, and nothing else
	// stores it.
	if status := doJSON(t, "PATCH", sessURL, SessionUpdateRequest{Title: &title}, nil); status != http.StatusNotFound {
		t.Errorf("title before first message: status %d, want 404", status)
	}
	if listed := sessionListed(t, ts, sess.ID); listed {
		t.Error("fresh session listed before first message")
	}
	var msgs []json.RawMessage
	if status := doJSON(t, "GET", msgURL, nil, &msgs); status != http.StatusOK {
		t.Fatalf("list messages: status %d", status)
	}
	if len(msgs) != 0 {
		t.Errorf("fresh session has %d messages, want 0", len(msgs))
	}
	if status := doJSON(t, "DELETE", sessURL, nil, nil); status != http.StatusNotFound {
		t.Errorf("delete before first message: status %d, want 404", status)
	}

	if status := doJSON(t, "POST", msgURL, SendRequest{Text: "hi"}, nil); status != http.StatusAccepted {
		t.Fatalf("send: status %d", status)
	}
	waitForMessages(t, ts, msgURL)
	if listed := sessionListed(t, ts, sess.ID); !listed {
		t.Error("session not listed after first message")
	}
	var stored session.Session
	if err := json.Unmarshal([]byte(awaitSSE(t, events, app.TypeSessionUpdated)), &stored); err != nil || stored.Title != "" || stored.CreatedAt.IsZero() {
		t.Errorf("session.updated once stored = %+v, %v; want it untitled, with its creation", stored, err)
	}
	var titled session.Session
	if status := doJSON(t, "PATCH", sessURL, SessionUpdateRequest{Title: &title}, &titled); status != http.StatusOK || titled.Title != title {
		t.Errorf("title after first message: status %d, title %q; want 200, %q", status, titled.Title, title)
	}
	if titled.CreatedAt.IsZero() || titled.UpdatedAt.Before(titled.CreatedAt) {
		t.Errorf("session created %v, updated %v; want its creation, and an update no earlier", titled.CreatedAt, titled.UpdatedAt)
	}
	var streamed session.Session
	if err := json.Unmarshal([]byte(awaitSSE(t, events, app.TypeSessionUpdated)), &streamed); err != nil ||
		streamed.Title != title || !streamed.CreatedAt.Equal(titled.CreatedAt) || !streamed.UpdatedAt.Equal(titled.UpdatedAt) {
		t.Errorf("session.updated once titled = %+v, %v; want the session PATCH answered with, %+v", streamed, err, titled)
	}
	// An update that leaves the title out keeps it; an empty one clears it.
	var kept session.Session
	if status := doJSON(t, "PATCH", sessURL, SessionUpdateRequest{}, &kept); status != http.StatusOK || kept.Title != title {
		t.Errorf("update without a title: status %d, title %q; want 200, %q", status, kept.Title, title)
	}
	var cleared session.Session
	if status := doJSON(t, "PATCH", sessURL, SessionUpdateRequest{Title: new("")}, &cleared); status != http.StatusOK || cleared.Title != "" {
		t.Errorf("update with an empty title: status %d, title %q; want 200, none", status, cleared.Title)
	}

	if status := doJSON(t, "DELETE", sessURL, nil, nil); status != http.StatusNoContent {
		t.Fatalf("delete: status %d", status)
	}
	awaitSSE(t, events, app.TypeSessionDeleted)
	msgs = nil
	if status := doJSON(t, "GET", msgURL, nil, &msgs); status != http.StatusOK || len(msgs) != 0 {
		t.Errorf("messages after delete: status %d, %d messages; want 200, 0", status, len(msgs))
	}
	// A deleted session reads as one that does not exist.
	if listed := sessionListed(t, ts, sess.ID); listed {
		t.Error("session listed after delete")
	}
	if status := doJSON(t, "GET", sessURL, nil, nil); status != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", status)
	}
	if status := doJSON(t, "DELETE", sessURL, nil, nil); status != http.StatusNotFound {
		t.Errorf("delete again: status %d, want 404", status)
	}
}

// sessionListed reports whether GET /session includes id.
func sessionListed(t *testing.T, ts *httptest.Server, id message.SessionID) bool {
	t.Helper()
	var sessions []session.Session
	if status := doJSON(t, "GET", ts.URL+"/session", nil, &sessions); status != http.StatusOK {
		t.Fatalf("list sessions: status %d", status)
	}
	return slices.ContainsFunc(sessions, func(s session.Session) bool { return s.ID == id })
}

// waitForMessages waits for the asynchronous send to persist a message.
func waitForMessages(t *testing.T, ts *httptest.Server, msgURL string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var msgs []json.RawMessage
		if status := doJSON(t, "GET", msgURL, nil, &msgs); status == http.StatusOK && len(msgs) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first message to persist")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMessageSendValidation(t *testing.T) {
	ts := newTestServer(t)
	sess := createSession(t, ts)
	msgURL := fmt.Sprintf("%s/session/%s/message", ts.URL, sess.ID)

	tests := []struct {
		name string
		url  string
		body any
		want int
	}{
		// An unseen session ID is stored when the message is processed.
		{"unseen session accepted", ts.URL + "/session/ses_fresh/message", SendRequest{Text: "hi"}, http.StatusAccepted},
		{"empty text", msgURL, SendRequest{}, http.StatusBadRequest},
		{"accepted", msgURL, SendRequest{Text: "hi"}, http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status := doJSON(t, "POST", tt.url, tt.body, nil); status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

// TestBusySessionConflicts checks that a request to change a session that
// another operation holds is answered 409 Conflict, and changes nothing.
func TestBusySessionConflicts(t *testing.T) {
	ts, a := newTestServerApp(t)
	sess := createSession(t, ts)
	op, _, err := a.Claim(t.Context(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release(sess.ID, op)

	base := fmt.Sprintf("%s/session/%s", ts.URL, sess.ID)
	tests := []struct {
		name, method, url string
		body              any
	}{
		{"send", "POST", base + "/message", SendRequest{Text: "hi"}},
		{"streamed send", "POST", base + "/message", SendRequest{Text: "hi", Stream: true}},
		{"undo", "POST", base + "/undo", nil},
		{"redo", "POST", base + "/redo", nil},
		{"checkout", "POST", base + "/checkout", nil},
		{"branch switch", "POST", base + "/branch", BranchSwitchRequest{MessageID: "m1"}},
		{"navigate", "POST", base + "/navigate", NavigateRequest{MessageID: "m1"}},
		{"message delete", "DELETE", base + "/message/m1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status := doJSON(t, tt.method, tt.url, tt.body, nil); status != http.StatusConflict {
				t.Errorf("status = %d, want %d", status, http.StatusConflict)
			}
		})
	}

	var msgs []json.RawMessage
	if status := doJSON(t, "GET", base+"/message", nil, &msgs); status != http.StatusOK || len(msgs) != 0 {
		t.Errorf("messages = %d with status %d, want none with 200", len(msgs), status)
	}
}

// TestMessageSendStream checks that a send with "stream": true answers with
// the run's events as server-sent events, from its start to its end, and that
// the response ends once the run has let go of the session.
func TestMessageSendStream(t *testing.T) {
	ts := newTestServer(t)
	sess := createSession(t, ts)
	msgURL := fmt.Sprintf("%s/session/%s/message", ts.URL, sess.ID)
	body, err := json.Marshal(SendRequest{Text: "hello", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", msgURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q; want 200 and an event stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	var events []string
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		if event, ok := strings.CutPrefix(lines.Text(), "event: "); ok {
			events = append(events, event)
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0] != string(app.TypeAgentStarted) || events[len(events)-1] != string(app.TypeAgentEnded) {
		t.Fatalf("events = %v, want the run's start first and its end last", events)
	}
	if !slices.Contains(events, string(app.TypeMessageStarted)) {
		t.Errorf("events = %v, want the prompt's start", events)
	}

	if status := doJSON(t, "POST", msgURL, SendRequest{Text: "again"}, nil); status != http.StatusAccepted {
		t.Errorf("send after the stream ended: status %d, want %d", status, http.StatusAccepted)
	}
}

// TestContinue checks that POST /continue starts a run whatever the
// transcript ends at, and refuses one that is empty or whose session another
// operation holds.
func TestContinue(t *testing.T) {
	tests := []struct {
		name string
		msgs []message.Content
		busy bool
		want int
	}{
		{name: "empty", want: http.StatusConflict},
		{name: "ends at a reply", msgs: []message.Content{promptMsg("u1", "hi"), replyMsg("a1", "hello")}, want: http.StatusAccepted},
		{name: "ends at a prompt", msgs: []message.Content{promptMsg("u1", "hi")}, want: http.StatusAccepted},
		{name: "busy", msgs: []message.Content{promptMsg("u1", "hi")}, busy: true, want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, a := newTestServerApp(t)
			sess := createSession(t, ts)
			appendMessages(t, a, sess.ID, tt.msgs...)
			if tt.busy {
				op, _, err := a.Claim(t.Context(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Release(sess.ID, op)
			}
			url := fmt.Sprintf("%s/session/%s/continue", ts.URL, sess.ID)
			if status := doJSON(t, "POST", url, nil, nil); status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
			if tt.want == http.StatusAccepted {
				if err := a.Session(sess.ID).Wait(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestContinueStream checks that a continue with "stream": true answers with
// the run's events, from its start to its end, without a new prompt, and that
// a refused one is answered with its status rather than a stream.
func TestContinueStream(t *testing.T) {
	ts, a := newTestServerApp(t)
	sess := createSession(t, ts)
	appendMessages(t, a, sess.ID, promptMsg("u1", "hi"))
	url := fmt.Sprintf("%s/session/%s/continue", ts.URL, sess.ID)

	body, err := json.Marshal(ContinueRequest{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q; want 200 and an event stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var events []string
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		if event, ok := strings.CutPrefix(lines.Text(), "event: "); ok {
			events = append(events, event)
		}
		if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok && strings.Contains(data, `"role":"user"`) {
			t.Errorf("event %s publishes a prompt, want none", data)
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0] != string(app.TypeAgentStarted) || events[len(events)-1] != string(app.TypeAgentEnded) {
		t.Fatalf("events = %v, want the run's start first and its end last", events)
	}

	empty := createSession(t, ts)
	emptyURL := fmt.Sprintf("%s/session/%s/continue", ts.URL, empty.ID)
	if status := doJSON(t, "POST", emptyURL, ContinueRequest{Stream: true}, nil); status != http.StatusConflict {
		t.Errorf("streamed continue of an empty session: status %d, want %d", status, http.StatusConflict)
	}
}

func TestEventStream(t *testing.T) {
	ts := newTestServer(t)
	sess := createSession(t, ts)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/event?session="+string(sess.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event stream: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}

	var send SendResponse
	msgURL := fmt.Sprintf("%s/session/%s/message", ts.URL, sess.ID)
	if status := doJSON(t, "POST", msgURL, SendRequest{Text: "hello"}, &send); status != http.StatusAccepted {
		t.Fatalf("send: status %d", status)
	}

	body := bufio.NewReader(resp.Body)
	if event, _ := readSSEEvent(t, body); event != string(app.TypeAgentStarted) {
		t.Fatalf("first event = %q, want %q", event, app.TypeAgentStarted)
	}
	event, data := readSSEEvent(t, body)
	if event != string(app.TypeMessageStarted) {
		t.Fatalf("second event = %q, want %q", event, app.TypeMessageStarted)
	}
	var started struct {
		SessionID message.SessionID `json:"session_id"`
		Message   struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &started); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if started.SessionID != sess.ID {
		t.Errorf("session_id = %q, want %q", started.SessionID, sess.ID)
	}
	if msg := started.Message; msg.ID != string(send.MessageID) || msg.Role != "user" {
		t.Errorf("message id, role = %q, %q, want %q, user", msg.ID, msg.Role, send.MessageID)
	}
	if parts := started.Message.Parts; len(parts) != 1 || parts[0].Text != "hello" {
		t.Errorf("parts = %+v, want the text hello", parts)
	}
}

// readSSEEvent reads one event and data pair from an SSE stream.
// openEvents subscribes to the event stream at url, for ten seconds at most,
// and returns the stream to read.
func openEvents(t *testing.T, url string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewReader(resp.Body)
}

// awaitSSE reads r up to the next event of type typ, and returns its data.
func awaitSSE(t *testing.T, r *bufio.Reader, typ app.EventType) string {
	t.Helper()
	for {
		if event, data := readSSEEvent(t, r); event == string(typ) {
			return data
		}
	}
}

func readSSEEvent(t *testing.T, r *bufio.Reader) (event, data string) {
	t.Helper()
	for event == "" || data == "" {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	return event, data
}
