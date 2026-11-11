package server

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/sse"
)

// handleEvents streams app events as server-sent events, encoded as
//
//	event: <EventType>
//	data: <event JSON>
//
// The optional session query filters events by session ID. While no event
// comes, a keepalive comment goes every Options.Heartbeat, so that proxies
// keep the connection open and a client can tell a quiet stream from a dead
// one.
//
// No client holds up the app's runs: the stream ends when its client falls
// too far behind the events, as app.App.SubscribeDroppable drops it, or
// takes no write within Options.WriteTimeout. A client whose stream ended
// that way subscribes again, and reads again the state it follows.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		respondErr(w, errStreamingUnsupported)
		return
	}
	filter := message.SessionID(r.URL.Query().Get("session"))

	// Subscribe before sending the successful response.
	events, ended := s.app.SubscribeDroppable(r.Context())

	stream := s.StartStream(w)
	if err := stream.Flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(s.opts.heartbeat())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ended:
			if r.Context().Err() == nil {
				slog.Warn("event stream ended: the client fell behind", "remote", r.RemoteAddr)
			}
			return
		case <-heartbeat.C:
			if err := stream.Comment("keepalive"); err != nil {
				return
			}
		case ev := <-events:
			if filter != "" && ev.Base().SessionID != filter {
				continue
			}
			if err := stream.Event(ev); err != nil {
				return
			}
		}
	}
}

// streamSend runs input in the request and answers with the run's events, as
// streamRun does.
func (s *Server) streamSend(w http.ResponseWriter, r *http.Request, input app.SendInput) {
	s.streamRun(w, r, s.session(r).Stream(input))
}

// streamRun runs run in the request and answers with its events as
// server-sent events, framed as handleEvents frames them, ending the response
// once the run has ended. A failed run's error is in its last event,
// EventAgentEnded. A run that cannot start is answered with its error status
// instead, and a client that leaves stops the run, as does one that takes no
// write within Options.WriteTimeout.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, run iter.Seq2[app.Event, error]) {
	if _, ok := w.(http.Flusher); !ok {
		respondErr(w, errStreamingUnsupported)
		return
	}
	var stream *EventStream
	for ev, err := range run {
		if err != nil {
			if stream == nil {
				respondSessionErr(w, err)
			}
			return
		}
		if stream == nil {
			stream = s.StartStream(w)
		}
		// Leaving the loop stops the run.
		if r.Context().Err() != nil || stream.Event(ev) != nil {
			return
		}
	}
}

// EventStream writes the server-sent events of a streamed response, each
// flushed to the client within a write timeout.
type EventStream struct {
	// W is the response the events are written to.
	W http.ResponseWriter
	// RC sets the write deadlines of W and flushes it.
	RC *http.ResponseController
	// Timeout bounds each write and its flush.
	Timeout time.Duration
}

// StartStream starts w as a stream of server-sent events, as sse.Start
// does, whose writes are bounded by Options.WriteTimeout. w must support
// flushing.
func (s *Server) StartStream(w http.ResponseWriter) *EventStream {
	sse.Start(w)
	return &EventStream{W: w, RC: http.NewResponseController(w), Timeout: s.opts.writeTimeout()}
}

// Event writes ev, as writeSSE encodes it, and flushes it to the client.
func (e *EventStream) Event(ev app.Event) error {
	return e.write(func(w io.Writer) error { return writeSSE(w, ev) })
}

// Send writes v as the data of one event of type typ, as writeEvent
// encodes it, and flushes it to the client.
func (e *EventStream) Send(typ string, v any) error {
	return e.write(func(w io.Writer) error { return writeEvent(w, typ, v) })
}

// Comment writes the comment text, which an event stream reader skips, and
// flushes it to the client.
func (e *EventStream) Comment(text string) error {
	return e.write(func(w io.Writer) error { return sse.WriteComment(w, text) })
}

// Flush flushes what was written to the client, within the timeout.
func (e *EventStream) Flush() error {
	return e.write(func(io.Writer) error { return nil })
}

// write runs encode on the response and flushes it, failing when the
// client takes neither within Timeout. The deadline stays set once write
// returns, so that the server's last write of the response, once the
// handler ends, is bounded too; the server clears it after that. A response
// that cannot set deadlines is written without them.
func (e *EventStream) write(encode func(io.Writer) error) error {
	if err := e.RC.SetWriteDeadline(time.Now().Add(e.Timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if err := encode(e.W); err != nil {
		return err
	}
	return e.RC.Flush()
}

// writeSSE encodes one app event in SSE framing: its EventType, and the JSON
// the event encodes as, which app/testdata/events.golden records.
func writeSSE(w io.Writer, ev app.Event) error {
	return writeEvent(w, string(ev.EventType()), ev)
}

// writeEvent encodes v as the data of one server-sent event of type typ.
func writeEvent(w io.Writer, typ string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return sse.Write(w, sse.Event{Type: typ, Data: string(data)})
}
