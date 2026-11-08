package server

import (
	"encoding/json"
	"net/http"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// handleTree returns the session's whole history, as an [TreeResponse]. An
// unknown session, such as a new one before its first message, has an empty
// tree.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.session(r).Tree()
	if err != nil {
		respondSessionErr(w, err)
		return
	}
	out := TreeResponse{Messages: make([]app.Message, 0, len(tree.Messages)), Head: tree.Head}
	for _, m := range tree.Messages {
		out.Messages = append(out.Messages, app.Message{Content: m})
	}
	respondJSON(w, http.StatusOK, out)
}

// handleNavigate moves the session so that its transcript ends at a message
// on any branch, and reports the workspace changes the move made, as
// [app.Session.Navigate] describes. A move to where the transcript already
// ends is answered 409.
func (s *Server) handleNavigate(w http.ResponseWriter, r *http.Request) {
	var req NavigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondInvalid(w, "invalid request body")
		return
	}
	if req.MessageID == "" {
		respondInvalid(w, "message_id must not be empty")
		return
	}
	moved, err := s.session(r).Navigate(req.MessageID)
	respondCheckout(w, moved, err)
}

// TreeResponse is the whole of a session's history: every message on every
// branch, and the point on one of them the transcript ends at.
type TreeResponse struct {
	// Messages are the session's messages in the order they were stored. Each
	// names the message it continues in parent_id, which comes before it.
	Messages []app.Message `json:"messages"`
	// Head is the last message of the transcript, or empty when the
	// transcript shows none.
	Head message.ID `json:"head"`
}

// NavigateRequest is the body of POST /session/{id}/navigate.
type NavigateRequest struct {
	// MessageID is the message the transcript is to end at.
	MessageID message.ID `json:"message_id"`
}
