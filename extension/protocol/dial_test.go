package protocol

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/adc"
	"github.com/unaquatic/flow/extension/protocol/content"
	"github.com/unaquatic/flow/extension/protocol/login/backendflow"
	"github.com/unaquatic/flow/extension/protocol/messages"
	"github.com/unaquatic/flow/extension/protocol/responses"
)

// key authorizes every request with the API key "sk".
type key struct{}

func (key) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization{Kind: agent.CredentialAPIKey, Token: "sk"}, nil
}

// TestDialer checks which endpoints have a provider: those of the formats
// flow speaks, authorized by an API key or by a cloud's credentials, and
// with a base URL; and that a provider is named after the catalog's
// provider.
func TestDialer(t *testing.T) {
	dial := withAuth(Dialer(Options{}), key{})
	bearer := gateway.Auth{Scheme: gateway.AuthBearer}
	tests := []struct {
		name string
		ep   gateway.Endpoint
		want bool
	}{
		{"messages", gateway.Endpoint{Format: FormatMessages, API: "https://a/v1", Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-api-key"}}, true},
		{"responses", gateway.Endpoint{Format: FormatResponses, API: "https://o/v1", Auth: bearer}, true},
		{"chat completions", gateway.Endpoint{Format: FormatChat, API: "https://c/v1", Auth: bearer}, true},
		{"generate content", gateway.Endpoint{Format: FormatContent, API: "https://g", Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: content.DefaultKeyHeader}}, true},
		{"generate content with cloud credentials", gateway.Endpoint{Format: FormatContent, API: "https://v", Auth: gateway.Auth{Scheme: AuthCloud}}, true},
		{"messages raw predict with cloud credentials", gateway.Endpoint{Format: FormatMessagesRawPredict, API: "https://v", Auth: gateway.Auth{Scheme: AuthCloud}}, true},
		{"chat completions with cloud credentials", gateway.Endpoint{Format: FormatChat, API: "https://v", Auth: gateway.Auth{Scheme: AuthCloud}}, true},
		{"unknown authentication", gateway.Endpoint{Format: FormatChat, API: "https://x"}, false},
		{"no base URL", gateway.Endpoint{Format: FormatChat, Auth: bearer}, false},
		{"unknown format", gateway.Endpoint{API: "https://u", Auth: bearer}, false},
	}
	for _, tt := range tests {
		p, ok := dial("gw", tt.ep)
		if ok != tt.want || ok && p.Name() != "gw" {
			t.Errorf("%s: provider %v, %v; want one named gw: %v", tt.name, p, ok, tt.want)
		}
	}
}

// TestKeyHeaders checks that an API key goes in the header the catalog's
// endpoint names: x-api-key by default in the Messages format, a bearer
// token where it says so, as some gateways' Messages endpoints do, and an
// api-key header in the Responses format.
func TestKeyHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	dial := withAuth(Dialer(Options{}), key{})
	tests := []struct {
		name   string
		ep     gateway.Endpoint
		header string
		want   string
	}{
		{"provider-a", gateway.Endpoint{Format: FormatMessages, Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-api-key"}}, "X-Api-Key", "sk"},
		{"provider-b", gateway.Endpoint{Format: FormatMessages, Auth: gateway.Auth{Scheme: gateway.AuthBearer}}, "Authorization", "Bearer sk"},
		{"provider-c", gateway.Endpoint{Format: FormatResponses, Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "api-key"}}, "Api-Key", "sk"},
		{"provider-d", gateway.Endpoint{Format: FormatChat, Auth: gateway.Auth{Scheme: gateway.AuthBearer}}, "Authorization", "Bearer sk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.ep.API = srv.URL + "/v1"
			p, ok := dial(agent.ProviderID(tt.name), tt.ep)
			if !ok {
				t.Fatal("no provider")
			}
			stream, err := p.Model("m").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}, MaxTokens: 10})
			if err != nil {
				t.Fatal(err)
			}
			for range stream {
			}
			if v := got.Get(tt.header); v != tt.want {
				t.Errorf("%s = %q, want %q (headers %v)", tt.header, v, tt.want, got)
			}
		})
	}
}

// none finds no API key.
type none struct{}

func (none) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization{}, agent.ErrNotAuthenticated
}

// recorder is an API that records the requests it gets and answers each
// with body.
func recorder(t *testing.T, body []byte) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// TestCloudCredentials checks the requests of a provider that a cloud's
// credentials authorize: with a token from the application default
// credentials, in the generateContent and the raw predict Messages formats
// alike, with the quota project; and that it reports the credentials.
func TestCloudCredentials(t *testing.T) {
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"access_token":"ya29.test","expires_in":3600}`)
	}))
	t.Cleanup(tokens.Close)
	file := filepath.Join(t.TempDir(), "adc.json")
	os.WriteFile(file, []byte(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r","quota_project_id":"qp","token_uri":"`+tokens.URL+`"}`), 0o600)
	env := func(k string) string {
		if k == adc.CredentialsEnv {
			return file
		}
		return ""
	}
	srv, got := recorder(t, []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\nevent: message_stop\ndata: {}\n\n"))
	dial := withAuth(Dialer(Options{Env: env}), none{})
	req := agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}, MaxTokens: 100}
	for _, ep := range []gateway.Endpoint{
		{Format: FormatContent, API: srv.URL + "/publishers/lab-c", Auth: gateway.Auth{Scheme: AuthCloud}},
		{Format: FormatMessagesRawPredict, API: srv.URL + "/publishers/lab-b/models", Auth: gateway.Auth{Scheme: AuthCloud}},
	} {
		p, ok := dial("provider-c", ep)
		if !ok {
			t.Fatal("no provider")
		}
		stream, err := p.Model("m").Stream(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		for range stream {
		}
		if !p.(agent.AmbientProvider).AmbientCredentials() {
			t.Error("the application default credentials not reported")
		}
	}
	for i, path := range []string{"/publishers/lab-c/models/m:streamGenerateContent", "/publishers/lab-b/models/m:streamRawPredict"} {
		r := (*got)[i]
		if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer ya29.test" || r.Header.Get(adc.QuotaHeader) != "qp" {
			t.Errorf("request %d to %s, Authorization %q, quota project %q", i, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get(adc.QuotaHeader))
		}
	}
}

