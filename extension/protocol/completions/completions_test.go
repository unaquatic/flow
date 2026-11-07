package completions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
)

// eventBody returns server-sent events, each a pair of event type and data. An
// empty type writes the data alone, as Chat Completions does.
func eventBody(events ...[2]string) io.ReadCloser {
	var b strings.Builder
	for _, ev := range events {
		if ev[0] != "" {
			b.WriteString("event: " + ev[0] + "\n")
		}
		b.WriteString("data: " + ev[1] + "\n\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

// Call is a tool call as its stream parts describe it.
type Call struct {
	// ID and Name are those of the call's ToolUseStart.
	ID, Name string
	// Deltas is the input the call's deltas built.
	Deltas string
	// End is the input of the call's ToolUseEnd, and Ends counts them.
	End  string
	Ends int
}

// callsOf drains stream and returns its tool calls in the order they
// started. A delta or end for a call that did not start fails the test.
func callsOf(t *testing.T, stream agent.StreamResponse) []Call {
	t.Helper()
	var calls []Call
	find := func(id string) *Call {
		for i := range calls {
			if calls[i].ID == id {
				return &calls[i]
			}
		}
		t.Fatalf("input for call %q, which did not start", id)
		return nil
	}
	for part, err := range stream {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		switch p := part.(type) {
		case agent.ToolUseStart:
			calls = append(calls, Call{ID: p.ID, Name: p.Name})
		case agent.ToolUseDelta:
			find(p.ID).Deltas += p.JSON
		case agent.ToolUseEnd:
			c := find(p.ID)
			c.End, c.Ends = string(p.Input), c.Ends+1
		}
	}
	return calls
}

const (
	pwd = `{"command":"pwd"}`
	ls  = `{"command":"ls"}`
	cat = `{"command":"cat go.mod"}`
)

// chunk returns a Chat Completions chunk whose delta has toolCalls, and
// finishes with reason if it is not empty.
func chunk(reason string, toolCalls ...map[string]any) [2]string {
	choice := map[string]any{"delta": map[string]any{"tool_calls": toolCalls}}
	if reason != "" {
		choice["finish_reason"] = reason
	}
	b, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	return [2]string{"", string(b)}
}

// toolCall returns a Chat Completions tool call delta. An empty id or name
// is left out, as in a call's later chunks.
func toolCall(index int, id, name, args string) map[string]any {
	fn := map[string]any{"arguments": args}
	tc := map[string]any{"index": index, "function": fn}
	if id != "" {
		tc["id"], tc["type"] = id, "function"
	}
	if name != "" {
		fn["name"] = name
	}
	return tc
}

// TestHandler_ToolCalls checks that parallel calls keep their
// own arguments, whether chunks name a call by index or by id, and that each
// call ends once when the choice finishes, with an empty object if it got no
// arguments.
func TestHandler_ToolCalls(t *testing.T) {
	want := []Call{
		{ID: "call_a", Name: "bash", Deltas: pwd, End: pwd, Ends: 1},
		{ID: "call_b", Name: "bash", Deltas: ls, End: ls, Ends: 1},
	}
	tests := []struct {
		name   string
		events [][2]string
		want   []Call
	}{
		{
			name: "by index",
			events: [][2]string{
				chunk("", toolCall(0, "call_a", "bash", "")),
				chunk("", toolCall(0, "", "", `{"command":`)),
				chunk("", toolCall(1, "call_b", "bash", `{"command":`)),
				chunk("", toolCall(1, "", "", `"ls"}`)),
				chunk("", toolCall(0, "", "", `"pwd"}`)),
				chunk("tool_calls"),
				{"", "[DONE]"},
			},
			want: want,
		},
		{
			name: "by id on one index",
			events: [][2]string{
				chunk("", toolCall(0, "call_a", "bash", `{"command":`)),
				chunk("", toolCall(0, "call_b", "bash", `{"command":`)),
				chunk("", toolCall(0, "call_a", "", `"pwd"}`)),
				chunk("", toolCall(0, "call_b", "", `"ls"}`)),
				chunk("tool_calls"),
				{"", "[DONE]"},
			},
			want: want,
		},
		{
			// Some servers send no arguments for a function that takes none.
			name: "no arguments",
			events: [][2]string{
				chunk("", toolCall(0, "call_a", "bash", "")),
				chunk("tool_calls"),
				{"", "[DONE]"},
			},
			want: []Call{{ID: "call_a", Name: "bash", End: `{}`, Ends: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{}
			got := callsOf(t, agent.StreamEvents(eventBody(tt.events...), h.Handle))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("calls = %+v\nwant    %+v", got, tt.want)
			}
		})
	}
}

// staticAuth authorizes every request with itself.
type staticAuth agent.Authorization

func (a staticAuth) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization(a), nil
}

// TestStream_Request checks where Chat Completions takes a request's system
// messages, output limit and message roles: the system messages in place,
// and the limit as max_completion_tokens.
func TestStream_Request(t *testing.T) {
	req := agent.Request{Messages: []agent.Message{
		agent.NewSystemMessage("Be brief."),
		agent.NewSystemMessage("Use Go."),
		agent.NewUserMessage("hi"),
		agent.NewAgentMessage("hello"),
	}}
	tests := []struct {
		name      string
		maxTokens int
		want      any
	}{
		{name: "with a limit", maxTokens: 64000, want: float64(64000)},
		{name: "without a limit", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
			}))
			defer srv.Close()

			limited := req
			limited.MaxTokens = tt.maxTokens
			p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "token"}, BaseURL: srv.URL + "/v1"})
			stream, err := p.Model("gpt-x").Stream(t.Context(), limited)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}

			if path != "/v1/chat/completions" {
				t.Errorf("path = %q, want /v1/chat/completions", path)
			}
			if body["max_completion_tokens"] != tt.want {
				t.Errorf("max_completion_tokens = %v, want %v", body["max_completion_tokens"], tt.want)
			}
			items, _ := body["messages"].([]any)
			var roles []any
			for _, m := range items {
				roles = append(roles, m.(map[string]any)["role"])
			}
			if want := []any{"system", "system", "user", "assistant"}; !reflect.DeepEqual(roles, want) {
				t.Errorf("roles = %v, want %v", roles, want)
			}
		})
	}
}

