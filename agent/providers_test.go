package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
)

// fakeFlow logs in with the OAuth token "old" and refreshes it to "new".
type fakeFlow struct {
	refreshes atomic.Int32
}

func (f *fakeFlow) Browser(context.Context, func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	return agent.OAuthToken{Access: "old", Refresh: "r"}, nil
}

func (f *fakeFlow) Device(ctx context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	return f.Browser(ctx, show)
}

func (f *fakeFlow) Refresh(context.Context, agent.OAuthToken) (agent.OAuthToken, error) {
	f.refreshes.Add(1)
	return agent.OAuthToken{Access: "new"}, nil
}

// apiServer is a provider API that accepts one credential and answers every
// other with 401 Unauthorized. It records the credential of each request.
type apiServer struct {
	*httptest.Server

	accept string

	mu   sync.Mutex
	sent []string
}

func newAPIServer(t *testing.T, accept string) *apiServer {
	t.Helper()
	s := &apiServer{accept: accept}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *apiServer) serve(w http.ResponseWriter, r *http.Request) {
	cred := r.Header.Get("x-api-key")
	if cred == "" {
		cred = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	s.mu.Lock()
	s.sent = append(s.sent, cred)
	s.mu.Unlock()

	if cred != s.accept {
		http.Error(w, `{"error":{"message":"invalid credential"}}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
}

// credentials returns the credentials the server received, in order.
func (s *apiServer) credentials() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// newProviders returns Providers over the catalog of doc, an
// api.json, as gatewaytest.FromAPI builds it, whose providers ask keys for
// their credentials, with overrides in place of the catalog's.
func newProviders(t *testing.T, doc string, keys agent.APIKeyFunc, overrides ...agent.Provider) *agent.Providers {
	t.Helper()
	store, dir := gatewaytest.StoreAPI(t, doc)
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}, Keys: keys})
	return agent.NewProviders(agent.ProviderOptions{Catalog: store, Dial: protocol.Dialer(protocol.Options{}), Auth: accounts.For}, overrides...)
}

// requestRecorder is a provider whose models record the requests they get
// and stream nothing.
type requestRecorder struct {
	got *[]agent.Request
}

func (p requestRecorder) Name() string { return string(claude.ProviderID) }

func (p requestRecorder) Model(string) agent.Model { return p }

func (p requestRecorder) Stream(_ context.Context, req agent.Request) (agent.StreamResponse, error) {
	*p.got = append(*p.got, req)
	return func(func(agent.StreamPart, error) bool) {}, nil
}

// TestWithProvider checks that Providers with a provider put in place of
// one send their requests to the new provider, and that the Providers they
// came from keep sending them to the old one.
func TestWithProvider(t *testing.T) {
	var old, replaced []agent.Request
	providers := newProviders(t, `{}`, nil, requestRecorder{got: &old})
	with := providers.WithProvider(requestRecorder{got: &replaced})
	v := agent.Variant{Provider: claude.ProviderID, Model: "model"}
	for _, p := range []*agent.Providers{with, providers} {
		if _, err := p.Stream(t.Context(), v, agent.Request{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(replaced) != 1 || len(old) != 1 {
		t.Errorf("the new provider got %d requests and the old %d, want one each", len(replaced), len(old))
	}
}

// TestStreamMaxTokens checks that a request without MaxTokens gets the
// model's output limit from the catalog, and that a set limit or a model the
// catalog does not list leaves it as it is.
func TestStreamMaxTokens(t *testing.T) {
	doc := `{"anthropic":{"id":"anthropic","models":{"claude-x":{"id":"claude-x","limit":{"context":200000,"output":64000}}}}}`
	tests := []struct {
		name  string
		model string
		set   int
		want  int
	}{
		{name: "from the catalog", model: "anthropic/claude-x", want: 64000},
		{name: "set by the caller", model: "anthropic/claude-x", set: 100, want: 100},
		{name: "model not in the catalog", model: "claude-new", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []agent.Request
			ps := newProviders(t, doc, nil, requestRecorder{got: &got})
			if _, err := ps.Stream(t.Context(), agent.Variant{Provider: claude.ProviderID, Model: tt.model}, agent.Request{MaxTokens: tt.set}); err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if len(got) != 1 || got[0].MaxTokens != tt.want {
				t.Errorf("requests = %+v, want one with MaxTokens %d", got, tt.want)
			}
		})
	}
}

// TestStreamReplacesRejectedToken sends requests through the catalog's
// providers, at a configured base URL, and checks that a token the API
// rejects is replaced and retried once.
func TestStreamReplacesRejectedToken(t *testing.T) {
	providers := []agent.ProviderID{claude.ProviderID, chatgpt.ProviderID}
	tests := []struct {
		name          string
		key           agent.APIKey // saved API key; empty to log in instead
		accept        string       // credential the API accepts
		wantSent      []string
		wantRefreshes int32
		wantErr       bool
	}{
		{name: "replaces a rejected token", accept: "new", wantSent: []string{"old", "new"}, wantRefreshes: 1},
		{name: "gives up when the replacement is rejected", accept: "none", wantSent: []string{"old", "new"}, wantRefreshes: 1, wantErr: true},
		{name: "accepted token", accept: "old", wantSent: []string{"old"}},
		{name: "does not replace a rejected API key", key: "sk", accept: "none", wantSent: []string{"sk"}, wantErr: true},
	}
	for _, pt := range providers {
		for _, tt := range tests {
			t.Run(string(pt)+"/"+tt.name, func(t *testing.T) {
				api := newAPIServer(t, tt.accept)
				flow := &fakeFlow{}
				store, dir := gatewaytest.StoreAPI(t, `{"anthropic":{"id":"anthropic","models":{}},"openai":{"id":"openai","models":{}}}`)
				accounts := agent.NewAccounts(agent.AccountsConfig{
					Store: agent.AuthFile{Dir: dir},
					Flows: map[agent.ProviderID]agent.LoginFlow{pt: flow},
				})
				ps := agent.NewProviders(agent.ProviderOptions{
					Catalog:  store,
					Dial:     protocol.Dialer(protocol.Options{}),
					Auth:     accounts.For,
					Settings: func(agent.ProviderID) agent.Settings { return agent.Settings{BaseURL: api.URL + "/v1"} },
				})

				var err error
				if tt.key != "" {
					err = accounts.SetKey(t.Context(), pt, agent.NewAccountID(), tt.key)
				} else {
					err = accounts.Login(t.Context(), pt, agent.NewAccountID(), agent.LoginBrowser, func(agent.LoginPrompt) {})
				}
				if err != nil {
					t.Fatal(err)
				}

				stream, err := ps.Stream(t.Context(), agent.Variant{Provider: pt, Model: "model"}, agent.Request{})
				if err == nil {
					for range stream {
					}
				}
				if (err != nil) != tt.wantErr {
					t.Fatalf("Stream error = %v, want error %v", err, tt.wantErr)
				}
				if tt.wantErr && !agent.IsUnauthorized(err) {
					t.Errorf("Stream error = %v, want 401 Unauthorized", err)
				}
				if got := api.credentials(); !slices.Equal(got, tt.wantSent) {
					t.Errorf("API received %q, want %q", got, tt.wantSent)
				}
				if n := flow.refreshes.Load(); n != tt.wantRefreshes {
					t.Errorf("refreshed %d times, want %d", n, tt.wantRefreshes)
				}
			})
		}
	}
}

// TestStreamVariant checks that Stream sets the reasoning of the variant's
// level and the body and headers of its mode, and that a caller's own
// settings win.
func TestStreamVariant(t *testing.T) {
	tests := []struct {
		name        string
		variant     agent.Variant
		req         agent.Request
		wantNil     bool
		wantLevel   agent.ReasoningEffort
		wantBody    map[string]any
		wantHeaders map[string]string
		wantErr     error
	}{
		{
			name:      "level",
			variant:   agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-opus-4-8", Effort: "xhigh"},
			wantLevel: "xhigh",
		},
		{
			name:        "fast tier",
			variant:     agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-opus-4-8", Option: "fast"},
			wantBody:    map[string]any{"speed": "fast"},
			wantHeaders: map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
		},
		{
			name:        "caller's reasoning and body win",
			variant:     agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-opus-4-8", Option: "fast", Effort: "max"},
			req:         agent.Request{Reasoning: &agent.Reasoning{Effort: "low"}, Body: map[string]any{"speed": "slow"}},
			wantLevel:   "low",
			wantBody:    map[string]any{"speed": "slow"},
			wantHeaders: map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
		},
		{
			name:    "variant the catalog lacks",
			variant: agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-sonnet-5", Option: "fast"},
			wantErr: agent.ErrVariantNotFound,
		},
		{
			name:    "model not in the catalog",
			variant: agent.Variant{Provider: claude.ProviderID, Model: "claude-new", Effort: "high"},
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []agent.Request
			ps := newProviders(t, variantDoc, nil)
			if tt.wantErr == nil {
				ps = newProviders(t, variantDoc, nil, requestRecorder{got: &got})
			}
			_, err := ps.Stream(t.Context(), tt.variant, tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Stream error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			req := got[0]
			switch r := req.Reasoning; {
			case r == nil || tt.wantNil:
				if (r == nil) != tt.wantNil {
					t.Errorf("Reasoning = %+v, want nil %v", r, tt.wantNil)
				}
			case r.Effort != tt.wantLevel:
				t.Errorf("level = %q, want %q", r.Effort, tt.wantLevel)
			}
			if len(req.Body)+len(tt.wantBody) > 0 && !reflect.DeepEqual(req.Body, tt.wantBody) {
				t.Errorf("Body = %v, want %v", req.Body, tt.wantBody)
			}
			if len(req.Headers)+len(tt.wantHeaders) > 0 && !reflect.DeepEqual(req.Headers, tt.wantHeaders) {
				t.Errorf("Headers = %v, want %v", req.Headers, tt.wantHeaders)
			}
		})
	}
}

// TestStreamDropsOthersReasoning checks that the reasoning of messages
// another model wrote does not reach the model, while its own and that of
// messages whose writer is not known do.
func TestStreamDropsOthersReasoning(t *testing.T) {
	reasoned := func(pt agent.ProviderID, model, text string) agent.Message {
		return agent.Message{
			Role:     message.RoleAgent,
			Provider: pt,
			Model:    model,
			Content:  []message.ContentPart{message.ReasoningPart{Text: "thought"}, message.TextPart{Text: text}},
		}
	}
	history := []agent.Message{
		reasoned(chatgpt.ProviderID, "openai/gpt-5.6", "from openai"),
		reasoned(claude.ProviderID, "anthropic/claude-sonnet-5", "from another claude"),
		reasoned(claude.ProviderID, "anthropic/claude-opus-4-8", "own"),
		reasoned("", "", "unknown"),
	}
	var got []agent.Request
	ps := newProviders(t, variantDoc, nil, requestRecorder{got: &got})
	v := agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-opus-4-8", Option: "fast"}
	if _, err := ps.Stream(t.Context(), v, agent.Request{Messages: history}); err != nil {
		t.Fatal(err)
	}
	var kept []int
	for _, m := range got[0].Messages {
		kept = append(kept, len(m.Content))
	}
	if want := []int{1, 1, 2, 2}; !slices.Equal(kept, want) {
		t.Errorf("parts per message = %v, want %v", kept, want)
	}
	if len(history[0].Content) != 2 {
		t.Errorf("Stream changed the caller's history: %+v", history[0])
	}
}

// TestServiceDefaultReasoningEffort checks that the default effort of a
// model is the catalog's for its offering, as the rules give Claude Opus 4.8
// high; or else medium, whatever its lab or provider: of Claude Opus 5.5 and
// a Claude model the rules do not know, which the catalog gives none, of a
// gateway's model, and of a model the catalog does not list.
func TestServiceDefaultReasoningEffort(t *testing.T) {
	var got []agent.Request
	efforts := `"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]`
	ps := newProviders(t, `{"anthropic":{"id":"anthropic","models":{
  "claude-opus-4-8":{"id":"claude-opus-4-8",`+efforts+`},
  "claude-opus-5-5":{"id":"claude-opus-5-5",`+efforts+`},
  "claude-next":{"id":"claude-next",`+efforts+`}}}}`, nil, requestRecorder{got: &got})
	for model, want := range map[string]agent.ReasoningEffort{
		"anthropic/claude-opus-4-8": "high", "anthropic/claude-opus-5-5": "medium", "anthropic/claude-next": "medium",
	} {
		if d := ps.DefaultReasoningEffort(agent.Variant{Provider: claude.ProviderID, Model: model}); d != want {
			t.Errorf("default of %s = %q, want %q", model, d, want)
		}
	}

	ps = newProviders(t, variantDoc, nil)
	for _, v := range []agent.Variant{{Provider: "router", Model: "openai/gpt-5.6"}, {Provider: "nope", Model: "x"}} {
		if d := ps.DefaultReasoningEffort(v); d != agent.FallbackEffort {
			t.Errorf("default of %s = %q, want medium", v, d)
		}
	}
}

// gatewayDoc is an api.json of providers of every kind of endpoint: two of
// the formats flow speaks, at base URLs of their own, two it does not, one
// of them at a base URL that names an environment variable, and one it
// speaks whose base URL names one.
const gatewayDoc = `{
"anthropic":{"id":"anthropic","models":{"claude-x":{"id":"claude-x","limit":{"context":200000,"output":64000}}}},
"gw":{"id":"gw","npm":"@ai-sdk/openai-compatible","api":"BASE","models":{
  "lab/m":{"id":"lab/m","limit":{"context":100000,"output":8000},"cost":{"input":1,"output":2},
    "experimental":{"modes":{"fast":{"cost":{"input":2,"output":4},"provider":{"body":{"service_tier":"priority"}}}}}},
  "lab/m-fast":{"id":"lab/m-fast","limit":{"context":100000,"output":8000}}}},
"gitlab":{"id":"gitlab","npm":"@gitlab/gitlab-ai-provider","models":{"duo":{"id":"duo","limit":{"context":1,"output":1}}}},
"sap":{"id":"sap","npm":"@jerome-benoit/sap-ai-provider-v2","api":"https://${SAP_HOST}/v2","env":["SAP_HOST"],
  "models":{"m":{"id":"m","limit":{"context":1,"output":1}}}},
"azure":{"id":"azure","npm":"@ai-sdk/azure","env":["AZURE_RESOURCE_NAME","AZURE_API_KEY"],
  "models":{"gpt-x":{"id":"gpt-x","limit":{"context":1,"output":1}}}}
}`

// TestServiceProviders checks that the providers of the catalog whose
// endpoints flow speaks are registered, and not those it does not, or the
// ones the configuration disables.
func TestServiceProviders(t *testing.T) {
	store, dir := gatewaytest.StoreAPI(t, strings.ReplaceAll(gatewayDoc, "BASE", "https://gw.invalid/v1"))
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}})
	ps := agent.NewProviders(agent.ProviderOptions{
		Catalog:  store,
		Dial:     protocol.Dialer(protocol.Options{}),
		Auth:     accounts.For,
		Settings: func(t agent.ProviderID) agent.Settings { return agent.Settings{Disabled: t == "azure"} },
	})
	if got, want := ps.Providers(), []agent.ProviderID{"anthropic", "gw"}; !slices.Equal(got, want) {
		t.Errorf("Providers = %v, want %v: not the formats of GitLab and SAP, not the disabled azure", got, want)
	}
	listed, err := ps.ListModels(t.Context(), "gw")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range listed {
		for _, v := range l.Variants("gw") {
			ids = append(ids, v.String())
		}
	}
	// gw's models are its lab's, gw/lab/m with its fast tier by request
	// fields, and gw/lab/m-fast, a model of its own in this catalog.
	if want := []string{"gw/lab/m", "gw/lab/m:fast", "gw/lab/m-fast"}; !slices.Equal(ids, want) {
		t.Errorf("gw variants = %q, want %q", ids, want)
	}
	if _, err := ps.ListModels(t.Context(), "gitlab"); !errors.Is(err, agent.ErrProviderNotRegistered) {
		t.Errorf("ListModels(gitlab) error = %v, want ErrProviderNotRegistered", err)
	}
}

