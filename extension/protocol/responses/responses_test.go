package responses

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// eventBody returns server-sent events, each a pair of event type and data. An
// empty type writes the data alone.
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

// fnItem returns a Responses API function_call output item.
func fnItem(itemID, callID, args string) string {
	b, _ := json.Marshal(map[string]any{
		"id": itemID, "type": "function_call", "call_id": callID, "name": "bash", "arguments": args,
	})
	return string(b)
}

// itemEvent returns an output item event of item at output index.
func itemEvent(typ string, index int, item string) [2]string {
	b, _ := json.Marshal(map[string]any{"type": typ, "output_index": index, "item": json.RawMessage(item)})
	return [2]string{typ, string(b)}
}

// argsDelta returns an argument delta of output item itemID at index.
func argsDelta(itemID string, index int, delta string) [2]string {
	m := map[string]any{"type": "response.function_call_arguments.delta", "output_index": index, "delta": delta}
	if itemID != "" {
		m["item_id"] = itemID
	}
	b, _ := json.Marshal(m)
	return [2]string{"response.function_call_arguments.delta", string(b)}
}

// completed returns a response.completed event listing output.
func completed(output ...string) [2]string {
	items := make([]json.RawMessage, 0, len(output))
	for _, o := range output {
		items = append(items, json.RawMessage(o))
	}
	b, _ := json.Marshal(map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": items, "usage": map[string]int{"input_tokens": 1}},
	})
	return [2]string{"response.completed", string(b)}
}

const (
	pwd = `{"command":"pwd"}`
	ls  = `{"command":"ls"}`
	cat = `{"command":"cat go.mod"}`
)

// TestHandler_ToolCalls checks that each call gets its own
// arguments, however the stream delivers them, and ends once with all of
// them.
func TestHandler_ToolCalls(t *testing.T) {
	tests := []struct {
		name   string
		events [][2]string
		want   []Call
	}{
		{
			name: "deltas",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, fnItem("fc_1", "call_1", "")),
				argsDelta("fc_1", 0, `{"command":`),
				argsDelta("fc_1", 0, `"pwd"}`),
				itemEvent("response.output_item.done", 0, fnItem("fc_1", "call_1", pwd)),
				completed(),
			},
			want: []Call{{ID: "call_1", Name: "bash", Deltas: pwd, End: pwd, Ends: 1}},
		},
		{
			// A provider's backend sent parallel calls like these with no
			// argument deltas, so only the done items carry the arguments.
			name: "arguments only in done items",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, fnItem("fc_a", "call_f03a47b7c2c44376a8dc8c62d968c498", "")),
				itemEvent("response.output_item.added", 1, fnItem("fc_b", "call_728dfd3aa5474ba68489d74f9e62401e", "")),
				itemEvent("response.output_item.added", 2, fnItem("fc_c", "call_3ded36dd9df04faead3c9a05ce2eb3b0", "")),
				itemEvent("response.output_item.done", 0, fnItem("fc_a", "call_f03a47b7c2c44376a8dc8c62d968c498", pwd)),
				itemEvent("response.output_item.done", 1, fnItem("fc_b", "call_728dfd3aa5474ba68489d74f9e62401e", ls)),
				itemEvent("response.output_item.done", 2, fnItem("fc_c", "call_3ded36dd9df04faead3c9a05ce2eb3b0", cat)),
				completed(),
			},
			want: []Call{
				{ID: "call_f03a47b7c2c44376a8dc8c62d968c498", Name: "bash", End: pwd, Ends: 1},
				{ID: "call_728dfd3aa5474ba68489d74f9e62401e", Name: "bash", End: ls, Ends: 1},
				{ID: "call_3ded36dd9df04faead3c9a05ce2eb3b0", Name: "bash", End: cat, Ends: 1},
			},
		},
		{
			name: "interleaved deltas",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, fnItem("fc_a", "call_a", "")),
				itemEvent("response.output_item.added", 1, fnItem("fc_b", "call_b", "")),
				argsDelta("fc_b", 1, `{"command":`),
				argsDelta("fc_a", 0, `{"command":`),
				argsDelta("fc_a", 0, `"pwd"}`),
				argsDelta("fc_b", 1, `"ls"}`),
				itemEvent("response.output_item.done", 0, fnItem("fc_a", "call_a", pwd)),
				itemEvent("response.output_item.done", 1, fnItem("fc_b", "call_b", ls)),
				completed(),
			},
			want: []Call{
				{ID: "call_a", Name: "bash", Deltas: pwd, End: pwd, Ends: 1},
				{ID: "call_b", Name: "bash", Deltas: ls, End: ls, Ends: 1},
			},
		},
		{
			name: "deltas without item IDs",
			events: [][2]string{
				itemEvent("response.output_item.added", 1, fnItem("", "call_a", "")),
				itemEvent("response.output_item.added", 2, fnItem("", "call_b", "")),
				argsDelta("", 2, ls),
				argsDelta("", 1, pwd),
				itemEvent("response.output_item.done", 1, fnItem("", "call_a", pwd)),
				itemEvent("response.output_item.done", 2, fnItem("", "call_b", ls)),
				completed(),
			},
			want: []Call{
				{ID: "call_a", Name: "bash", Deltas: pwd, End: pwd, Ends: 1},
				{ID: "call_b", Name: "bash", Deltas: ls, End: ls, Ends: 1},
			},
		},
		{
			name: "arguments in the added item",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, fnItem("fc_1", "call_1", pwd)),
				completed(fnItem("fc_1", "call_1", pwd)),
			},
			want: []Call{{ID: "call_1", Name: "bash", Deltas: pwd, End: pwd, Ends: 1}},
		},
		{
			name:   "call only in the completed output",
			events: [][2]string{completed(fnItem("fc_1", "call_1", ls))},
			want:   []Call{{ID: "call_1", Name: "bash", Deltas: ls, End: ls, Ends: 1}},
		},
		{
			name: "no arguments",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, fnItem("fc_1", "call_1", "")),
				itemEvent("response.output_item.done", 0, fnItem("fc_1", "call_1", "")),
				completed(),
			},
			want: []Call{{ID: "call_1", Name: "bash", End: `{}`, Ends: 1}},
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

