package messages

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

// eventBody returns server-sent events, each a pair of event type and data.
func eventBody(events ...[2]string) io.ReadCloser {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString("event: " + ev[0] + "\ndata: " + ev[1] + "\n\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

// event returns an event of type typ whose data is fields as JSON.
func event(typ string, fields map[string]any) [2]string {
	fields["type"] = typ
	b, _ := json.Marshal(fields)
	return [2]string{typ, string(b)}
}

// toolStart starts tool_use block index with id and input.
func toolStart(index int, id, input string) [2]string {
	return event("content_block_start", map[string]any{
		"index": index,
		"content_block": map[string]any{
			"type": "tool_use", "id": id, "name": "bash", "input": json.RawMessage(input),
		},
	})
}

// inputDelta streams partial JSON input of block index.
func inputDelta(index int, partial string) [2]string {
	return event("content_block_delta", map[string]any{
		"index": index,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": partial},
	})
}

// blockStop ends block index.
func blockStop(index int) [2]string {
	return event("content_block_stop", map[string]any{"index": index})
}

// Call is a tool call as its stream parts describe it.
type Call struct {
	// ID is that of the call's ToolUseStart.
	ID string
	// Deltas is the input the call's deltas built.
	Deltas string
	// End is the input of the call's ToolUseEnd, and Ends counts them.
	End  string
	Ends int
}

// TestStream_ToolCalls checks that each tool_use block streams its input
// under its own call and ends with all of it when the block stops.
func TestStream_ToolCalls(t *testing.T) {
	stream := eventStream(eventBody(
		event("message_start", map[string]any{"message": map[string]any{"id": "msg_1", "usage": map[string]int{}}}),
		// Input that streams in deltas.
		toolStart(0, "toolu_1", `{}`),
		inputDelta(0, `{"command":`),
		inputDelta(0, `"pwd"}`),
		blockStop(0),
		// Input that comes whole with the block.
		toolStart(1, "toolu_2", `{"command":"ls"}`),
		blockStop(1),
		// No input, for a tool that takes none.
		toolStart(2, "toolu_3", `{}`),
		blockStop(2),
		event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{}}),
		event("message_stop", map[string]any{}),
	), "anthropic")

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
			calls = append(calls, Call{ID: p.ID})
		case agent.ToolUseDelta:
			find(p.ID).Deltas += p.JSON
		case agent.ToolUseEnd:
			c := find(p.ID)
			c.End, c.Ends = string(p.Input), c.Ends+1
		}
	}

	want := []Call{
		{ID: "toolu_1", Deltas: `{"command":"pwd"}`, End: `{"command":"pwd"}`, Ends: 1},
		{ID: "toolu_2", Deltas: `{"command":"ls"}`, End: `{"command":"ls"}`, Ends: 1},
		{ID: "toolu_3", End: `{}`, Ends: 1},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v\nwant    %+v", calls, want)
	}
}

// TestStream_CollectsEveryBlock checks that a response's parts alone carry
// all of it: thinking with its signature, text, and a tool call, in block
// order, with the stop reason and usage.
func TestStream_CollectsEveryBlock(t *testing.T) {
	resp, err := eventStream(eventBody(
		event("message_start", map[string]any{"message": map[string]any{"usage": map[string]int{"input_tokens": 12}}}),
		event("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}}),
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "Need the "}}),
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "files."}}),
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "EqQBCgIYAh"}}),
		blockStop(0),
		event("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "text", "text": ""}}),
		event("content_block_delta", map[string]any{"index": 1, "delta": map[string]any{"type": "text_delta", "text": "Listing."}}),
		blockStop(1),
		toolStart(2, "toolu_1", `{}`),
		inputDelta(2, `{"command":"ls"}`),
		blockStop(2),
		event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 30}}),
		event("message_stop", map[string]any{}),
	), "anthropic").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := []message.ContentPart{
		message.ReasoningPart{Text: "Need the files.", Signature: "EqQBCgIYAh"},
		message.TextPart{Text: "Listing."},
		message.ToolUsePart{CallID: "toolu_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
	}
	if !reflect.DeepEqual(resp.Message.Content, want) {
		t.Errorf("content = %+v\nwant      %+v", resp.Message.Content, want)
	}
	if resp.StopReason != agent.StopToolUse || resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 30 {
		t.Errorf("stop reason, usage = %q, %+v, want %q with 12 in and 30 out", resp.StopReason, resp.Usage, agent.StopToolUse)
	}
}

