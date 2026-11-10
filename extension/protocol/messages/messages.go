// Package messages speaks the Messages request format, for every provider
// whose API takes it: the format's own API at DefaultBaseURL, a cloud's, in
// the raw predict shape it takes, and gateways at their own base URLs.
package messages

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/request"
	"github.com/unaquatic/flow/extension/protocol/internal/post"
	"github.com/unaquatic/flow/internal/sse"
)

// DefaultBaseURL is the base URL of the format's own API.
const DefaultBaseURL = "https://api.anthropic.com/v1"

// The headers and body field that name the API version and its betas.
const (
	// VersionHeader names the API version of a request.
	VersionHeader = "Anthropic-Version"
	// BetaHeader names the beta features a request turns on, a
	// comma-separated list.
	BetaHeader = "anthropic-beta"
	// VersionField names the API version in the body of a raw predict
	// request, in place of VersionHeader.
	VersionField = "anthropic_version"
)

// APIVersion is the API version that requests name in VersionHeader.
const APIVersion = "2023-06-01"

// RawPredictVersion is the API version that raw predict requests name in
// VersionField.
const RawPredictVersion = "vertex-2023-10-16"

// Client implements agent.Provider for an API that takes Messages
// requests: the format's own, or another provider's at its base URL, such
// as a gateway's.
type Client struct {
	name       string
	auth       agent.Authorizer
	key        gateway.Auth
	baseURL    string
	rawPredict bool
	httpClient *http.Client
}

// Init configures a Client.
type Init struct {
	// Name is the provider's catalog ID, such as "provider-a".
	Name string
	// Auth resolves the credential of every request.
	Auth agent.Authorizer
	// Key says how an API key goes with a request: in a header, which is
	// x-api-key when it names none, or as a bearer token. An OAuth token
	// always goes as a bearer token.
	Key gateway.Auth
	// BaseURL is the base URL that "/messages" follows. Empty is
	// DefaultBaseURL.
	BaseURL string
	// RawPredict sends requests as a cloud's raw predict endpoint takes
	// them: to "{BaseURL}/{model}:streamRawPredict", the model in the URL
	// and not the body, which names RawPredictVersion instead.
	RawPredict bool
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
		rawPredict: init.RawPredict,
		httpClient: cmp.Or(init.HTTPClient, &http.Client{}),
	}
}

// Name returns the provider's catalog ID.
func (c *Client) Name() string { return c.name }

// Model returns the model with ID name.
func (c *Client) Model(name string) agent.Model {
	return &model{c: c, name: name}
}

// model is one model of a Client.
type model struct {
	c    *Client
	name string
}

