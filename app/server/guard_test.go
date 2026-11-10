package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/app"
)

// newGuardedServer returns a server of a fresh app that takes the requests
// opts lets in.
func newGuardedServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	_, a := newTestServerApp(t)
	ts := httptest.NewServer(New(a, opts).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// guardRequest is a request a test sends past Guard.
type guardRequest struct {
	method, path string
	host         string
	header       map[string]string
	body         string
}

// send sends req to ts, as GET /health unless it names its method and path,
// and returns the response, whose body the caller closes.
func (req guardRequest) send(t *testing.T, ts *httptest.Server) *http.Response {
	t.Helper()
	method := req.method
	if method == "" {
		method = "GET"
	}
	path := req.path
	if path == "" {
		path = "/health"
	}
	r, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, strings.NewReader(req.body))
	if err != nil {
		t.Fatal(err)
	}
	if req.host != "" {
		r.Host = req.host
	}
	for k, v := range req.header {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// do sends req as send does, and returns its response, whose body is
// closed.
func (req guardRequest) do(t *testing.T, ts *httptest.Server) *http.Response {
	t.Helper()
	resp := req.send(t, ts)
	resp.Body.Close()
	return resp
}

// TestGuardHost checks that a server on the loopback interface refuses a
// request that names another host, as a rebound domain name sends, and that
// one listening on every interface takes it.
func TestGuardHost(t *testing.T) {
	loopback := newGuardedServer(t, Options{})
	for _, tt := range []struct {
		host string
		want int
	}{
		{"127.0.0.1:4096", http.StatusOK},
		{"localhost:4096", http.StatusOK},
		{"LOCALHOST", http.StatusOK},
		{"[::1]:4096", http.StatusOK},
		{"evil.example:4096", http.StatusForbidden},
		{"10.0.0.1:4096", http.StatusForbidden},
	} {
		if got := (guardRequest{host: tt.host}).do(t, loopback).StatusCode; got != tt.want {
			t.Errorf("Host %s: status %d, want %d", tt.host, got, tt.want)
		}
	}

	everywhere := newGuardedServer(t, Options{Hostname: "0.0.0.0"})
	if got := (guardRequest{host: "flow.example:4096"}).do(t, everywhere).StatusCode; got != http.StatusOK {
		t.Errorf("Host flow.example on every interface: status %d, want 200", got)
	}
}

// TestGuardOrigin checks that a page served from the loopback interface, or
// one --cors lists, may call the API and read its response, and that another
// site's page may not, not even with a request a browser sends without
// asking first.
func TestGuardOrigin(t *testing.T) {
	ts := newGuardedServer(t, Options{CORS: []string{"https://app.example"}})
	for _, tt := range []struct {
		origin string
		want   int
	}{
		{"http://localhost:3000", http.StatusOK},
		{"http://127.0.0.1:5173", http.StatusOK},
		{"https://app.example", http.StatusOK},
		{"https://evil.example", http.StatusForbidden},
		{"https://app.example.evil.example", http.StatusForbidden},
		{"null", http.StatusForbidden},
	} {
		resp := guardRequest{header: map[string]string{"Origin": tt.origin}}.do(t, ts)
		if resp.StatusCode != tt.want {
			t.Errorf("Origin %s: status %d, want %d", tt.origin, resp.StatusCode, tt.want)
		}
		allow := resp.Header.Get("Access-Control-Allow-Origin")
		if tt.want == http.StatusOK && (allow != tt.origin || resp.Header.Get("Vary") != "Origin") {
			t.Errorf("Origin %s: Access-Control-Allow-Origin %q, Vary %q; want the origin, Origin", tt.origin, allow, resp.Header.Get("Vary"))
		}
		if tt.want != http.StatusOK && allow != "" {
			t.Errorf("Origin %s: Access-Control-Allow-Origin %q, want none", tt.origin, allow)
		}
	}

	// A form or a no-cors fetch of another site sends its prompt as plain
	// text, which no preflight checks first: no run starts, and no session
	// is stored.
	status, got := doGuardError(t, guardRequest{method: "POST", path: "/session/ses_evil/message",
		header: map[string]string{"Origin": "https://evil.example", "Content-Type": "text/plain"},
		body:   `{"text":"run rm -rf ~"}`}, ts)
	if status != http.StatusForbidden || got.Code != app.CodeOriginNotAllowed {
		t.Errorf("cross-site prompt: %d %+v, want 403 %s", status, got, app.CodeOriginNotAllowed)
	}
	if s := (guardRequest{path: "/session/ses_evil"}).do(t, ts).StatusCode; s != http.StatusNotFound {
		t.Errorf("GET /session/ses_evil after a cross-site prompt: status %d, want 404", s)
	}
}

// TestGuardPreflight checks that the preflight of an allowed page is
// answered with the methods and headers it may send, without credentials,
// which browsers leave out of a preflight.
func TestGuardPreflight(t *testing.T) {
	ts := newGuardedServer(t, Options{Password: "secret"})
	resp := guardRequest{method: "OPTIONS", path: "/session/ses_1/message", header: map[string]string{
		"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization, content-type",
	}}.do(t, ts)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: status %d, want 204", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":  "http://localhost:3000",
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("preflight %s = %q, want %q", k, got, want)
		}
	}
}

