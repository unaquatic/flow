package agent

// Response is a complete model response, as [StreamResponse.Collect]
// gathers it from a stream.
type Response struct {
	// Message is the complete agent message.
	Message Message

	// StopReason indicates why the model stopped generating.
	StopReason StopReason

	// Usage reports token consumption for the request.
	Usage Usage
}

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	// StopEndTurn indicates natural completion.
	StopEndTurn StopReason = "end_turn"
	// StopMaxTokens indicates that the token limit was reached.
	StopMaxTokens StopReason = "max_tokens"
	// StopToolUse indicates that the model requested a tool.
	StopToolUse StopReason = "tool_use"
)

// Usage contains provider-reported token counts for one request.
type Usage struct {
	// InputTokens counts the prompt tokens read from a prompt cache or
	// processed anew. It leaves out the tokens written to a prompt cache,
	// which CacheCreationInputTokens counts.
	InputTokens int
	// CachedInputTokens is the subset of InputTokens read from a prompt
	// cache.
	CachedInputTokens int
	// CacheCreationInputTokens counts the prompt tokens written to a prompt
	// cache.
	CacheCreationInputTokens int
	// OutputTokens is the total output count.
	OutputTokens int
	// ReasoningOutputTokens is the reasoning subset of output tokens.
	ReasoningOutputTokens int
	// TotalTokens is the provider-reported total.
	TotalTokens int
}
