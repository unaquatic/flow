// Package message defines conversation messages and content parts, and the
// IDs of the sessions that hold them. It stores nothing:
// [github.com/unaquatic/flow/agent/session] keeps them in the database.
package message

import (
	"encoding/json"
)

// ContentType identifies the kind of content part.
type ContentType string

const (
	// TypeText identifies plain text.
	TypeText ContentType = "text"
	// TypeImage identifies image data.
	TypeImage ContentType = "image"
	// TypeFile identifies attached file data.
	TypeFile ContentType = "file"
	// TypeReasoning identifies model reasoning text.
	TypeReasoning ContentType = "reasoning"
	// TypeTool identifies a fused tool call and result.
	TypeTool ContentType = "tool"
	// TypeToolUse identifies a provider tool request.
	TypeToolUse ContentType = "tool_use"
	// TypeToolResult identifies a provider tool result.
	TypeToolResult ContentType = "tool_result"
	// TypeUsage identifies the token counts of the request that produced a
	// message.
	TypeUsage ContentType = "usage"
)

// builtinTypes lists the part types this package defines, which
// RegisterPart refuses.
var builtinTypes = []ContentType{
	TypeText, TypeImage, TypeFile, TypeReasoning, TypeTool, TypeToolUse, TypeToolResult, TypeUsage,
}

// ContentPart is one typed part of a message.
type ContentPart interface {
	PartType() ContentType
	// PartID returns the application-assigned part ID, which may be empty.
	PartID() PartID
}

