package server

import (
	"encoding/json"
	"net/http"

	"github.com/unaquatic/flow/product"
)

// handleHealth answers that the server runs, and its version.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, Health{Version: product.Version()})
}

// handleConfigGet answers with the settings of the config file.
func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	s.RespondConfig(w)
}

// handleConfigUpdate writes the settings the body names to the config file,
// which every flow process reads, and answers with the settings. It refuses
// a setting the API cannot change.
func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var req ConfigUpdate
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid config update: "+err.Error())
		return
	}
	if req.Model != nil {
		if err := s.app.Models().SaveDefault(*req.Model); err != nil {
			respondModelErr(w, err)
			return
		}
	}
	s.RespondConfig(w)
}

// RespondConfig answers with the settings of the config file, or with why it
// cannot be read.
func (s *Server) RespondConfig(w http.ResponseWriter) {
	model, err := s.app.Models().Default()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, Config{Model: model})
}

// Health is the answer of GET /health.
type Health struct {
	// Version is the running application version.
	Version string `json:"version"`
}

// Config is the wire form of the settings of the config file that the API
// exposes, as GET /config and PATCH /config answer with them.
type Config struct {
	// Model is the default model of a request that names none, in the
	// format of SendRequest.Model.
	Model string `json:"model"`
}

// ConfigUpdate is the body of PATCH /config. It changes the settings it
// names, and leaves the others as they are.
type ConfigUpdate struct {
	// Model is the new default model, in the format of Config.Model.
	Model *string `json:"model,omitempty"`
}