// Stream sends req. The API requires an output limit, so a request without
// MaxTokens, which agent.Providers sets for a model the catalog lists, fails.
func (m *model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	system, messages := ToMessages(req.Messages)
	body := map[string]any{
		"model":    m.name,
		"messages": messages,
		"stream":   true,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(system) > 0 {
		body["system"] = system
	}
	if t := toToolDefs(req.Tools); t != nil {
		body["tools"] = t
	}
	thinkingHeaders := SetReasoning(body, req.Reasoning, req.MaxTokens)
	body = request.MergeBody(body, req.Body)
	url := m.c.baseURL + "/messages"
	if m.c.rawPredict {
		delete(body, "model")
		body[VersionField] = RawPredictVersion
		url = m.c.baseURL + "/" + m.name + ":streamRawPredict"
	}

	return agent.Authorized(ctx, m.c.auth, func(a agent.Authorization) (agent.StreamResponse, error) {
		h := headers(a, m.c.key)
		if m.c.rawPredict {
			h.Del(VersionHeader)
		}
		request.AddHeaders(h, thinkingHeaders)
		request.AddHeaders(h, req.Headers)
		resp, err := post.JSON(ctx, m.c.httpClient, m.c.Name(), url, h, body)
		if err != nil {
			return nil, err
		}
		return eventStream(resp.Body, m.c.name), nil
	})
}

// minThinkingBudget is the smallest thinking budget the API takes, and the
// fewest output tokens SetReasoning leaves for the answer beside one.
const minThinkingBudget = 1024

// interleavedThinkingBeta lets extended thinking continue between tool
// calls, rather than only at the start of the reply. Adaptive thinking does
// so without it, and models that cannot interleave, which take a budget
// alone, ignore it.
const interleavedThinkingBeta = "interleaved-thinking-2025-05-14"

// ThinkingBudgets maps effort levels to the thinking budget extended
// thinking takes at each, since the catalog lists only the bounds of a
// budget.
var ThinkingBudgets = map[agent.ReasoningEffort]int{
	agent.EffortMinimal: 1024,
	agent.EffortLow:     2048,
	agent.EffortMedium:  8192,
	agent.EffortHigh:    16384,
}

// AdaptiveThinking reports whether a model with the reasoning controls c
// thinks adaptively, though it may take a token budget too: whether its
// effort levels include max. The format's API offers effort max on exactly
// the models that think adaptively, and not on the models that take an
// effort and a budget but not adaptive thinking.
func AdaptiveThinking(c gateway.Reasoning) bool {
	return slices.Contains(c.Efforts, gateway.Effort(agent.EffortMax))
}

// SetReasoning sets the thinking and effort of r in body, a request whose
// output limit is maxTokens, or zero when it has none, and returns the
// headers that the thinking needs, or none. A nil r, or one that sets no
// level, sets nothing. The level none turns thinking off, and sets no
// effort, so the model's default applies.
//
// A model that takes a token budget, unless it thinks adaptively as
// AdaptiveThinking reports, thinks with extended thinking, at the budget
// ThinkingBudgets gives the level, at least the least the catalog allows,
// cut to leave minThinkingBudget output tokens for the answer, and
// interleaved between tool calls; a limit too small for both leaves thinking
// off. Any other model thinks adaptively. Both return the summarized
// thinking, which the newest models otherwise omit. The level goes in
// output_config as the effort.
func SetReasoning(body map[string]any, r *agent.Reasoning, maxTokens int) map[string]string {
	if r == nil || r.Effort == "" {
		return nil
	}
	if r.Effort == agent.EffortNone {
		body["thinking"] = map[string]any{"type": "disabled"}
		return nil
	}
	var extra map[string]string
	if b := r.Controls.Budget; b != nil && !AdaptiveThinking(r.Controls) {
		budget := max(ThinkingBudgets[r.Effort], b.Min)
		if maxTokens > 0 {
			budget = min(budget, maxTokens-minThinkingBudget)
		}
		if budget >= minThinkingBudget {
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget, "display": "summarized"}
			extra = map[string]string{BetaHeader: interleavedThinkingBeta}
		}
	} else {
		body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
	}
	body["output_config"] = map[string]any{"effort": string(r.Effort)}
	return extra
}

// headers returns the headers of a streaming request that a authorizes: a
// bearer token for OAuth, and for an API key the header key names, x-api-key
// unless it names another or a bearer token.
func headers(a agent.Authorization, key gateway.Auth) http.Header {
	h := http.Header{}
	h.Set("Accept", sse.ContentType)
	h.Set(VersionHeader, APIVersion)
	switch {
	case a.Kind == agent.CredentialOAuth || key.Scheme == gateway.AuthBearer:
		h.Set("Authorization", "Bearer "+a.Token)
	default:
		h.Set(cmp.Or(key.Header, "X-Api-Key"), a.Token)
	}
	return h
}

// Message is a request message.
type Message struct {
	// Role identifies the message sender.
	Role string `json:"role"`
	// Content is a string or slice of ContentBlock values.
	Content any `json:"content"`
}

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

