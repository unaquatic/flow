package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension/protocol/messages"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/flow/internal/catalog"
	"github.com/unaquatic/flow/internal/pubsub"
)

// The providers of the tests' catalogs, which speak the formats their
// api.json entries name.
const (
	providerA agent.ProviderID = "provider-a"
	providerB agent.ProviderID = "provider-b"
)

func TestMatchModel(t *testing.T) {
	choices := []ModelChoice{
		{Name: "provider-b/model-b-large-5", Released: "2026-07-24"},
		{Name: "provider-b/model-b-small-4-5", Released: "2025-09-29"},
		{Name: "provider-b/model-b-small-5", Released: "2026-06-29"},
		{Name: "provider-a/model-a-5", Released: "2025-08-07"},
		{Name: "provider-a/model-a-5-mini", Released: "2025-08-07"},
		{Name: "provider-a/model-a-6-nova", Released: "2026-09-04"},
	}
	tests := []struct {
		pattern string
		want    string // "" means no match
	}{
		{pattern: "provider-a/model-a-5", want: "provider-a/model-a-5"},
		{pattern: "model-a-5", want: "provider-a/model-a-5"},
		{pattern: "MODEL-A-5", want: "provider-a/model-a-5"},
		{pattern: "  model-b-large-5 ", want: "provider-b/model-b-large-5"},
		// Every small model matches as well; the newest wins.
		{pattern: "small", want: "provider-b/model-b-small-5"},
		{pattern: "large", want: "provider-b/model-b-large-5"},
		{pattern: "nova", want: "provider-a/model-a-6-nova"},
		{pattern: "model-a-5-m", want: "provider-a/model-a-5-mini"},
		// A pattern can name the provider too.
		{pattern: "provider-b/small", want: "provider-b/model-b-small-5"},
		{pattern: "zzzz"},
		{pattern: ""},
	}
	for _, tt := range tests {
		got, ok := MatchModel(tt.pattern, choices)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("MatchModel(%q) = %q, %v; want %q", tt.pattern, got, ok, tt.want)
		}
	}
}

// TestModelsFindRefreshesOnlyWhenNothingMatches checks that Find matches
// against the cached catalog without a download, and downloads the catalog to
// find a model newer than it, but still reports a pattern nothing matches.
func TestModelsFindRefreshesOnlyWhenNothingMatches(t *testing.T) {
	var hits atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write(providerDoc(t, "model-old", "model-new"))
	}))
	defer src.Close()

	tests := []struct {
		name     string
		pattern  string
		want     string
		wantErr  error
		wantHits int32
	}{
		{name: "in the cached catalog", pattern: "model-old", want: "provider-a/model-old"},
		{name: "newer than the cached catalog", pattern: "model-new", want: "provider-a/model-new", wantHits: 1},
		{name: "nowhere", pattern: "zzzz", wantErr: ErrNoModel, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			dir := t.TempDir()
			gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: providerDoc(t, "model-old")})
			a := newCatalogApp(t, dir, src.URL)

			got, err := a.Models().Find(t.Context(), "", tt.pattern)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("Models.Find(%q) = %q, %v, want %q, %v", tt.pattern, got, err, tt.want, tt.wantErr)
			}
			if n := hits.Load(); n != tt.wantHits {
				t.Errorf("catalog downloaded %d times, want %d", n, tt.wantHits)
			}
		})
	}
}

// providerDoc returns a catalog document, as a catalog server serves it,
// listing the models named for provider-a.
func providerDoc(t *testing.T, models ...string) []byte {
	ms := map[string]any{}
	for _, m := range models {
		ms[m] = map[string]any{"id": m}
	}
	b, _ := json.Marshal(map[string]any{string(providerA): map[string]any{"id": providerA, "format": "responses", "models": ms}})
	return catalogDoc(t, string(b))
}

