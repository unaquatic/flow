// Package sse reads and writes server-sent events.
package sse

import (
	"bufio"
	"io"
	"iter"
	"net/http"
	"strings"
)

// ContentType is the media type of an event stream.
const ContentType = "text/event-stream"

// Event is one server-sent event.
type Event struct {
	// Type is the event type, empty when the server names none.
	Type string
	// Data is the event data, its data lines joined by newlines.
	Data string
}

// Read yields the events of the event stream r in order, then the error
// reading r failed with, if any. An event is the lines up to a blank line:
// its type is the last "event" field and its data the "data" fields joined
// by newlines. A line starting with a colon is a comment, other fields are
// ignored, and an event with no data line is dropped. An event the stream
// ends before its blank line is still yielded. Lines may be of any length,
// as an event can carry a whole response.
func Read(r io.Reader) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		br := bufio.NewReader(r)
		var ev Event
		var data []string
		// dispatch yields the event read so far, if it has data, and starts
		// the next one. It reports whether to go on.
		dispatch := func() bool {
			defer func() { ev, data = Event{}, nil }()
			if data == nil {
				return true
			}
			ev.Data = strings.Join(data, "\n")
			return yield(ev, nil)
		}
		for {
			line, err := br.ReadString('\n')
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			switch {
			case line == "" && err == nil:
				if !dispatch() {
					return
				}
			case line != "":
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "event":
					ev.Type = value
				case "data":
					data = append(data, value)
				}
			}
			if err == io.EOF {
				dispatch()
				return
			}
			if err != nil {
				yield(Event{}, err)
				return
			}
		}
	}
}

// Start answers with the header of an event stream: its content type, no
// caching, and status 200 OK. The events follow as Write writes them to w.
func Start(w http.ResponseWriter) {
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
}

// Write writes ev to w as one event of an event stream: an "event" field when
// ev has a Type, a "data" field for each line of ev.Data, and a blank line.
// Read reads it back as ev, as long as Type has no line break and Data no
// carriage return.
func Write(w io.Writer, ev Event) error {
	var b strings.Builder
	if ev.Type != "" {
		b.WriteString("event: " + ev.Type + "\n")
	}
	for line := range strings.SplitSeq(ev.Data, "\n") {
		b.WriteString("data: " + line + "\n")
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}