// TestGuardPassword checks that a server with a password takes the requests
// that present it, with the user name, by basic authentication or in the
// auth_token query, and refuses the others.
func TestGuardPassword(t *testing.T) {
	ts := newGuardedServer(t, Options{Username: "ada", Password: "secret"})
	basic := func(user, pass string) map[string]string {
		return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))}
	}
	token := base64.StdEncoding.EncodeToString([]byte("ada:secret"))
	for _, tt := range []struct {
		name string
		req  guardRequest
		want int
	}{
		{"no credentials", guardRequest{}, http.StatusUnauthorized},
		{"wrong password", guardRequest{header: basic("ada", "nope")}, http.StatusUnauthorized},
		{"wrong user", guardRequest{header: basic("flow", "secret")}, http.StatusUnauthorized},
		{"basic", guardRequest{header: basic("ada", "secret")}, http.StatusOK},
		{"auth_token", guardRequest{path: "/health?" + AuthTokenQuery + "=" + token}, http.StatusOK},
		{"bad auth_token", guardRequest{path: "/health?" + AuthTokenQuery + "=%%%"}, http.StatusUnauthorized},
	} {
		resp := tt.req.do(t, ts)
		if resp.StatusCode != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
		if tt.want == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate header", tt.name)
		}
	}
	status, got := doGuardError(t, guardRequest{}, ts)
	if status != http.StatusUnauthorized || got.Code != app.CodeUnauthorized {
		t.Errorf("no credentials: %d %+v, want 401 %s", status, got, app.CodeUnauthorized)
	}
}

// doGuardError sends req to ts and returns the status of its response and
// the error it holds.
func doGuardError(t *testing.T, req guardRequest, ts *httptest.Server) (int, app.Error) {
	t.Helper()
	resp := req.send(t, ts)
	defer resp.Body.Close()
	var got ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return resp.StatusCode, got.Error
}

// TestEventKeepalive checks that a quiet event stream sends a keepalive
// comment every heartbeat, which an event stream reader skips.
func TestEventKeepalive(t *testing.T) {
	_, a := newTestServerApp(t)
	ts := httptest.NewServer(New(a, Options{Heartbeat: 10 * time.Millisecond}).Handler())
	t.Cleanup(ts.Close)
	events := openEvents(t, ts.URL+"/event")
	for range 2 {
		for {
			line, err := events.ReadString('\n')
			if err != nil {
				t.Fatalf("read event stream: %v", err)
			}
			if line == ": keepalive\n" {
				break
			}
		}
	}
}

// TestAuthLoginMethods checks that GET /auth reports, for a provider with no
// login flow that flow is not built with, no login methods as an empty list.
func TestAuthLoginMethods(t *testing.T) {
	ts := newTestServer(t)
	var got []AuthStatus
	if status := doJSON(t, "GET", ts.URL+"/auth", nil, &got); status != http.StatusOK || len(got) == 0 {
		t.Fatalf("GET /auth = %d %+v, want providers", status, got)
	}
	for _, st := range got {
		if st.LoginMethods == nil || len(st.LoginMethods) != 0 || st.BuiltIn {
			t.Errorf("GET /auth %s: login methods %#v, built in %v; want [] and false", st.Provider, st.LoginMethods, st.BuiltIn)
		}
	}
}
