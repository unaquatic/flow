package app

import (
	"github.com/unaquatic/flow/agent"
)

const (
	// TypeModelsListed identifies a completed model listing.
	TypeModelsListed EventType = "catalog.models.listed"
	// TypeModelsRefreshed identifies a completed refresh of the model
	// catalog.
	TypeModelsRefreshed EventType = "catalog.models.refreshed"
)

// ModelListing contains the catalog's models of one provider.
type ModelListing struct {
	// Provider identifies the provider.
	Provider agent.ProviderID `json:"provider"`
	// Models contains the provider's offerings of the catalog's models,
	// and then its unlisted models, each with its variants.
	Models []agent.Listing `json:"models"`
	// ReasoningEfforts maps the ID of each model that takes reasoning efforts
	// to them, in the order the catalog lists them, as the ":effort" suffix
	// of a model names them.
	ReasoningEfforts map[string][]agent.ReasoningEffort `json:"reasoning_efforts,omitempty"`
}

// NewModelListing returns the listing of models of provider t, with the
// effort levels the catalog lists for each.
func NewModelListing(t agent.ProviderID, models []agent.Listing) ModelListing {
	l := ModelListing{Provider: t, Models: models}
	for _, m := range models {
		if levels := agent.ReasoningEfforts(m.Reasoning); len(levels) > 0 {
			if l.ReasoningEfforts == nil {
				l.ReasoningEfforts = make(map[string][]agent.ReasoningEffort)
			}
			l.ReasoningEfforts[m.Model] = levels
		}
	}
	return l
}

// EventModelsListed contains the model listings of the signed-in providers,
// or why they could not be read.
type EventModelsListed struct {
	EventBase
	Listings []ModelListing `json:"listings"`
	// Error is why the listings could not be read, or nil when they were.
	Error *Error `json:"error,omitempty"`
}

func (EventModelsListed) EventType() EventType { return TypeModelsListed }

// EventModelsRefreshed reports that a refresh of the model catalog
// completed, so model listings may have changed. It is published after every
// successful refresh, also when the source reported no change.
type EventModelsRefreshed struct {
	EventBase
}

func (EventModelsRefreshed) EventType() EventType { return TypeModelsRefreshed }