// TestHandler_CollectKeepsEveryCall checks that a response whose
// completed event lists no output still stops for tool use with its text and
// every call with its arguments.
func TestHandler_CollectKeepsEveryCall(t *testing.T) {
	h := &Handler{}
	stream := agent.StreamEvents(eventBody(
		textDelta(0, "Looking."),
		itemEvent("response.output_item.added", 1, fnItem("fc_a", "call_a", "")),
		itemEvent("response.output_item.added", 2, fnItem("fc_b", "call_b", "")),
		itemEvent("response.output_item.done", 1, fnItem("fc_a", "call_a", pwd)),
		itemEvent("response.output_item.done", 2, fnItem("fc_b", "call_b", ls)),
		completed(),
	), h.Handle)

	resp, err := stream.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.StopReason != agent.StopToolUse {
		t.Errorf("stop reason = %q, want %q", resp.StopReason, agent.StopToolUse)
	}
	want := []message.ContentPart{
		message.TextPart{Text: "Looking."},
		message.ToolUsePart{CallID: "call_a", Name: "bash", Input: json.RawMessage(pwd)},
		message.ToolUsePart{CallID: "call_b", Name: "bash", Input: json.RawMessage(ls)},
	}
	if !reflect.DeepEqual(resp.Message.Content, want) {
		t.Errorf("content = %+v, want %+v", resp.Message.Content, want)
	}
}

// textDelta returns an output text delta of the message at index.
func textDelta(index int, delta string) [2]string {
	b, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "output_index": index, "delta": delta})
	return [2]string{"response.output_text.delta", string(b)}
}

// msgItem returns a Responses API message output item with text.
func msgItem(itemID, text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": itemID, "type": "message", "role": "assistant",
		"content": []map[string]string{{"type": "output_text", "text": text}},
	})
	return string(b)
}

// TestHandler_Text checks that a message's text arrives once,
// whether it streams or comes only with the finished item or the completed
// response.
func TestHandler_Text(t *testing.T) {
	tests := []struct {
		name   string
		events [][2]string
	}{
		{
			name: "streamed",
			events: [][2]string{
				textDelta(0, "Hel"),
				textDelta(0, "lo"),
				itemEvent("response.output_item.done", 0, msgItem("msg_1", "Hello")),
				completed(msgItem("msg_1", "Hello")),
			},
		},
		{
			name: "only in the done item",
			events: [][2]string{
				itemEvent("response.output_item.done", 0, msgItem("msg_1", "Hello")),
				completed(),
			},
		},
		{
			name:   "only in the completed output",
			events: [][2]string{completed(msgItem("msg_1", "Hello"))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{}
			resp, err := agent.StreamEvents(eventBody(tt.events...), h.Handle).Collect()
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			want := []message.ContentPart{message.TextPart{Text: "Hello"}}
			if !reflect.DeepEqual(resp.Message.Content, want) || resp.StopReason != agent.StopEndTurn {
				t.Errorf("content, stop = %+v, %q, want %+v, %q", resp.Message.Content, resp.StopReason, want, agent.StopEndTurn)
			}
		})
	}
}

