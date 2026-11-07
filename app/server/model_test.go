package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
)

// catalogDoc returns a catalog document, as a catalog server serves it,
// listing claude-x for Anthropic and the models named for OpenAI.
func catalogDoc(t *testing.T, openai ...string) []byte {
	models := map[string]any{}
	for _, id := range openai {
		models[id] = map[string]any{"id": id, "limit": map[string]int{"context": 128000, "output": 16384}}
	}
	b, _ := json.Marshal(map[string]any{
		"anthropic": map[string]any{"id": "anthropic", "models": map[string]any{
			"claude-x": map[string]any{"id": "claude-x", "limit": map[string]int{"context": 200000, "output": 64000}},
		}},
		"openai": map[string]any{"id": "openai", "models": models},
	})
	return apiCatalog(t, string(b))
}

// apiCatalog returns the catalog document of api, an api.json, as
// gatewaytest.FromAPI builds it.
func apiCatalog(t *testing.T, api string) []byte {
	t.Helper()
	b, err := json.Marshal(gatewaytest.FromAPI(t, api))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withCatalog returns a setup that caches doc as a fresh catalog, so that no
// read downloads one.
func withCatalog(t *testing.T, doc []byte) func(*config.Config) {
	return func(cfg *config.Config) {
		gatewaytest.WriteEntry(t, cfg.HomeDir, gatewaytest.Entry{Fetched: time.Now(), Data: doc})
	}
}

// modelIDs returns the models of listings, as variants name them.
func modelIDs(listings []app.ModelListing) []string {
	var out []string
	for _, l := range listings {
		for _, m := range l.Models {
			out = append(out, agent.Variant{Provider: l.Provider, Model: m.Model}.String())
		}
	}
	return out
}

// TestModelList checks that GET /model lists the catalog models of the
// signed-in providers, or of the one the provider query names.
func TestModelList(t *testing.T) {
	ts, _ := newTestServerWith(t, withCatalog(t, catalogDoc(t, "gpt-x")))

	var got []app.ModelListing
	if status := doJSON(t, "GET", ts.URL+"/model", nil, &got); status != http.StatusOK || len(got) != 0 {
		t.Fatalf("before sign-in: status %d, listings %+v, want 200 and none", status, got)
	}

	addKey(t, ts)
	tests := []struct {
		query      string
		wantStatus int
		want       []string
	}{
		{query: "", wantStatus: http.StatusOK, want: []string{"openai/gpt-x"}},
		{query: "?provider=openai", wantStatus: http.StatusOK, want: []string{"openai/gpt-x"}},
		{query: "?provider=anthropic", wantStatus: http.StatusOK},
		{query: "?provider=nope", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		got = nil
		status := doJSON(t, "GET", ts.URL+"/model"+tt.query, nil, &got)
		if status != tt.wantStatus || !slices.Equal(modelIDs(got), tt.want) {
			t.Errorf("GET /model%s = %d, %v, want %d, %v", tt.query, status, modelIDs(got), tt.wantStatus, tt.want)
		}
	}
}

// TestModelGet checks that GET /model/{provider}/{id} answers with a model's
// catalog entry, and 404 for a model or provider it does not know.
func TestModelGet(t *testing.T) {
	ts, _ := newTestServerWith(t, withCatalog(t, catalogDoc(t, "gpt-x")))

	var m agent.Listing
	if status := doJSON(t, "GET", ts.URL+"/model/anthropic/claude-x", nil, &m); status != http.StatusOK || m.Model != "anthropic/claude-x" || m.Limit.Output != 64000 {
		t.Errorf("GET claude-x = %d, %+v, want 200 with its 64000-token output limit", status, m)
	}
	for _, path := range []string{"/model/anthropic/missing", "/model/nope/x"} {
		if status := doJSON(t, "GET", ts.URL+path, nil, nil); status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, status)
		}
	}
}

// TestModelRefresh checks that POST /model/refresh downloads the catalog,
// answers with the listings taken from it, and publishes
// catalog.models.refreshed, and that a failed download is a bad gateway.
func TestModelRefresh(t *testing.T) {
	var failing atomic.Bool
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(catalogDoc(t, "gpt-x", "gpt-new"))
	}))
	defer src.Close()
	ts, _ := newTestServerWith(t, func(cfg *config.Config) {
		withCatalog(t, catalogDoc(t, "gpt-x"))(cfg)
		cfg.CatalogSource = src.URL
	})
	addKey(t, ts)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/event", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()

	var got []app.ModelListing
	status := doJSON(t, "POST", ts.URL+"/model/refresh", nil, &got)
	if want := []string{"openai/gpt-new", "openai/gpt-x"}; status != http.StatusOK || !slices.Equal(modelIDs(got), want) {
		t.Errorf("POST /model/refresh = %d, %v, want 200, %v", status, modelIDs(got), want)
	}
	body := bufio.NewReader(events.Body)
	for {
		if event, _ := readSSEEvent(t, body); event == string(app.TypeModelsRefreshed) {
			break
		}
	}

	failing.Store(true)
	if status := doJSON(t, "POST", ts.URL+"/model/refresh", nil, nil); status != http.StatusBadGateway {
		t.Errorf("POST /model/refresh with a failing source = %d, want 502", status)
	}
}