// staticAuth authorizes every request with itself.
type staticAuth agent.Authorization

func (a staticAuth) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization(a), nil
}

// TestStream_Request checks that a request's system messages become the
// system blocks, with their cache control, and that its output limit and the
// roles of its other messages reach the API, and that a request without a
// limit sends none, for the API to reject.
func TestStream_Request(t *testing.T) {
	system := agent.Message{Role: message.RoleSystem, Content: []message.ContentPart{
		message.TextPart{Text: "Be brief.", CacheControl: &message.CacheControl{Type: "ephemeral"}},
	}}
	tests := []struct {
		name       string
		system     []agent.Message
		maxTokens  int
		wantSystem any
		wantMax    any
	}{
		{
			name: "system prompt and limit", system: []agent.Message{system}, maxTokens: 64000,
			wantSystem: []any{map[string]any{"type": "text", "text": "Be brief.", "cache_control": map[string]any{"type": "ephemeral"}}},
			wantMax:    float64(64000),
		},
		{name: "neither", wantSystem: nil, wantMax: nil},
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

			p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "key"}, BaseURL: srv.URL})
			stream, err := p.Model("claude-x").Stream(t.Context(), agent.Request{
				Messages:  append(tt.system, agent.NewUserMessage("hi"), agent.NewAgentMessage("hello")),
				MaxTokens: tt.maxTokens,
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}

			var roles []any
			for _, m := range body["messages"].([]any) {
				roles = append(roles, m.(map[string]any)["role"])
			}
			if !reflect.DeepEqual(body["system"], tt.wantSystem) || body["max_tokens"] != tt.wantMax || !reflect.DeepEqual(roles, []any{"user", "assistant"}) {
				t.Errorf("system, max_tokens, roles = %v, %v, %v, want %v, %v, [user assistant]", body["system"], body["max_tokens"], roles, tt.wantSystem, tt.wantMax)
			}
		})
	}
}

// controls returns reasoning controls with an effort control listing
// levels, and a token budget of at least lo tokens when lo is positive.
func controls(lo int, levels ...string) gateway.Reasoning {
	c := gateway.Reasoning{}
	for _, l := range levels {
		c.Efforts = append(c.Efforts, gateway.Effort(l))
	}
	if lo > 0 {
		c.Budget = &gateway.Budget{Min: lo}
	}
	return c
}

// The reasoning controls of real models, as the catalog lists them.
var (
	haiku45 = controls(1024)
	opus45  = controls(1024, "low", "medium", "high")
	opus46  = controls(1024, "low", "medium", "high", "max")
	opus48  = controls(0, "low", "medium", "high", "xhigh", "max")
	// opus48Off is Opus 4.8's as the catalog lists them: its reasoning can
	// be turned off, by the effort none.
	opus48Off = func() gateway.Reasoning {
		c := controls(0, "none", "low", "medium", "high", "xhigh", "max")
		c.Mandatory = new(false)
		return c
	}()
)

