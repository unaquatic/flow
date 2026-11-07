package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent/message"
)

// png is an attachment of an image, as a read of an image file returns it.
var png = message.FilePart{Data: "aW1hZ2U=", MediaType: "image/png", Filename: "shot.png"}

// TestAttachments_FlowFromToolToRequest checks that the attachments a tool
// returns reach the completed state stored with its call, and the tool
// result ExpandToolParts sends; and that a failed run's are dropped.
func TestAttachments_FlowFromToolToRequest(t *testing.T) {
	tools := NewTools()
	tools.Register(Tool{Name: "read", Execute: func(context.Context, map[string]any) (ToolOutput, error) {
		return ToolOutput{Text: "Read image shot.png", Attachments: []message.FilePart{png}}, nil
	}})
	tools.Register(Tool{Name: "fail", Execute: func(context.Context, map[string]any) (ToolOutput, error) {
		return ToolOutput{Text: "broken", IsError: true, Attachments: []message.FilePart{png}}, nil
	}})

	res := tools.Execute(t.Context(), ToolCall{ID: "c1", Name: "read"})
	state, ok := ToolStateFor(res, json.RawMessage(`{"path":"shot.png"}`), time.Now()).(message.ToolStateCompleted)
	if !ok || !slices.Equal(state.Attachments, []message.FilePart{png}) {
		t.Fatalf("state = %#v, want completed with the image", state)
	}
	if failed := tools.Execute(t.Context(), ToolCall{ID: "c2", Name: "fail"}); len(failed.Attachments) != 0 {
		t.Errorf("failed run kept %d attachments, want none", len(failed.Attachments))
	}

	part := message.ToolPart{CallID: "c1", Name: "read", State: state}
	data, err := json.Marshal(message.PartToJSON(part))
	if err != nil {
		t.Fatal(err)
	}
	var j message.PartJSON
	if err := json.Unmarshal(data, &j); err != nil {
		t.Fatal(err)
	}
	stored := message.PartFromJSON(j).(message.ToolPart).State.(message.ToolStateCompleted)
	if !slices.Equal(stored.Attachments, []message.FilePart{png}) {
		t.Errorf("stored state's attachments = %#v, want the image", stored.Attachments)
	}

	expanded := ExpandToolParts([]Message{{Role: message.RoleAgent, Content: []message.ContentPart{part}}})
	result := expanded[1].Content[0].(message.ToolResultPart)
	if result.Text != "Read image shot.png" || !slices.Equal(result.Attachments, []message.FilePart{png}) {
		t.Errorf("result = %#v, want the text and the image", result)
	}
}

// TestAttachmentModality checks the modality each kind of file is.
func TestAttachmentModality(t *testing.T) {
	for mediaType, want := range map[string]gateway.Modality{
		"image/png":       "image",
		"IMAGE/JPEG":      "image",
		"application/pdf": "pdf",
		"audio/wav":       "audio",
		"video/mp4":       "video",
		"text/plain":      "file",
	} {
		if got := AttachmentModality(mediaType); got != want {
			t.Errorf("AttachmentModality(%q) = %q, want %q", mediaType, got, want)
		}
	}
}