// TestStreamEndpoint sends a request to a gateway as the catalog resolves
// its variant: to the gateway's base URL, in its format, with its key,
// under the provider's model ID of the variant, with the variant's fields.
func TestStreamEndpoint(t *testing.T) {
	var got struct {
		path, auth string
		body       map[string]any
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(api.Close)
	ps := newProviders(t, strings.ReplaceAll(gatewayDoc, "BASE", api.URL+"/v1"),
		func(agent.ProviderID) (agent.APIKey, error) { return "sk-gw", nil })

	v := agent.ParseVariant("gw/lab/m:fast", ps.Catalog())
	stream, err := ps.Stream(t.Context(), v, agent.Request{Messages: []agent.Message{agent.NewTextMessage(message.RoleUser, "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	if got.path != "/v1/chat/completions" || got.auth != "Bearer sk-gw" || got.body["model"] != "lab/m" ||
		got.body["service_tier"] != "priority" {
		t.Errorf("request to %s, auth %q, model %v, tier %v; want /v1/chat/completions, the key, lab/m, priority",
			got.path, got.auth, got.body["model"], got.body["service_tier"])
	}
	if got.body["max_completion_tokens"] != float64(8000) {
		t.Errorf("max_completion_tokens = %v, want the catalog's 8000", got.body["max_completion_tokens"])
	}
}

// TestStreamUnsupported checks that a provider whose format the catalog does
// not know, which the catalog leaves out, and a base URL whose variable is
// not set, fail before any request: the one as no provider, whatever
// variables its base URL lacks.
func TestStreamUnsupported(t *testing.T) {
	ps := newProviders(t, strings.ReplaceAll(gatewayDoc, "BASE", "https://gw.invalid/v1"), nil)
	cat := ps.Catalog()
	// The catalog leaves out providers whose format it does not know.
	if _, err := ps.Stream(t.Context(), agent.ParseVariant("gitlab/duo", cat), agent.Request{}); !errors.Is(err, agent.ErrProviderNotRegistered) {
		t.Errorf("gitlab error = %v, want ErrProviderNotRegistered", err)
	}
	t.Setenv("SAP_HOST", "")
	if _, err := ps.Stream(t.Context(), agent.ParseVariant("sap/m", cat), agent.Request{}); !errors.Is(err, agent.ErrProviderNotRegistered) {
		t.Errorf("sap error = %v, want ErrProviderNotRegistered", err)
	}
	t.Setenv("AZURE_RESOURCE_NAME", "")
	if _, err := ps.Stream(t.Context(), agent.ParseVariant("azure/gpt-x", cat), agent.Request{}); !errors.Is(err, agent.ErrMissingVariable) ||
		!strings.Contains(err.Error(), "AZURE_RESOURCE_NAME") {
		t.Errorf("azure error = %v, want ErrMissingVariable naming AZURE_RESOURCE_NAME", err)
	}
	ep, err := ps.Endpoint("azure", gateway.Endpoint{API: "https://${R}.example/${R}/v1"})
	if err == nil || ep.API != "https://${R}.example/${R}/v1" {
		t.Errorf("Endpoint = %+v, %v; want ErrMissingVariable", ep, err)
	}
}

// mixedDoc is an api.json of a gateway that takes OpenAI Chat requests,
// Anthropic Messages requests for one model at another path, and requests
// of a format flow does not speak for another.
const mixedDoc = `{"mx":{"id":"mx","npm":"@ai-sdk/openai-compatible","api":"https://mx.invalid/openai/v1","env":["MX_KEY"],"models":{
  "lab/chat":{"id":"lab/chat","limit":{"context":1000,"output":100}},
  "lab/claude":{"id":"lab/claude","limit":{"context":1000,"output":100},"provider":{"npm":"@ai-sdk/anthropic","api":"https://mx.invalid/anthropic/v1"}},
  "lab/odd":{"id":"lab/odd","limit":{"context":1000,"output":100},"provider":{"npm":"odd-ai-sdk-provider","api":"https://mx.invalid/odd"}}}}}`

// TestListModelsCallable checks that a provider's listing leaves out the
// models whose endpoint takes a format flow does not speak, and that
// sending one fails as such.
func TestListModelsCallable(t *testing.T) {
	ps := newProviders(t, mixedDoc, func(agent.ProviderID) (agent.APIKey, error) { return "sk", nil })
	listed, err := ps.ListModels(t.Context(), "mx")
	if err != nil {
		t.Fatal(err)
	}
	var models []string
	for _, l := range listed {
		models = append(models, l.Model)
	}
	if want := []string{"mx/lab/chat", "mx/lab/claude"}; !slices.Equal(models, want) {
		t.Errorf("listed %v, want %v: not lab/odd, whose format the catalog does not know", models, want)
	}
	if _, ok := agent.Lookup(ps.Catalog(), agent.ParseVariant("mx/lab/odd", ps.Catalog())); ok {
		t.Error("the catalog lists lab/odd, whose format it does not know")
	}
}

// TestStreamBaseURL checks where a configured base URL takes a provider's
// requests: a host alone keeps each endpoint's path, and a base URL with a
// path replaces the provider's own endpoint and keeps an entry's own path.
func TestStreamBaseURL(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\nevent: message_stop\ndata: {}\n\n")
	}))
	t.Cleanup(srv.Close)
	store, dir := gatewaytest.StoreAPI(t, mixedDoc)
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}, Keys: func(agent.ProviderID) (agent.APIKey, error) { return "sk", nil }})
	for _, tt := range []struct {
		base string
		want []string
	}{
		{srv.URL, []string{"/openai/v1/chat/completions", "/anthropic/v1/messages"}},
		{srv.URL + "/custom/v1", []string{"/custom/v1/chat/completions", "/anthropic/v1/messages"}},
	} {
		paths = nil
		ps := agent.NewProviders(agent.ProviderOptions{
			Catalog:  store,
			Dial:     protocol.Dialer(protocol.Options{}),
			Auth:     accounts.For,
			Settings: func(agent.ProviderID) agent.Settings { return agent.Settings{BaseURL: tt.base} },
		})
		for _, model := range []string{"mx/lab/chat", "mx/lab/claude"} {
			stream, err := ps.Stream(t.Context(), agent.ParseVariant(model, ps.Catalog()), agent.Request{Messages: []agent.Message{agent.NewUserMessage("hi")}})
			if err != nil {
				t.Fatal(err)
			}
			for range stream {
			}
		}
		if !slices.Equal(paths, tt.want) {
			t.Errorf("base URL %s: requests to %v, want %v", tt.base, paths, tt.want)
		}
	}
}
