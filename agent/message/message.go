package message

import (
	"encoding/json"
	"time"
	"uuid"
)

// Role identifies the sender of a message.
type Role int

const (
	// RoleAgent identifies model output.
	RoleAgent Role = iota
	// RoleUser identifies user input.
	RoleUser
	// RoleSystem identifies system instructions.
	RoleSystem
)

var roleStrings = [...]string{
	RoleAgent:  "agent",
	RoleUser:   "user",
	RoleSystem: "system",
}

var roleFromString = map[string]Role{
	"agent":  RoleAgent,
	"user":   RoleUser,
	"system": RoleSystem,
}

// ParseRole returns the role named by s, which is the form String writes. An
// unrecognized name returns RoleAgent.
func ParseRole(s string) Role {
	return roleFromString[s]
}

// String returns the wire name of r, or "unknown" for an unsupported role.
func (r Role) String() string {
	if int(r) < len(roleStrings) {
		return roleStrings[r]
	}
	return "unknown"
}

// MarshalJSON encodes r as its wire name.
func (r Role) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

// UnmarshalJSON decodes a recognized role name. An unknown name leaves r
// unchanged.
func (r *Role) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if v, ok := roleFromString[s]; ok {
		*r = v
	}
	return nil
}

// ID identifies a message.
type ID string

// NewID returns a message ID. Version 7 UUIDs lead with a timestamp, so they
// sort in creation order.
func NewID() ID {
	return ID(uuid.NewV7().String())
}

// PartID identifies a content part independently of provider tool-call IDs.
type PartID string

// NewPartID returns a part ID.
func NewPartID() PartID {
	return PartID(uuid.NewV7().String())
}

// Usage contains provider-reported token counts for the request that produced
// one message, which its [UsagePart] records. A zero value can mean either
// zero usage or an unavailable metric.
type Usage struct {
	// InputTokens counts the prompt tokens read from a prompt cache or
	// processed anew. It leaves out the tokens written to a prompt cache,
	// which CacheCreationInputTokens counts.
	InputTokens int `json:"input_tokens,omitempty"`
	// CachedInputTokens is the subset of InputTokens served from cache.
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	// CacheCreationInputTokens counts input written to a prompt cache.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// OutputTokens is the provider's total output count.
	OutputTokens int `json:"output_tokens,omitempty"`
	// ReasoningOutputTokens is the reasoning subset of output tokens.
	ReasoningOutputTokens int `json:"reasoning_output_tokens,omitempty"`
	// TotalTokens is the total reported by the provider.
	TotalTokens int `json:"total_tokens,omitempty"`
}

// IsZero reports whether the Usage carries no token counts.
func (u Usage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

// UsageIn returns the token counts that the [UsagePart] among parts records,
// or the zero Usage when parts hold none.
func UsageIn(parts []ContentPart) Usage {
	u, _ := LastPart[UsagePart](parts)
	return u.Usage
}

// Content is one conversation message.
//
// Messages form a tree: ParentID links each message to the one it follows, so
// a session can hold several alternative continuations of the same point. The
// conversation a consumer sees is one root-to-leaf path through that tree.
type Content struct {
	// ID identifies the message.
	ID ID `json:"id"`
	// ParentID identifies the message this one continues, or is nil for a
	// message that opens a session's history.
	ParentID *ID `json:"parent_id"`
	// Role identifies the sender.
	Role Role `json:"role"`
	// Parts contains message content in order.
	Parts []ContentPart `json:"parts"`
	// Model names the model variant that produced the message, or that a
	// prompt was sent to, in "provider/model:option:effort" form, as
	// agent.Variant.String writes it, such as
	// "provider-a/model-a:fast:xhigh". It leaves out the option for
	// the default variant, which with the model decides the price of the
	// usage, and the effort when the provider chose it. It is empty when
	// the model is not known.
	Model string `json:"model,omitempty"`
	// CreatedAt is when the message was created. A stored message does not
	// change afterwards.
	CreatedAt time.Time `json:"created_at"`
}

// Usage returns the token counts of the request that produced the message,
// as its [UsagePart] records them, or the zero Usage when it has none.
func (c Content) Usage() Usage {
	return UsageIn(c.Parts)
}
