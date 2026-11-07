package agent

import (
	"context"
)

// Model sends requests to one provider model.
type Model interface {
	// Stream starts a request and returns an iterator over response parts. The
	// outer error reports setup failures; yielded errors terminate the stream.
	// [StreamResponse.Collect] gathers the parts into a whole response.
	Stream(ctx context.Context, req Request) (StreamResponse, error)
}

// Request is one model request.
type Request struct {
	// Messages is the conversation so far. System messages hold the
	// instructions; APIs that take instructions apart from the conversation,
	// as Anthropic and the ChatGPT Responses API do, gather them from
	// wherever they are.
	Messages []Message
	// Tools are the tools the model may call.
	Tools []ToolDefinition
	// MaxTokens limits the output tokens of the response. Zero sends no
	// limit, which Anthropic rejects.
	MaxTokens int
	// CacheKey names the requests that share a prompt prefix, such as the
	// turns of one session. Providers that route by key, as OpenAI does,
	// send them to the same prompt cache. Empty sends no key.
	CacheKey string
	// Reasoning sets how much the model reasons. Nil sends no reasoning
	// settings, as for a model not known to reason. [Providers.Stream] sets
	// it from the catalog for a request that leaves it nil.
	Reasoning *Reasoning
	// Body holds fields to set in the request body over those the provider
	// sets, as a catalog variant such as a fast tier sets them. A field that
	// holds an object in both is merged field by field; any other field
	// replaces the provider's. Providers do not modify it.
	Body map[string]any
	// Headers holds headers to add to the request, as a catalog variant
	// sets them. A header the provider sets already takes the value after
	// its own, comma-separated, as list headers such as anthropic-beta take
	// them.
	Headers map[string]string
}
