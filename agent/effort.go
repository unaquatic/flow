package agent

import (
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
)

// ReasoningEffort is a reasoning effort level, as the catalog names it, such
// as "low" or "high". The empty ReasoningEffort sets none, which leaves the
// effort to the provider.
type ReasoningEffort string

// Common reasoning efforts, least first. Which efforts a model takes, and
// their names, come from its catalog offering, as ReasoningEfforts lists
// them.
const (
	EffortNone    ReasoningEffort = "none"
	EffortMinimal ReasoningEffort = "minimal"
	EffortLow     ReasoningEffort = "low"
	EffortMedium  ReasoningEffort = "medium"
	EffortHigh    ReasoningEffort = "high"
	EffortXHigh   ReasoningEffort = "xhigh"
	EffortMax     ReasoningEffort = "max"
)

// FallbackEffort is the effort a model runs at when none is chosen and its
// catalog offering names no default effort, when the model takes it.
const FallbackEffort = EffortMedium

// ParseReasoningEffort returns the effort that s names, ignoring case and the
// spaces around it.
func ParseReasoningEffort(s string) ReasoningEffort {
	return ReasoningEffort(strings.ToLower(strings.TrimSpace(s)))
}

// ReasoningEfforts returns the effort levels of the reasoning controls r, in
// their order, or none when r is nil or takes no effort. The catalog lists
// none, which turns reasoning off, wherever reasoning can be turned off;
// each provider sends it as its API turns reasoning off.
func ReasoningEfforts(r *gateway.Reasoning) []ReasoningEffort {
	if r == nil || len(r.Efforts) == 0 {
		return nil
	}
	levels := make([]ReasoningEffort, 0, len(r.Efforts))
	for _, e := range r.Efforts {
		levels = append(levels, ReasoningEffort(e))
	}
	return levels
}

// DefaultEffort returns the effort a model with the reasoning controls r
// runs at when none is chosen: r's default effort, or else FallbackEffort.
func DefaultEffort(r *gateway.Reasoning) ReasoningEffort {
	if r != nil && r.DefaultEffort != "" {
		return ReasoningEffort(r.DefaultEffort)
	}
	return FallbackEffort
}

// Fit returns chosen when levels holds it, or else def when levels holds
// it, or else none.
func Fit(levels []ReasoningEffort, chosen, def ReasoningEffort) ReasoningEffort {
	switch {
	case slices.Contains(levels, chosen):
		return chosen
	case slices.Contains(levels, def):
		return def
	}
	return ""
}

// Reasoning is the effort level a request reasons at, and the reasoning
// controls the model's catalog offering lists, which each provider turns
// into the fields of its own requests.
type Reasoning struct {
	// Effort is the effort level, one the model lists, or empty to leave the
	// effort to the provider.
	Effort ReasoningEffort
	// Controls are the reasoning controls the catalog lists for the model.
	Controls gateway.Reasoning
}

// ReasoningFor returns the reasoning of offering off at level t, or nil when
// it does not reason. A level off does not list sets none.
func ReasoningFor(off gateway.Offering, t ReasoningEffort) *Reasoning {
	if off.Reasoning == nil {
		return nil
	}
	c := *off.Reasoning
	c.Efforts = slices.Clone(c.Efforts)
	return &Reasoning{Effort: Fit(ReasoningEfforts(off.Reasoning), t, ""), Controls: c}
}