// staticAuth authorizes every request with itself.
type staticAuth agent.Authorization

func (a staticAuth) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization(a), nil
}

// backend is the backend of the tests' providers.
const backend = "https://backend.test/api"

// TestStream_Request checks where the Responses API takes a request's
// system messages, output limit and message roles: as instructions, the
// system texts joined, and as max_output_tokens, which the backend,
// which an OAuth token reaches, does not take.
func TestStream_Request(t *testing.T) {
	req := agent.Request{Messages: []agent.Message{
		agent.NewSystemMessage("Be brief."),
		agent.NewSystemMessage("Use Go."),
		agent.NewUserMessage("hi"),
		agent.NewAgentMessage("hello"),
	}, MaxTokens: 64000}
	tests := []struct {
		name     string
		kind     agent.CredentialKind
		wantPath string
		wantMax  any
	}{
		{name: "backend", kind: agent.CredentialOAuth, wantPath: "/backend/responses", wantMax: nil},
		{name: "api key", kind: agent.CredentialAPIKey, wantPath: "/v1/responses", wantMax: float64(64000)},
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

			p := New(Init{Auth: staticAuth{Kind: tt.kind, Token: "token"}, BaseURL: srv.URL + "/v1", BackendURL: srv.URL + "/backend"})
			stream, err := p.Model("model-x").Stream(t.Context(), req)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}

			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if body["instructions"] != "Be brief.\n\nUse Go." {
				t.Errorf("instructions = %v, want the system texts joined", body["instructions"])
			}
			if body["max_output_tokens"] != tt.wantMax {
				t.Errorf("max_output_tokens = %v, want %v", body["max_output_tokens"], tt.wantMax)
			}
			items, _ := body["input"].([]any)
			var roles []any
			for _, m := range items {
				roles = append(roles, m.(map[string]any)["role"])
			}
			if want := []any{"user", "assistant"}; !reflect.DeepEqual(roles, want) {
				t.Errorf("roles = %v, want %v", roles, want)
			}
		})
	}
}

// TestUsage checks that the usage reports the input that a prompt cache
// gets apart from it: the API counts the tokens written to the cache in the
// input, and Usage does not.
func TestUsage(t *testing.T) {
	want := agent.Usage{
		InputTokens: 900, CachedInputTokens: 600, CacheCreationInputTokens: 100,
		OutputTokens: 50, ReasoningOutputTokens: 20, TotalTokens: 1050,
	}
	responses, _ := json.Marshal(map[string]any{"response": map[string]any{
		"status": "completed",
		"usage": map[string]any{
			"input_tokens":          1000,
			"input_tokens_details":  map[string]int{"cached_tokens": 600, "cache_write_tokens": 100},
			"output_tokens":         50,
			"output_tokens_details": map[string]int{"reasoning_tokens": 20},
			"total_tokens":          1050,
		},
	}})
	var got agent.Usage
	for part, err := range agent.StreamEvents(eventBody([2]string{"response.completed", string(responses)}), (&Handler{}).Handle) {
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

// TestStream_CacheKey checks where each credential's requests go, and that
// a request's cache key reaches the API as prompt_cache_key, cut to 64
// characters: on the backend, which an OAuth token reaches and
// which also takes it as the session headers, and on the provider's API,
// which an API key reaches, as does an OAuth token where the provider has
// no backend.
func TestStream_CacheKey(t *testing.T) {
	long := strings.Repeat("k", 80)
	tests := []struct {
		name        string
		kind        agent.CredentialKind
		backend     string
		key         string
		wantURL     string
		wantKey     any
		wantSession string
	}{
		{
			name: "backend", kind: agent.CredentialOAuth, backend: backend, key: "sess-1",
			wantURL: backend + "/responses", wantKey: "sess-1", wantSession: "sess-1",
		},
		{
			name: "backend without a key", kind: agent.CredentialOAuth, backend: backend,
			wantURL: backend + "/responses", wantKey: nil,
		},
		{
			name: "backend with a long key", kind: agent.CredentialOAuth, backend: backend, key: long,
			wantURL: backend + "/responses", wantKey: long[:64], wantSession: long[:64],
		},
		{
			name: "api key", kind: agent.CredentialAPIKey, backend: backend, key: "sess-1",
			wantURL: DefaultBaseURL + "/responses", wantKey: "sess-1",
		},
		{
			name: "oauth without a backend", kind: agent.CredentialOAuth, key: "sess-1",
			wantURL: DefaultBaseURL + "/responses", wantKey: "sess-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent *http.Request
			var body map[string]any
			p := New(Init{Auth: staticAuth{Kind: tt.kind, Token: "token"}, BackendURL: tt.backend})
			p.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				sent = r
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			})

			stream, err := p.Model("model-x").Stream(t.Context(), agent.Request{
				Messages: []agent.Message{agent.NewUserMessage("hi")},
				CacheKey: tt.key,
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}

			if got := sent.URL.String(); got != tt.wantURL {
				t.Errorf("url = %q, want %q", got, tt.wantURL)
			}
			if body["prompt_cache_key"] != tt.wantKey {
				t.Errorf("prompt_cache_key = %v, want %v", body["prompt_cache_key"], tt.wantKey)
			}
			for _, h := range []string{"session-id", "x-client-request-id"} {
				if got := sent.Header.Get(h); got != tt.wantSession {
					t.Errorf("%s header = %q, want %q", h, got, tt.wantSession)
				}
			}
		})
	}
}