// TextPart is a plain text content part.
type TextPart struct {
	// ID identifies the part.
	ID PartID `json:"id,omitempty"`
	// Text is the plain-text content.
	Text string `json:"text"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (TextPart) PartType() ContentType { return TypeText }
func (p TextPart) PartID() PartID      { return p.ID }

// ReasoningPart contains model reasoning text.
type ReasoningPart struct {
	// ID identifies the part.
	ID PartID `json:"id,omitempty"`
	// Text is the reasoning content.
	Text string `json:"text"`
	// Signature contains provider verification data, or the reasoning
	// itself, encrypted, when Redacted is set.
	Signature string `json:"signature,omitempty"`
	// Redacted reports that the provider withheld the reasoning, as
	// the Messages format does in a redacted_thinking block, so it has no text and
	// Signature holds the reasoning to send back unchanged.
	Redacted bool `json:"redacted,omitempty"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (ReasoningPart) PartType() ContentType { return TypeReasoning }
func (p ReasoningPart) PartID() PartID      { return p.ID }

// ToolUsePart is a provider-facing tool invocation.
type ToolUsePart struct {
	// ID identifies the content part.
	ID PartID `json:"id,omitempty"`
	// CallID is the provider-assigned tool-call identifier.
	CallID string `json:"call_id,omitempty"`
	// Name identifies the tool.
	Name string `json:"name"`
	// Input contains raw tool arguments.
	Input json.RawMessage `json:"input"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (ToolUsePart) PartType() ContentType { return TypeToolUse }
func (p ToolUsePart) PartID() PartID      { return p.ID }

// ToolResultPart is the result of a tool invocation.
type ToolResultPart struct {
	// ID identifies the content part.
	ID PartID `json:"id,omitempty"`
	// ToolUseID identifies the corresponding provider tool request.
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Name identifies the tool.
	Name string `json:"name,omitempty"`
	// Text contains tool output.
	Text string `json:"text"`
	// IsError reports whether Text describes a failure.
	IsError bool `json:"is_error,omitempty"`
	// Attachments are files the tool returned for the model to see beside
	// Text, such as an image, which each provider format sends as it can.
	Attachments []FilePart `json:"attachments,omitempty"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (ToolResultPart) PartType() ContentType { return TypeToolResult }
func (p ToolResultPart) PartID() PartID      { return p.ID }

// ImagePart is an image content part.
type ImagePart struct {
	// ID identifies the part.
	ID PartID `json:"id,omitempty"`
	// Source describes the image data.
	Source *ImageSource `json:"source"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (ImagePart) PartType() ContentType { return TypeImage }
func (p ImagePart) PartID() PartID      { return p.ID }

// FilePart is an attached file.
type FilePart struct {
	// ID identifies the part.
	ID PartID `json:"id,omitempty"`
	// Data contains encoded file data.
	Data string `json:"data"`
	// MediaType is the file media type.
	MediaType string `json:"media_type"`
	// Filename is the optional display name.
	Filename string `json:"filename,omitempty"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (FilePart) PartType() ContentType { return TypeFile }
func (p FilePart) PartID() PartID      { return p.ID }

// UsagePart records the token counts the provider reported for the request
// that produced an agent message. A message holds at most one, and none
// when the provider reported no usage. It is Metadata: it is stored with the
// message but neither shown nor sent to a provider.
type UsagePart struct {
	// ID identifies the part.
	ID PartID `json:"id,omitempty"`
	// Usage is the reported token counts.
	Usage Usage `json:"usage"`
}

func (UsagePart) PartType() ContentType { return TypeUsage }
func (p UsagePart) PartID() PartID      { return p.ID }

// Metadata marks the part as Metadata.
func (UsagePart) Metadata() {}

// ImageSourceType identifies the origin of image data.
type ImageSourceType string

const (
	// ImageSourceBase64 identifies inline base64 data.
	ImageSourceBase64 ImageSourceType = "base64"
	// ImageSourceURL identifies an image URL.
	ImageSourceURL ImageSourceType = "url"
)

// ImageSource provides image data.
type ImageSource struct {
	// SourceType identifies how image data is supplied.
	SourceType ImageSourceType `json:"type"`
	// MediaType is the image media type.
	MediaType string `json:"media_type,omitempty"`
	// Data contains inline base64 data.
	Data string `json:"data,omitempty"`
	// URL contains the remote image location.
	URL string `json:"url,omitempty"`
}

// CacheControl provides prompt caching hints to providers that support it.
type CacheControl struct {
	// Type identifies the provider cache-control mode.
	Type string `json:"type"`
}

// PartJSON is the tagged JSON representation of a ContentPart: the fields of
// a part of this package, or of a part of a type RegisterPart registered,
// which Ext holds.
type PartJSON struct {
	// Type selects the concrete content-part type.
	Type ContentType `json:"type"`
	// PartID identifies the application content part.
	PartID PartID `json:"id,omitempty"`
	// Text contains text, reasoning, or tool-result content.
	Text string `json:"text,omitempty"`
	// Source describes image data.
	Source *ImageSource `json:"source,omitempty"`
	// Data contains attached file data.
	Data string `json:"data,omitempty"`
	// MediaType describes attached file data.
	MediaType string `json:"media_type,omitempty"`
	// Filename is the optional file name.
	Filename string `json:"filename,omitempty"`
	// Signature contains provider reasoning verification data.
	Signature string `json:"signature,omitempty"`
	// Redacted marks reasoning that the provider withheld.
	Redacted bool `json:"redacted,omitempty"`
	// Name identifies a tool.
	Name string `json:"name,omitempty"`
	// Input contains raw tool arguments.
	Input json.RawMessage `json:"input,omitempty"`
	// ToolUseID identifies a provider tool request.
	ToolUseID string `json:"tool_use_id,omitempty"`
	// IsError marks tool-result failures.
	IsError bool `json:"is_error,omitempty"`
	// Attachments are the files a tool result carries beside its text.
	Attachments []FilePart `json:"attachments,omitempty"`
	// CallID is the provider-assigned tool-call identifier.
	CallID string `json:"call_id,omitempty"`
	// State contains a fused tool lifecycle state.
	State *ToolStateJSON `json:"state,omitempty"`
	// Usage contains the token counts of a usage part.
	Usage *Usage `json:"usage,omitempty"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	// Ext is a part of a type RegisterPart registered, which encodes its own
	// fields beside Type. It is nil for a part of this package.
	Ext ContentPart `json:"-"`
}

// ensurePartID returns id or generates a new part ID.
func ensurePartID(id PartID) PartID {
	if id != "" {
		return id
	}
	return NewPartID()
}

// PartToJSON converts a recognized ContentPart, one of this package or of a
// registered type, to tagged JSON and assigns a missing part ID.
func PartToJSON(p ContentPart) PartJSON {
	j := PartJSON{Type: p.PartType()}
	switch v := p.(type) {
	case TextPart:
		j.PartID = ensurePartID(v.ID)
		j.Text = v.Text
		j.CacheControl = v.CacheControl
	case ReasoningPart:
		j.PartID = ensurePartID(v.ID)
		j.Text = v.Text
		j.Signature = v.Signature
		j.Redacted = v.Redacted
		j.CacheControl = v.CacheControl
	case ToolUsePart:
		j.PartID = ensurePartID(v.ID)
		j.CallID = v.CallID
		j.Name = v.Name
		j.Input = v.Input
		j.CacheControl = v.CacheControl
	case ToolResultPart:
		j.PartID = ensurePartID(v.ID)
		j.ToolUseID = v.ToolUseID
		j.Name = v.Name
		j.Text = v.Text
		j.IsError = v.IsError
		j.Attachments = v.Attachments
		j.CacheControl = v.CacheControl
	case ToolPart:
		j.PartID = ensurePartID(v.ID)
		j.CallID = v.CallID
		j.Name = v.Name
		state := encodeToolState(v.State)
		j.State = &state
		j.CacheControl = v.CacheControl
	case ImagePart:
		j.PartID = ensurePartID(v.ID)
		j.Source = v.Source
		j.CacheControl = v.CacheControl
	case FilePart:
		j.PartID = ensurePartID(v.ID)
		j.Data = v.Data
		j.MediaType = v.MediaType
		j.Filename = v.Filename
		j.CacheControl = v.CacheControl
	case UsagePart:
		j.PartID = ensurePartID(v.ID)
		j.Usage = &v.Usage
	default:
		if c, ok := codecFor(p.PartType()); ok {
			j.PartID = ensurePartID(p.PartID())
			j.Ext = c.withID(p, j.PartID)
		}
	}
	return j
}

// PartFromJSON converts tagged JSON to a ContentPart. Legacy tool-use records
// with a provider call ID in PartID are assigned a new PartID and migrated to
// CallID. A part of a registered type is the one Ext holds. Unknown types
// become TextPart values.
func PartFromJSON(j PartJSON) ContentPart {
	if j.Ext != nil {
		return j.Ext
	}
	id := ensurePartID(j.PartID)
	switch j.Type {
	case TypeText:
		return TextPart{ID: id, Text: j.Text, CacheControl: j.CacheControl}
	case TypeReasoning:
		return ReasoningPart{ID: id, Text: j.Text, Signature: j.Signature, Redacted: j.Redacted, CacheControl: j.CacheControl}
	case TypeTool:
		var state ToolState
		if j.State != nil {
			state = j.State.decode()
		} else {
			state = ToolStatePending{}
		}
		return ToolPart{ID: id, CallID: j.CallID, Name: j.Name, State: state, CacheControl: j.CacheControl}
	case TypeToolUse:
		return ToolUsePart{ID: id, CallID: j.CallID, Name: j.Name, Input: j.Input, CacheControl: j.CacheControl}
	case TypeToolResult:
		return ToolResultPart{ID: id, ToolUseID: j.ToolUseID, Name: j.Name, Text: j.Text, IsError: j.IsError, Attachments: j.Attachments, CacheControl: j.CacheControl}
	case TypeImage:
		return ImagePart{ID: id, Source: j.Source, CacheControl: j.CacheControl}
	case TypeFile:
		return FilePart{ID: id, Data: j.Data, MediaType: j.MediaType, Filename: j.Filename, CacheControl: j.CacheControl}
	case TypeUsage:
		var u Usage
		if j.Usage != nil {
			u = *j.Usage
		}
		return UsagePart{ID: id, Usage: u}
	default:
		return TextPart{ID: id, Text: j.Text}
	}
}

// MarshalParts encodes content parts as a JSON array.
func MarshalParts(parts []ContentPart) (json.RawMessage, error) {
	if parts == nil {
		return json.RawMessage("[]"), nil
	}
	jj := make([]PartJSON, len(parts))
	for i, p := range parts {
		jj[i] = PartToJSON(p)
	}
	return json.Marshal(jj)
}

// UnmarshalParts decodes a JSON array of content parts.
func UnmarshalParts(data json.RawMessage) ([]ContentPart, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var jj []PartJSON
	if err := json.Unmarshal(data, &jj); err != nil {
		return nil, err
	}
	parts := make([]ContentPart, len(jj))
	for i, j := range jj {
		parts[i] = PartFromJSON(j)
	}
	return parts, nil
}
