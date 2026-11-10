// Package completions speaks the Chat Completions request format, which
// most of the catalog's providers take: the format's own platform API at
// DefaultBaseURL, and every API that takes its chat requests at its own
// base URL, such as a gateway's or a cloud's compatible endpoint.
package completions

import (
	"cmp"
	"context"
	"encoding/json"
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

// DefaultBaseURL is the base URL of the format's own platform API.
const DefaultBaseURL = "https://api.openai.com/v1"

// Client implements agent.Provider for an API that takes Chat Completions
// requests at its base URL. Every credential, an API key or another
// provider's OAuth token, such as a cloud's access token, goes to that API.
type Client struct {
	name       string
	auth       agent.Authorizer
	key        gateway.Auth
	baseURL    string
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
	// BaseURL is the base URL that "/chat/completions" follows. Empty is
	// DefaultBaseURL.
	BaseURL string
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
		httpClient: cmp.Or(init.HTTPClient, &http.Client{}),
	}
}

// Name returns the provider's catalog ID.
func (c *Client) Name() string { return c.name }

// Message is a Chat Completions message.
type Message struct {
	// Role identifies the message sender.
	Role string `json:"role"`
	// Content is a string or slice of ContentPart values.
	Content any `json:"content,omitempty"`
	// ToolCalls contains the tool requests of an agent message.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID identifies the request answered by a tool message.
	ToolCallID string `json:"tool_call_id,omitempty"`
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

// ContentPart is one multimodal input part.
type ContentPart struct {
	// Type selects the content representation.
	Type string `json:"type"`
	// Text contains text input.
	Text string `json:"text,omitempty"`
	// ImageURL contains an image reference.
	ImageURL *ImageURL `json:"image_url,omitempty"`
	// InputAudio contains inline audio.
	InputAudio *Audio `json:"input_audio,omitempty"`
	// File contains an inline file that is neither an image nor audio.
	File *File `json:"file,omitempty"`
}

// File is an inline file in a content part.
type File struct {
	// Filename is the file's name.
	Filename string `json:"filename,omitempty"`
	// FileData is the file as a data URL.
	FileData string `json:"file_data"`
}

// ImageURL is an image reference in a content part.
type ImageURL struct {
	// URL is a remote URL or data URL.
	URL string `json:"url"`
}

// Audio is an audio input in a content part.
type Audio struct {
	// Data contains base64 audio.
	Data string `json:"data"`
	// Format identifies the audio encoding.
	Format string `json:"format"`
}

// ToolCall is a tool request of an agent message.
type ToolCall struct {
	// ID identifies the tool call.
	ID string `json:"id"`
	// Type is "function".
	Type string `json:"type"`
	// Function describes the requested call.
	Function ToolFunction `json:"function"`
}

// ToolFunction describes the function being called in a tool call.
type ToolFunction struct {
	// Name identifies the function.
	Name string `json:"name"`
	// Arguments contains JSON arguments as a string.
	Arguments string `json:"arguments"`
}

// ToMessages converts provider-agnostic Messages to Chat Completions
// messages. Each tool result is a tool message, whose content is text alone,
// so the attachments of a message's tool results follow its tool messages in
// a user message, each after a line that names its call.
func ToMessages(msgs []agent.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		var toolCalls []ToolCall
		var contentParts, attached []ContentPart

		for _, p := range m.Content {
			switch v := p.(type) {
			case message.TextPart:
				contentParts = append(contentParts, ContentPart{Type: "text", Text: v.Text})
			case message.ImagePart:
				url := ""
				if v.Source != nil {
					if v.Source.SourceType == message.ImageSourceURL {
						url = v.Source.URL
					} else if v.Source.SourceType == message.ImageSourceBase64 {
						url = "data:" + v.Source.MediaType + ";base64," + v.Source.Data
					}
				}
				contentParts = append(contentParts, ContentPart{
					Type:     "image_url",
					ImageURL: &ImageURL{URL: url},
				})
			case message.FilePart:
				contentParts = append(contentParts, FileContentPart(v))
			case message.ReasoningPart:
			case message.ToolUsePart:
				args := string(v.Input)
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, ToolCall{
					ID:   v.CallID,
					Type: "function",
					Function: ToolFunction{
						Name:      v.Name,
						Arguments: args,
					},
				})
			case message.ToolResultPart:
				// Emit each tool result as its own tool message.
				out = append(out, Message{
					Role:       "tool",
					Content:    v.Text,
					ToolCallID: v.ToolUseID,
				})
				if len(v.Attachments) > 0 {
					attached = append(attached, ContentPart{Type: "text", Text: "Attachments of tool call " + v.ToolUseID + ":"})
					for _, f := range v.Attachments {
						attached = append(attached, FileContentPart(f))
					}
				}
			}
		}
		if len(attached) > 0 {
			out = append(out, Message{Role: Role(message.RoleUser), Content: attached})
		}

		// Tool-result-only messages were emitted above.
		if len(contentParts) == 0 && len(toolCalls) == 0 {
			continue
		}

		om := Message{Role: Role(m.Role)}
		if len(toolCalls) > 0 {
			om.ToolCalls = toolCalls
		}
		// Use the compact string form for one text part.
		if len(contentParts) == 1 && contentParts[0].Type == "text" && len(toolCalls) == 0 {
			om.Content = contentParts[0].Text
		} else if len(contentParts) > 0 {
			om.Content = contentParts
		}
		out = append(out, om)
	}
	return out
}