// catalogDoc returns the catalog document of api, an api.json, as
// protocoltest.FromAPI builds it.
func catalogDoc(t *testing.T, api string) []byte {
	t.Helper()
	b, err := json.Marshal(protocoltest.FromAPI(t, api))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newCatalogApp returns an app over the catalog cached in dir, downloaded
// from src, or from no server when src is empty, with provider-a registered.
// The test ends by closing it.
func newCatalogApp(t *testing.T, dir, src string) *App {
	t.Helper()
	if src == "" {
		src = "http://127.0.0.1:1"
	}
	store := catalog.NewStore(gateway.StoreOptions{Dir: dir, Source: src})
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}})
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		accounts:  accounts,
		providers: agent.NewProviders(agent.ProviderOptions{Catalog: store}, agenttest.Provider{ID: providerA, Gateway: &agenttest.Gateway{}}),
		broker:    pubsub.NewBroker[Event](),
		workerCtx: ctx,
		cancel:    cancel,
	}
	t.Cleanup(a.Close)
	return a
}

// staleCatalog caches a catalog of model-old in a new directory, fetched two
// days ago, past the default TTL, and returns the directory.
func staleCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now().Add(-48 * time.Hour), Data: providerDoc(t, "model-old")})
	return dir
}

// listedIDs returns the model IDs of models.
func listedIDs(models []agent.Listing) []string {
	var out []string
	for _, m := range models {
		out = append(out, m.Model)
	}
	return out
}

// TestModelsListRefreshesInBackground checks that listing a stale catalog
// answers from it at once, starts one refresh in the background, which
// publishes EventModelsRefreshed, and that later listings take the refreshed
// catalog.
func TestModelsListRefreshesInBackground(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		w.Write(providerDoc(t, "model-old", "model-new"))
	}))
	defer src.Close()
	a := newCatalogApp(t, staleCatalog(t), src.URL)
	events := a.Subscribe(t.Context())

	for range 2 {
		models, err := a.Models().List(t.Context(), providerA)
		if err != nil || !slices.Equal(listedIDs(models), []string{"provider-a/model-old"}) {
			t.Fatalf("ListModels while downloading = %v, %v, want [provider-a/model-old]", listedIDs(models), err)
		}
	}
	close(release)
	select {
	case ev := <-events:
		if _, ok := ev.(EventModelsRefreshed); !ok {
			t.Fatalf("event = %T, want EventModelsRefreshed", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no EventModelsRefreshed after the download")
	}

	models, err := a.Models().List(t.Context(), providerA)
	if want := []string{"provider-a/model-new", "provider-a/model-old"}; err != nil || !slices.Equal(listedIDs(models), want) {
		t.Errorf("ListModels after the download = %v, %v, want %v", listedIDs(models), err, want)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("catalog downloaded %d times, want 1", n)
	}
}

// TestCloseStopsModelRefresh checks that Close cancels a background refresh
// waiting on a source that does not answer, which then publishes nothing.
func TestCloseStopsModelRefresh(t *testing.T) {
	asked, cancelled := make(chan struct{}), make(chan struct{})
	src := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(asked)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer src.Close()
	a := newCatalogApp(t, staleCatalog(t), src.URL)
	events := a.Subscribe(t.Context())

	if _, err := a.Models().List(t.Context(), providerA); err != nil {
		t.Fatal(err)
	}
	<-asked
	a.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh kept waiting on the source after Close")
	}
	select {
	case ev := <-events:
		t.Errorf("event after Close = %T, want none", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestModelsDefaultReadsTheConfigFile checks that the default model is the
// one the config file sets at each call, so a change to the file applies at
// once, and that a file it cannot read is an error.
func TestModelsDefaultReadsTheConfigFile(t *testing.T) {
	home := t.TempDir()
	a := &App{config: config.NewService(home)}
	for _, model := range []string{"provider-a/model-x", "provider-b/model-y"} {
		data := []byte(`{"model":"` + model + `"}`)
		if err := os.WriteFile(filepath.Join(home, config.FileName), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := a.Models().Default(); got != model || err != nil {
			t.Errorf("Models.Default after writing %s = %q, %v", model, got, err)
		}
	}

	// A file it cannot read is an error, not the default model.
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Models().Default(); err == nil {
		t.Errorf("Models.Default with an unreadable config file = %q, nil error, want an error", got)
	}
}

// TestModelsFindReportsFailedRefresh checks that when no model matches and the
// catalog cannot be downloaded, Find reports the failed download along
// with ErrNoModel, rather than ErrNoModel alone.
func TestModelsFindReportsFailedRefresh(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer src.Close()
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: providerDoc(t, "model-old")})
	a := newCatalogApp(t, dir, src.URL)

	_, err := a.Models().Find(t.Context(), "", "model-new")
	if !errors.Is(err, ErrNoModel) || !strings.Contains(err.Error(), "could not be refreshed") {
		t.Errorf("Models.Find = %v, want ErrNoModel and the failed refresh", err)
	}
}

// TestModelsStartListingsReportsError checks that when the accounts cannot be
// read, StartListings publishes why, rather than an empty listing.
func TestModelsStartListingsReportsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newCatalogApp(t, dir, "")
	events := a.Subscribe(t.Context())

	a.Models().StartListings(t.Context())
	select {
	case ev := <-events:
		listed, ok := ev.(EventModelsListed)
		if !ok || listed.Error == nil || !strings.Contains(listed.Error.Message, "auth.json") {
			t.Errorf("event = %+v, want EventModelsListed with the auth.json error", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no EventModelsListed")
	}
}

// TestModelsFindDownloadsOnce checks that Find on a stale catalog, with
// a pattern only the source's catalog matches, downloads the catalog once:
// listing the choices starts no background refresh beside its own.
func TestModelsFindDownloadsOnce(t *testing.T) {
	var hits atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.Write(providerDoc(t, "model-old", "model-new"))
	}))
	defer src.Close()
	a := newCatalogApp(t, staleCatalog(t), src.URL)

	if got, err := a.Models().Find(t.Context(), "", "model-new"); got != "provider-a/model-new" || err != nil {
		t.Fatalf("Models.Find = %q, %v, want provider-a/model-new", got, err)
	}
	time.Sleep(100 * time.Millisecond) // time for a stray background download
	if n := hits.Load(); n != 1 {
		t.Errorf("catalog downloaded %d times, want 1", n)
	}
}

