package agent

import (
	"encoding/json"
	"slices"

	"github.com/unaquatic/flow/agent/message"
)

// StreamResponse yields streamed parts and terminal errors.
type StreamResponse func(yield func(StreamPart, error) bool)

// Collect drains the stream and assembles the response its parts describe:
// its text and reasoning blocks and its tool calls in the order they start,
// each call with the input its deltas built or its ToolUseEnd gave.
func (s StreamResponse) Collect() (*Response, error) {
	resp := &Response{Message: Message{Role: message.RoleAgent}}

	// BlockKey identifies a text or reasoning block.
	type BlockKey struct {
		Reasoning bool
		Block     Block
	}
	var content []message.ContentPart
	blocks := make(map[BlockKey]int)
	calls := make(map[string]int)

	// slot returns the index in content of the part of the block key names,
	// starting the part if the block has none yet.
	slot := func(key BlockKey) int {
		i, ok := blocks[key]
		if !ok {
			i = len(content)
			blocks[key] = i
			if key.Reasoning {
				content = append(content, message.ReasoningPart{})
			} else {
				content = append(content, message.TextPart{})
			}
		}
		return i
	}

	for part, err := range s {
		if err != nil {
			return nil, err
		}
		switch p := part.(type) {
		case TextDelta:
			if p.Text != "" {
				i := slot(BlockKey{Block: p.Block})
				tp := content[i].(message.TextPart)
				tp.Text += p.Text
				content[i] = tp
			}
		case ReasoningDelta:
			if p.Text != "" {
				i := slot(BlockKey{Reasoning: true, Block: p.Block})
				rp := content[i].(message.ReasoningPart)
				rp.Text += p.Text
				content[i] = rp
			}
		case ReasoningMetadata:
			i := slot(BlockKey{Reasoning: true, Block: p.Block})
			rp := content[i].(message.ReasoningPart)
			rp.Metadata = rp.Metadata.Merge(p.Metadata)
			content[i] = rp
		case ToolUseStart:
			calls[p.ID] = len(content)
			content = append(content, message.ToolUsePart{CallID: p.ID, Name: p.Name, Metadata: p.Metadata})
		case ToolUseDelta:
			if i, ok := calls[p.ID]; ok {
				tu := content[i].(message.ToolUsePart)
				tu.Input = append(tu.Input, p.JSON...)
				content[i] = tu
			}
		case ToolUseEnd:
			if i, ok := calls[p.ID]; ok {
				tu := content[i].(message.ToolUsePart)
				tu.Input = slices.Clone(p.Input)
				content[i] = tu
			}
		case Usage:
			resp.Usage = p
		case Stop:
			resp.StopReason = p.Reason
		}
	}

	// A call whose input never arrived still needs a JSON object.
	for i, p := range content {
		if tu, ok := p.(message.ToolUsePart); ok && len(tu.Input) == 0 {
			tu.Input = json.RawMessage("{}")
			content[i] = tu
		}
	}
	resp.Message.Content = content
	return resp, nil
}
