package agent

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
)

// Message is a provider-independent model message.
type Message struct {
	// Role identifies the sender: the system, the user, or the agent.
	Role message.Role
	// Content contains message parts in order.
	Content []message.ContentPart
	// Provider and Model identify the model that wrote the message, and are
	// empty when it is not known. Its reasoning goes back only to that
	// model, as other models cannot read it; see OwnReasoning.
	Provider ProviderID
	// Model is the model ID, without the mode.
	Model string
}

// OwnReasoning returns msgs without the reasoning parts of the messages
// that a model other than model of provider t wrote. Messages whose writer
// is not known keep theirs. It copies only the messages it changes.
func OwnReasoning(msgs []Message, t ProviderID, model string) []Message {
	out, copied := msgs, false
	for i, m := range msgs {
		if m.Provider == "" || (m.Provider == t && m.Model == model) {
			continue
		}
		kept := slices.DeleteFunc(slices.Clone(m.Content), func(p message.ContentPart) bool {
			_, ok := p.(message.ReasoningPart)
			return ok
		})
		if len(kept) == len(m.Content) {
			continue
		}
		if !copied {
			out, copied = slices.Clone(msgs), true
		}
		out[i].Content = kept
	}
	return out
}

// NewTextMessage returns a message with one text part.
func NewTextMessage(role message.Role, text string) Message {
	return Message{
		Role:    role,
		Content: []message.ContentPart{message.TextPart{Text: text}},
	}
}

// NewSystemMessage returns a system message with one text part.
func NewSystemMessage(text string) Message {
	return NewTextMessage(message.RoleSystem, text)
}

// NewUserMessage returns a user message with one text part.
func NewUserMessage(text string) Message {
	return NewTextMessage(message.RoleUser, text)
}

// NewAgentMessage returns an agent message with one text part.
func NewAgentMessage(text string) Message {
	return NewTextMessage(message.RoleAgent, text)
}

// NewFileMessage returns a user message with an optional prompt and file.
func NewFileMessage(text, mediaType, base64Data string) Message {
	var parts []message.ContentPart
	if text != "" {
		parts = append(parts, message.TextPart{Text: text})
	}
	parts = append(parts, message.FilePart{Data: base64Data, MediaType: mediaType})
	return Message{Role: message.RoleUser, Content: parts}
}

// NewImageMessage returns a user message with an optional prompt and inline
// base64 image.
func NewImageMessage(text, mediaType, base64Data string) Message {
	var parts []message.ContentPart
	if text != "" {
		parts = append(parts, message.TextPart{Text: text})
	}
	parts = append(parts, message.ImagePart{
		Source: &message.ImageSource{
			SourceType: message.ImageSourceBase64,
			MediaType:  mediaType,
			Data:       base64Data,
		},
	})
	return Message{Role: message.RoleUser, Content: parts}
}

// NewImageURLMessage returns a user message with an optional prompt and image URL.
func NewImageURLMessage(text, imageURL string) Message {
	var parts []message.ContentPart
	if text != "" {
		parts = append(parts, message.TextPart{Text: text})
	}
	parts = append(parts, message.ImagePart{
		Source: &message.ImageSource{
			SourceType: message.ImageSourceURL,
			URL:        imageURL,
		},
	})
	return Message{Role: message.RoleUser, Content: parts}
}

// TextContent joins text parts with newlines.
func (m Message) TextContent() string {
	var sb strings.Builder
	for _, p := range m.Content {
		if t, ok := p.(message.TextPart); ok {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// IsTextOnly reports whether the message is nonempty and contains only text.
func (m Message) IsTextOnly() bool {
	if len(m.Content) == 0 {
		return false
	}
	for _, p := range m.Content {
		if _, ok := p.(message.TextPart); !ok {
			return false
		}
	}
	return true
}

// MarshalJSON encodes content using message.PartJSON.
func (m Message) MarshalJSON() ([]byte, error) {
	parts := make([]message.PartJSON, len(m.Content))
	for i, p := range m.Content {
		parts[i] = message.PartToJSON(p)
	}
	return json.Marshal(struct {
		Role    message.Role       `json:"role"`
		Content []message.PartJSON `json:"content"`
	}{m.Role, parts})
}

// UnmarshalJSON decodes content using message.PartFromJSON.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    message.Role       `json:"role"`
		Content []message.PartJSON `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Content = make([]message.ContentPart, len(raw.Content))
	for i, j := range raw.Content {
		m.Content[i] = message.PartFromJSON(j)
	}
	return nil
}