// TestModelsRefreshPublishesEveryRefresh checks that a refresh publishes
// EventModelsRefreshed also when the source answers that nothing changed.
func TestModelsRefreshPublishesEveryRefresh(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer src.Close()
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{ETag: `"v1"`, Fetched: time.Now(), Data: providerDoc(t, "model-old")})
	a := newCatalogApp(t, dir, src.URL)
	events := a.Subscribe(t.Context())

	if err := a.Models().Refresh(t.Context()); err != nil {
		t.Fatalf("RefreshModels: %v", err)
	}
	select {
	case ev := <-events:
		if _, ok := ev.(EventModelsRefreshed); !ok {
			t.Errorf("event after an unchanged refresh = %T, want EventModelsRefreshed", ev)
		}
	case <-time.After(5 * time.Second):
		t.Error("no EventModelsRefreshed after an unchanged refresh")
	}
}

// variantDoc is an api.json of a model of provider-a with fast and pro
// modes and effort levels, its pro mode a model of its own, of a model whose ID ends in a mode's name, and of
// a model-c whose effort list ends in xxxhigh, a level flow does not know.
const variantDoc = `{"provider-a":{"id":"provider-a","format":"responses","models":{
  "model-a":{"id":"model-a","release_date":"2026-08-01","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"fast":{},"pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "model-b-pro":{"id":"model-b-pro","release_date":"2026-04-01"},
  "model-c":{"id":"model-c","release_date":"2026-12-01","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max","xxxhigh"]}]}
}}}`

