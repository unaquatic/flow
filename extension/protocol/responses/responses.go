// Package responses speaks the Responses request format, for every
// provider whose API takes it: the platform API at DefaultBaseURL, a
// provider's backend for its login, and gateways at their own base URLs.
package responses

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/request"
	"github.com/unaquatic/flow/extension/protocol/internal/post"
	"github.com/unaquatic/flow/sse"
)

const (
	// DefaultBaseURL is the base URL of the format's own platform API.
	DefaultBaseURL = "https://api.openai.com/v1"

	// maxCacheKeyLen is the most characters the platform API takes in a
	// prompt_cache_key.
	maxCacheKeyLen = 64
)

// Client implements agent.Provider for an API that takes Responses
// requests at its base URL. An OAuth credential goes to the provider's
// backend instead, when it has one for its login; another provider's, such
// as a cloud's access token, goes as a bearer token to its API.
type Client struct {
	name       string
	auth       agent.Authorizer
	key        gateway.Auth
	baseURL    string
	backendURL string
	httpClient *http.Client
}

// Init configures a Client.
type Init struct {
	// Name is the provider's catalog ID, such as "provider-a".
	Name string
	// Auth resolves the credential of every request.
	Auth agent.Authorizer
	// Key says how an API key goes with a request: as a bearer token, or
	// in the header it names, such as api-key.
	Key gateway.Auth
	// BaseURL is the base URL that "/responses" follows. Empty is
	// DefaultBaseURL.
	BaseURL string
	// BackendURL is the base URL of the provider's backend, which an OAuth
	// credential's requests reach, in the shape its clients send them.
	// Empty sends them to BaseURL, as an API key's.
	BackendURL string
	// HTTPClient sends the requests. Nil is a new HTTP client.
	HTTPClient *http.Client
}

// New returns a client configured by init.
func New(init Init) *Client {
	return &Client{
		name:       init.Name,
		auth:       init.Auth,
		key:        init.Key,
		baseURL:    strings.TrimSuffix(cmp.Or(init.BaseURL, DefaultBaseURL), "/"),
		backendURL: strings.TrimSuffix(init.BackendURL, "/"),
		httpClient: cmp.Or(init.HTTPClient, &http.Client{}),
	}
}

// Name returns the provider's catalog ID.
func (c *Client) Name() string { return c.name }

// Role returns the name the API gives r: "assistant" for an agent message,
// and the name r writes for any other role.
func Role(r message.Role) string {
	switch r {
	case message.RoleAgent:
		return "assistant"
	default:
		return r.String()
	}
}

// Model returns the model with ID name.
func (c *Client) Model(name string) agent.Model {
	return &model{c: c, name: name}
}

// model is one model of a Client.
type model struct {
	c    *Client
	name string
}

