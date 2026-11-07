// Package protocol is the extension that gives flow the providers of the
// model catalog, by the request protocols they speak, and the logins of the
// providers that have their own. It plugs into flow at one of the points
// package extension describes:
//
//   - Behaviour: it makes the provider of each catalog endpoint whose format
//     and authentication it speaks through extension.DialHook, and adds the
//     login flows of the packages of login through extension.LoginHook, as
//     the app starts.
//
// The protocols live in its subpackages, one for each request format of the
// catalog, which each package's doc names: messages, completions, responses
// and content. Package adc authorizes requests with a cloud's application
// default credentials, and the packages of login sign in to the providers
// that have logins of their own: codeflow and backendflow.
package protocol

import (
	"context"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol/login/backendflow"
	"github.com/unaquatic/flow/extension/protocol/login/codeflow"
)

// Name names the extension.
const Name = "protocol"

// Extension makes the providers of the protocols it speaks, and logs in to
// the providers of the packages of login. It implements extension.DialHook
// and extension.LoginHook.
type Extension struct {
	// Dialer makes the provider of an endpoint.
	Dialer agent.Dialer
}

var (
	_ extension.DialHook  = (*Extension)(nil)
	_ extension.LoginHook = (*Extension)(nil)
)

// New returns the extension, dialing as Dialer does with opts.
func New(opts Options) *Extension {
	return &Extension{Dialer: Dialer(opts)}
}

// Load loads the extension, whose providers send their requests with their
// own clients and read cloud credentials from the process's environment.
func Load(context.Context, extension.Host) (extension.Extension, error) {
	return New(Options{}), nil
}

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Dial returns the provider of endpoint ep for provider t, authorized by az,
// as e.Dialer makes it, and reports whether the extension speaks ep.
func (e *Extension) Dial(t agent.ProviderID, ep gateway.Endpoint, az agent.Authorizer) (agent.Provider, bool) {
	return e.Dialer(t, ep, az)
}

// LoginFlows returns the logins of the packages of login, each for its
// provider.
func (e *Extension) LoginFlows() map[agent.ProviderID]agent.LoginFlow {
	return map[agent.ProviderID]agent.LoginFlow{
		codeflow.ProviderID:    codeflow.NewOAuth(),
		backendflow.ProviderID: backendflow.NewOAuth(),
	}
}