// token authorizes every request with itself.
type token agent.Authorization

func (a token) Authorize(context.Context, *agent.Authorization) (agent.Authorization, error) {
	return agent.Authorization(a), nil
}

// roundTripFunc answers a client's requests without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestBackendURL checks where the requests of backendflow's provider's
// Responses endpoint go: its login's to its backend, or to the proxy the
// endpoint moved to, and an API key's to the endpoint; and that another
// provider's OAuth token goes to its own endpoint.
func TestBackendURL(t *testing.T) {
	oauth := token{Kind: agent.CredentialOAuth, Token: "tok"}
	key := token{Kind: agent.CredentialAPIKey, Token: "sk"}
	tests := []struct {
		name     string
		provider agent.ProviderID
		api      string
		auth     token
		want     string
	}{
		{"login", backendflow.ProviderID, responses.DefaultBaseURL, oauth, backendflow.BaseURL + "/responses"},
		{"login at a proxy", backendflow.ProviderID, "https://proxy.test/v1/", oauth, "https://proxy.test/v1/responses"},
		{"api key", backendflow.ProviderID, responses.DefaultBaseURL, key, responses.DefaultBaseURL + "/responses"},
		{"another provider's oauth", "provider-c", "https://provider-c.test/v1", oauth, "https://provider-c.test/v1/responses"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r.URL.String()
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			})}
			ep := gateway.Endpoint{Format: FormatResponses, API: tt.api, Auth: gateway.Auth{Scheme: gateway.AuthBearer}}
			p, ok := Dialer(Options{Client: client})(tt.provider, ep, tt.auth)
			if !ok {
				t.Fatal("no provider")
			}
			stream, err := p.Model("model-x").Stream(t.Context(), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}})
			if err != nil {
				t.Fatal(err)
			}
			for range stream {
			}
			if got != tt.want {
				t.Errorf("request to %s, want %s", got, tt.want)
			}
		})
	}
}

// TestCachesPrompts checks that the Messages requests to the format's own
// API alone turn on automatic prompt caching: not those to a proxy the
// configuration moves the endpoint to, a cloud's raw predict endpoint, or
// an endpoint of another format.
func TestCachesPrompts(t *testing.T) {
	for _, tt := range []struct {
		ep   gateway.Endpoint
		want bool
	}{
		{gateway.Endpoint{Format: FormatMessages, API: messages.DefaultBaseURL}, true},
		{gateway.Endpoint{Format: FormatMessages, API: messages.DefaultBaseURL + "/"}, true},
		{gateway.Endpoint{Format: FormatMessages, API: "https://proxy.example/v1"}, false},
		{gateway.Endpoint{Format: FormatMessagesRawPredict, API: messages.DefaultBaseURL}, false},
		{gateway.Endpoint{Format: FormatResponses, API: messages.DefaultBaseURL}, false},
	} {
		if got := CachesPrompts(tt.ep); got != tt.want {
			t.Errorf("CachesPrompts(%s at %s) = %v, want %v", tt.ep.Format, tt.ep.API, got, tt.want)
		}
	}
}

// TestEveryFormat checks that flow speaks every request format and
// authentication scheme the catalog knows, so that a format added to the
// catalog fails here until flow speaks it, rather than its models being
// hidden from the menu.
func TestEveryFormat(t *testing.T) {
	if !slices.Equal(slices.Sorted(slices.Values(Formats)), slices.Sorted(slices.Values(gateway.Formats))) {
		t.Errorf("flow speaks %v; the catalog knows %v", Formats, gateway.Formats)
	}
	dial := withAuth(Dialer(Options{}), key{})
	for _, f := range gateway.Formats {
		for _, a := range gateway.AuthSchemes {
			if _, ok := dial("p", gateway.Endpoint{Format: f, API: "https://p.invalid/v1", Auth: gateway.Auth{Scheme: a}}); !ok {
				t.Errorf("no provider for format %s with %s authentication", f, a)
			}
		}
	}
}

// withAuth returns d dialing with az, as a Providers gives it the authorizer
// of the provider it dials.
func withAuth(d agent.Dialer, az agent.Authorizer) func(agent.ProviderID, gateway.Endpoint) (agent.Provider, bool) {
	return func(t agent.ProviderID, ep gateway.Endpoint) (agent.Provider, bool) { return d(t, ep, az) }
}