// FileContentPart returns the content part that sends f inline: an
// input_audio part for audio, an image_url part holding a data URL for an
// image, and a file part holding one for any other file, such as a PDF.
func FileContentPart(f message.FilePart) ContentPart {
	url := "data:" + f.MediaType + ";base64," + f.Data
	switch {
	case isAudioMediaType(f.MediaType):
		return ContentPart{Type: "input_audio", InputAudio: &Audio{Data: f.Data, Format: audioFormat(f.MediaType)}}
	case agent.AttachmentModality(f.MediaType) == "image":
		return ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url}}
	}
	return ContentPart{Type: "file", File: &File{Filename: f.Filename, FileData: url}}
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

// authHeader returns the header that authorizes a request with token: in
// the header the key names, when it goes in one, and else as a bearer token.
func (c *Client) authHeader(token string) http.Header {
	if c.key.Scheme == gateway.AuthHeader && c.key.Header != "" {
		return http.Header{http.CanonicalHeaderKey(c.key.Header): {token}}
	}
	return http.Header{"Authorization": {"Bearer " + token}}
}

// StreamedCall is one tool call of a streamed Chat Completions response.
type StreamedCall struct {
	// Index is the position of the call among the response's tool calls,
	// which its chunks carry.
	Index int
	// CallID identifies the call to the model.
	CallID string
	// Name identifies the called function.
	Name string
	// Arguments is the JSON input streamed so far, and all of it once Done.
	Arguments string
	// Done reports that the call's complete input was yielded.
	Done bool
}

// Stream opens a Chat Completions event stream at the base URL. The API
// takes system messages where they are in the conversation. It sends no
// prompt_cache_key, which servers other than the platform API may reject.
func (m *model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	return agent.Authorized(ctx, m.c.auth, func(a agent.Authorization) (agent.StreamResponse, error) {
		return m.stream(ctx, a.Token, req)
	})
}

// stream sends req, authorized by token.
func (m *model) stream(ctx context.Context, token string, req agent.Request) (agent.StreamResponse, error) {
	body := map[string]any{
		"model":          m.name,
		"messages":       ToMessages(req.Messages),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}
	if t := toTools(req.Tools); t != nil {
		body["tools"] = t
	}
	if r := req.Reasoning; r != nil && r.Effort != "" {
		body["reasoning_effort"] = string(r.Effort)
	}
	body = request.MergeBody(body, req.Body)
	header := m.c.authHeader(token)
	request.AddHeaders(header, req.Headers)

	url := m.c.baseURL + "/chat/completions"
	resp, err := post.JSON(ctx, m.c.httpClient, m.c.Name(), url, header, body)
	if err != nil {
		return nil, err
	}
	return agent.StreamEvents(resp.Body, (&Handler{}).Handle), nil
}

// Handler turns Chat Completions stream chunks into stream parts.
type Handler struct {
	// Calls holds the response's tool calls in the order they started.
	Calls []*StreamedCall
}

