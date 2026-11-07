package builtin

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // registers the GIF header ImageSize reads
	_ "image/jpeg" // registers the JPEG header ImageSize reads
	_ "image/png"  // registers the PNG header ImageSize reads
	"path/filepath"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// The largest image read returns, which every provider takes. A larger
// one is an error that tells the model the limits, as a file too long to
// read whole is cut with a notice, so that it can resize the image and
// read it again.
const (
	// MaxImageEncoded is the most bytes an image may take base64-encoded,
	// as requests send it: 5MB, the least any provider takes per image,
	// as Claude on a cloud does.
	MaxImageEncoded = 5 << 20
	// MaxImageSide is the most pixels each side of an image may have:
	// 3000, which Claude keeps every image of a request to once it holds
	// more than 20, as a long session's history does.
	MaxImageSide = 3000
)

// imageTypes are the media types of the images read returns as images, as
// http.DetectContentType names them: those every request format takes.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// ReadImage returns the output of a read of the image at path, of
// mediaType, whose content is data: a line that names it, its type, its
// size in pixels and bytes, and the image whole as an attachment. An image
// over MaxImageEncoded or MaxImageSide, or whose size cannot be read, is
// an error that says so.
func ReadImage(path, mediaType string, data []byte) (agent.ToolOutput, error) {
	name := filepath.Base(path)
	w, h, err := ImageSize(data, mediaType)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("read the size of image %s: %w", name, err)
	}
	encoded := base64.StdEncoding.EncodedLen(len(data))
	if w > MaxImageSide || h > MaxImageSide || encoded > MaxImageEncoded {
		return agent.ToolOutput{}, fmt.Errorf(
			"image %s is %dx%d px and %s encoded; read takes images up to %d px per side and %s encoded. Resize or compress it into another file, and read that",
			name, w, h, FormatSize(encoded), MaxImageSide, FormatSize(MaxImageEncoded))
	}
	return agent.ToolOutput{
		Text: fmt.Sprintf("Read image %s (%s, %dx%d px, %s)", name, mediaType, w, h, FormatSize(len(data))),
		Attachments: []message.FilePart{{
			Data:      base64.StdEncoding.EncodeToString(data),
			MediaType: mediaType,
			Filename:  name,
		}},
	}, nil
}

// ImageSize returns the width and height in pixels of data, an image of
// mediaType, one of imageTypes, as its header gives them.
func ImageSize(data []byte, mediaType string) (w, h int, err error) {
	if mediaType == "image/webp" {
		return WebPSize(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// errWebP reports a WebP file whose header WebPSize cannot read.
var errWebP = errors.New("not a WebP header flow reads")

// WebPSize returns the width and height in pixels of data, a WebP image, as
// the header of its first chunk gives them: a lossy VP8 frame, a lossless
// VP8L one, or the canvas of an extended VP8X file.
func WebPSize(data []byte) (w, h int, err error) {
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, errWebP
	}
	chunk := data[20:]
	switch string(data[12:16]) {
	case "VP8 ":
		// A frame tag, then the start code, then 14-bit sizes.
		if chunk[3] != 0x9d || chunk[4] != 0x01 || chunk[5] != 0x2a {
			return 0, 0, errWebP
		}
		return int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff), nil
	case "VP8L":
		// A signature byte, then 14-bit sizes less one.
		if chunk[0] != 0x2f {
			return 0, 0, errWebP
		}
		bits := binary.LittleEndian.Uint32(chunk[1:5])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		// Flags, then 24-bit canvas sizes less one.
		u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
		return u24(chunk[4:7]) + 1, u24(chunk[7:10]) + 1, nil
	}
	return 0, 0, errWebP
}
