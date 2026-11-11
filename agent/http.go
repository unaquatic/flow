package agent

import (
	"fmt"
	"io"

	"github.com/unaquatic/flow/internal/sse"
)

// StreamEvents returns the stream of the server-sent events of body. It
// passes each event to handle, which yields the parts it makes and reports
// whether to go on, and closes body once the stream ends. A failure to read
// body ends the stream with a *ResponseParseError.
func StreamEvents(body io.ReadCloser, handle func(sse.Event, func(StreamPart, error) bool) bool) StreamResponse {
	return func(yield func(StreamPart, error) bool) {
		defer body.Close()
		for ev, err := range sse.Read(body) {
			if err != nil {
				yield(nil, &ResponseParseError{Err: fmt.Errorf("read event stream: %w", err)})
				return
			}
			if !handle(ev, yield) {
				return
			}
		}
	}
}
