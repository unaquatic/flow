package server

import (
	"encoding/json"
	"net/http"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
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
		respondInvalid(w, "invalid config update: "+err.Error())
		return
	}
	if req.DefaultProvider != nil || req.DefaultModel != nil {
		err := s.app.Models().UpdateDefaultModel(func(d *app.DefaultModel) {
			if req.DefaultProvider != nil {
				d.Provider = *req.DefaultProvider
			}
			if req.DefaultModel != nil {
				d.Model = *req.DefaultModel
			}
		})
		if err != nil {
			respondErr(w, err)
			return
		}
	}
	s.RespondConfig(w)
}

// RespondConfig answers with the settings of the config file, or with why it
// cannot be read.
func (s *Server) RespondConfig(w http.ResponseWriter) {
	d, err := s.app.Models().DefaultModel()
	if err != nil {
		respondErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, Config{DefaultProvider: d.Provider, DefaultModel: d.Model})
}

// Health is the answer of GET /health.
type Health struct {
	// Version is the running application version.
	Version string `json:"version"`
}

// Config is the wire form of the settings of the config file that the API
// exposes, as GET /config and PATCH /config answer with them.
type Config struct {
	// DefaultProvider is the provider of the default model of a request
	// that names none, by its catalog ID, such as "provider-a", or empty
	// when the config file sets none.
	DefaultProvider agent.ProviderID `json:"default_provider"`
	// DefaultModel is the default model without its provider, in
	// "model:option:effort" form, such as "lab-a/model-a:fast:high", or
	// empty when the config file sets none.
	DefaultModel string `json:"default_model"`
}

// ConfigUpdate is the body of PATCH /config. It changes the settings it
// names, and leaves the others as they are. A default provider or model
// it names is saved with the other as the config file sets it; the two
// must name a registered provider and a model.
type ConfigUpdate struct {
	// DefaultProvider is the new default provider, in the form of
	// Config.DefaultProvider.
	DefaultProvider *agent.ProviderID `json:"default_provider,omitempty"`
	// DefaultModel is the new default model, in the form of
	// Config.DefaultModel.
	DefaultModel *string `json:"default_model,omitempty"`
}
