package builtin

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"
)

// pngOf returns a PNG of w by h pixels.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestReadImage checks that read returns an image, as its content shows
// whatever its name, whole as an attachment, after a line that names it,
// its type, and its size, whatever offset and limit say; and that it reads
// a file named as an image that holds text as text.
func TestReadImage(t *testing.T) {
	t.Chdir(t.TempDir())
	shot := pngOf(t, 4, 3)
	var anim bytes.Buffer
	if err := gif.Encode(&anim, image.NewPaletted(image.Rect(0, 0, 2, 5), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "shot.png", string(shot), 0o644)
	writeFile(t, "logo", anim.String(), 0o644)
	writeFile(t, "notes.png", "not an image\n", 0o644)

	for _, tt := range []struct {
		path, mediaType, size string
		data                  []byte
		args                  map[string]any
	}{
		{path: "shot.png", mediaType: "image/png", size: "4x3", data: shot},
		{path: "shot.png", mediaType: "image/png", size: "4x3", data: shot, args: map[string]any{"offset": float64(3), "limit": float64(1)}},
		{path: "logo", mediaType: "image/gif", size: "2x5", data: anim.Bytes()},
	} {
		args := map[string]any{"path": tt.path}
		for k, v := range tt.args {
			args[k] = v
		}
		out, err := Read().Execute(t.Context(), args)
		if err != nil {
			t.Fatalf("read %s: %v", tt.path, err)
		}
		if want := fmt.Sprintf("Read image %s (%s, %s px, %s)", tt.path, tt.mediaType, tt.size, FormatSize(len(tt.data))); out.Text != want {
			t.Errorf("read %s = %q, want %q", tt.path, out.Text, want)
		}
		if len(out.Attachments) != 1 {
			t.Fatalf("read %s returned %d attachments, want the image", tt.path, len(out.Attachments))
		}
		a := out.Attachments[0]
		data, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || !bytes.Equal(data, tt.data) || a.MediaType != tt.mediaType || a.Filename != tt.path {
			t.Errorf("read %s attached %s %q of %d bytes, %v; want the whole %s", tt.path, a.MediaType, a.Filename, len(data), err, tt.mediaType)
		}
	}

	if out, err := Read().Execute(t.Context(), map[string]any{"path": "notes.png"}); err != nil || out.Text != "not an image" || len(out.Attachments) != 0 {
		t.Errorf("read notes.png = %q with %d attachments, %v; want its text", out.Text, len(out.Attachments), err)
	}
}

// TestReadImageOverLimits checks that read returns no image over
// MaxImageSide pixels on either side or MaxImageEncoded bytes encoded, but
// an error that gives its size and the limits and says to resize it, for
// the model to resize the image and read it again, as it reads on from a
// file too long to read whole.
func TestReadImageOverLimits(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, "wide.png", string(pngOf(t, MaxImageSide+1, 2)), 0o644)
	writeFile(t, "tall.png", string(pngOf(t, 2, MaxImageSide+1)), 0o644)
	// The header gives a small image, and the bytes after it pass the
	// limit, as a large photo's do.
	writeFile(t, "heavy.png", string(pngOf(t, 2, 2))+strings.Repeat("\x00", MaxImageEncoded), 0o644)
	writeFile(t, "fits.png", string(pngOf(t, MaxImageSide, MaxImageSide)), 0o644)

	for path, want := range map[string]string{
		"wide.png":  fmt.Sprintf("image wide.png is %dx2 px", MaxImageSide+1),
		"tall.png":  fmt.Sprintf("image tall.png is 2x%d px", MaxImageSide+1),
		"heavy.png": "image heavy.png is 2x2 px and 6.7MB encoded",
	} {
		out, err := Read().Execute(t.Context(), map[string]any{"path": path})
		if err == nil {
			t.Errorf("read %s = %q with %d attachments, want an error", path, out.Text, len(out.Attachments))
			continue
		}
		for _, part := range []string{want, fmt.Sprintf("up to %d px per side and 5.0MB encoded", MaxImageSide), "Resize or compress it"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("read %s: error %q does not say %q", path, err, part)
			}
		}
	}
	if out, err := Read().Execute(t.Context(), map[string]any{"path": "fits.png"}); err != nil || len(out.Attachments) != 1 {
		t.Errorf("read of an image at the limits = %v, %d attachments; want it returned", err, len(out.Attachments))
	}
}

// webp returns a WebP file whose first chunk is fourcc holding data.
func webp(fourcc string, data []byte) []byte {
	data = append(data, make([]byte, 16)...)
	b := []byte("RIFF\x00\x00\x00\x00WEBP" + fourcc)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

// TestWebPSize checks that WebPSize reads the size of a lossy, a lossless,
// and an extended WebP from its header, and fails for a header it does not
// know.
func TestWebPSize(t *testing.T) {
	lossy := append([]byte{0, 0, 0, 0x9d, 0x01, 0x2a}, binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, 640), 480)...)
	lossless := append([]byte{0x2f}, binary.LittleEndian.AppendUint32(nil, uint32(800-1)|uint32(600-1)<<14)...)
	extended := []byte{0, 0, 0, 0, 0xb7, 0x0b, 0x00, 0x0f, 0x00, 0x00} // 3000-1 and 16-1
	for _, tt := range []struct {
		name string
		data []byte
		w, h int
	}{
		{"lossy", webp("VP8 ", lossy), 640, 480},
		{"lossless", webp("VP8L", lossless), 800, 600},
		{"extended", webp("VP8X", extended), 3000, 16},
	} {
		w, h, err := WebPSize(tt.data)
		if err != nil || w != tt.w || h != tt.h {
			t.Errorf("%s: %dx%d, %v; want %dx%d", tt.name, w, h, err, tt.w, tt.h)
		}
	}
	if _, _, err := WebPSize(webp("ALPH", make([]byte, 10))); err == nil {
		t.Error("an unknown first chunk read as a size")
	}
	if _, _, err := WebPSize([]byte("RIFF")); err == nil {
		t.Error("a cut header read as a size")
	}
}
