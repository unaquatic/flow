package server

import (
	"encoding/json"
	"net/http"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
)

// handleSessionList returns the project's sessions, as a list of
// [session.Session] that is empty rather than null when there are none.
func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.app.Sessions().ListSessions(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sessions == nil {
		sessions = []session.Session{}
	}
	respondJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusCreated, s.app.Sessions().CreateSession(r.Context()))
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	busy, err := s.app.BusySessions(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make(map[string]SessionStatusInfo, len(busy))
	for id := range busy {
		out[string(id)] = SessionStatusInfo{Type: "busy"}
	}
	respondJSON(w, http.StatusOK, out)
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	sess, err := s.app.Sessions().Session(r.Context(), sessionID(r))
	if err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, sess)
}

func (s *Server) handleSessionUpdate(w http.ResponseWriter, r *http.Request) {
	var req SessionUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := sessionID(r)
	title := ""
	if req.Title != nil {
		title = *req.Title
	}
	sess, err := s.app.Session(id).SetTitle(title)
	if err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, sess)
}

func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteSession(r.Context(), sessionID(r)); err != nil {
		respondErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMessageList returns the messages on the session's active branch, with
// their positions at forks. Unknown sessions, new ones among them, produce an
// empty list.
func (s *Server) handleMessageList(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.session(r).Messages()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request) {
	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Text == "" {
		respondError(w, http.StatusBadRequest, "text must not be empty")
		return
	}
	id := message.NewID()
	input := app.SendInput{Text: req.Text, Model: req.Model, MessageID: id}
	if req.Stream {
		s.streamSend(w, r, input)
		return
	}
	if err := s.session(r).Send(input); err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusAccepted, SendResponse{MessageID: id})
}

// handleContinue starts a run that picks up from where the transcript ends,
// without a new prompt, as [app.Session.Continue] describes. It answers 202
// at once, or streams the run's events when the request asks to. An empty
// transcript is answered 409.
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	var req ContinueRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if req.Stream {
		s.streamRun(w, r, s.session(r).ContinueStream(req.Model))
		return
	}
	if err := s.session(r).Continue(req.Model); err != nil {
		respondErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleMessageGet returns one message. Its position at a fork is known only
// while it is on the session's active branch.
func (s *Server) handleMessageGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.session(r).Message(message.ID(r.PathValue("messageID")))
	if err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, m)
}

// handleMessageDelete removes a message and every message below it. It is
// refused while the session has an operation in progress.
func (s *Server) handleMessageDelete(w http.ResponseWriter, r *http.Request) {
	err := s.session(r).DeleteMessage(message.ID(r.PathValue("messageID")))
	if err != nil {
		respondErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.session(r).Stop()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	files, err := s.session(r).Undo()
	respondCheckout(w, files, err)
}

func (s *Server) handleRedo(w http.ResponseWriter, r *http.Request) {
	files, err := s.session(r).Redo()
	respondCheckout(w, files, err)
}

func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	var req CheckoutRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	files, err := s.session(r).Checkout(req.MessageID)
	respondCheckout(w, files, err)
}

// respondCheckout writes a checkout outcome or its mapped failure status. A
// move that changed no file reports an empty list rather than null.
func respondCheckout(w http.ResponseWriter, files []app.FileDiff, err error) {
	if err != nil {
		respondErr(w, err)
		return
	}
	if files == nil {
		files = []app.FileDiff{}
	}
	respondJSON(w, http.StatusOK, CheckoutResponse{Files: files})
}

// session returns a handle on the session named by the request's path.
func (s *Server) session(r *http.Request) app.Session {
	return s.app.Session(sessionID(r))
}

// sessionID returns the session ID in the request's path.
func sessionID(r *http.Request) message.SessionID {
	return message.SessionID(r.PathValue("id"))
}

// SendRequest is the body of POST /session/{id}/message.
type SendRequest struct {
	// Text is the user prompt.
	Text string `json:"text"`
	// Model is the model to answer with, in "provider/model:option:effort"
	// format, such as "openai/gpt-5.6:fast:high", leaving out the option
	// and the level it does not set. The level is one the catalog lists for the
	// model; a model that names none runs at its default level, the
	// catalog's or else medium, where the catalog lists it for the model. Empty selects the default
	// model, as GET /config reports it.
	Model string `json:"model,omitempty"`
	// Stream answers with the run's events as server-sent events, framed as
	// GET /event frames them, ending once the run has ended, instead of
	// answering 202 at once. Leaving the request early stops the run.
	Stream bool `json:"stream,omitempty"`
}

// ContinueRequest is the body of POST /session/{id}/continue, which may be
// left out.
type ContinueRequest struct {
	// Model is the model to answer with, in the format of
	// SendRequest.Model. Empty selects the default model, as GET /config
	// reports it.
	Model string `json:"model,omitempty"`
	// Stream answers with the run's events as server-sent events, as
	// SendRequest.Stream does.
	Stream bool `json:"stream,omitempty"`
}

// SendResponse acknowledges a queued prompt.
type SendResponse struct {
	// MessageID identifies the queued user message.
	MessageID message.ID `json:"message_id"`
}

// CheckoutRequest names the history point to move to.
type CheckoutRequest struct {
	// MessageID is the first message to hide. Empty selects the latest state.
	MessageID message.ID `json:"message_id,omitempty"`
}

// CheckoutResponse reports the outcome of a checkout.
type CheckoutResponse struct {
	// Files contains workspace changes made by the move.
	Files []app.FileDiff `json:"files"`
}

// SessionStatusInfo is the status returned for one session. Idle sessions are
// not returned.
type SessionStatusInfo struct {
	// Type is "busy".
	Type string `json:"type"`
}

// SessionUpdateRequest is the body of PATCH /session/{id}.
type SessionUpdateRequest struct {
	// Title replaces the session title when present.
	Title *string `json:"title,omitempty"`
}
