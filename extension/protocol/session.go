package protocol

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/product"
)

// SessionHeader is the header that names a request's session to the
// gateways of SessionProviders, which route the requests of one session to
// the same prompt cache, and reject a request without one.
const SessionHeader = "x-opencode-session"

// SessionProviders are the providers whose gateway takes SessionHeader and
// asks a client to name itself in its user agent: OpenCode Zen and OpenCode
// Go, at every request format of their endpoints.
var SessionProviders = []agent.ProviderID{"opencode", "opencode-go"}

// UserAgent names flow and its version in the requests of SessionProviders,
// in place of the HTTP client's own, as their gateway asks of a client.
var UserAgent = product.Name + "/" + product.Version()

// SessionProvider is a provider whose requests name their session in
// SessionHeader, and flow in UserAgent, as SessionModel sends them.
type SessionProvider struct {
	agent.Provider
}

// Model returns the model name of the provider, whose requests name their
// session.
func (p SessionProvider) Model(name string) agent.Model {
	return SessionModel{Model: p.Provider.Model(name)}
}

// SessionModel is a model whose requests name their session in
// SessionHeader: the request's cache key, which is the session's ID, or a
// new ID for a request of no session, as the HTTP API's are. A request
// names flow in UserAgent too, unless its headers set one.
type SessionModel struct {
	agent.Model
}

// Stream starts req, with the headers SessionModel adds, as the model does.
// It does not modify req's headers.
func (m SessionModel) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	headers := maps.Clone(req.Headers)
	if headers == nil {
		headers = make(map[string]string, 2)
	}
	session := req.CacheKey
	if session == "" {
		session = string(message.NewSessionID())
	}
	headers[SessionHeader] = session
	if !hasHeader(headers, "User-Agent") {
		headers["User-Agent"] = UserAgent
	}
	req.Headers = headers
	return m.Model.Stream(ctx, req)
}

// hasHeader reports whether headers sets header name, in any case, as HTTP
// header names are matched.
func hasHeader(headers map[string]string, name string) bool {
	return slices.ContainsFunc(slices.Collect(maps.Keys(headers)), func(k string) bool {
		return strings.EqualFold(k, name)
	})
}