// ContentBlock is one content block of a message.
type ContentBlock struct {
	// Type selects the block representation.
	Type string `json:"type"`

	// Text contains text content.
	Text string `json:"text,omitempty"`

	// Source describes image or document data.
	Source *Source `json:"source,omitempty"`

	// Thinking contains reasoning text.
	Thinking string `json:"thinking,omitempty"`
	// Signature contains reasoning verification data.
	Signature string `json:"signature,omitempty"`
	// Data contains the encrypted reasoning of a redacted_thinking block.
	Data string `json:"data,omitempty"`

	// ID identifies a tool call.
	ID string `json:"id,omitempty"`
	// Name identifies a tool.
	Name string `json:"name,omitempty"`
	// Input contains raw tool arguments.
	Input json.RawMessage `json:"input,omitempty"`

	// ToolUseID identifies the corresponding tool call.
	ToolUseID string `json:"tool_use_id,omitempty"`
	// ResultContent contains tool output: its text, or the blocks that
	// ToolResultContent makes when the result carries attachments. It is nil
	// for a result with neither.
	ResultContent any `json:"content,omitempty"`
	// IsError marks failed tool output.
	IsError bool `json:"is_error,omitempty"`

	// CacheControl contains prompt-cache hints.
	CacheControl *message.CacheControl `json:"cache_control,omitempty"`
}

// Source describes image or document data.
type Source struct {
	// Type is "base64" or "url".
	Type string `json:"type"`
	// MediaType is the MIME type.
	MediaType string `json:"media_type,omitempty"`
	// Data contains base64 data.
	Data string `json:"data,omitempty"`
	// URL contains a remote source location.
	URL string `json:"url,omitempty"`
}

// ToMessages converts provider messages to request messages, and the text
// of the system messages to the system blocks, which the API takes apart from
// the conversation. Each block keeps its part's cache control.
//
// Reasoning goes back as the API sent it: redacted reasoning as a
// redacted_thinking block, and signed reasoning as a thinking block.
// Reasoning without a signature, as a stop mid-thought leaves it, would fail
// the API's check, so it goes back as text, and is left out when it has no
// text. A message left with no blocks is left out, as the API joins the
// messages of one role on either side of it.
func ToMessages(msgs []agent.Message) (system []ContentBlock, out []Message) {
	out = make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == message.RoleSystem {
			for _, p := range m.Content {
				if t, ok := p.(message.TextPart); ok {
					system = append(system, ContentBlock{Type: "text", Text: t.Text, CacheControl: t.CacheControl})
				}
			}
			continue
		}

		// Use the compact string form for one text part.
		if m.IsTextOnly() && len(m.Content) == 1 {
			out = append(out, Message{Role: Role(m.Role), Content: m.Content[0].(message.TextPart).Text})
			continue
		}

		blocks := make([]ContentBlock, 0, len(m.Content))
		for _, p := range m.Content {
			var ab ContentBlock
			switch v := p.(type) {
			case message.TextPart:
				ab.Type = "text"
				ab.Text = v.Text
				ab.CacheControl = v.CacheControl
			case message.ImagePart:
				ab.Type = "image"
				ab.CacheControl = v.CacheControl
				if v.Source != nil {
					ab.Source = &Source{
						Type:      string(v.Source.SourceType),
						MediaType: v.Source.MediaType,
						Data:      v.Source.Data,
						URL:       v.Source.URL,
					}
				}
			case message.FilePart:
				ab.Type = "document"
				ab.CacheControl = v.CacheControl
				ab.Source = &Source{
					Type:      "base64",
					MediaType: v.MediaType,
					Data:      v.Data,
				}
			case message.ReasoningPart:
				ab.CacheControl = v.CacheControl
				switch {
				case v.Redacted:
					ab.Type = "redacted_thinking"
					ab.Data = v.Signature
				case v.Signature != "":
					ab.Type = "thinking"
					ab.Thinking = v.Text
					ab.Signature = v.Signature
				case strings.TrimSpace(v.Text) != "":
					ab.Type = "text"
					ab.Text = v.Text
				default:
					continue
				}
			case message.ToolUsePart:
				ab.Type = "tool_use"
				ab.ID = v.CallID
				ab.Name = v.Name
				ab.CacheControl = v.CacheControl
				if v.Input != nil {
					ab.Input = v.Input
				} else {
					ab.Input = json.RawMessage("{}")
				}
			case message.ToolResultPart:
				ab.Type = "tool_result"
				ab.ToolUseID = v.ToolUseID
				ab.ResultContent = ToolResultContent(v)
				ab.IsError = v.IsError
				ab.CacheControl = v.CacheControl
			}
			blocks = append(blocks, ab)
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, Message{Role: Role(m.Role), Content: blocks})
	}
	return system, out
}