// stream opens a Responses API event stream: the backend's when backend is
// set, and else the one at the provider's base URL. Neither stores the
// response, so each request sends the whole conversation. The backend
// requires the instructions and takes no output limit, as
// its clients send none; the others take the limit, and the instructions
// where there are some.
func (m *model) stream(ctx context.Context, token string, req agent.Request, backend bool) (agent.StreamResponse, error) {
	instructions, input := ToInput(req.Messages)
	body := map[string]any{
		"model":  m.name,
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if backend || instructions != "" {
		body["instructions"] = instructions
	}
	if !backend && req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if t := toTools(req.Tools); t != nil {
		body["tools"] = t
	}
	// A model that reasons returns a summary of its reasoning to show, and
	// the reasoning itself, encrypted, which goes back with the next request,
	// since nothing is stored.
	if r := req.Reasoning; r != nil {
		reasoning := map[string]any{"summary": "auto"}
		if r.Effort != "" {
			reasoning["effort"] = string(r.Effort)
		}
		body["reasoning"] = reasoning
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	body = request.MergeBody(body, req.Body)
	header := m.c.authHeader(token, backend)
	// The platform API routes a request to a prompt cache by its key. A
	// request without one rarely reaches a cache that holds its prefix. The
	// backend's session headers carry the same key.
	if key := cacheKey(req.CacheKey); key != "" {
		body["prompt_cache_key"] = key
		if backend {
			header.Set("session-id", key)
			header.Set("x-client-request-id", key)
		}
	}
	request.AddHeaders(header, req.Headers)

	url := m.c.baseURL + "/responses"
	if backend {
		url = m.c.backendURL + "/responses"
	}
	resp, err := post.JSON(ctx, m.c.httpClient, m.c.Name(), url, header, body)
	if err != nil {
		return nil, err
	}
	return agent.StreamEvents(resp.Body, (&Handler{Name: m.c.name}).Handle), nil
}

// authHeader returns the header that authorizes a request with token: as a
// bearer token, for an OAuth token and unless the key goes in a header of
// its own.
func (c *Client) authHeader(token string, oauth bool) http.Header {
	if !oauth && c.key.Scheme == gateway.AuthHeader && c.key.Header != "" {
		return http.Header{http.CanonicalHeaderKey(c.key.Header): {token}}
	}
	return http.Header{"Authorization": {"Bearer " + token}}
}

// cacheKey returns key cut to the characters the platform API takes in a
// prompt_cache_key.
func cacheKey(key string) string {
	if utf8.RuneCountInString(key) <= maxCacheKeyLen {
		return key
	}
	return string([]rune(key)[:maxCacheKeyLen])
}

// StreamedCall is one function call of a streamed Responses API response.
type StreamedCall struct {
	// Index is the position of the call in the response output, which the
	// call's events carry.
	Index int
	// ItemID identifies the call's Responses API output item, which its
	// argument deltas name.
	ItemID string
	// CallID identifies the call to the model.
	CallID string
	// Name identifies the called function.
	Name string
	// Arguments is the JSON input streamed so far, and all of it once Done.
	Arguments string
	// Done reports that the call's complete input was yielded.
	Done bool
}

// StreamedReasoning is one reasoning item of a streamed Responses API
// response.
type StreamedReasoning struct {
	// Streamed reports that summary text of the item was yielded.
	Streamed bool
	// Summary is the index of the summary part whose text streamed last.
	Summary int
	// Signed reports that the item's signature, with its encrypted
	// reasoning, was yielded.
	Signed bool
}

// Handler turns Responses API events into stream parts and keeps the
// streamed output for the final response.
type Handler struct {
	// Name is the catalog ID of the provider that sends the events, which
	// the errors they report name.
	Name string
	// TextItems holds the output indexes of the message items whose text
	// was yielded.
	TextItems []int
	// Calls holds the response's function calls in the order they started.
	Calls []*StreamedCall
	// Reasoning holds the response's reasoning items by output index.
	Reasoning map[int]*StreamedReasoning
}

// ReasoningAt returns the reasoning item at output index, which it starts
// tracking when it has not yet.
func (h *Handler) ReasoningAt(index int) *StreamedReasoning {
	if h.Reasoning == nil {
		h.Reasoning = make(map[int]*StreamedReasoning)
	}
	r, ok := h.Reasoning[index]
	if !ok {
		r = &StreamedReasoning{}
		h.Reasoning[index] = r
	}
	return r
}

// ReasoningItem is a Responses API reasoning item as a reasoning part keeps
// it in its signature, to send it back to the model with later requests, as
// the API returned it.
type ReasoningItem struct {
	// ID identifies the item, as the API returned it.
	ID string `json:"id,omitempty"`
	// Type is "reasoning".
	Type string `json:"type"`
	// Summary holds the parts of the reasoning's summary.
	Summary []OutputContent `json:"summary"`
	// EncryptedContent is the reasoning, which only the model can read.
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// ParseReasoningItem returns the reasoning item that signature holds, and
// reports whether it holds one with encrypted reasoning, the only kind
// worth sending back. The signatures of the Messages format hold none.
func ParseReasoningItem(signature string) (ReasoningItem, bool) {
	var item ReasoningItem
	if json.Unmarshal([]byte(signature), &item) != nil || item.Type != "reasoning" || item.EncryptedContent == "" {
		return ReasoningItem{}, false
	}
	return item, true
}

// EndReasoning completes item, the reasoning at output index: it yields
// the text of its summary unless the text streamed, and then, once, its
// signature, the item as ReasoningItem keeps it, when it has encrypted
// reasoning to send back. Some servers send the encrypted reasoning only in
// the completed response, which ends the item again. It reports whether
// streaming should continue.
func (h *Handler) EndReasoning(item Output, index int, yield func(agent.StreamPart, error) bool) bool {
	r := h.ReasoningAt(index)
	block := agent.Block(index)
	if !r.Streamed {
		var texts []string
		for _, s := range item.Summary {
			if s.Text != "" {
				texts = append(texts, s.Text)
			}
		}
		if len(texts) > 0 {
			r.Streamed = true
			if !yield(agent.ReasoningDelta{Block: block, Text: strings.Join(texts, "\n\n")}, nil) {
				return false
			}
		}
	}
	if r.Signed || item.EncryptedContent == "" {
		return true
	}
	// The API requires the summary, even when it is empty.
	summary := item.Summary
	if summary == nil {
		summary = []OutputContent{}
	}
	sig, err := json.Marshal(ReasoningItem{ID: item.ID, Type: "reasoning", Summary: summary, EncryptedContent: item.EncryptedContent})
	if err != nil {
		return true
	}
	r.Signed = true
	return yield(agent.ReasoningSignature{Block: block, Signature: string(sig)}, nil)
}

// Handle folds one event into the response, yields the stream parts it
// makes, and reports whether streaming should continue.
func (h *Handler) Handle(event sse.Event, yield func(agent.StreamPart, error) bool) bool {
	data := []byte(event.Data)
	switch event.Type {
	case "response.output_text.delta":
		var delta struct {
			OutputIndex int    `json:"output_index"`
			Delta       string `json:"delta"`
		}
		if json.Unmarshal(data, &delta) == nil && delta.Delta != "" {
			if !slices.Contains(h.TextItems, delta.OutputIndex) {
				h.TextItems = append(h.TextItems, delta.OutputIndex)
			}
			return yield(agent.TextDelta{Block: agent.Block(delta.OutputIndex), Text: delta.Delta}, nil)
		}

	case "response.output_item.added", "response.output_item.done":
		var ev struct {
			OutputIndex int    `json:"output_index"`
			Item        Output `json:"item"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		done := event.Type == "response.output_item.done"
		switch {
		case ev.Item.Type == "message" && done:
			return h.EndMessage(ev.Item, ev.OutputIndex, yield)
		case ev.Item.Type == "reasoning" && done:
			return h.EndReasoning(ev.Item, ev.OutputIndex, yield)
		case ev.Item.Type != "function_call":
			return true
		case done:
			return h.EndCall(ev.Item, ev.OutputIndex, yield)
		case h.Call(ev.Item.ID, ev.OutputIndex) != nil:
			return true
		}
		_, ok := h.StartCall(ev.Item, ev.OutputIndex, yield)
		return ok

	case "response.reasoning_summary_text.delta":
		var ev struct {
			OutputIndex  int    `json:"output_index"`
			SummaryIndex int    `json:"summary_index"`
			Delta        string `json:"delta"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Delta == "" {
			return true
		}
		r := h.ReasoningAt(ev.OutputIndex)
		text := ev.Delta
		// A blank line parts a summary's parts, as the summary is written.
		if r.Streamed && ev.SummaryIndex != r.Summary {
			text = "\n\n" + text
		}
		r.Streamed, r.Summary = true, ev.SummaryIndex
		return yield(agent.ReasoningDelta{Block: agent.Block(ev.OutputIndex), Text: text}, nil)

	case "response.function_call_arguments.delta":
		var ev struct {
			ItemID      string `json:"item_id"`
			OutputIndex int    `json:"output_index"`
			Delta       string `json:"delta"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Delta == "" {
			return true
		}
		c := h.Call(ev.ItemID, ev.OutputIndex)
		if c == nil || c.Done {
			return true
		}
		c.Arguments += ev.Delta
		return yield(agent.ToolUseDelta{ID: c.CallID, JSON: ev.Delta}, nil)

	// A response cut short, as by its output limit, ends incomplete.
	case "response.completed", "response.incomplete":
		return h.Complete(data, yield)

	case "response.failed":
		// The body keeps only the response's error, not the output that
		// came before it, whose text Classify would read as the error's.
		var ev struct {
			Response struct {
				Error json.RawMessage `json:"error"`
			} `json:"response"`
		}
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil || string(ev.Response.Error) == "null" {
			ev.Response.Error = nil
		}
		json.Unmarshal(ev.Response.Error, &e)
		yield(nil, &agent.ResponseError{
			Provider:     h.Name,
			Message:      cmp.Or(e.Message, "response failed"),
			ResponseBody: string(ev.Response.Error),
		})
		return false

	case "error":
		var errData struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(data, &errData) == nil && errData.Message != "" {
			yield(nil, &agent.ResponseError{
				Provider:     h.Name,
				StatusCode:   0,
				Message:      errData.Message,
				ResponseBody: string(data),
			})
			return false
		}
	}
	return true
}

// Call returns the function call of output item itemID, or the one at
// output index when no call has that item ID, and nil when there is none.
func (h *Handler) Call(itemID string, index int) *StreamedCall {
	if itemID != "" {
		if i := slices.IndexFunc(h.Calls, func(c *StreamedCall) bool { return c.ItemID == itemID }); i >= 0 {
			return h.Calls[i]
		}
	}
	if i := slices.IndexFunc(h.Calls, func(c *StreamedCall) bool { return c.Index == index }); i >= 0 {
		return h.Calls[i]
	}
	return nil
}

// StartCall records item, a function call the response began at output
// index, and yields its start and the arguments it already has. It returns
// the call and whether streaming should continue.
func (h *Handler) StartCall(item Output, index int, yield func(agent.StreamPart, error) bool) (*StreamedCall, bool) {
	c := &StreamedCall{Index: index, ItemID: item.ID, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments}
	h.Calls = append(h.Calls, c)
	if !yield(agent.ToolUseStart{ID: c.CallID, Name: c.Name}, nil) {
		return c, false
	}
	if c.Arguments != "" && !yield(agent.ToolUseDelta{ID: c.CallID, JSON: c.Arguments}, nil) {
		return c, false
	}
	return c, true
}

// EndCall completes item, the function call at output index, and yields its
// complete arguments once, starting the call first if no event did. The
// item's arguments are the call's whole input, and some calls stream no
// deltas, so they replace the input the deltas built. A call that ends with
// no arguments takes none, and gets an empty object. It reports whether
// streaming should continue.
func (h *Handler) EndCall(item Output, index int, yield func(agent.StreamPart, error) bool) bool {
	c := h.Call(item.ID, index)
	if c == nil {
		var ok bool
		if c, ok = h.StartCall(item, index, yield); !ok {
			return false
		}
	}
	if c.Done {
		return true
	}
	c.Done = true
	c.Arguments = cmp.Or(item.Arguments, c.Arguments, "{}")
	return yield(agent.ToolUseEnd{ID: c.CallID, Input: json.RawMessage(c.Arguments)}, nil)
}

// EndMessage yields the text of item, the message at output index, unless
// its text already streamed. Some responses stream no text deltas, so the
// finished item is where their text is. It reports whether streaming should
// continue.
func (h *Handler) EndMessage(item Output, index int, yield func(agent.StreamPart, error) bool) bool {
	if slices.Contains(h.TextItems, index) {
		return true
	}
	h.TextItems = append(h.TextItems, index)
	for _, c := range item.Content {
		if c.Text != "" && !yield(agent.TextDelta{Block: agent.Block(index), Text: c.Text}, nil) {
			return false
		}
	}
	return true
}

// Complete handles response.completed: it yields the text and ends the
// function calls of the output items the stream left unfinished, then
// yields the usage and the stop.
func (h *Handler) Complete(data []byte, yield func(agent.StreamPart, error) bool) bool {
	var envelope struct {
		Response *apiResponse `json:"response"`
	}
	var completed apiResponse
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Response != nil {
		completed = *envelope.Response
	} else if json.Unmarshal(data, &completed) != nil {
		return true
	}

	for i, item := range completed.Output {
		switch item.Type {
		case "message":
			if !h.EndMessage(item, i, yield) {
				return false
			}
		case "reasoning":
			if !h.EndReasoning(item, i, yield) {
				return false
			}
		case "function_call":
			if !h.EndCall(item, i, yield) {
				return false
			}
		}
	}

	inTok := completed.Usage.InputTokens
	if inTok == 0 {
		inTok = completed.Usage.PromptTokens
	}
	outTok := completed.Usage.OutputTokens
	if outTok == 0 {
		outTok = completed.Usage.CompletionTokens
	}
	totalTok := completed.Usage.TotalTokens
	if totalTok == 0 {
		totalTok = inTok + outTok
	}
	// The input count holds the tokens written to the cache, which Usage
	// counts apart.
	written := completed.Usage.InputTokensDetails.CacheWriteTokens
	if !yield(agent.Usage{
		InputTokens:              max(0, inTok-written),
		CachedInputTokens:        completed.Usage.InputTokensDetails.CachedTokens,
		CacheCreationInputTokens: written,
		OutputTokens:             outTok,
		ReasoningOutputTokens:    completed.Usage.OutputTokensDetails.ReasoningTokens,
		TotalTokens:              totalTok,
	}, nil) {
		return false
	}

	// Calls holds every streamed call and every call the output lists.
	var stopReason agent.StopReason
	switch {
	case len(h.Calls) > 0:
		stopReason = agent.StopToolUse
	case completed.Status == "incomplete":
		stopReason = agent.StopMaxTokens
	default:
		stopReason = agent.StopEndTurn
	}
	yield(agent.Stop{Reason: stopReason}, nil)
	return false
}

// apiResponse is a Responses API response envelope.
type apiResponse struct {
	ID     string   `json:"id"`
	Model  string   `json:"model"`
	Status string   `json:"status"`
	Output []Output `json:"output"`
	Usage  struct {
		InputTokens        int `json:"input_tokens"`
		InputTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"input_tokens_details"`
		OutputTokens        int `json:"output_tokens"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
		TotalTokens int `json:"total_tokens"`
		// The backend can reuse Chat Completions usage field names.
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Output is one Responses API output item.
type Output struct {
	// Type selects the item kind, such as "message" or "function_call".
	Type string `json:"type"`
	// ID identifies the item, which the events of its parts name.
	ID string `json:"id"`
	// Role is the sender of a message item.
	Role string `json:"role,omitempty"`
	// Content holds the parts of a message item.
	Content []OutputContent `json:"content,omitempty"`
	// CallID identifies a function call to the model.
	CallID string `json:"call_id,omitempty"`
	// Name identifies the function a call invokes.
	Name string `json:"name,omitempty"`
	// Arguments is the JSON input of a function call.
	Arguments string `json:"arguments,omitempty"`
	// Status reports whether the item is in progress or complete.
	Status string `json:"status,omitempty"`
	// Summary holds the summary parts of a reasoning item.
	Summary []OutputContent `json:"summary,omitempty"`
	// EncryptedContent is the reasoning of a reasoning item, encrypted,
	// when the request included it.
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// OutputContent is one Responses API message output part, or one
// part of a reasoning summary.
type OutputContent struct {
	// Type selects the part kind, such as "output_text" or "summary_text".
	Type string `json:"type"`
	// Text is the text of the part.
	Text string `json:"text"`
}

// ToInput converts provider messages to Responses API input items,
// and the text of the system messages, joined by blank lines, to the
// instructions, which the API takes apart from the input.
func ToInput(msgs []agent.Message) (instructions string, input []any) {
	var system []string
	for _, m := range msgs {
		if m.Role == message.RoleSystem {
			system = append(system, m.TextContent())
			continue
		}

		// Items go in the order of the parts, as the model wrote them: a
		// reasoning item must come just before the item it led to. Text and
		// images gather into one message item until another item comes.
		var content []map[string]any
		var results []any
		flush := func() {
			if len(content) == 0 {
				return
			}
			msg := map[string]any{"role": Role(m.Role)}
			// Use the compact string form for one user text part.
			if len(content) == 1 && content[0]["type"] == "input_text" {
				msg["content"] = content[0]["text"]
			} else {
				msg["content"] = content
			}
			input = append(input, msg)
			content = nil
		}

		for _, p := range m.Content {
			switch v := p.(type) {
			case message.TextPart:
				contentType := "input_text"
				if m.Role == message.RoleAgent {
					contentType = "output_text"
				}
				content = append(content, map[string]any{
					"type": contentType,
					"text": v.Text,
				})
			case message.ImagePart:
				url := ""
				if v.Source != nil {
					if v.Source.SourceType == message.ImageSourceURL {
						url = v.Source.URL
					} else if v.Source.SourceType == message.ImageSourceBase64 {
						url = "data:" + v.Source.MediaType + ";base64," + v.Source.Data
					}
				}
				content = append(content, map[string]any{
					"type":      "input_image",
					"image_url": url,
				})
			case message.ReasoningPart:
				if item, ok := ParseReasoningItem(v.Signature); ok {
					flush()
					input = append(input, item)
				}
			case message.ToolUsePart:
				flush()
				args := string(v.Input)
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   v.CallID,
					"name":      v.Name,
					"arguments": args,
				})
			case message.ToolResultPart:
				results = append(results, map[string]any{
					"type":    "function_call_output",
					"call_id": v.ToolUseID,
					"output":  v.Text,
				})
			}
		}
		flush()
		input = append(input, results...)
	}
	return strings.Join(system, "\n\n"), input
}

// Stream sends req: with an OAuth credential to the provider's backend, when
// the provider has one, and else with the credential to the provider's API
// at its base URL.
func (m *model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	return agent.Authorized(ctx, m.c.auth, func(a agent.Authorization) (agent.StreamResponse, error) {
		backend := a.Kind == agent.CredentialOAuth && m.c.backendURL != ""
		return m.stream(ctx, a.Token, req, backend)
	})
}

// tool is a Responses API tool definition.
type tool struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Parameters  agent.InputSchema `json:"parameters"`
}

// toTools converts tools to Responses API definitions.
func toTools(tools []agent.ToolDefinition) []tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema.Type == "" {
			schema.Type = "object"
		}
		out = append(out, tool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}
	return out
}
