package server

import (
	"encoding/json"
	"net/http"

	"github.com/unaquatic/flow/agent/message"
)

// handleBranchList returns the newest fork on the session's active branch, as
// an [app.Fork].
func (s *Server) handleBranchList(w http.ResponseWriter, r *http.Request) {
	fork, err := s.session(r).Branches()
	if err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, fork)
}

// handleBranchSwitch moves the session onto another branch and reports the
// workspace changes the move made.
func (s *Server) handleBranchSwitch(w http.ResponseWriter, r *http.Request) {
	var req BranchSwitchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MessageID == "" {
		respondError(w, http.StatusBadRequest, "message_id must not be empty")
		return
	}
	moved, err := s.session(r).SwitchBranch(req.MessageID)
	respondCheckout(w, moved, err)
}

// BranchSwitchRequest names the branch to move onto.
type BranchSwitchRequest struct {
	// MessageID identifies the message that opens the branch.
	MessageID message.ID `json:"message_id"`
}