// reasoningItem returns a Responses API reasoning output item.
func reasoningItem(summary []string, encrypted string) string {
	parts := []map[string]string{}
	for _, s := range summary {
		parts = append(parts, map[string]string{"type": "summary_text", "text": s})
	}
	item := map[string]any{"id": "rs_1", "type": "reasoning", "summary": parts}
	if encrypted != "" {
		item["encrypted_content"] = encrypted
	}
	b, _ := json.Marshal(item)
	return string(b)
}

// summaryDelta returns a reasoning summary text delta of summary part part
// of the reasoning at index.
func summaryDelta(index, part int, delta string) [2]string {
	b, _ := json.Marshal(map[string]any{
		"type": "response.reasoning_summary_text.delta", "output_index": index, "summary_index": part, "delta": delta,
	})
	return [2]string{"response.reasoning_summary_text.delta", string(b)}
}

// TestHandler_Reasoning checks that a reasoning item becomes a
// reasoning part: its summary text, streamed or whole, with its parts set
// apart by blank lines, signed with the item that goes back to the model,
// encrypted reasoning included, even when only the completed response
// carries it.
func TestHandler_Reasoning(t *testing.T) {
	sig := func(summary []string, encrypted string) string {
		parts := []OutputContent{}
		for _, s := range summary {
			parts = append(parts, OutputContent{Type: "summary_text", Text: s})
		}
		b, _ := json.Marshal(ReasoningItem{ID: "rs_1", Type: "reasoning", Summary: parts, EncryptedContent: encrypted})
		return string(b)
	}
	tests := []struct {
		name   string
		events [][2]string
		want   message.ReasoningPart
	}{
		{
			name: "streamed summary",
			events: [][2]string{
				itemEvent("response.output_item.added", 0, reasoningItem(nil, "")),
				summaryDelta(0, 0, "Look at "),
				summaryDelta(0, 0, "the files."),
				summaryDelta(0, 1, "Then run tests."),
				itemEvent("response.output_item.done", 0, reasoningItem([]string{"Look at the files.", "Then run tests."}, "enc")),
				completed(reasoningItem([]string{"Look at the files.", "Then run tests."}, "enc")),
			},
			want: message.ReasoningPart{
				Text:      "Look at the files.\n\nThen run tests.",
				Signature: sig([]string{"Look at the files.", "Then run tests."}, "enc"),
			},
		},
		{
			name: "summary only in the done item",
			events: [][2]string{
				itemEvent("response.output_item.done", 0, reasoningItem([]string{"First.", "Second."}, "enc")),
				completed(reasoningItem([]string{"First.", "Second."}, "enc")),
			},
			want: message.ReasoningPart{Text: "First.\n\nSecond.", Signature: sig([]string{"First.", "Second."}, "enc")},
		},
		{
			name: "encrypted reasoning only when completed",
			events: [][2]string{
				itemEvent("response.output_item.done", 0, reasoningItem(nil, "")),
				completed(reasoningItem(nil, "late")),
			},
			want: message.ReasoningPart{Signature: sig(nil, "late")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := agent.StreamEvents(eventBody(tt.events...), (&Handler{}).Handle).Collect()
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if len(resp.Message.Content) != 1 || !reflect.DeepEqual(resp.Message.Content[0], tt.want) {
				t.Errorf("content = %+v\nwant      [%+v]", resp.Message.Content, tt.want)
			}
		})
	}
}

