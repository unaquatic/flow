package agent

import (
	"strings"

	"github.com/unaquatic/catalog/gateway"
)

// AttachmentModality returns the modality of a file of mediaType, as the
// catalog lists the modalities a model takes: "image" for an image, "pdf"
// for a PDF, "audio" and "video" for those, and "file" for anything else.
// The request formats tell by it how to send an attachment.
func AttachmentModality(mediaType string) gateway.Modality {
	kind, _, _ := strings.Cut(strings.ToLower(mediaType), "/")
	switch {
	case kind == "image", kind == "audio", kind == "video":
		return gateway.Modality(kind)
	case strings.EqualFold(mediaType, "application/pdf"):
		return "pdf"
	}
	return "file"
}