// TestAdaptiveThinking checks that a model thinks adaptively when its effort
// control lists max, whether or not it takes a budget too, and that one that
// takes a budget alone, or an effort without max, does not.
func TestAdaptiveThinking(t *testing.T) {
	tests := []struct {
		name    string
		options gateway.Reasoning
		want    bool
	}{
		{"haiku 4.5", haiku45, false},
		{"opus 4.5", opus45, false},
		{"opus 4.6", opus46, true},
		{"opus 4.8", opus48, true},
	}
	for _, tt := range tests {
		if got := AdaptiveThinking(tt.options); got != tt.want {
			t.Errorf("AdaptiveThinking(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestSetReasoning checks what each model's controls send at a level: the
// level as the effort, with adaptive thinking, which interleaves by itself,
// also on a model that takes a budget but thinks adaptively, or extended
// thinking at the level's budget, which leaves room for the answer and
// interleaves between tool calls; and nothing without a level, as for a
// model that lists no effort levels.
func TestSetReasoning(t *testing.T) {
	summarized := func(m map[string]any) map[string]any { m["display"] = "summarized"; return m }
	interleaved := map[string]string{"anthropic-beta": "interleaved-thinking-2025-05-14"}
	// at returns the reasoning of a model with options at level, as Stream
	// sets it.
	at := func(level agent.ReasoningEffort, options gateway.Reasoning) *agent.Reasoning {
		return agent.ReasoningFor(gateway.Offering{Reasoning: &options}, level)
	}
	tests := []struct {
		name         string
		reasoning    *agent.Reasoning
		maxTokens    int
		wantThinking any
		wantEffort   any
		wantHeaders  map[string]string
	}{
		{name: "nil"},
		{name: "no level", reasoning: at("", opus48)},
		{name: "budget alone lists no levels", reasoning: at("high", haiku45)},
		{
			name: "budget", reasoning: at("medium", opus45), maxTokens: 64000,
			wantThinking: summarized(map[string]any{"type": "enabled", "budget_tokens": 8192}),
			wantEffort:   map[string]any{"effort": "medium"},
			wantHeaders:  interleaved,
		},
		{
			name: "budget cut to leave room", reasoning: at("high", opus45), maxTokens: 4096,
			wantThinking: summarized(map[string]any{"type": "enabled", "budget_tokens": 3072}),
			wantEffort:   map[string]any{"effort": "high"},
			wantHeaders:  interleaved,
		},
		{
			name: "no room for a budget", reasoning: at("low", opus45), maxTokens: 1500,
			wantEffort: map[string]any{"effort": "low"},
		},
		{
			name: "adaptive though it takes a budget", reasoning: at("max", opus46), maxTokens: 128000,
			wantThinking: summarized(map[string]any{"type": "adaptive"}),
			wantEffort:   map[string]any{"effort": "max"},
		},
		{
			name: "effort", reasoning: at("xhigh", opus48),
			wantThinking: summarized(map[string]any{"type": "adaptive"}),
			wantEffort:   map[string]any{"effort": "xhigh"},
		},
		{
			name: "off", reasoning: at("none", opus48Off),
			wantThinking: map[string]any{"type": "disabled"},
		},
		{name: "off where reasoning cannot be turned off", reasoning: at("none", opus48)},
		{
			name: "effort flow does not know", reasoning: at("xxxhigh", controls(0, "low", "high", "max", "xxxhigh")),
			wantThinking: summarized(map[string]any{"type": "adaptive"}),
			wantEffort:   map[string]any{"effort": "xxxhigh"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{}
			headers := SetReasoning(body, tt.reasoning, tt.maxTokens)
			if !reflect.DeepEqual(body["thinking"], tt.wantThinking) || !reflect.DeepEqual(body["output_config"], tt.wantEffort) {
				t.Errorf("thinking, output_config = %v, %v, want %v, %v", body["thinking"], body["output_config"], tt.wantThinking, tt.wantEffort)
			}
			if !reflect.DeepEqual(headers, tt.wantHeaders) {
				t.Errorf("headers = %v, want %v", headers, tt.wantHeaders)
			}
		})
	}
}

// TestStream_Mode checks that a mode's body fields and headers reach the
// API beside the provider's own.
func TestStream_Mode(t *testing.T) {
	var body map[string]any
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
	}))
	defer srv.Close()

	p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "key"}, BaseURL: srv.URL})
	stream, err := p.Model("claude-opus-4-8").Stream(t.Context(), agent.Request{
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		MaxTokens: 64000,
		Reasoning: &agent.Reasoning{Effort: "high", Controls: opus48},
		Body:      map[string]any{"speed": "fast"},
		Headers:   map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}
	if body["speed"] != "fast" || body["model"] != "claude-opus-4-8" || body["output_config"] == nil {
		t.Errorf("body = %v, want speed fast beside the model and effort", body)
	}
	if got := header.Get("anthropic-beta"); got != "fast-mode-2026-02-01" {
		t.Errorf("anthropic-beta = %q", got)
	}
	if got := header.Get("x-api-key"); got != "key" {
		t.Errorf("x-api-key = %q, want key", got)
	}
}

