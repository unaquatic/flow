package content

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

// sseBody returns the server-sent events of chunks.
func sseBody(chunks ...string) io.ReadCloser {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\r\n\r\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

// reply is a streamed response of a version 3 model that thinks, answers, and calls two
// functions in parallel, the first signed.
var reply = []string{
	`{"candidates":[{"content":{"role":"model","parts":[{"text":"Looking at ","thought":true}]}}]}`,
	`{"candidates":[{"content":{"role":"model","parts":[{"text":"the files.","thought":true,"thoughtSignature":"sig-thought"}]}}]}`,
	`{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me "},{"text":"check.","thoughtSignature":"sig-text"}]}}]}`,
	`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"bash","args":{"command":"ls"}},"thoughtSignature":"sig-call"},{"functionCall":{"name":"read","args":{"path":"go.mod"}}}]},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":30,"totalTokenCount":150}}`,
}

// TestStream checks the parts of a streamed response, as Collect gathers
// them: the thoughts with their signature, the text, the first call's
// signature just before it, and both calls, with the API's ID or one of
// their own, and the usage, the thoughts counted as output.
func TestStream(t *testing.T) {
	resp, err := Stream(sseBody(reply...), "provider-c").Collect()
	if err != nil {
		t.Fatal(err)
	}
	c := resp.Message.Content
	if len(c) != 5 {
		t.Fatalf("content = %#v, want 5 parts", c)
	}
	if r, ok := c[0].(message.ReasoningPart); !ok || r.Text != "Looking at the files." || r.Signature != "sig-thought" {
		t.Errorf("part 0 = %#v, want the signed thoughts", c[0])
	}
	if tp, ok := c[1].(message.TextPart); !ok || tp.Text != "Let me check." {
		t.Errorf("part 1 = %#v, want the text", c[1])
	}
	if r, ok := c[2].(message.ReasoningPart); !ok || r.Text != "" || r.Signature != "sig-call" {
		t.Errorf("part 2 = %#v, want the call's signature alone", c[2])
	}
	first, ok1 := c[3].(message.ToolUsePart)
	second, ok2 := c[4].(message.ToolUsePart)
	if !ok1 || !ok2 || first.CallID != "c1" || first.Name != "bash" || string(first.Input) != `{"command":"ls"}` ||
		second.Name != "read" || !strings.HasPrefix(second.CallID, "call_") || string(second.Input) != `{"path":"go.mod"}` {
		t.Errorf("calls = %#v, %#v", c[3], c[4])
	}
	if resp.StopReason != agent.StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	want := agent.Usage{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 50, ReasoningOutputTokens: 30, TotalTokens: 150}
	if resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

// TestToContents checks that a streamed reply goes back as the API sent it:
// the thoughts signed, and the signature on the call it came with; and
// that the first call of another model's turn takes SkipSignature, unless
// the model is older than version 3, which takes no IDs either.
func TestToContents(t *testing.T) {
	resp, err := Stream(sseBody(reply...), "provider-c").Collect()
	if err != nil {
		t.Fatal(err)
	}
	other := agent.Message{Role: message.RoleAgent, Content: []message.ContentPart{
		message.ToolUsePart{CallID: "toolu_1", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
		message.ToolUsePart{CallID: "toolu_2", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
	}}
	msgs := []agent.Message{
		agent.NewSystemMessage("Be brief."),
		agent.NewUserMessage("hi"),
		resp.Message,
		{Role: message.RoleUser, Content: []message.ContentPart{
			message.ToolResultPart{ToolUseID: "c1", Name: "bash", Text: "go.mod"},
			message.ToolResultPart{ToolUseID: resp.Message.Content[4].(message.ToolUsePart).CallID, Name: "read", Text: "no such file", IsError: true},
		}},
		other,
	}

	system, contents := ToContents(msgs, "model-a")
	if !reflect.DeepEqual(system, []Part{{Text: "Be brief."}}) {
		t.Errorf("system = %+v", system)
	}
	if len(contents) != 4 || contents[0].Role != "user" || contents[1].Role != "model" || contents[2].Role != "user" || contents[3].Role != "model" {
		t.Fatalf("contents = %+v", contents)
	}
	model := contents[1].Parts
	if len(model) != 4 {
		t.Fatalf("model parts = %+v, want thought, text and two calls", model)
	}
	if !model[0].Thought || model[0].ThoughtSignature != "sig-thought" || model[1].Text != "Let me check." || model[1].ThoughtSignature != "" {
		t.Errorf("thought and text = %+v, %+v", model[0], model[1])
	}
	if model[2].FunctionCall == nil || model[2].FunctionCall.ID != "c1" || model[2].ThoughtSignature != "sig-call" || model[3].ThoughtSignature != "" {
		t.Errorf("calls = %+v, %+v; want the first signed, as the API sent them", model[2], model[3])
	}
	results := contents[2].Parts
	if results[0].FunctionResponse.Response["output"] != "go.mod" || results[1].FunctionResponse.Response["error"] != "no such file" || results[0].FunctionResponse.ID != "c1" {
		t.Errorf("results = %+v, %+v", results[0].FunctionResponse, results[1].FunctionResponse)
	}
	if calls := contents[3].Parts; calls[0].ThoughtSignature != SkipSignature || calls[1].ThoughtSignature != "" {
		t.Errorf("another model's calls signed %q, %q; want SkipSignature on the first", calls[0].ThoughtSignature, calls[1].ThoughtSignature)
	}

	// The protocol tells a model before version 3 by its ID alone, so this
	// test names one of LegacyFamily.
	_, legacy := ToContents(msgs, LegacyFamily+"-2.5")
	if calls := legacy[3].Parts; calls[0].ThoughtSignature != "" || calls[0].FunctionCall.ID != "" || legacy[2].Parts[0].FunctionResponse.ID != "" {
		t.Errorf("version 2.5 call = %+v, response = %+v; want no signature and no IDs", calls[0], legacy[2].Parts[0].FunctionResponse)
	}
}

// key authorizes with an API key, or an OAuth token when oauth is set.
type key struct{ oauth bool }

func (k key) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	if k.oauth {
		return agent.Authorization{Kind: agent.CredentialOAuth, Token: "ya29"}, nil
	}
	return agent.Authorization{Kind: agent.CredentialAPIKey, Token: "gk"}, nil
}

// TestRequest checks the request a model sends: its URL and key, its
// system instruction, tools, output limit and thinking level, with a
// variant's fields over them; and an OAuth token as a bearer token.
func TestRequest(t *testing.T) {
	var got struct {
		path, query, key, bearer string
		body                     map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.query, got.key, got.bearer = r.URL.Path, r.URL.RawQuery, r.Header.Get("x-goog-api-key"), r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got.body)
		io.WriteString(w, "data: "+`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer srv.Close()

	p := New(Init{Auth: key{}, Key: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-goog-api-key"}, BaseURL: srv.URL + "/v1beta"})
	req := agent.Request{
		Messages:  []agent.Message{agent.NewSystemMessage("sys"), agent.NewUserMessage("hi")},
		Tools:     []agent.ToolDefinition{{Name: "bash", Description: "Run", InputSchema: agent.InputSchema{Properties: map[string]agent.PropertySchema{"command": {Type: "string"}}}}},
		MaxTokens: 8000,
		Reasoning: &agent.Reasoning{Effort: "low", Controls: gateway.Reasoning{Efforts: []gateway.Effort{"low", "high"}}},
		Body:      map[string]any{"serviceTier": "priority", "generationConfig": map[string]any{"temperature": 0.5}},
	}
	resp, err := p.Model("model-a").Stream(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := resp.Collect(); err != nil || r.Message.TextContent() != "ok" || r.StopReason != agent.StopEndTurn {
		t.Fatalf("Collect = %+v, %v", r, err)
	}
	if got.path != "/v1beta/models/model-a:streamGenerateContent" || got.query != "alt=sse" || got.key != "gk" || got.bearer != "" {
		t.Errorf("request to %s?%s, key %q, bearer %q", got.path, got.query, got.key, got.bearer)
	}
	gen := got.body["generationConfig"].(map[string]any)
	tc := gen["thinkingConfig"].(map[string]any)
	if gen["maxOutputTokens"] != float64(8000) || gen["temperature"] != 0.5 || tc["thinkingLevel"] != "low" || tc["includeThoughts"] != true || got.body["serviceTier"] != "priority" {
		t.Errorf("body = %v", got.body)
	}
	decl := got.body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["name"] != "bash" || decl["parametersJsonSchema"].(map[string]any)["type"] != "object" {
		t.Errorf("tool = %v", decl)
	}
	if si := got.body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any); si["text"] != "sys" {
		t.Errorf("systemInstruction = %v", si)
	}

	cloud := New(Init{Auth: key{oauth: true}, BaseURL: srv.URL + "/v1/projects/p/locations/global/publishers/lab-c"})
	resp, err = cloud.Model("model-a").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Collect()
	if got.bearer != "Bearer ya29" || got.key != "" || got.path != "/v1/projects/p/locations/global/publishers/lab-c/models/model-a:streamGenerateContent" {
		t.Errorf("cloud request to %s, bearer %q, key %q", got.path, got.bearer, got.key)
	}
}

// TestStreamFailures checks an error event, a blocked prompt, and a stream
// that ends before the response finishes.
func TestStreamFailures(t *testing.T) {
	_, err := Stream(sseBody(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`), "provider-c").Collect()
	if re, ok := errors.AsType[*agent.ResponseError](err); !ok || re.StatusCode != 429 || re.Message != "Resource exhausted" {
		t.Errorf("error event: %v", err)
	}
	_, err = Stream(sseBody(`{"promptFeedback":{"blockReason":"SAFETY"}}`), "provider-c").Collect()
	if re, ok := errors.AsType[*agent.ResponseError](err); !ok || !strings.Contains(re.Message, "SAFETY") {
		t.Errorf("blocked prompt: %v", err)
	}
	_, err = Stream(sseBody(`{"candidates":[{"content":{"parts":[{"text":"cut"}]}}]}`), "provider-c").Collect()
	if _, ok := errors.AsType[*agent.ResponseParseError](err); !ok {
		t.Errorf("cut stream: %v, want a parse error", err)
	}
	r, err := Stream(sseBody(`{"candidates":[{"content":{"parts":[{"text":"long"}]},"finishReason":"MAX_TOKENS"}]}`), "provider-c").Collect()
	if err != nil || r.StopReason != agent.StopMaxTokens {
		t.Errorf("MAX_TOKENS: %+v, %v", r, err)
	}
}

// TestToContents_ToolResultAttachments checks that the attachments of a
// tool result go in its function response's parts, named, to a model of
// version 3 and later, and, to a model before it, in a user turn after the
// turn of the function responses, after a line that names the call.
func TestToContents_ToolResultAttachments(t *testing.T) {
	result := message.ToolResultPart{ToolUseID: "c1", Name: "read", Text: "Read image shot.png", Attachments: []message.FilePart{
		{Data: "aW1n", MediaType: "image/png", Filename: "shot.png"},
	}}
	msgs := []agent.Message{{Role: message.RoleUser, Content: []message.ContentPart{result}}}
	encode := func(model string) string {
		t.Helper()
		_, out := ToContents(msgs, model)
		data, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	want := `[{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"read","response":{"output":"Read image shot.png"},` +
		`"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1n","displayName":"shot.png"}}]}}]}]`
	if got := encode("model-x-3"); got != want {
		t.Errorf("version 3 contents =\n%s\nwant\n%s", got, want)
	}

	want = `[{"role":"user","parts":[{"functionResponse":{"name":"read","response":{"output":"Read image shot.png"}}}]},` +
		`{"role":"user","parts":[{"text":"Attachments of the read call:"},{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]}]`
	if got := encode(LegacyFamily + "-2.5-flash"); got != want {
		t.Errorf("legacy contents =\n%s\nwant\n%s", got, want)
	}
}