// Call returns the tool call a chunk names: by id when the chunk has one,
// as some servers repeat it in every chunk and give parallel calls one
// index, and otherwise by index, as only a call's first chunk must carry its
// id. It returns nil when there is no such call.
func (h *Handler) Call(id string, index int) *StreamedCall {
	match := func(c *StreamedCall) bool { return c.Index == index }
	if id != "" {
		match = func(c *StreamedCall) bool { return c.CallID == id }
	}
	if i := slices.IndexFunc(h.Calls, match); i >= 0 {
		return h.Calls[i]
	}
	return nil
}

// Handle parses one Chat Completions chunk, yields the stream parts it makes,
// and reports whether streaming should continue.
func (h *Handler) Handle(event sse.Event, yield func(agent.StreamPart, error) bool) bool {
	if event.Data == "[DONE]" {
		return false
	}

	var chunk streamChunk
	if json.Unmarshal([]byte(event.Data), &chunk) != nil {
		return true
	}

	if chunk.Usage != nil {
		total := chunk.Usage.TotalTokens
		if total == 0 {
			total = chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
		}
		// The prompt count holds the tokens written to the cache, which
		// Usage counts apart.
		written := chunk.Usage.PromptTokensDetails.CacheWriteTokens
		if !yield(agent.Usage{
			InputTokens:              max(0, chunk.Usage.PromptTokens-written),
			CachedInputTokens:        chunk.Usage.PromptTokensDetails.CachedTokens,
			CacheCreationInputTokens: written,
			OutputTokens:             chunk.Usage.CompletionTokens,
			ReasoningOutputTokens:    chunk.Usage.CompletionTokensDetails.ReasoningTokens,
			TotalTokens:              total,
		}, nil) {
			return false
		}
	}

	if len(chunk.Choices) == 0 {
		return true
	}
	choice := chunk.Choices[0]

	if choice.Delta.Content != "" {
		if !yield(agent.TextDelta{Text: choice.Delta.Content}, nil) {
			return false
		}
	}

	for _, tc := range choice.Delta.ToolCalls {
		c := h.Call(tc.ID, tc.Index)
		if c == nil {
			c = &StreamedCall{Index: tc.Index, CallID: tc.ID, Name: tc.Function.Name}
			h.Calls = append(h.Calls, c)
			if !yield(agent.ToolUseStart{ID: c.CallID, Name: c.Name}, nil) {
				return false
			}
		}
		if tc.Function.Arguments != "" {
			c.Arguments += tc.Function.Arguments
			if !yield(agent.ToolUseDelta{ID: c.CallID, JSON: tc.Function.Arguments}, nil) {
				return false
			}
		}
	}

	if choice.FinishReason != nil {
		// Every call's arguments are complete when the choice finishes. Some
		// servers send none for a function that takes none.
		for _, c := range h.Calls {
			if c.Done {
				continue
			}
			c.Done = true
			c.Arguments = cmp.Or(c.Arguments, "{}")
			if !yield(agent.ToolUseEnd{ID: c.CallID, Input: json.RawMessage(c.Arguments)}, nil) {
				return false
			}
		}
		var stopReason agent.StopReason
		switch *choice.FinishReason {
		case "stop":
			stopReason = agent.StopEndTurn
		case "length":
			stopReason = agent.StopMaxTokens
		case "tool_calls":
			stopReason = agent.StopToolUse
		default:
			stopReason = agent.StopReason(*choice.FinishReason)
		}
		yield(agent.Stop{Reason: stopReason}, nil)
	}

	return true
}

// streamChunk is one Chat Completions stream payload.
type streamChunk struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Choices []struct {
		FinishReason *string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// tool is a Chat Completions tool definition.
type tool struct {
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type function struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Parameters  agent.InputSchema `json:"parameters"`
}

// toTools converts tools to Chat Completions definitions.
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
			Type: "function",
			Function: function{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

// isAudioMediaType reports whether mediaType has the audio MIME prefix.
func isAudioMediaType(mediaType string) bool {
	return len(mediaType) > 6 && mediaType[:6] == "audio/"
}

// audioFormat returns the subtype of an audio MIME type and maps mpeg to mp3.
func audioFormat(mediaType string) string {
	if len(mediaType) <= 6 {
		return mediaType
	}
	sub := mediaType[6:]
	if sub == "mpeg" {
		return "mp3"
	}
	return sub
}