// TestStream_RedactedThinking checks that a redacted_thinking block, which
// comes whole, becomes a reasoning part marked redacted, holding the
// encrypted reasoning, in its place among the other blocks.
func TestStream_RedactedThinking(t *testing.T) {
	resp, err := eventStream(eventBody(
		event("message_start", map[string]any{"message": map[string]any{"usage": map[string]int{}}}),
		event("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}}),
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "Plan."}}),
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig"}}),
		blockStop(0),
		event("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "redacted_thinking", "data": "EmwKAhgB"}}),
		blockStop(1),
		toolStart(2, "toolu_1", `{"command":"ls"}`),
		blockStop(2),
		event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{}}),
		event("message_stop", map[string]any{}),
	), "anthropic").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []message.ContentPart{
		message.ReasoningPart{Text: "Plan.", Signature: "sig"},
		message.ReasoningPart{Signature: "EmwKAhgB", Redacted: true},
		message.ToolUsePart{CallID: "toolu_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
	}
	if !reflect.DeepEqual(resp.Message.Content, want) {
		t.Errorf("content = %+v\nwant      %+v", resp.Message.Content, want)
	}
}

// TestToMessages_Reasoning checks how reasoning goes back to the API:
// redacted reasoning as redacted_thinking, signed reasoning as thinking,
// unsigned reasoning, as a stop mid-thought leaves it, as text, and empty
// unsigned reasoning not at all, with a message it empties left out.
func TestToMessages_Reasoning(t *testing.T) {
	agentMessage := func(parts ...message.ContentPart) agent.Message {
		return agent.Message{Role: message.RoleAgent, Content: parts}
	}
	_, out := ToMessages([]agent.Message{
		agent.NewUserMessage("list files"),
		agentMessage(
			message.ReasoningPart{Text: "Plan.", Signature: "sig"},
			message.ReasoningPart{Signature: "EmwKAhgB", Redacted: true},
			message.ToolUsePart{CallID: "toolu_1", Name: "bash", Input: json.RawMessage(`{}`)},
		),
		{Role: message.RoleUser, Content: []message.ContentPart{message.ToolResultPart{ToolUseID: "toolu_1", Text: "go.mod"}}},
		agentMessage(message.ReasoningPart{Text: "I was thinking abo"}),
		agentMessage(message.ReasoningPart{Text: "  "}, message.ReasoningPart{}),
		agent.NewUserMessage("go on"),
	})
	// A message's content is its compact string or its blocks, each as JSON.
	var got [][]string
	for _, m := range out {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			b, _ := json.Marshal(m.Content)
			got = append(got, []string{string(b)})
			continue
		}
		var encoded []string
		for _, block := range blocks {
			b, _ := json.Marshal(block)
			encoded = append(encoded, string(b))
		}
		got = append(got, encoded)
	}

	want := [][]string{
		{`"list files"`},
		{
			`{"type":"thinking","thinking":"Plan.","signature":"sig"}`,
			`{"type":"redacted_thinking","data":"EmwKAhgB"}`,
			`{"type":"tool_use","id":"toolu_1","name":"bash","input":{}}`,
		},
		{`{"type":"tool_result","tool_use_id":"toolu_1","content":"go.mod"}`},
		{`{"type":"text","text":"I was thinking abo"}`},
		{`"go on"`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("messages = %q\nwant       %q", got, want)
	}
}

