package agent

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/unaquatic/flow/sse"
)

// TestStreamEvents checks that each event reaches the handler, which can end
// the stream, that a read failure ends it with a *ResponseParseError, and
// that the body is closed either way.
func TestStreamEvents(t *testing.T) {
	handle := func(ev sse.Event, yield func(StreamPart, error) bool) bool {
		if ev.Data == "stop" {
			return false
		}
		return yield(TextDelta{Text: ev.Data}, nil)
	}
	tests := []struct {
		name      string
		body      io.Reader
		want      string
		wantParse bool
	}{
		{name: "handler stops", body: strings.NewReader("data: a\n\ndata: b\n\ndata: stop\n\ndata: c\n\n"), want: "ab"},
		{name: "read failure", body: io.MultiReader(strings.NewReader("data: a\n\n"), failingReader{}), want: "a", wantParse: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &closeRecorder{Reader: tt.body}
			var text string
			var parseErr bool
			for part, err := range StreamEvents(body, handle) {
				if err != nil {
					_, parseErr = errors.AsType[*ResponseParseError](err)
					continue
				}
				text += part.(TextDelta).Text
			}
			if text != tt.want || parseErr != tt.wantParse || !body.closed {
				t.Errorf("text, parse error, closed = %q, %v, %v, want %q, %v, true", text, parseErr, body.closed, tt.want, tt.wantParse)
			}
		})
	}
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// closeRecorder is a body that records whether it was closed.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}