// ToolResultContent returns the content of the tool_result block of r: its
// text, or, when r carries attachments, a text block, when there is text,
// and then the block of each attachment, as AttachmentBlock makes it. It
// returns nil for a result with neither.
func ToolResultContent(r message.ToolResultPart) any {
	if len(r.Attachments) == 0 {
		if r.Text == "" {
			return nil
		}
		return r.Text
	}
	blocks := make([]ContentBlock, 0, len(r.Attachments)+1)
	if r.Text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: r.Text})
	}
	for _, f := range r.Attachments {
		blocks = append(blocks, AttachmentBlock(f))
	}
	return blocks
}

// AttachmentBlock returns the block that sends f, inline: an image block for
// an image, and a document block for any other file.
func AttachmentBlock(f message.FilePart) ContentBlock {
	typ := "document"
	if agent.AttachmentModality(f.MediaType) == "image" {
		typ = "image"
	}
	return ContentBlock{Type: typ, Source: &Source{Type: "base64", MediaType: f.MediaType, Data: f.Data}}
}

// buildUsage adds cache reads to input while retaining cache creation
// as a separate count.
func buildUsage(input, cacheRead, cacheCreation, output int) agent.Usage {
	in := input + cacheRead
	return agent.Usage{
		InputTokens:              in,
		CachedInputTokens:        cacheRead,
		CacheCreationInputTokens: cacheCreation,
		OutputTokens:             output,
		TotalTokens:              in + cacheCreation + output,
	}
}

// eventStream returns the stream of the Messages events of body,
// which provider name sent, and closes body once the stream ends.
func eventStream(body io.ReadCloser, name string) agent.StreamResponse {
	st := &streamState{name: name, blocks: map[int]*blockState{}}
	return agent.StreamEvents(body, st.Handle)
}

// streamState tracks one Messages response stream.
type streamState struct {
	// name is the catalog ID of the provider that sends the stream, which
	// the errors it reports name.
	name   string
	blocks map[int]*blockState

	stopReason   string
	inputTokens  int
	cacheRead    int
	cacheCreate  int
	outputTokens int
}

// blockState tracks what one streamed content block needs when it stops: the
// signature of a thinking block, and the input of a tool_use block.
type blockState struct {
	kind      string
	signature string

	toolID    string
	toolName  string
	toolInput strings.Builder
}

// ToolInput returns the JSON input of a tool_use block, an empty object when
// it streamed none, as the block started with one.
func (bs *blockState) ToolInput() json.RawMessage {
	return json.RawMessage(cmp.Or(bs.toolInput.String(), "{}"))
}