// TestStream_InterleavedThinking checks that a request with extended
// thinking asks for it to interleave, beside the beta a mode adds, and that
// one with adaptive thinking adds no beta of its own.
func TestStream_InterleavedThinking(t *testing.T) {
	tests := []struct {
		name      string
		reasoning *agent.Reasoning
		want      string
	}{
		{name: "budget", reasoning: &agent.Reasoning{Effort: "medium", Controls: opus45}, want: "interleaved-thinking-2025-05-14,fast-mode-2026-02-01"},
		{name: "adaptive", reasoning: &agent.Reasoning{Effort: "high", Controls: opus48}, want: "fast-mode-2026-02-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var header http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header = r.Header
			}))
			defer srv.Close()

			p := New(Init{Auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "key"}, BaseURL: srv.URL})
			stream, err := p.Model("claude-x").Stream(t.Context(), agent.Request{
				Messages:  []agent.Message{agent.NewUserMessage("hi")},
				MaxTokens: 64000,
				Reasoning: tt.reasoning,
				Headers:   map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream {
			}
			if got := header.Get("anthropic-beta"); got != tt.want {
				t.Errorf("anthropic-beta = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStream_Vertex checks a request as Vertex AI takes it: to the model's
// streamRawPredict URL with a Google token as a bearer token, and a body
// that names Vertex's API version and not the model.
func TestStream_Vertex(t *testing.T) {
	var path, auth, version string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, version = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Anthropic-Version")
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer srv.Close()
	p := New(Init{Name: "google-vertex-anthropic", Auth: staticAuth{Kind: agent.CredentialOAuth, Token: "ya29"},
		Key: gateway.Auth{Scheme: gateway.AuthGoogle}, BaseURL: srv.URL + "/v1/projects/p/locations/global/publishers/anthropic/models", Vertex: true})
	resp, err := p.Model("claude-sonnet-4-5@20250929").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}, MaxTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	resp.Collect()
	if path != "/v1/projects/p/locations/global/publishers/anthropic/models/claude-sonnet-4-5@20250929:streamRawPredict" || auth != "Bearer ya29" || version != "" {
		t.Errorf("request to %s, Authorization %q, anthropic-version %q", path, auth, version)
	}
	if _, ok := body["model"]; ok || body["anthropic_version"] != VertexVersion || body["max_tokens"] != float64(1000) {
		t.Errorf("body = %v, want Vertex's version and no model", body)
	}
}

// TestStream_RateLimited checks that HTTP 429 responses preserve their body
// and remain retryable regardless of credentials or error text.
func TestStream_RateLimited(t *testing.T) {
	const generic = `{"type":"error","error":{"type":"rate_limit_error","message":"Error"},"request_id":"req_1"}`
	const limited = `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your rate limit"}}`
	oauth := staticAuth{Kind: agent.CredentialOAuth, Token: "sk-ant-oat"}
	for _, tt := range []struct {
		name   string
		auth   staticAuth
		vertex bool
		reply  string
	}{
		{name: "oauth generic error", auth: oauth, reply: generic},
		{name: "oauth rate limited", auth: oauth, reply: limited},
		{name: "api key", auth: staticAuth{Kind: agent.CredentialAPIKey, Token: "key"}, reply: generic},
		{name: "vertex", auth: oauth, vertex: true, reply: generic},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				io.WriteString(w, tt.reply)
			}))
			defer srv.Close()
			p := New(Init{Name: "anthropic", Auth: tt.auth, BaseURL: srv.URL, Vertex: tt.vertex})
			_, err := p.Model("claude-opus-5-5").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}, MaxTokens: 1000})
			respErr, ok := errors.AsType[*agent.ResponseError](err)
			if !ok || respErr.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("error = %#v, want a 429 response error", err)
			}
			if want := "anthropic response error (429): " + tt.reply; err.Error() != want {
				t.Errorf("error text = %q, want %q", err.Error(), want)
			}
			if !agent.Classify(err).Retryable {
				t.Errorf("Classify(%v).Retryable = false, want true", err)
			}
		})
	}
}
