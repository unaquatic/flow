package server

import (
	"encoding/json"
	"io"
	"iter"
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
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondErr(w, errStreamingUnsupported)
		return
	}
	filter := message.SessionID(r.URL.Query().Get("session"))

	// Subscribe before sending the successful response.
	events := s.app.Subscribe(r.Context())

	sse.Start(w)
	flusher.Flush()
	heartbeat := time.NewTicker(s.opts.heartbeat())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := sse.WriteComment(w, "keepalive"); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-events:
			if filter != "" && ev.Base().SessionID != filter {
				continue
			}
			if err := writeSSE(w, ev); err != nil {
				return
			}
			flusher.Flush()
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
// instead, and a client that leaves stops the run.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, run iter.Seq2[app.Event, error]) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondErr(w, errStreamingUnsupported)
		return
	}
	started := false
	for ev, err := range run {
		if err != nil {
			if !started {
				respondSessionErr(w, err)
			}
			return
		}
		if !started {
			sse.Start(w)
			started = true
		}
		// Leaving the loop stops the run.
		if r.Context().Err() != nil || writeSSE(w, ev) != nil {
			return
		}
		flusher.Flush()
	}
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
