package agent

import (
	"regexp"
	"strings"

	"github.com/unaquatic/catalog/gateway"
)

// ProviderID identifies a provider by its ID in the catalog, such as
// "provider-a" or "provider-b"; it is the catalog's own gateway.ProviderID. The
// agent knows no provider by name: the extensions that make providers, such
// as extension/protocol, name theirs.
type ProviderID = gateway.ProviderID

// ParseModel splits a "provider/model" string into its provider ID and
// model ID. A string without "/" names no provider: its provider ID is
// empty, and the whole string is the model ID.
func ParseModel(s string) (ProviderID, string) {
	if before, after, ok := strings.Cut(s, "/"); ok {
		return ProviderID(before), after
	}
	return "", s
}

// modelID matches a model ID: lab/model, such as lab-a/model-a:fast:high, or
// model alone, each part with no slash or white space.
var modelID = regexp.MustCompile(`^[^/\s]+(/[^/\s]+)?$`)

// ValidModelID reports whether id matches modelID.
func ValidModelID(id string) bool { return modelID.MatchString(id) }

// FormatModel returns the canonical "provider/model" string, which
// ParseModel reads back, or "" when t and model are both empty, so that no
// model reads as none rather than as "/".
func FormatModel(t ProviderID, model string) string {
	if t == "" && model == "" {
		return ""
	}
	return string(t) + "/" + model
}

// Provider supplies the models of one provider at one endpoint, such as
// a gateway's at its API. The clients of extension/protocol implement it,
// one for each request format, such as completions.Client for Chat
// Completions.
type Provider interface {
	// Name returns the provider's ID in the catalog.
	Name() string

	// Model returns a Model bound to this provider and the given model ID.
	Model(name string) Model
}

// AmbientProvider is a Provider whose requests credentials of the
// environment may authorize without an account, such as a cloud's
// application default credentials.
type AmbientProvider interface {
	Provider
	// AmbientCredentials reports whether the environment holds such
	// credentials.
	AmbientCredentials() bool
}