// TestToInput_Reasoning checks that a reasoning part that holds
// encrypted reasoning goes back as the reasoning item the API returned, ID
// included, just before the items that followed it, and that other
// reasoning parts, such as those of the Messages format, are left out.
func TestToInput_Reasoning(t *testing.T) {
	sig, _ := json.Marshal(ReasoningItem{ID: "rs_1", Type: "reasoning", Summary: []OutputContent{}, EncryptedContent: "enc"})
	msgs := []agent.Message{
		agent.NewUserMessage("list files"),
		{Role: message.RoleAgent, Content: []message.ContentPart{
			message.ReasoningPart{Text: "Need ls.", Signature: string(sig)},
			message.TextPart{Text: "Listing."},
			message.ToolUsePart{CallID: "call_1", Name: "bash", Input: json.RawMessage(ls)},
		}},
		{Role: message.RoleUser, Content: []message.ContentPart{
			message.ToolResultPart{ToolUseID: "call_1", Text: "go.mod"},
		}},
		{Role: message.RoleAgent, Content: []message.ContentPart{
			message.ReasoningPart{Text: "Another format's.", Signature: "EqQBCgIYAh"},
			message.TextPart{Text: "Done."},
		}},
	}
	_, input := ToInput(msgs)
	b, _ := json.Marshal(input)
	var items []map[string]any
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range items {
		kind, _ := it["type"].(string)
		if kind == "" {
			kind = it["role"].(string)
		}
		kinds = append(kinds, kind)
	}
	want := []string{"user", "reasoning", "assistant", "function_call", "function_call_output", "assistant"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("items = %v, want %v", kinds, want)
	}
	if items[1]["encrypted_content"] != "enc" || items[1]["id"] != "rs_1" {
		t.Errorf("reasoning item = %v, want the encrypted reasoning with its ID", items[1])
	}
}

// effortOptions returns the reasoning controls of a model whose effort
// control lists values.
func effortOptions(values ...string) gateway.Reasoning {
	var c gateway.Reasoning
	for _, v := range values {
		c.Efforts = append(c.Efforts, gateway.Effort(v))
	}
	return c
}

// The reasoning controls of a model whose efforts reach max, as the catalog lists them, and of a
// model whose list ends in a level flow does not know.
var (
	effortsMax   = effortOptions("none", "low", "medium", "high", "xhigh", "max")
	effortsUltra = effortOptions("none", "low", "medium", "high", "xhigh", "max", "ultra")
)

// TestStream_Reasoning checks how the Responses API takes a request's
// reasoning and mode: as reasoning with a summary, asking for the encrypted
// reasoning, with the mode's body fields merged in.
func TestStream_Reasoning(t *testing.T) {
	tests := []struct {
		name      string
		kind      agent.CredentialKind
		reasoning *agent.Reasoning
		want      map[string]any
	}{
		{
			name: "responses", kind: agent.CredentialOAuth, reasoning: &agent.Reasoning{Effort: "high", Controls: effortsMax},
			want: map[string]any{
				"reasoning":    map[string]any{"effort": "high", "summary": "auto", "mode": "pro"},
				"include":      []any{"reasoning.encrypted_content"},
				"service_tier": "priority",
			},
		},
		{
			name: "responses at the provider's default", kind: agent.CredentialOAuth, reasoning: &agent.Reasoning{Controls: effortsMax},
			want: map[string]any{
				"reasoning":    map[string]any{"summary": "auto", "mode": "pro"},
				"include":      []any{"reasoning.encrypted_content"},
				"service_tier": "priority",
			},
		},
		{
			name: "responses without reasoning", kind: agent.CredentialOAuth,
			want: map[string]any{"reasoning": map[string]any{"mode": "pro"}, "include": nil, "service_tier": "priority"},
		},
		{
			name: "effort flow does not know", kind: agent.CredentialOAuth, reasoning: &agent.Reasoning{Effort: "ultra", Controls: effortsUltra},
			want: map[string]any{
				"reasoning":    map[string]any{"effort": "ultra", "summary": "auto", "mode": "pro"},
				"include":      []any{"reasoning.encrypted_content"},
				"service_tier": "priority",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
			}))
			defer srv.Close()

			p := New(Init{Auth: staticAuth{Kind: tt.kind, Token: "token"}, BaseURL: srv.URL, BackendURL: srv.URL})
			stream, err := p.Model("model-a").Stream(t.Context(), agent.Request{
				Messages:  []agent.Message{agent.NewUserMessage("hi")},
				Reasoning: tt.reasoning,
				Body:      map[string]any{"service_tier": "priority", "reasoning": map[string]any{"mode": "pro"}},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}
			for field, want := range tt.want {
				if !reflect.DeepEqual(body[field], want) {
					t.Errorf("%s = %v, want %v", field, body[field], want)
				}
			}
		})
	}
}

