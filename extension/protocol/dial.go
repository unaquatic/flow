package protocol

import (
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/adc"
	"github.com/unaquatic/flow/extension/protocol/completions"
	"github.com/unaquatic/flow/extension/protocol/content"
	"github.com/unaquatic/flow/extension/protocol/login/backendflow"
	"github.com/unaquatic/flow/extension/protocol/messages"
	"github.com/unaquatic/flow/extension/protocol/responses"
)

// The request formats of the catalog, named by the protocol package that
// speaks each.
const (
	// FormatMessages is the format of package messages.
	FormatMessages = gateway.FormatAnthropicMessages
	// FormatMessagesRawPredict is the format of package messages in the
	// raw predict shape of a cloud, as messages.Init.RawPredict sends it.
	FormatMessagesRawPredict = gateway.FormatAnthropicVertex
	// FormatResponses is the format of package responses.
	FormatResponses = gateway.FormatOpenAIResponses
	// FormatChat is the format of package completions.
	FormatChat = gateway.FormatOpenAIChat
	// FormatContent is the format of package content.
	FormatContent = gateway.FormatGemini
)

// AuthCloud is the authentication of an endpoint that a cloud's
// application default credentials authorize, as package adc reads them.
const AuthCloud = gateway.AuthGoogle

// Formats are the request formats of the catalog that the protocols speak.
var Formats = []gateway.Format{FormatMessages, FormatMessagesRawPredict, FormatResponses, FormatChat, FormatContent}

// Options configures a Dialer.
type Options struct {
	// Client sends the requests. Nil is a new client of each provider.
	Client *http.Client
	// Env reads the environment variables of cloud credentials. Nil reads
	// the process's.
	Env func(string) string
}

// Dialer returns the agent.Dialer that speaks the Formats, at an endpoint
// with a base URL, by the protocol of its format, authorized as the
// endpoint says:
//
//   - with an API key, from the authorizer it is given, as a bearer token
//     or in a header;
//   - by a cloud, with OAuth tokens from the application default
//     credentials of the environment, as adc.Credentials makes them.
//
// The Responses endpoints of backendflow's provider also send the requests
// of its login to its backend, as BackendURL says, and the requests of
// SessionProviders name their session, as SessionProvider sends them. An
// endpoint of another format or authentication has no provider.
func Dialer(opts Options) agent.Dialer {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	cloud := &adc.Credentials{Env: env, Client: opts.Client}
	var base http.RoundTripper
	if opts.Client != nil {
		base = opts.Client.Transport
	}
	return func(t agent.ProviderID, ep gateway.Endpoint, az agent.Authorizer) (agent.Provider, bool) {
		if ep.API == "" {
			return nil, false
		}
		client, ambient := opts.Client, func() bool { return false }
		switch ep.Auth.Scheme {
		case gateway.AuthBearer, gateway.AuthHeader:
		case AuthCloud:
			az, ambient = cloud, cloud.Available
			client = &http.Client{Transport: cloud.Transport(base)}
		default:
			return nil, false
		}
		name := string(t)
		var p agent.Provider
		switch ep.Format {
		case FormatMessages, FormatMessagesRawPredict:
			p = messages.New(messages.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, RawPredict: ep.Format == FormatMessagesRawPredict, HTTPClient: client})
		case FormatResponses:
			init := responses.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client}
			if t == backendflow.ProviderID {
				init.BackendURL = BackendURL(ep.API)
			}
			p = responses.New(init)
		case FormatChat:
			p = completions.New(completions.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client})
		case FormatContent:
			p = content.New(content.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client})
		default:
			return nil, false
		}
		if slices.Contains(SessionProviders, t) {
			p = SessionProvider{Provider: p}
		}
		return Provider{Provider: p, Ambient: ambient}, true
	}
}

// BackendURL returns the base URL that the requests of backendflow's login
// go to, for its provider's Responses endpoint at api: the backend for the
// provider's platform API, and api itself for another, such as a proxy's
// that the configuration moves the provider to.
func BackendURL(api string) string {
	if strings.TrimSuffix(api, "/") == responses.DefaultBaseURL {
		return backendflow.BaseURL
	}
	return api
}

// Provider is a provider that Dialer makes: its models run at the effort
// their lab documents as their default, whichever provider serves them, as
// a gateway's lab-a model runs at lab-a's, and it reports whether the
// environment holds credentials that authorize it.
type Provider struct {
	agent.Provider
	// Ambient reports whether the environment holds credentials that
	// authorize the provider's requests without an account, such as a
	// cloud's application default credentials.
	Ambient func() bool
}

// AmbientCredentials reports whether the environment holds credentials that
// authorize the provider, as p.Ambient reports it.
func (p Provider) AmbientCredentials() bool { return p.Ambient != nil && p.Ambient() }
