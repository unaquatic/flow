package agent

import (
	"encoding/json"

	"github.com/unaquatic/flow/agent/message"
)

// ExpandToolParts removes internal metadata and splits fused ToolPart values
// into provider-facing tool requests and results. Nonterminal tool states,
// those a stop or a crash left unfinished, become interrupted error results,
// and arguments that are not valid JSON become an empty object.
func ExpandToolParts(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		agentParts := make([]message.ContentPart, 0, len(m.Content))
		var resultParts []message.ContentPart
		for _, p := range m.Content {
			var tp message.ToolPart
			switch v := p.(type) {
			case message.Metadata:
				continue
			case message.ToolPart:
				tp = v
			default:
				agentParts = append(agentParts, p)
				continue
			}

			// Arguments a stop cut off, or that the model malformed, are
			// not valid JSON, which providers reject.
			input := tp.Input()
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage("{}")
			}
			agentParts = append(agentParts, message.ToolUsePart{
				CallID:       tp.CallID,
				Name:         tp.Name,
				Input:        input,
				CacheControl: tp.CacheControl,
			})

			result := message.ToolResultPart{ToolUseID: tp.CallID, Name: tp.Name, IsError: true}
			switch s := tp.State.(type) {
			case message.ToolStateCompleted:
				result.Text, result.IsError, result.Attachments = s.Output, false, s.Attachments
			case message.ToolStateError:
				result.Text = s.Error
			default:
				result.Text = message.InterruptedError
			}
			resultParts = append(resultParts, result)
		}

		// Drop messages emptied by metadata removal.
		if len(agentParts) > 0 || len(resultParts) > 0 {
			out = append(out, Message{Role: m.Role, Content: agentParts, Provider: m.Provider, Model: m.Model})
		}
		if len(resultParts) > 0 {
			out = append(out, Message{Role: message.RoleUser, Content: resultParts})
		}
	}
	return out
}