// TestModelsFindVariants checks that a pattern can name a variant of a
// model, by its ID or a fuzzy pattern of it, and keep a reasoning effort,
// which Resolve then reads back as the variant, at the default level
// when it names none and the model takes levels.
func TestModelsFindVariants(t *testing.T) {
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
	a := newCatalogApp(t, dir, "")
	tests := []struct {
		pattern string
		want    string
		variant agent.Variant
	}{
		{"model-a", "provider-a/model-a", agent.Variant{Provider: providerA, Model: "provider-a/model-a", Effort: "medium"}},
		{"model-a:fast", "provider-a/model-a:fast", agent.Variant{Provider: providerA, Model: "provider-a/model-a", Option: "fast", Effort: "medium"}},
		{"a-pro:xhigh", "provider-a/model-a-pro:xhigh", agent.Variant{Provider: providerA, Model: "provider-a/model-a-pro", Effort: "xhigh"}},
		{"model-a:none", "provider-a/model-a:none", agent.Variant{Provider: providerA, Model: "provider-a/model-a", Effort: "none"}},
		{"model-b-pro", "provider-a/model-b-pro", agent.Variant{Provider: providerA, Model: "provider-a/model-b-pro"}},
	}
	for _, tt := range tests {
		got, err := a.Models().Find(t.Context(), "", tt.pattern)
		if got != tt.want || err != nil {
			t.Errorf("Models.Find(%q) = %q, %v, want %q", tt.pattern, got, err, tt.want)
			continue
		}
		if v, err := a.Models().Resolve(got); v != tt.variant || err != nil {
			t.Errorf("Models.Resolve(%q) = %+v, %v, want %+v", got, v, err, tt.variant)
		}
	}
	want := []agent.ReasoningEffort{"none", "low", "medium", "high", "xhigh"}
	if got := a.Models().Efforts(a.Models().ParseVariant("provider-a/model-a:fast")); !slices.Equal(got, want) {
		t.Errorf("Models.Efforts = %v, want %v", got, want)
	}
	if got := a.Models().Efforts(a.Models().ParseVariant("provider-a/model-unknown")); got != nil {
		t.Errorf("Models.Efforts of an unlisted model = %v, want none", got)
	}
}

// TestUnknownEffort checks that a level only the catalog names, xxxhigh,
// works as any other: a pattern keeps it, the model runs at it, and a model
// that does not list it runs at its provider's default.
func TestUnknownEffort(t *testing.T) {
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
	a := newCatalogApp(t, dir, "")

	got, err := a.Models().Find(t.Context(), "", "model-c:XXXHIGH")
	if got != "provider-a/model-c:xxxhigh" || err != nil {
		t.Fatalf("Models.Find = %q, %v, want provider-a/model-c:xxxhigh", got, err)
	}
	want := agent.Variant{Provider: providerA, Model: "provider-a/model-c", Effort: "xxxhigh"}
	if v, err := a.Models().Resolve(got); v != want || err != nil {
		t.Errorf("Models.Resolve = %+v, %v, want %+v", v, err, want)
	}
	levels := []agent.ReasoningEffort{"low", "medium", "high", "xhigh", "max", "xxxhigh"}
	if got := a.Models().Efforts(want); !slices.Equal(got, levels) {
		t.Errorf("Models.Efforts = %q, want %q", got, levels)
	}
	// model-a does not list xxxhigh: the pattern does not name a level of
	// it, and the variant runs at the provider's default.
	if got, err := a.Models().Find(t.Context(), "", "model-a:xxxhigh"); err == nil && got == "provider-a/model-a:xxxhigh" {
		t.Errorf("Models.Find(model-a:xxxhigh) = %q, want no level kept", got)
	}
	if v := a.Models().FitEffort(agent.Variant{Provider: providerA, Model: "provider-a/model-a", Effort: "xxxhigh"}); v.Effort != "medium" {
		t.Errorf("model-a at xxxhigh runs at %q, want medium", v.Effort)
	}
}