// anthropicReply is a streamed Anthropic reply of "Hello there".
const anthropicReply = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":12}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

// newGenerateServer returns a server whose Anthropic provider is api, with
// an API key, its app, and a function returning the bodies of the requests
// api received.
func newGenerateServer(t *testing.T, api http.HandlerFunc, setups ...func(*config.Config)) (*httptest.Server, *app.App, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		api(w, r)
	}))
	t.Cleanup(stub.Close)
	ts, a := newTestServerWith(t, func(cfg *config.Config) {
		cfg.Provider = map[string]*config.Provider{"anthropic": {BaseURL: stub.URL}}
		for _, setup := range setups {
			setup(cfg)
		}
	})
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	return ts, a, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}
}

// generateBody is a request for claude-x with a system prompt.
var generateBody = GenerateRequest{
	Model:     "anthropic/claude-x",
	Messages:  []GenerateMessage{{Role: "system", Content: "Be brief."}, {Role: "user", Content: "hi"}},
	MaxTokens: 100,
}

// TestGenerate checks that POST /generate sends its messages and limit to
// the model, and answers with the whole reply.
func TestGenerate(t *testing.T) {
	ts, _, bodies := newGenerateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, anthropicReply)
	})

	var got GenerateResponse
	if status := doJSON(t, "POST", ts.URL+"/generate", generateBody, &got); status != http.StatusOK {
		t.Fatalf("POST /generate: status %d", status)
	}
	if got.Model != "anthropic/claude-x" || got.Text != "Hello there" || got.StopReason != "end_turn" ||
		got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 5 {
		t.Errorf("response = %+v, want claude-x's reply Hello there, end_turn, 12 in and 5 out", got)
	}
	sentBodies := bodies()
	if len(sentBodies) != 1 {
		t.Fatalf("model got %d requests, want 1", len(sentBodies))
	}
	sent, _ := json.Marshal(sentBodies[0])
	for _, want := range []string{`"max_tokens":100`, `"system":[{"text":"Be brief.","type":"text"}]`, `"messages":[{"content":"hi","role":"user"}]`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("model request %s lacks %s", sent, want)
		}
	}
}

// TestGenerateStream checks that a streamed POST /generate sends each text
// delta as it arrives, then the whole reply.
func TestGenerateStream(t *testing.T) {
	ts, _, _ := newGenerateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, anthropicReply)
	})

	body := generateBody
	body.Stream = true
	b, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/generate", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status, content type = %d, %q, want 200, text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	r := bufio.NewReader(resp.Body)
	var texts []string
	for range 2 {
		event, data := readSSEEvent(t, r)
		var delta GenerateDelta
		if event != EventGenerateTextDelta || json.Unmarshal([]byte(data), &delta) != nil {
			t.Fatalf("event %s %s, want %s", event, data, EventGenerateTextDelta)
		}
		texts = append(texts, delta.Text)
	}
	if want := []string{"Hello", " there"}; !slices.Equal(texts, want) {
		t.Errorf("deltas = %q, want %q", texts, want)
	}
	event, data := readSSEEvent(t, r)
	var ended GenerateResponse
	if event != EventGenerateEnded || json.Unmarshal([]byte(data), &ended) != nil || ended.Text != "Hello there" {
		t.Errorf("last event = %s %s, want %s with the text Hello there", event, data, EventGenerateEnded)
	}
}

// TestGenerateErrors checks the status of a POST /generate that is invalid,
// names an unknown provider, has no credentials, or that the model refuses.
func TestGenerateErrors(t *testing.T) {
	ts, _, _ := newGenerateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	user := []GenerateMessage{{Role: "user", Content: "hi"}}

	tests := []struct {
		name       string
		body       any
		wantStatus int
	}{
		{name: "not an object", body: "hi", wantStatus: http.StatusBadRequest},
		{name: "no messages", body: GenerateRequest{Model: "anthropic/claude-x"}, wantStatus: http.StatusBadRequest},
		{name: "unknown role", body: GenerateRequest{Model: "anthropic/claude-x", Messages: []GenerateMessage{{Role: "tool", Content: "x"}}}, wantStatus: http.StatusBadRequest},
		{name: "negative limit", body: GenerateRequest{Model: "anthropic/claude-x", Messages: user, MaxTokens: -1}, wantStatus: http.StatusBadRequest},
		{name: "unknown provider", body: GenerateRequest{Model: "nope/x", Messages: user}, wantStatus: http.StatusNotFound},
		{name: "no credentials", body: GenerateRequest{Model: "openai/gpt-x", Messages: user}, wantStatus: http.StatusUnauthorized},
		{name: "refused by the model", body: GenerateRequest{Model: "anthropic/claude-x", Messages: user, MaxTokens: 10}, wantStatus: http.StatusBadGateway},
	}
	for _, tt := range tests {
		if status := doJSON(t, "POST", ts.URL+"/generate", tt.body, nil); status != tt.wantStatus {
			t.Errorf("%s: status %d, want %d", tt.name, status, tt.wantStatus)
		}
	}
}

