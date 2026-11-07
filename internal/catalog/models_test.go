package catalog

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
)

// baseCatalog returns a catalog of lab-a, which makes and serves model-a,
// a reasoning model, and of gw, a gateway that serves it too.
func baseCatalog() *gateway.Catalog {
	efforts := &gateway.Reasoning{Efforts: []gateway.Effort{"none", "low", "high"}, DefaultEffort: "high"}
	model := gateway.Model{
		ID: "lab-a/model-a", Lab: "lab-a", Name: "Model A", Reasoning: true,
		Capabilities: gateway.Capabilities{Tools: true, Input: []gateway.Modality{"text"}, Output: []gateway.Modality{"text"}},
		Limit:        gateway.Limit{Context: 200000, Output: 32000},
		Providers:    []gateway.ProviderID{"gw", "lab-a"},
	}
	offering := func(id string) gateway.Offering {
		return gateway.Offering{Name: "Model A", Capabilities: model.Capabilities, Limit: model.Limit, Reasoning: efforts,
			ProviderOptions: gateway.ProviderOptions{ID: id}}
	}
	return &gateway.Catalog{
		Version: gateway.Version,
		Models:  map[gateway.ModelID]gateway.Model{model.ID: model},
		Providers: map[gateway.ProviderID]gateway.Provider{
			"lab-a": {ID: "lab-a", Name: "Lab A", Kind: gateway.KindLab, Type: gateway.ProviderOfficial,
				Endpoint: gateway.Endpoint{Format: gateway.FormatAnthropicMessages, API: "https://api.lab-a.example/v1",
					Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-api-key"}, Env: []string{"LAB_A_API_KEY"}},
				Models: map[gateway.ModelID]gateway.Offering{model.ID: offering("model-a")}},
			"gw": {ID: "gw", Name: "Gateway", Kind: gateway.KindGateway, Type: gateway.ProviderCommunity,
				Endpoint: gateway.Endpoint{Format: gateway.FormatOpenAIChat, API: "https://gw.example/v1", Auth: gateway.Auth{Scheme: gateway.AuthBearer}},
				Models:   map[gateway.ModelID]gateway.Offering{model.ID: offering("lab-a/model-a")}},
		},
	}
}

// chat returns doc with each CHAT in it replaced by the chat completions
// format's ID.
func chat(doc string) string {
	return strings.ReplaceAll(doc, "CHAT", string(gateway.FormatOpenAIChat))
}

// TestMergeNewProvider checks that a provider the catalog does not list is
// added, as a community gateway named by its ID, with its unlisted models
// sending their IDs as the file spells them and its offerings of the
// catalog's models starting from the model and its lab's reasoning, and
// that the catalog lists it among the providers of the models it offers.
func TestMergeNewProvider(t *testing.T) {
	base := baseCatalog()
	c, err := Merge(base, []byte(chat(`{
		"$schema": "https://schema.example/models.json",
		"providers": {
			"local": {
				"endpoint": {"format": "CHAT", "api": "http://local.example/v1", "auth": {"scheme": "bearer"}, "env": ["LOCAL_KEY"]},
				"unlisted": {"Model-X:Large": {"limit": {"context": 131072, "output": 16384}, "capabilities": {"tools": true}}},
				"models": {"lab-a/model-a": {"limit": {"output": 8000}}}
			}
		}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.Providers["local"]
	if !ok {
		t.Fatal("local not added")
	}
	if p.ID != "local" || p.Name != "local" || p.Kind != gateway.KindGateway || p.Type != gateway.ProviderCommunity {
		t.Errorf("local = %s %q %s %s, want a community gateway named local", p.ID, p.Name, p.Kind, p.Type)
	}
	if p.Endpoint.Format != gateway.FormatOpenAIChat || p.Endpoint.API != "http://local.example/v1" || !slices.Equal(p.Endpoint.KeyEnv(), []string{"LOCAL_KEY"}) {
		t.Errorf("endpoint = %+v", p.Endpoint)
	}

	u, ok := p.Unlisted["model-x:large"]
	if !ok {
		t.Fatalf("unlisted = %v, want model-x:large in lower case", p.Unlisted)
	}
	if u.Name != "Model-X:Large" || u.ProviderOptions.ID != "Model-X:Large" || u.Limit.Context != 131072 || !u.Capabilities.Tools {
		t.Errorf("unlisted offering = %+v", u)
	}
	if r, ok := c.Resolve("local", "model-x:large"); !ok || r.ProviderOptions.ID != "Model-X:Large" || r.ProviderOptions.Endpoint.API != p.Endpoint.API {
		t.Errorf("Resolve(local, model-x:large) = %+v, %v", r, ok)
	}

	o := p.Models["lab-a/model-a"]
	if o.Name != "Model A" || o.ProviderOptions.ID != "model-a" || o.Limit != (gateway.Limit{Context: 200000, Output: 8000}) {
		t.Errorf("offering of lab-a/model-a = %+v, want the model's, sending model-a, with output 8000", o)
	}
	if o.Reasoning == nil || o.Reasoning.DefaultEffort != "high" || len(o.Reasoning.Efforts) != 3 {
		t.Errorf("reasoning = %+v, want lab-a's", o.Reasoning)
	}
	if got := c.Models["lab-a/model-a"].Providers; !slices.Equal(got, []gateway.ProviderID{"gw", "lab-a", "local"}) {
		t.Errorf("providers of lab-a/model-a = %v", got)
	}

	// The catalog merged into is left as it was.
	if _, ok := base.Providers["local"]; ok || len(base.Models["lab-a/model-a"].Providers) != 2 {
		t.Error("Merge changed the catalog it merged into")
	}
}

// TestMergeCatalogProvider checks that a change to a provider the catalog
// lists merges into it: objects key by key, other values in place of the
// catalog's, null removing what it names, and the offerings it does not
// name left as they are.
func TestMergeCatalogProvider(t *testing.T) {
	base := baseCatalog()
	c, err := Merge(base, []byte(`{"providers": {
		"lab-a": {
			"name": "Lab A (proxy)",
			"endpoint": {"api": "https://proxy.example/v1"},
			"models": {"lab-a/model-a": {
				"limit": {"context": 1000000},
				"reasoning": {"default_effort": "low"},
				"provider_options": {"headers": {"x-team": "infra"}, "body": {"metadata": {"user": "me"}}}
			}}
		},
		"gw": {"models": {"lab-a/model-a": null}}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers["lab-a"]
	if p.Name != "Lab A (proxy)" || p.Kind != gateway.KindLab {
		t.Errorf("lab-a = %q %s, want the new name, still a lab", p.Name, p.Kind)
	}
	if p.Endpoint.API != "https://proxy.example/v1" || p.Endpoint.Format != gateway.FormatAnthropicMessages || p.Endpoint.Auth.Header != "x-api-key" {
		t.Errorf("endpoint = %+v, want the catalog's at the proxy", p.Endpoint)
	}
	o := p.Models["lab-a/model-a"]
	if o.Limit != (gateway.Limit{Context: 1000000, Output: 32000}) {
		t.Errorf("limit = %+v", o.Limit)
	}
	if o.Reasoning.DefaultEffort != "low" || len(o.Reasoning.Efforts) != 3 {
		t.Errorf("reasoning = %+v, want the catalog's efforts at default low", o.Reasoning)
	}
	r, _ := c.Resolve("lab-a", "lab-a/model-a")
	if r.ProviderOptions.ID != "model-a" || r.ProviderOptions.Headers["x-team"] != "infra" || r.ProviderOptions.Body["metadata"] == nil {
		t.Errorf("resolved = %+v", r.ProviderOptions)
	}

	if _, ok := c.Providers["gw"].Models["lab-a/model-a"]; ok {
		t.Error("gw still offers lab-a/model-a")
	}
	if got := c.Models["lab-a/model-a"].Providers; !slices.Equal(got, []gateway.ProviderID{"lab-a"}) {
		t.Errorf("providers of lab-a/model-a = %v, want lab-a alone", got)
	}
	if base.Providers["lab-a"].Models["lab-a/model-a"].Limit.Context != 200000 || len(base.Providers["gw"].Models) != 1 {
		t.Error("Merge changed the catalog it merged into")
	}
}

// TestMergeRemoveProvider checks that null removes a provider, and it from
// the providers of the models it offered.
func TestMergeRemoveProvider(t *testing.T) {
	c, err := Merge(baseCatalog(), []byte(`{"providers": {"gw": null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Providers["gw"]; ok {
		t.Error("gw not removed")
	}
	if got := c.Models["lab-a/model-a"].Providers; !slices.Equal(got, []gateway.ProviderID{"lab-a"}) {
		t.Errorf("providers of lab-a/model-a = %v", got)
	}
}

// TestMergeErrors checks that a file Merge cannot apply fails, naming the
// key at fault.
func TestMergeErrors(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"not JSON", `{`, "unexpected EOF"},
		{"unknown top-level key", `{"provider": {}}`, `unknown field "provider"`},
		{"provider not an object", `{"providers": {"x": 1}}`, "providers.x: not an object"},
		{"new provider without endpoint", `{"providers": {"x": {"unlisted": {}}}}`, "providers.x.endpoint.format"},
		{"unknown format", `{"providers": {"x": {"endpoint": {"format": "soap", "api": "http://h"}}}}`, `providers.x.endpoint.format: "soap"`},
		{"no base URL", chat(`{"providers": {"x": {"endpoint": {"format": "CHAT"}}}}`), "providers.x.endpoint.api: not set"},
		{"unknown provider key", `{"providers": {"gw": {"base_url": "http://h"}}}`, `providers.gw: json: unknown field "base_url"`},
		{"unknown offering key", `{"providers": {"gw": {"unlisted": {"m": {"context_window": 1}}}}}`, `providers.gw.unlisted.m: json: unknown field "context_window"`},
		{"model not in the catalog", `{"providers": {"gw": {"models": {"lab-z/m": {}}}}}`, "providers.gw.models.lab-z/m: not a model of the catalog"},
		{"wrong type", `{"providers": {"gw": {"models": {"lab-a/model-a": {"limit": {"context": "big"}}}}}}`, "providers.gw.models.lab-a/model-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Merge(baseCatalog(), []byte(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Merge error = %v, want one containing %q", err, tt.want)
			}
		})
	}
	_, err := Merge(baseCatalog(), []byte(chat(`{"providers": {"x": {"endpoint": {"format": "CHAT"}}}}`)))
	if pe, ok := errors.AsType[*PathError](err); !ok || pe.Path != "providers.x.endpoint.api" {
		t.Errorf("Merge error = %#v, want a PathError at providers.x.endpoint.api", err)
	}
}

// TestOverlayApply checks that Apply leaves the catalog as it is without a
// models file or with an empty one, applies the file, returns the same
// catalog until the file or the catalog changes, and, for a version of the
// file it cannot apply, logs it once and keeps the last version that
// merged, even over a new catalog.
func TestOverlayApply(t *testing.T) {
	dir := t.TempDir()
	m := NewOverlay(dir)
	base := baseCatalog()
	if got := m.Apply(base); got != base {
		t.Error("Apply without a file changed the catalog")
	}
	now := time.Now()
	write := func(doc string, after time.Duration) {
		t.Helper()
		if err := os.WriteFile(m.Path(), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(m.Path(), now.Add(after), now.Add(after)); err != nil {
			t.Fatal(err)
		}
	}
	write("", 0)
	if got := m.Apply(base); got != base {
		t.Error("Apply with an empty file changed the catalog")
	}

	write(`{"providers": {"gw": {"name": "One"}}}`, time.Second)
	first := m.Apply(base)
	if first.Providers["gw"].Name != "One" {
		t.Fatalf("gw = %q, want One", first.Providers["gw"].Name)
	}
	if m.Apply(base) != first {
		t.Error("Apply made a new catalog of the same file and catalog")
	}
	other := baseCatalog()
	if got := m.Apply(other); got == first || got.Providers["gw"].Name != "One" {
		t.Error("Apply to another catalog returned the first catalog's")
	}

	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	write(`{"providers": {"gw": {"nope": 1}}}`, 2*time.Second)
	for _, c := range []*gateway.Catalog{base, base, baseCatalog()} {
		if got := m.Apply(c); got.Providers["gw"].Name != "One" {
			t.Errorf("Apply of a broken file: gw = %q, want One, of the last version that merged", got.Providers["gw"].Name)
		}
	}
	if n := strings.Count(logs.String(), "models file not applied"); n != 2 {
		t.Errorf("logged %d times, want once for each catalog:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), `unknown field \"nope\"`) {
		t.Errorf("log does not say what is wrong:\n%s", logs.String())
	}

	write(`{"providers": {"gw": {"name": "Two"}}}`, 3*time.Second)
	if got := m.Apply(base); got.Providers["gw"].Name != "Two" {
		t.Errorf("Apply once the file is fixed: gw = %q, want Two", got.Providers["gw"].Name)
	}
}

// TestOverlayLoad checks that Load fails on a models file it cannot merge,
// naming the file and the key at fault, and on one it cannot read, and
// applies a missing file, or one that merges, as Apply does.
func TestOverlayLoad(t *testing.T) {
	m := NewOverlay(t.TempDir())
	base := baseCatalog()
	if got, err := m.Load(base); got != base || err != nil {
		t.Errorf("Load without a file = %p, %v, want the catalog itself", got, err)
	}
	if err := os.WriteFile(m.Path(), []byte(chat(`{"providers": {"x": {"endpoint": {"format": "CHAT"}}}}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := m.Load(base)
	if got != nil || err == nil || !strings.Contains(err.Error(), m.Path()) || !strings.Contains(err.Error(), "providers.x.endpoint.api: not set") {
		t.Errorf("Load of a broken file = %p, %v, want an error naming the file and the key", got, err)
	}
	if _, again := m.Load(base); again == nil {
		t.Error("Load of the same broken file again succeeded")
	}
	if err := os.Remove(m.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(base); err == nil {
		t.Error("Load of a directory in place of the file succeeded")
	}
}

// TestOverlayRemovesLegacyCache checks that Load removes the catalog cache
// that flow once kept in the models file's place, and goes on as without a
// models file, and that it keeps a file that is no such cache.
func TestOverlayRemovesLegacyCache(t *testing.T) {
	m := NewOverlay(t.TempDir())
	base := baseCatalog()
	legacy := `{"etag":"W/\"abc\"","fetched":"2026-01-02T03:04:05Z","checked":"2026-01-02T03:04:05Z","data":{"provider-a":{"id":"provider-a"}}}`
	if err := os.WriteFile(m.Path(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Load(base); got != base || err != nil {
		t.Errorf("Load over the old cache = %p, %v, want the catalog itself", got, err)
	}
	if _, err := os.Stat(m.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old cache not removed: %v", err)
	}

	for _, doc := range []string{
		`{"providers": {}, "data": {}}`,
		`{"etag": "x", "fetched": "2026-01-02T03:04:05Z"}`,
	} {
		if LegacyCache([]byte(doc)) {
			t.Errorf("LegacyCache(%s) = true, want false", doc)
		}
	}
}