// TestModelsSelect checks that Select finds the model a pattern names, or
// takes the default model without one, at the effort given over the one the
// pattern or the default names, at the model's default effort without
// either, and that it rejects an effort the catalog does not list for the
// model.
func TestModelsSelect(t *testing.T) {
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
	a := newCatalogApp(t, dir, "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"model":"provider-a/model-a:fast:low"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.config = config.NewService(home)

	modelA := func(option string, effort agent.ReasoningEffort) agent.Variant {
		return agent.Variant{Provider: providerA, Model: "provider-a/model-a", Option: option, Effort: effort}
	}
	tests := []struct {
		name     string
		provider agent.ProviderID
		pattern  string
		effort   agent.ReasoningEffort
		want     agent.Variant
		wantErr  string
	}{
		{name: "pattern", pattern: "model-a", want: modelA("", "medium")},
		{name: "pattern of a provider", provider: providerA, pattern: "model-a:fast", want: modelA("fast", "medium")},
		{name: "effort over the pattern's", pattern: "model-a:low", effort: "xhigh", want: modelA("", "xhigh")},
		{name: "default model", want: modelA("fast", "low")},
		{name: "effort over the default's", effort: "high", want: modelA("fast", "high")},
		{name: "effort the model does not list", pattern: "model-a", effort: "max",
			wantErr: `provider-a/model-a has no reasoning effort "max"; the catalog lists ["none" "low" "medium" "high" "xhigh"]`},
		{name: "model without efforts", pattern: "model-b-pro", effort: "high",
			wantErr: `provider-a/model-b-pro has no reasoning effort "high"; the catalog lists []`},
		{name: "no model", pattern: "zzzz", wantErr: ErrNoModel.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.Models().Select(t.Context(), tt.provider, tt.pattern, tt.effort)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Models.Select = %+v, %v, want an error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if got != tt.want || err != nil {
				t.Errorf("Models.Select = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

// TestModelsFitEffortTakesTheDefault checks that a model that names no
// effort, and that the catalog gives no default effort, runs at medium.
func TestModelsFitEffortTakesTheDefault(t *testing.T) {
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
	a := newCatalogApp(t, dir, "")
	a.providers = agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: dir})},
		agenttest.Provider{ID: providerA, Gateway: &agenttest.Gateway{}})

	if d := a.Models().DefaultEffort(agent.Variant{Provider: providerA, Model: "provider-a/model-a"}); d != "medium" {
		t.Errorf("Models.DefaultEffort = %q, want medium", d)
	}
	if v, _ := a.Models().Resolve("provider-a/model-a"); v.Effort != "medium" {
		t.Errorf("model-a runs at %q, want medium", v.Effort)
	}
	// A chosen level wins over the default.
	if v, _ := a.Models().Resolve("provider-a/model-a:low"); v.Effort != "low" {
		t.Errorf("model-a:low runs at %q, want low", v.Effort)
	}
	// A model must name its provider: flow defaults to none.
	if _, err := a.Models().Resolve("model-a"); !errors.Is(err, ErrModelFormat) {
		t.Errorf("Resolve of a model without a provider = %v, want ErrModelFormat", err)
	}
}

// TestDefaultEffortPerModel checks that a model that names no effort runs at
// the catalog's default effort for it, high for model-d, or else at medium,
// as model-e, which the catalog gives none.
func TestDefaultEffortPerModel(t *testing.T) {
	const doc = `{"provider-b":{"id":"provider-b","format":"messages","models":{
  "model-d":{"id":"model-d","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]},
  "model-e":{"id":"model-e","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}}}`
	cat := protocoltest.FromAPI(t, doc)
	// An api.json names no default effort; the catalog's rules give one to
	// the models whose labs document it.
	cat.Providers[providerB].Models["provider-b/model-d"].Reasoning.DefaultEffort = "high"
	dir := t.TempDir()
	gatewaytest.WriteCache(t, dir, cat)
	a := newCatalogApp(t, dir, "")
	a.providers = agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: dir})}, messages.New(messages.Init{Name: string(providerB)}))

	for model, want := range map[string]agent.ReasoningEffort{
		"provider-b/model-e": "medium",
		"provider-b/model-d": "high",
	} {
		if v, err := a.Models().Resolve(model); v.Effort != want || err != nil {
			t.Errorf("Models.Resolve(%s) runs at %q, %v, want %q", model, v.Effort, err, want)
		}
	}
}
