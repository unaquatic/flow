package message

import (
	"reflect"
	"strings"
	"testing"
	"uuid"
)

// TypeNote identifies notePart, the registered part of these tests.
const TypeNote ContentType = "note"

// notePart is a part of a type that RegisterPart registers, as an
// extension's would be.
type notePart struct {
	ID      PartID `json:"id,omitempty"`
	Note    string `json:"note,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

func (notePart) PartType() ContentType       { return TypeNote }
func (p notePart) PartID() PartID            { return p.ID }
func (p notePart) WithID(id PartID) notePart { p.ID = id; return p }
func (notePart) Metadata()                   {}

func init() { RegisterPart[notePart](TypeNote) }

// stripPartIDs clears generated IDs before structural comparisons.
func stripPartIDs(parts []ContentPart) []ContentPart {
	out := make([]ContentPart, len(parts))
	for i, p := range parts {
		switch v := p.(type) {
		case TextPart:
			v.ID = ""
			out[i] = v
		case ReasoningPart:
			v.ID = ""
			out[i] = v
		case ToolUsePart:
			v.ID = ""
			out[i] = v
		case ToolResultPart:
			v.ID = ""
			out[i] = v
		case ToolPart:
			v.ID = ""
			out[i] = v
		case ImagePart:
			v.ID = ""
			out[i] = v
		case FilePart:
			v.ID = ""
			out[i] = v
		case notePart:
			v.ID = ""
			out[i] = v
		case UsagePart:
			v.ID = ""
			out[i] = v
		case ToolSetPart:
			v.ID = ""
			out[i] = v
		default:
			out[i] = p
		}
	}
	return out
}

// TestRegisterPart_JSONRoundtrip checks that a part of a registered type
// keeps its fields through the tagged form, which is the fields the part
// encodes with "type" in front.
func TestRegisterPart_JSONRoundtrip(t *testing.T) {
	parts := []ContentPart{notePart{Note: "a"}, notePart{}, notePart{Pending: true}}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(stripPartIDs(got), parts) {
		t.Fatalf("round-trip lost data: got %+v want %+v", got, parts)
	}
	if _, err := uuid.Parse(string(got[0].PartID())); err != nil {
		t.Fatalf("auto-assigned PartID %q is not a UUID: %v", got[0].PartID(), err)
	}
	if !strings.HasPrefix(string(data), `[{"type":"note","id":"`) {
		t.Errorf("stored form %s, want the type and then the id first", data)
	}
	if n := strings.Count(string(data), `"pending"`); n != 1 {
		t.Errorf("stored form %s, want the mark on the pending part alone", data)
	}
}

// TestRegisterPart_PreservesID checks that a registered part keeps an
// explicit ID through the tagged form, and through PartJSON.
func TestRegisterPart_PreservesID(t *testing.T) {
	in := notePart{ID: "prt_1", Note: "kept"}
	j := PartToJSON(in)
	if j.PartID != in.ID {
		t.Errorf("PartJSON.PartID = %q, want %q", j.PartID, in.ID)
	}
	data, err := MarshalParts([]ContentPart{in})
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != in {
		t.Errorf("round trip = %+v, want %+v", got[0], in)
	}
}

// TestRegisterPart_Malformed checks that a registered part whose fields do
// not decode into its type fails to decode rather than reading as another.
func TestRegisterPart_Malformed(t *testing.T) {
	if got, err := UnmarshalParts([]byte(`[{"type":"note","note":42}]`)); err == nil {
		t.Fatalf("unmarshal = %+v, want an error", got)
	}
	// A field that a built-in part holds as another type does not get in the
	// way.
	if _, err := UnmarshalParts([]byte(`[{"type":"note","note":"x","usage":"n/a"}]`)); err != nil {
		t.Fatalf("unmarshal with a clashing field: %v", err)
	}
}

// TestRegisterPart_Twice checks that a part type is registered once.
func TestRegisterPart_Twice(t *testing.T) {
	for _, typ := range []ContentType{TypeNote, TypeText} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("registering %q again did not panic", typ)
				}
			}()
			RegisterPart[notePart](typ)
		}()
	}
}

// TestLastPart finds the last part of a type among a message's parts.
func TestLastPart(t *testing.T) {
	parts := []ContentPart{notePart{Note: "first"}, TextPart{Text: "hi"}, notePart{Note: "last"}}
	if got, ok := LastPart[notePart](parts); !ok || got.Note != "last" {
		t.Errorf("LastPart = %+v, %v; want the last note", got, ok)
	}
	if got, ok := LastPart[UsagePart](parts); ok {
		t.Errorf("LastPart of an absent type = %+v, true; want false", got)
	}
}