// TestUsage checks that the usage reports the input that a prompt cache
// gets apart from it: OpenAI counts the tokens written to the cache in the
// input, and Usage does not.
func TestUsage(t *testing.T) {
	want := agent.Usage{
		InputTokens: 900, CachedInputTokens: 600, CacheCreationInputTokens: 100,
		OutputTokens: 50, ReasoningOutputTokens: 20, TotalTokens: 1050,
	}
	chat, _ := json.Marshal(map[string]any{"usage": map[string]any{
		"prompt_tokens":             1000,
		"prompt_tokens_details":     map[string]int{"cached_tokens": 600, "cache_write_tokens": 100},
		"completion_tokens":         50,
		"completion_tokens_details": map[string]int{"reasoning_tokens": 20},
		"total_tokens":              1050,
	}})
	var got agent.Usage
	for part, err := range agent.StreamEvents(eventBody([2]string{"", string(chat)}, [2]string{"", "[DONE]"}), (&Handler{}).Handle) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if u, ok := part.(agent.Usage); ok {
			got = u
		}
	}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

// roundTripFunc answers a client's requests without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestStream_NoCacheKey checks that a request's cache key does not reach a
// Chat Completions API, which may reject the field, as prompt_cache_key or
// as session headers.
func TestStream_NoCacheKey(t *testing.T) {
	var sent *http.Request
	var body map[string]any
	p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "token"}, BaseURL: "http://localhost:8080/v1"})
	p.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent = r
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	stream, err := p.Model("gpt-x").Stream(t.Context(), agent.Request{
		Messages: []agent.Message{agent.NewUserMessage("hi")},
		CacheKey: "sess-1",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}

	if got := sent.URL.String(); got != "http://localhost:8080/v1/chat/completions" {
		t.Errorf("url = %q, want the server's chat completions", got)
	}
	if v, ok := body["prompt_cache_key"]; ok {
		t.Errorf("prompt_cache_key = %v, want none", v)
	}
	for _, h := range []string{"session-id", "x-client-request-id"} {
		if got := sent.Header.Get(h); got != "" {
			t.Errorf("%s header = %q, want none", h, got)
		}
	}
}

// TestStream_Reasoning checks that Chat Completions takes a request's
// reasoning effort as reasoning_effort, with the mode's body fields merged
// in.
func TestStream_Reasoning(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
	}))
	defer srv.Close()

	p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "token"}, BaseURL: srv.URL})
	controls := gateway.Reasoning{Efforts: []gateway.Effort{"none", "low", "medium", "high", "xhigh", "max"}}
	stream, err := p.Model("gpt-5.6").Stream(t.Context(), agent.Request{
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		Reasoning: &agent.Reasoning{Effort: "none", Controls: controls},
		Body:      map[string]any{"service_tier": "priority", "reasoning": map[string]any{"mode": "pro"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}
	want := map[string]any{"reasoning_effort": "none", "reasoning": map[string]any{"mode": "pro"}, "service_tier": "priority"}
	for field, w := range want {
		if !reflect.DeepEqual(body[field], w) {
			t.Errorf("%s = %v, want %v", field, body[field], w)
		}
	}
}

// TestStream_OtherOAuth checks that another provider's OAuth token, such as
// a Google token at Vertex AI's OpenAI-compatible endpoint, goes to the
// provider's API as a bearer token.
func TestStream_OtherOAuth(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := New(Init{Name: "google-vertex", Auth: staticAuth{Kind: agent.CredentialOAuth, Token: "ya29"},
		Key: gateway.Auth{Scheme: gateway.AuthGoogle}, BaseURL: srv.URL + "/v1/endpoints/openapi"})
	resp, err := p.Model("google/gemma").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Collect()
	if path != "/v1/endpoints/openapi/chat/completions" || auth != "Bearer ya29" {
		t.Errorf("request to %s with %q, want the provider's chat completions with the token", path, auth)
	}
}