// TestHandler_EmptyReasoning checks that a reasoning item with no
// summary and no encrypted reasoning, as a request that asked for neither
// gets, makes no part.
func TestHandler_EmptyReasoning(t *testing.T) {
	resp, err := agent.StreamEvents(eventBody(
		itemEvent("response.output_item.done", 0, reasoningItem(nil, "")),
		completed(reasoningItem(nil, "")),
	), (&Handler{}).Handle).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resp.Message.Content) != 0 {
		t.Errorf("content = %+v, want none", resp.Message.Content)
	}
}

// TestStream_PlatformResponses checks the platform Responses API request an
// API key makes: the output limit, which the backend does not take,
// no instructions when the request has no system messages, the reasoning,
// nothing stored, and no backend session headers.
func TestStream_PlatformResponses(t *testing.T) {
	var sent *http.Request
	var body map[string]any
	p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "sk"}})
	p.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent = r
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	stream, err := p.Model("model-b").Stream(t.Context(), agent.Request{
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		MaxTokens: 128000,
		Reasoning: &agent.Reasoning{Effort: "high", Controls: effortsMax},
		CacheKey:  "sess-1",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}

	if got := sent.URL.String(); got != DefaultBaseURL+"/responses" {
		t.Errorf("url = %q, want the platform Responses API", got)
	}
	want := map[string]any{
		"max_output_tokens": float64(128000),
		"store":             false,
		"reasoning":         map[string]any{"effort": "high", "summary": "auto"},
		"prompt_cache_key":  "sess-1",
		"instructions":      nil,
	}
	for field, w := range want {
		if !reflect.DeepEqual(body[field], w) {
			t.Errorf("%s = %v, want %v", field, body[field], w)
		}
	}
	if got := sent.Header.Get("session-id"); got != "" {
		t.Errorf("session-id header = %q, want none off the backend", got)
	}
}

// TestHandler_Ends checks how a response that does not complete ends: one
// cut short ends for its output limit, with its usage, and one that failed
// ends with its error, which names the provider that sent it.
func TestHandler_Ends(t *testing.T) {
	incomplete, _ := json.Marshal(map[string]any{
		"type":     "response.incomplete",
		"response": map[string]any{"status": "incomplete", "usage": map[string]int{"input_tokens": 10, "output_tokens": 128}},
	})
	resp, err := agent.StreamEvents(eventBody([2]string{"response.incomplete", string(incomplete)}), (&Handler{}).Handle).Collect()
	if err != nil || resp.StopReason != agent.StopMaxTokens || resp.Usage.OutputTokens != 128 {
		t.Errorf("incomplete response = %+v, %v, want stopped for max tokens with 128 output tokens", resp, err)
	}

	failed, _ := json.Marshal(map[string]any{
		"type":     "response.failed",
		"response": map[string]any{"status": "failed", "error": map[string]any{"code": "server_error", "message": "boom"}},
	})
	_, err = agent.StreamEvents(eventBody([2]string{"response.failed", string(failed)}), (&Handler{Name: "provider-a"}).Handle).Collect()
	if re, ok := errors.AsType[*agent.ResponseError](err); !ok || re.Message != "boom" || re.Provider != "provider-a" {
		t.Errorf("failed response error = %v, want a response error from provider-a saying boom", err)
	}
}
