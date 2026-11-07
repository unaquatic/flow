package agent_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/flow/internal/catalog"
)

// TestOverlayProvider checks that a provider a models file adds, which the
// catalog does not list, is registered, lists its models, and takes their
// requests at its endpoint, under the model ID the file spells, with the
// key its endpoint's variables hold, and the headers the file sets.
func TestOverlayProvider(t *testing.T) {
	type sent struct {
		path, auth, team, model string
	}
	var (
		mu  sync.Mutex
		got []sent
	)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, sent{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Team"), body.Model})
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(api.Close)

	store, dir := protocoltest.Store(t, `{"provider-b":{"id":"provider-b","format":"messages","models":{}}}`)
	overlay := catalog.NewOverlay(dir)
	doc := `{"providers": {"local": {
		"endpoint": {"format": "` + string(gateway.FormatOpenAIChat) + `", "api": "` + api.URL + `/v1", "auth": {"scheme": "bearer"}, "env": ["LOCAL_KEY"]},
		"unlisted": {"Model-X:Large": {
			"limit": {"context": 131072, "output": 16384},
			"provider_options": {"headers": {"X-Team": "infra"}}
		}}
	}}}`
	if err := os.WriteFile(overlay.Path(), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCAL_KEY", "sk-local")
	keys := func(p agent.ProviderID) (agent.APIKey, error) {
		for _, name := range overlay.Apply(store.Get()).Providers[p].Endpoint.KeyEnv() {
			if v := os.Getenv(name); v != "" {
				return agent.APIKey(v), nil
			}
		}
		return "", nil
	}
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}, Keys: keys})
	ps := agent.NewProviders(agent.ProviderOptions{
		Catalog: store,
		Overlay: overlay.Apply,
		Dial:    protocol.Dialer(protocol.Options{}),
		Auth:    accounts.For,
	})

	if ids := ps.Providers(); !slices.Contains(ids, "local") || !slices.Contains(ids, "provider-b") {
		t.Errorf("Providers() = %v, want local beside the catalog's", ids)
	}
	listed, err := ps.ListModels(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Model != "model-x:large" || !listed[0].Unlisted {
		t.Fatalf("ListModels(local) = %+v, want model-x:large, unlisted", listed)
	}

	v := agent.ParseVariant("local/Model-X:Large", ps.Catalog())
	if v.Provider != "local" {
		t.Fatalf("ParseVariant = %+v", v)
	}
	stream, err := ps.Stream(t.Context(), v, agent.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	want := sent{"/v1/chat/completions", "Bearer sk-local", "infra", "Model-X:Large"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("API received %+v, want %+v", got, want)
	}
	if off, ok := ps.Offering(v); !ok || off.Limit.Output != 16384 || !strings.EqualFold(off.Name, "Model-X:Large") {
		t.Errorf("Offering = %+v, %v", off, ok)
	}
}
