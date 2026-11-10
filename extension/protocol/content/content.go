// Package content speaks the generateContent request format, for every
// provider whose API takes it: the format's own API at DefaultBaseURL, a
// cloud's, and gateways at their own base URLs.
package content

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/request"
	"github.com/unaquatic/flow/extension/protocol/internal/post"
	"github.com/unaquatic/flow/internal/sse"
)

// DefaultBaseURL is the base URL of the format's own API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// DefaultKeyHeader is the header that carries an API key of the format's
// own API.
const DefaultKeyHeader = "x-goog-api-key"

// SkipSignature is the thought signature the API documents for a function
// call whose own signature is lost, such as one another model made, which
// the models of version 3 and later otherwise reject.
const SkipSignature = "skip_thought_signature_validator"

// Client implements agent.Provider for an API that takes generateContent
// requests at its base URL.
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
	// Key says how an API key goes with a request: in a header,
	// DefaultKeyHeader when it names none, or as a bearer token. An OAuth
	// token, such as a cloud's, always goes as a bearer token.
	Key gateway.Auth
	// BaseURL is the base URL that "/models/{model}" follows. Empty is
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

// Model returns the model with ID name.
func (c *Client) Model(name string) agent.Model { return &Model{Client: c, Name: name} }

// Model is one model of a Client.
type Model struct {
	Client *Client
	Name   string
}

