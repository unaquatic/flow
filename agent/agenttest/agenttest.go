// Package agenttest helps test code that asks models for responses: a
// Gateway that answers every request with a stream a test gives and records
// the requests, the providers that send their models' requests to it, and
// builders of the streams a provider returns.
package agenttest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/internal/catalog"
)

// Gateway answers the requests of the models of its Providers with the
// stream StreamFunc returns, and records each request.
type Gateway struct {
	// StreamFunc returns the response to each request.
	StreamFunc func() (agent.StreamResponse, error)

	// StreamCalls records the history each request sent.
	StreamCalls [][]agent.Message
	// StreamProviders records the provider of each request.
	StreamProviders []agent.ProviderID
	// StreamModels records the model of each request.
	StreamModels []string
	// StreamCacheKeys records the cache key of each request.
	StreamCacheKeys []string
}

// Stream records a request of model of provider pt, and returns the response
// StreamFunc gives.
func (g *Gateway) Stream(_ context.Context, pt agent.ProviderID, model string, req agent.Request) (agent.StreamResponse, error) {
	g.StreamCalls = append(g.StreamCalls, req.Messages)
	g.StreamProviders = append(g.StreamProviders, pt)
	g.StreamModels = append(g.StreamModels, model)
	g.StreamCacheKeys = append(g.StreamCacheKeys, req.CacheKey)
	return g.StreamFunc()
}

// Provider is a provider whose models send their requests to Gateway.
type Provider struct {
	// ID names the provider.
	ID agent.ProviderID
	// Gateway answers the requests of its models.
	Gateway *Gateway
}

// Name returns the provider's ID.
func (p Provider) Name() string { return string(p.ID) }

// Model returns the model called name, whose requests go to Gateway.
func (p Provider) Model(name string) agent.Model {
	return model{provider: p.ID, name: name, gw: p.Gateway}
}

// model is one model of a Provider.
type model struct {
	provider agent.ProviderID
	name     string
	gw       *Gateway
}

// Stream sends req to the model's gateway.
func (m model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	return m.gw.Stream(ctx, m.provider, m.name, req)
}

// NewProviders returns a Providers whose providers, "mock" and each one
// named in extra, all send their requests to gw, over an empty catalog in a
// temporary directory of t.
func NewProviders(t testing.TB, gw *Gateway, extra ...agent.ProviderID) *agent.Providers {
	t.Helper()
	providers := []agent.Provider{Provider{ID: "mock", Gateway: gw}}
	for _, name := range extra {
		providers = append(providers, Provider{ID: name, Gateway: gw})
	}
	return agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: t.TempDir()})}, providers...)
}

// Stream returns a stream that yields parts in order.
func Stream(parts ...agent.StreamPart) agent.StreamResponse {
	return func(yield func(agent.StreamPart, error) bool) {
		for _, p := range parts {
			if !yield(p, nil) {
				return
			}
		}
	}
}

// StreamError returns a stream that fails with err at once.
func StreamError(err error) agent.StreamResponse {
	return func(yield func(agent.StreamPart, error) bool) {
		yield(nil, err)
	}
}

// TextResponse returns a stream of a response that says text and ends its
// turn.
func TextResponse(text string) agent.StreamResponse {
	return Stream(
		agent.TextDelta{Text: text},
		agent.Stop{Reason: agent.StopEndTurn},
	)
}

// ToolCallResponse returns a stream of a response that makes one tool call,
// callID, of tool name with the arguments argsJSON.
func ToolCallResponse(callID, name, argsJSON string) agent.StreamResponse {
	return Stream(
		agent.ToolUseStart{ID: callID, Name: name},
		agent.ToolUseDelta{ID: callID, JSON: argsJSON},
		agent.ToolUseEnd{ID: callID, Input: json.RawMessage(argsJSON)},
		agent.Stop{Reason: agent.StopToolUse},
	)
}
