package protocol

import (
	"net/http"
	"os"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/completions"
	"github.com/unaquatic/flow/extension/protocol/content"
	"github.com/unaquatic/flow/extension/protocol/google"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/messages"
	"github.com/unaquatic/flow/extension/protocol/responses"
)

// Formats are the request formats of the catalog that the protocols speak.
var Formats = []gateway.Format{
	gateway.FormatAnthropicMessages, gateway.FormatAnthropicVertex, gateway.FormatOpenAIResponses,
	gateway.FormatOpenAIChat, gateway.FormatGemini,
}

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
//   - by Google, with OAuth tokens from the application default
//     credentials of the environment, as google.Credentials makes them.
//
// OpenAI's Responses endpoints also send the requests of a ChatGPT login to
// the ChatGPT backend, as ChatGPTURL says. An endpoint of another format or
// authentication has no provider.
func Dialer(opts Options) agent.Dialer {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	gcp := &google.Credentials{Env: env, Client: opts.Client}
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
		case gateway.AuthGoogle:
			az, ambient = gcp, gcp.Available
			client = &http.Client{Transport: gcp.Transport(base)}
		default:
			return nil, false
		}
		name := string(t)
		var p agent.Provider
		switch ep.Format {
		case gateway.FormatAnthropicMessages, gateway.FormatAnthropicVertex:
			p = messages.New(messages.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, Vertex: ep.Format == gateway.FormatAnthropicVertex, HTTPClient: client})
		case gateway.FormatOpenAIResponses:
			init := responses.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client}
			if t == chatgpt.ProviderID {
				init.ChatGPTBaseURL = ChatGPTURL(ep.API)
			}
			p = responses.New(init)
		case gateway.FormatOpenAIChat:
			p = completions.New(completions.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client})
		case gateway.FormatGemini:
			p = content.New(content.Init{Name: name, Auth: az, Key: ep.Auth, BaseURL: ep.API, HTTPClient: client})
		default:
			return nil, false
		}
		return Provider{Provider: p, Ambient: ambient}, true
	}
}

// ChatGPTURL returns the base URL that the requests of a ChatGPT login go
// to, for OpenAI's Responses endpoint at api: the ChatGPT backend for
// OpenAI's platform API, and api itself for another, such as a proxy's that
// the configuration moves OpenAI to.
func ChatGPTURL(api string) string {
	if strings.TrimSuffix(api, "/") == responses.DefaultBaseURL {
		return chatgpt.BaseURL
	}
	return api
}

// Provider is a provider that Dialer makes: its models run at the effort
// their lab documents as their default, whichever provider serves them, as
// OpenRouter's Claude runs at Anthropic's, and it reports whether the
// environment holds credentials that authorize it.
type Provider struct {
	agent.Provider
	// Ambient reports whether the environment holds credentials that
	// authorize the provider's requests without an account, such as
	// Google's application default credentials.
	Ambient func() bool
}

// AmbientCredentials reports whether the environment holds credentials that
// authorize the provider, as p.Ambient reports it.
func (p Provider) AmbientCredentials() bool { return p.Ambient != nil && p.Ambient() }