// Stream sends req to POST {base}/models/{model}:streamGenerateContent,
// whose server-sent events Stream reads.
func (m *Model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	system, contents := ToContents(req.Messages, m.Name)
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = map[string]any{"parts": system}
	}
	if decls := ToFunctionDeclarations(req.Tools); len(decls) > 0 {
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	gen := map[string]any{}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if tc := ThinkingConfig(req.Reasoning); tc != nil {
		gen["thinkingConfig"] = tc
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	body = request.MergeBody(body, req.Body)

	return agent.Authorized(ctx, m.Client.auth, func(a agent.Authorization) (agent.StreamResponse, error) {
		h := m.Client.Header(a)
		request.AddHeaders(h, req.Headers)
		url := m.Client.baseURL + "/models/" + m.Name + ":streamGenerateContent?alt=sse"
		resp, err := post.JSON(ctx, m.Client.httpClient, m.Client.name, url, h, body)
		if err != nil {
			return nil, err
		}
		return Stream(resp.Body, m.Client.name), nil
	})
}

// Header returns the headers that authorize a request with a: a bearer
// token for OAuth and for a key that goes as one, and else the key in its
// header.
func (c *Client) Header(a agent.Authorization) http.Header {
	h := http.Header{"Accept": {sse.ContentType}}
	if a.Kind == agent.CredentialOAuth || c.key.Scheme == gateway.AuthBearer {
		h.Set("Authorization", "Bearer "+a.Token)
	} else {
		h.Set(cmp.Or(c.key.Header, DefaultKeyHeader), a.Token)
	}
	return h
}

// Content is one turn of a conversation.
type Content struct {
	// Role is "user" or "model".
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one part of a Content, of a request or a response.
type Part struct {
	Text string `json:"text,omitempty"`
	// Thought marks a part of the model's thinking.
	Thought bool `json:"thought,omitempty"`
	// ThoughtSignature verifies the thinking that led to the part, which a
	// later request sends back on the same part.
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// Blob is inline data, such as an image.
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FileData refers to data by URI.
type FileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// FunctionCall is a call of a tool.
type FunctionCall struct {
	// ID is the call's ID, which models before version 3 do not give.
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// FunctionResponse is the result of a FunctionCall.
type FunctionResponse struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Response holds the result under "output", or a failure under
	// "error", as the API reads them.
	Response map[string]any `json:"response"`
	// Parts are the files of the result, such as an image, which models of
	// version 3 and later take in a function response.
	Parts []FunctionResponsePart `json:"parts,omitempty"`
}

// FunctionResponsePart is a file of a FunctionResponse.
type FunctionResponsePart struct {
	InlineData *FunctionResponseBlob `json:"inlineData"`
}

// FunctionResponseBlob is the inline data of a FunctionResponsePart, named
// by DisplayName.
type FunctionResponseBlob struct {
	MimeType    string `json:"mimeType"`
	Data        string `json:"data"`
	DisplayName string `json:"displayName,omitempty"`
}

// LegacyFamily is the family of the format's own models, whose IDs before
// version 3 legacyModel matches.
const LegacyFamily = "gemini"

// legacyModel matches the IDs of the format's models before version 3,
// which take no call IDs and need no thought signatures on calls.
var legacyModel = regexp.MustCompile(`(^|/)` + LegacyFamily + `(-live)?-[12]([.-]|$)`)

// ToContents converts provider messages to the turns of a request to
// model, and the text of the system messages to the parts of its system
// instruction.
//
// A reasoning part with text goes back as a thought, with its signature; a
// signature with no text is the signature of the part that follows it, as
// Stream yields a function call's. The first function call of a turn that
// has none, such as one another model made, takes SkipSignature, which
// models of version 3 and later need; models before it take no call IDs.
//
// The attachments of a tool result, such as an image, go in its function
// response's parts to models of version 3 and later. Models before it take
// none there, so theirs follow the turn of the function responses in a
// user turn, each after a line that names its call.
func ToContents(msgs []agent.Message, model string) (system []Part, out []Content) {
	legacy := legacyModel.MatchString(model)
	id := func(callID string) string {
		if legacy {
			return ""
		}
		return callID
	}
	for _, m := range msgs {
		if m.Role == message.RoleSystem {
			for _, p := range m.Content {
				if t, ok := p.(message.TextPart); ok && t.Text != "" {
					system = append(system, Part{Text: t.Text})
				}
			}
			continue
		}
		role := "user"
		if m.Role == message.RoleAgent {
			role = "model"
		}
		var parts, attached []Part
		var pending string
		signed := false
		take := func() string {
			s := pending
			pending = ""
			return s
		}
		for _, p := range m.Content {
			switch v := p.(type) {
			case message.TextPart:
				if v.Text != "" {
					parts = append(parts, Part{Text: v.Text, ThoughtSignature: take()})
				}
			case message.ReasoningPart:
				switch {
				case v.Redacted:
				case v.Text == "":
					pending = v.Signature
				default:
					parts = append(parts, Part{Text: v.Text, Thought: true, ThoughtSignature: v.Signature})
				}
			case message.ImagePart:
				if v.Source == nil {
					continue
				}
				if v.Source.SourceType == message.ImageSourceURL {
					parts = append(parts, Part{FileData: &FileData{MimeType: v.Source.MediaType, FileURI: v.Source.URL}})
				} else {
					parts = append(parts, Part{InlineData: &Blob{MimeType: v.Source.MediaType, Data: v.Source.Data}})
				}
			case message.FilePart:
				parts = append(parts, Part{InlineData: &Blob{MimeType: v.MediaType, Data: v.Data}})
			case message.ToolUsePart:
				sig := take()
				if sig == "" && !signed && !legacy {
					sig = SkipSignature
				}
				signed = true
				args := v.Input
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				parts = append(parts, Part{FunctionCall: &FunctionCall{ID: id(v.CallID), Name: v.Name, Args: args}, ThoughtSignature: sig})
			case message.ToolResultPart:
				key := "output"
				if v.IsError {
					key = "error"
				}
				fr := &FunctionResponse{ID: id(v.ToolUseID), Name: v.Name, Response: map[string]any{key: v.Text}}
				if legacy && len(v.Attachments) > 0 {
					attached = append(attached, Part{Text: "Attachments of the " + v.Name + " call:"})
				}
				for _, f := range v.Attachments {
					if legacy {
						attached = append(attached, Part{InlineData: &Blob{MimeType: f.MediaType, Data: f.Data}})
						continue
					}
					fr.Parts = append(fr.Parts, FunctionResponsePart{InlineData: &FunctionResponseBlob{
						MimeType: f.MediaType, Data: f.Data, DisplayName: f.Filename,
					}})
				}
				parts = append(parts, Part{FunctionResponse: fr})
			}
		}
		if len(parts) > 0 {
			out = append(out, Content{Role: role, Parts: parts})
		}
		if len(attached) > 0 {
			out = append(out, Content{Role: "user", Parts: attached})
		}
	}
	return system, out
}

// FunctionDeclaration declares a tool.
type FunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ParametersJSONSchema is the JSON Schema of the tool's input.
	ParametersJSONSchema agent.InputSchema `json:"parametersJsonSchema"`
}

// ToFunctionDeclarations converts tools to function declarations.
func ToFunctionDeclarations(tools []agent.ToolDefinition) []FunctionDeclaration {
	out := make([]FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		schema.Type = cmp.Or(schema.Type, "object")
		out = append(out, FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJSONSchema: schema})
	}
	return out
}

// ThinkingConfig returns the thinking settings of r, or nil when r is nil,
// for a model that does not reason: the model returns its thoughts, and
// thinks at r's effort as its thinking level, when its controls list
// effort levels and r sets one.
func ThinkingConfig(r *agent.Reasoning) map[string]any {
	if r == nil {
		return nil
	}
	tc := map[string]any{"includeThoughts": true}
	if r.Effort != "" && len(r.Controls.Efforts) > 0 {
		tc["thinkingLevel"] = string(r.Effort)
	}
	return tc
}

// Chunk is one server-sent event of a streamed response.
type Chunk struct {
	Candidates []struct {
		Content struct {
			Parts []Part `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata"`
	// PromptFeedback says why the prompt was blocked, when it was.
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// UsageMetadata holds the token counts of a response.
type UsageMetadata struct {
	// PromptTokenCount counts the prompt, cached tokens included.
	PromptTokenCount        int `json:"promptTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	// CandidatesTokenCount counts the output but for the thoughts, which
	// ThoughtsTokenCount counts.
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Usage returns u as provider usage.
func (u UsageMetadata) Usage() agent.Usage {
	out := u.CandidatesTokenCount + u.ThoughtsTokenCount
	return agent.Usage{
		InputTokens:           u.PromptTokenCount,
		CachedInputTokens:     u.CachedContentTokenCount,
		OutputTokens:          out,
		ReasoningOutputTokens: u.ThoughtsTokenCount,
		TotalTokens:           cmp.Or(u.TotalTokenCount, u.PromptTokenCount+out),
	}
}

// Stream returns the stream of the generateContent response body of the provider
// named name, which it closes once the stream ends: its thoughts and text,
// each run of them a block of its own, its function calls, each whole, and
// then its usage and why it stopped. A function call's thought signature
// streams as the signature of a reasoning block of its own, just before
// the call; the signatures of text parts, which the API does not need back,
// are left out.
func Stream(body io.ReadCloser, name string) agent.StreamResponse {
	return func(yield func(agent.StreamPart, error) bool) {
		defer body.Close()
		st := &StreamState{Provider: name}
		for ev, err := range sse.Read(body) {
			if err != nil {
				yield(nil, &agent.ResponseParseError{Err: err})
				return
			}
			if !st.Handle(ev.Data, yield) {
				return
			}
		}
		st.Finish(yield)
	}
}

// StreamState tracks one streamed response.
type StreamState struct {
	// Provider names the provider, for its errors.
	Provider string

	block   agent.Block
	thought bool
	open    bool
	calls   map[string]bool
	finish  string
	usage   *UsageMetadata
}

// next starts a new block for a run of thoughts, or of text when thought
// is false, unless one is open, and returns it.
func (st *StreamState) next(thought bool) agent.Block {
	if !st.open || st.thought != thought {
		st.block++
		st.open, st.thought = true, thought
	}
	return st.block
}

// Handle folds the event data into the stream, yields the parts it makes,
// and reports whether to go on.
func (st *StreamState) Handle(data string, yield func(agent.StreamPart, error) bool) bool {
	var c Chunk
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		yield(nil, &agent.ResponseParseError{Err: err})
		return false
	}
	if c.Error != nil {
		yield(nil, &agent.ResponseError{Provider: st.Provider, StatusCode: c.Error.Code, Message: cmp.Or(c.Error.Message, c.Error.Status), ResponseBody: data})
		return false
	}
	if c.UsageMetadata != nil {
		st.usage = c.UsageMetadata
	}
	if len(c.Candidates) == 0 {
		if c.PromptFeedback != nil && c.PromptFeedback.BlockReason != "" {
			yield(nil, &agent.ResponseError{Provider: st.Provider, Message: "prompt blocked: " + c.PromptFeedback.BlockReason, ResponseBody: data})
			return false
		}
		return true
	}
	cand := c.Candidates[0]
	st.finish = cmp.Or(cand.FinishReason, st.finish)
	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			st.open = false
			if p.ThoughtSignature != "" {
				st.block++
				if !yield(agent.ReasoningSignature{Block: st.block, Signature: p.ThoughtSignature}, nil) {
					return false
				}
			}
			id := st.CallID(p.FunctionCall.ID)
			args := p.FunctionCall.Args
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			if !yield(agent.ToolUseStart{ID: id, Name: p.FunctionCall.Name}, nil) ||
				!yield(agent.ToolUseEnd{ID: id, Input: args}, nil) {
				return false
			}
		case p.Thought:
			b := st.next(true)
			if p.Text != "" && !yield(agent.ReasoningDelta{Block: b, Text: p.Text}, nil) {
				return false
			}
			if p.ThoughtSignature != "" && !yield(agent.ReasoningSignature{Block: b, Signature: p.ThoughtSignature}, nil) {
				return false
			}
		case p.Text != "":
			if !yield(agent.TextDelta{Block: st.next(false), Text: p.Text}, nil) {
				return false
			}
		}
	}
	return true
}

// CallID returns the ID of a function call that the API gave id, or none:
// id, unless it is empty or an earlier call of the response had it, or
// else a new one.
func (st *StreamState) CallID(id string) string {
	if st.calls == nil {
		st.calls = map[string]bool{}
	}
	if id == "" || st.calls[id] {
		id = "call_" + strings.ToLower(rand.Text())
	}
	st.calls[id] = true
	return id
}

// Finish yields the usage of the response and why it stopped, or an error
// when the stream ended before the response finished.
func (st *StreamState) Finish(yield func(agent.StreamPart, error) bool) {
	if st.finish == "" {
		yield(nil, &agent.ResponseParseError{Err: errors.New("the stream ended before the response finished")})
		return
	}
	if st.usage != nil && !yield(st.usage.Usage(), nil) {
		return
	}
	yield(agent.Stop{Reason: StopReason(st.finish, len(st.calls) > 0)}, nil)
}

// StopReason returns the stop reason of the API's finish reason, after a
// response that called functions when calls is set.
func StopReason(finish string, calls bool) agent.StopReason {
	switch finish {
	case "STOP":
		if calls {
			return agent.StopToolUse
		}
		return agent.StopEndTurn
	case "MAX_TOKENS":
		return agent.StopMaxTokens
	}
	return agent.StopReason(strings.ToLower(finish))
}