// TestBrokenConfigFile checks that once the config file cannot be read, the
// requests that need the default model fail with the parse error, rather
// than quietly use the built-in default.
func TestBrokenConfigFile(t *testing.T) {
	var home string
	ts, _ := newTestServerWith(t, func(cfg *config.Config) { home = cfg.HomeDir })
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		method, path string
		body         any
	}{
		{method: "GET", path: "/config"},
		{method: "POST", path: "/generate", body: GenerateRequest{Messages: []GenerateMessage{{Role: "user", Content: "hi"}}}},
	} {
		req, _ := http.NewRequestWithContext(t.Context(), tt.method, ts.URL+tt.path, jsonBody(t, tt.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(got.Error, config.FileName) {
			t.Errorf("%s %s = %d %q, want 500 naming %s", tt.method, tt.path, resp.StatusCode, got.Error, config.FileName)
		}
	}

	if _, err := app.New(t.Context(), config.NewService(home), app.Options{}); err == nil {
		t.Error("app.New over a broken config file = nil error, want an error")
	}
}

// jsonBody returns v encoded as a JSON request body, or no body when v is nil.
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

// TestModelRefreshLocalFailure checks that POST /model/refresh answers a
// failure to store the downloaded catalog as the server's own, 500, rather
// than a bad gateway.
func TestModelRefreshLocalFailure(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(catalogDoc(t, "gpt-x"))
	}))
	defer src.Close()
	ts, _ := newTestServerWith(t, func(cfg *config.Config) {
		cfg.CatalogSource = src.URL
		// A directory where the cache file belongs cannot be written as one.
		if err := os.Mkdir(filepath.Join(cfg.HomeDir, gateway.StoreFile), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	if status := doJSON(t, "POST", ts.URL+"/model/refresh", nil, nil); status != http.StatusInternalServerError {
		t.Errorf("POST /model/refresh with an unwritable cache = %d, want 500", status)
	}
}

// variantCatalog is an api.json of an Anthropic model that takes
// effort levels and has a fast mode.
const variantCatalog = `{"anthropic":{"id":"anthropic","models":{"claude-v":{"id":"claude-v","reasoning":true,
  "limit":{"context":200000,"output":64000},
  "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}],
  "experimental":{"modes":{"fast":{"provider":{"body":{"speed":"fast"},"headers":{"anthropic-beta":"fast-mode-2026-02-01"}}}}}}}}}`

// TestGenerateVariant checks that POST /generate runs a model variant: its
// mode's body fields and its level's thinking and effort reach the model,
// and the reply names the variant. GET /model lists the levels the model
// takes.
func TestGenerateVariant(t *testing.T) {
	ts, _, bodies := newGenerateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, anthropicReply)
	}, withCatalog(t, apiCatalog(t, variantCatalog)))

	body := GenerateRequest{Model: "anthropic/claude-v:fast:high", Messages: []GenerateMessage{{Role: "user", Content: "hi"}}}
	var got GenerateResponse
	if status := doJSON(t, "POST", ts.URL+"/generate", body, &got); status != http.StatusOK {
		t.Fatalf("POST /generate: status %d", status)
	}
	if got.Model != "anthropic/claude-v:fast:high" {
		t.Errorf("model = %q, want anthropic/claude-v:fast:high", got.Model)
	}
	sent := bodies()[0]
	if sent["model"] != "claude-v" || sent["speed"] != "fast" {
		t.Errorf("model request = %v, want claude-v at speed fast", sent)
	}
	thinking, _ := sent["thinking"].(map[string]any)
	effort, _ := sent["output_config"].(map[string]any)
	if thinking["type"] != "adaptive" || effort["effort"] != "high" {
		t.Errorf("thinking, output_config = %v, %v, want adaptive at effort high", sent["thinking"], sent["output_config"])
	}

	var listings []app.ModelListing
	if status := doJSON(t, "GET", ts.URL+"/model?provider=anthropic", nil, &listings); status != http.StatusOK || len(listings) != 1 {
		t.Fatalf("GET /model = %d, %+v, want one listing", status, listings)
	}
	want := []agent.ReasoningEffort{"low", "medium", "high", "xhigh", "max"}
	if got := listings[0].ReasoningEfforts["anthropic/claude-v"]; !slices.Equal(got, want) {
		t.Errorf("reasoning efforts of claude-v = %v, want %v", got, want)
	}
}