// Handle folds one Messages event into the stream, yields the parts it
// makes, and reports whether streaming should continue.
func (st *streamState) Handle(event sse.Event, yield func(agent.StreamPart, error) bool) bool {
	data := []byte(event.Data)
	switch event.Type {
	case "message_start":
		var ev struct {
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		st.inputTokens = ev.Message.Usage.InputTokens
		st.cacheRead = ev.Message.Usage.CacheReadInputTokens
		st.cacheCreate = ev.Message.Usage.CacheCreationInputTokens
		// message_start contains a placeholder output count.

	case "content_block_start":
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
				Data  string          `json:"data"`
			} `json:"content_block"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		bs := &blockState{kind: ev.ContentBlock.Type}
		st.blocks[ev.Index] = bs

		switch ev.ContentBlock.Type {
		case "text":
			if ev.ContentBlock.Text != "" {
				if !yield(agent.TextDelta{Block: agent.Block(ev.Index), Text: ev.ContentBlock.Text}, nil) {
					return false
				}
			}
		case "thinking":
		case "redacted_thinking":
			// The block comes whole, its reasoning encrypted, and must go
			// back unchanged.
			if !yield(agent.ReasoningSignature{Block: agent.Block(ev.Index), Signature: ev.ContentBlock.Data, Redacted: true}, nil) {
				return false
			}
		case "tool_use":
			bs.toolID = ev.ContentBlock.ID
			bs.toolName = ev.ContentBlock.Name
			if !yield(agent.ToolUseStart{ID: bs.toolID, Name: bs.toolName}, nil) {
				return false
			}
			// The block usually starts with an empty object that deltas
			// replace, but may start with the whole input.
			if len(ev.ContentBlock.Input) > 0 && string(ev.ContentBlock.Input) != "{}" {
				bs.toolInput.Write(ev.ContentBlock.Input)
				if !yield(agent.ToolUseDelta{ID: bs.toolID, JSON: string(ev.ContentBlock.Input)}, nil) {
					return false
				}
			}
		}

	case "content_block_delta":
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		bs := st.blocks[ev.Index]
		if bs == nil {
			return true
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				if !yield(agent.TextDelta{Block: agent.Block(ev.Index), Text: ev.Delta.Text}, nil) {
					return false
				}
			}
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				if !yield(agent.ReasoningDelta{Block: agent.Block(ev.Index), Text: ev.Delta.Thinking}, nil) {
					return false
				}
			}
		case "signature_delta":
			bs.signature += ev.Delta.Signature
		case "input_json_delta":
			if ev.Delta.PartialJSON != "" {
				bs.toolInput.WriteString(ev.Delta.PartialJSON)
				if !yield(agent.ToolUseDelta{ID: bs.toolID, JSON: ev.Delta.PartialJSON}, nil) {
					return false
				}
			}
		}

	case "content_block_stop":
		var ev struct {
			Index int `json:"index"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		bs := st.blocks[ev.Index]
		if bs == nil {
			return true
		}
		switch bs.kind {
		case "thinking":
			if bs.signature != "" {
				return yield(agent.ReasoningSignature{Block: agent.Block(ev.Index), Signature: bs.signature}, nil)
			}
		case "tool_use":
			return yield(agent.ToolUseEnd{ID: bs.toolID, Input: bs.ToolInput()}, nil)
		}

	case "message_delta":
		var ev struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		if ev.Delta.StopReason != "" {
			st.stopReason = ev.Delta.StopReason
		}
		// Prefer nonzero cumulative usage from message_delta.
		if ev.Usage.OutputTokens > 0 {
			st.outputTokens = ev.Usage.OutputTokens
		}
		if ev.Usage.InputTokens > 0 {
			st.inputTokens = ev.Usage.InputTokens
		}
		if ev.Usage.CacheReadInputTokens > 0 {
			st.cacheRead = ev.Usage.CacheReadInputTokens
		}
		if ev.Usage.CacheCreationInputTokens > 0 {
			st.cacheCreate = ev.Usage.CacheCreationInputTokens
		}

	case "message_stop":
		if !yield(buildUsage(st.inputTokens, st.cacheRead, st.cacheCreate, st.outputTokens), nil) {
			return false
		}

		var stopReason agent.StopReason
		switch st.stopReason {
		case "end_turn":
			stopReason = agent.StopEndTurn
		case "max_tokens":
			stopReason = agent.StopMaxTokens
		case "tool_use":
			stopReason = agent.StopToolUse
		default:
			stopReason = agent.StopReason(st.stopReason)
		}

		yield(agent.Stop{Reason: stopReason}, nil)
		return false

	case "error":
		var ev struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &ev) == nil && ev.Error.Message != "" {
			yield(nil, &agent.ResponseError{
				Provider:     st.name,
				StatusCode:   0,
				Message:      ev.Error.Message,
				ResponseBody: string(data),
			})
			return false
		}

	case "ping":
	}
	return true
}

// toolDef is a tool definition of a request.
type toolDef struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	InputSchema agent.InputSchema `json:"input_schema"`
}

// toToolDefs converts tools to the definitions of a request.
func toToolDefs(tools []agent.ToolDefinition) []toolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolDef, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema.Type == "" {
			schema.Type = "object"
		}
		out = append(out, toolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}