// TestMetadata checks which parts are metadata, which no provider is sent.
func TestMetadata(t *testing.T) {
	for _, p := range []ContentPart{UsagePart{}, notePart{}} {
		if _, ok := p.(Metadata); !ok {
			t.Errorf("%T is not Metadata", p)
		}
	}
	if _, ok := ContentPart(TextPart{}).(Metadata); ok {
		t.Error("TextPart is Metadata")
	}
}

// TestUsagePart_JSONRoundtrip checks that a usage part keeps every count
// through the stored form, which leaves out the counts that are zero.
func TestUsagePart_JSONRoundtrip(t *testing.T) {
	parts := []ContentPart{UsagePart{Usage: Usage{
		InputTokens: 1200, CachedInputTokens: 1024, CacheCreationInputTokens: 176,
		OutputTokens: 300, ReasoningOutputTokens: 20, TotalTokens: 1676,
	}}, UsagePart{Usage: Usage{InputTokens: 10}}}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(stripPartIDs(got), parts) {
		t.Fatalf("round-trip lost data: got %+v want %+v", got, parts)
	}
	if strings.Count(string(data), `"output_tokens"`) != 1 {
		t.Errorf("stored form %s, want zero counts left out", data)
	}
}

// TestToolSetPart_JSONRoundtrip checks that a set of tools keeps each tool's
// name, description and whole input schema, in order, through the stored
// form, and that an empty set reads back as an empty set, written as an
// empty list.
func TestToolSetPart_JSONRoundtrip(t *testing.T) {
	parts := []ContentPart{
		ToolSetPart{Tools: []ToolDefinition{
			{Name: "edit", Description: "Edit a file.", InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path": {Type: "string", Description: "File to edit."},
					"mode": {Type: "string", Enum: []string{"replace", "append"}},
					"edits": {Type: "array", Items: &PropertySchema{
						Type:       "object",
						Properties: map[string]PropertySchema{"old": {Type: "string"}, "new": {Type: "string"}},
						Required:   []string{"old", "new"},
					}},
				},
				Required: []string{"path", "edits"},
			}},
			{Name: "noop", InputSchema: InputSchema{Type: "object"}},
		}},
		ToolSetPart{Tools: []ToolDefinition{}},
	}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(stripPartIDs(got), parts) {
		t.Fatalf("round-trip lost data: got %+v want %+v", got, parts)
	}
	if !strings.Contains(string(data), `"tools":[]`) {
		t.Errorf("stored form %s, want the empty set as an empty list", data)
	}
	if _, ok := got[0].(Metadata); ok {
		t.Error("ToolSetPart is Metadata, but providers are sent it")
	}
}

// TestUsageIn finds the usage part among a message's parts.
func TestUsageIn(t *testing.T) {
	u := Usage{InputTokens: 7, OutputTokens: 3}
	msg := Content{Parts: []ContentPart{
		TextPart{Text: "hi"}, UsagePart{Usage: u}, notePart{},
	}}
	if got := msg.Usage(); got != u {
		t.Errorf("usage = %+v, want %+v", got, u)
	}
	if got := UsageIn([]ContentPart{TextPart{Text: "hi"}}); !got.IsZero() {
		t.Errorf("usage of parts without a usage part = %+v, want zero", got)
	}
}

// TestPartID_PreservedThroughRoundtrip checks that an explicit ID is retained.
func TestPartID_PreservedThroughRoundtrip(t *testing.T) {
	id := PartID("0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	parts := []ContentPart{TextPart{ID: id, Text: "hello"}}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got[0].PartID() != id {
		t.Fatalf("expected PartID %q to round-trip; got %q", id, got[0].PartID())
	}
}

// TestPartID_AutoAssignedOnMissing checks ID assignment during serialization.
func TestPartID_AutoAssignedOnMissing(t *testing.T) {
	parts := []ContentPart{TextPart{Text: "hello"}}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"id":"`) {
		t.Fatalf("expected marshalled JSON to include an auto-assigned id, got %s", data)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, err := uuid.Parse(string(got[0].PartID())); err != nil {
		t.Fatalf("auto-assigned PartID %q is not a UUID: %v", got[0].PartID(), err)
	}
}

// TestReasoningPart_JSONRoundtripRedacted checks that redacted reasoning
// keeps its mark and its encrypted reasoning through the stored form.
func TestReasoningPart_JSONRoundtripRedacted(t *testing.T) {
	in := []ContentPart{
		ReasoningPart{Signature: "enc", Redacted: true},
		ReasoningPart{Text: "thought", Signature: "sig"},
	}
	data, err := MarshalParts(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalParts(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := stripPartIDs(out); !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
	if strings.Count(string(data), `"redacted"`) != 1 {
		t.Errorf("stored form %s, want the mark on the redacted part alone", data)
	}
}
