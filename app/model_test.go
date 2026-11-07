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
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
	"github.com/unaquatic/flow/extension/protocol/messages"
	"github.com/unaquatic/flow/internal/catalog"
	"github.com/unaquatic/flow/internal/pubsub"
)

func TestMatchModel(t *testing.T) {
	choices := []ModelChoice{
		{Name: "anthropic/claude-opus-5", Released: "2026-07-24"},
		{Name: "anthropic/claude-sonnet-4-5", Released: "2025-09-29"},
		{Name: "anthropic/claude-sonnet-5", Released: "2026-06-29"},
		{Name: "openai/gpt-5", Released: "2025-08-07"},
		{Name: "openai/gpt-5-mini", Released: "2025-08-07"},
		{Name: "openai/gpt-6-astra", Released: "2026-09-04"},
	}
	tests := []struct {
		pattern string
		want    string // "" means no match
	}{
		{pattern: "openai/gpt-5", want: "openai/gpt-5"},
		{pattern: "gpt-5", want: "openai/gpt-5"},
		{pattern: "GPT-5", want: "openai/gpt-5"},
		{pattern: "  claude-opus-5 ", want: "anthropic/claude-opus-5"},
		// Every sonnet matches as well; the newest wins.
		{pattern: "sonnet", want: "anthropic/claude-sonnet-5"},
		{pattern: "opus", want: "anthropic/claude-opus-5"},
		{pattern: "astra", want: "openai/gpt-6-astra"},
		{pattern: "gpt-5-m", want: "openai/gpt-5-mini"},
		// A pattern can name the provider too.
		{pattern: "anthropic/sonnet", want: "anthropic/claude-sonnet-5"},
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
		w.Write(openaiDoc(t, "gpt-old", "gpt-new"))
	}))
	defer src.Close()

	tests := []struct {
		name     string
		pattern  string
		want     string
		wantErr  error
		wantHits int32
	}{
		{name: "in the cached catalog", pattern: "gpt-old", want: "openai/gpt-old"},
		{name: "newer than the cached catalog", pattern: "gpt-new", want: "openai/gpt-new", wantHits: 1},
		{name: "nowhere", pattern: "claude", wantErr: ErrNoModel, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			dir := t.TempDir()
			gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: openaiDoc(t, "gpt-old")})
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

// openaiDoc returns a catalog document, as a catalog server serves it,
// listing the models named for OpenAI.
func openaiDoc(t *testing.T, models ...string) []byte {
	ms := map[string]any{}
	for _, m := range models {
		ms[m] = map[string]any{"id": m}
	}
	b, _ := json.Marshal(map[string]any{"openai": map[string]any{"id": "openai", "models": ms}})
	return catalogDoc(t, string(b))
}

// catalogDoc returns the catalog document of api, an api.json, as
// gatewaytest.FromAPI builds it.
func catalogDoc(t *testing.T, api string) []byte {
	t.Helper()
	b, err := json.Marshal(gatewaytest.FromAPI(t, api))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newCatalogApp returns an app over the catalog cached in dir, downloaded
// from src, or from no server when src is empty, with OpenAI registered.
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
		providers: agent.NewProviders(agent.ProviderOptions{Catalog: store}, agenttest.Provider{ID: chatgpt.ProviderID, Gateway: &agenttest.Gateway{}}),
		broker:    pubsub.NewBroker[Event](),
		workerCtx: ctx,
		cancel:    cancel,
	}
	t.Cleanup(a.Close)
	return a
}

// staleCatalog caches a catalog of gpt-old in a new directory, fetched two
// days ago, past the default TTL, and returns the directory.
func staleCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now().Add(-48 * time.Hour), Data: openaiDoc(t, "gpt-old")})
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
		w.Write(openaiDoc(t, "gpt-old", "gpt-new"))
	}))
	defer src.Close()
	a := newCatalogApp(t, staleCatalog(t), src.URL)
	events := a.Subscribe(t.Context())

	for range 2 {
		models, err := a.Models().List(t.Context(), chatgpt.ProviderID)
		if err != nil || !slices.Equal(listedIDs(models), []string{"openai/gpt-old"}) {
			t.Fatalf("ListModels while downloading = %v, %v, want [openai/gpt-old]", listedIDs(models), err)
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

	models, err := a.Models().List(t.Context(), chatgpt.ProviderID)
	if want := []string{"openai/gpt-new", "openai/gpt-old"}; err != nil || !slices.Equal(listedIDs(models), want) {
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

	if _, err := a.Models().List(t.Context(), chatgpt.ProviderID); err != nil {
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
	for _, model := range []string{"openai/gpt-x", "anthropic/claude-x"} {
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
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: openaiDoc(t, "gpt-old")})
	a := newCatalogApp(t, dir, src.URL)

	_, err := a.Models().Find(t.Context(), "", "gpt-new")
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
		w.Write(openaiDoc(t, "gpt-old", "gpt-new"))
	}))
	defer src.Close()
	a := newCatalogApp(t, staleCatalog(t), src.URL)

	if got, err := a.Models().Find(t.Context(), "", "gpt-new"); got != "openai/gpt-new" || err != nil {
		t.Fatalf("Models.Find = %q, %v, want openai/gpt-new", got, err)
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
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{ETag: `"v1"`, Fetched: time.Now(), Data: openaiDoc(t, "gpt-old")})
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

// variantDoc is an api.json of an OpenAI model with fast and pro
// modes and effort levels, its pro mode a model of its own, of a model whose ID ends in a mode's name, and of
// a gpt-7 whose effort list ends in xxxhigh, a level flow does not know.
const variantDoc = `{"openai":{"id":"openai","models":{
  "gpt-5.6":{"id":"gpt-5.6","release_date":"2026-08-01","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"fast":{},"pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "gpt-5.5-pro":{"id":"gpt-5.5-pro","release_date":"2026-04-01"},
  "gpt-7":{"id":"gpt-7","release_date":"2026-12-01","reasoning":true,
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
		{"gpt-5.6", "openai/gpt-5.6", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Effort: "medium"}},
		{"gpt-5.6:fast", "openai/gpt-5.6:fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Option: "fast", Effort: "medium"}},
		{"5.6-pro:xhigh", "openai/gpt-5.6-pro:xhigh", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6-pro", Effort: "xhigh"}},
		{"gpt-5.6:none", "openai/gpt-5.6:none", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Effort: "none"}},
		{"gpt-5.5-pro", "openai/gpt-5.5-pro", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.5-pro"}},
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
	if got := a.Models().Efforts(a.Models().ParseVariant("openai/gpt-5.6:fast")); !slices.Equal(got, want) {
		t.Errorf("Models.Efforts = %v, want %v", got, want)
	}
	if got := a.Models().Efforts(a.Models().ParseVariant("openai/gpt-unknown")); got != nil {
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

	got, err := a.Models().Find(t.Context(), "", "gpt-7:XXXHIGH")
	if got != "openai/gpt-7:xxxhigh" || err != nil {
		t.Fatalf("Models.Find = %q, %v, want openai/gpt-7:xxxhigh", got, err)
	}
	want := agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-7", Effort: "xxxhigh"}
	if v, err := a.Models().Resolve(got); v != want || err != nil {
		t.Errorf("Models.Resolve = %+v, %v, want %+v", v, err, want)
	}
	levels := []agent.ReasoningEffort{"low", "medium", "high", "xhigh", "max", "xxxhigh"}
	if got := a.Models().Efforts(want); !slices.Equal(got, levels) {
		t.Errorf("Models.Efforts = %q, want %q", got, levels)
	}
	// gpt-5.6 does not list xxxhigh: the pattern does not name a level of
	// it, and the variant runs at the provider's default.
	if got, err := a.Models().Find(t.Context(), "", "gpt-5.6:xxxhigh"); err == nil && got == "openai/gpt-5.6:xxxhigh" {
		t.Errorf("Models.Find(gpt-5.6:xxxhigh) = %q, want no level kept", got)
	}
	if v := a.Models().FitEffort(agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Effort: "xxxhigh"}); v.Effort != "medium" {
		t.Errorf("gpt-5.6 at xxxhigh runs at %q, want medium", v.Effort)
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
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"model":"openai/gpt-5.6:fast:low"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.config = config.NewService(home)

	gpt := func(option string, effort agent.ReasoningEffort) agent.Variant {
		return agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Option: option, Effort: effort}
	}
	tests := []struct {
		name     string
		provider agent.ProviderID
		pattern  string
		effort   agent.ReasoningEffort
		want     agent.Variant
		wantErr  string
	}{
		{name: "pattern", pattern: "gpt-5.6", want: gpt("", "medium")},
		{name: "pattern of a provider", provider: chatgpt.ProviderID, pattern: "gpt-5.6:fast", want: gpt("fast", "medium")},
		{name: "effort over the pattern's", pattern: "gpt-5.6:low", effort: "xhigh", want: gpt("", "xhigh")},
		{name: "default model", want: gpt("fast", "low")},
		{name: "effort over the default's", effort: "high", want: gpt("fast", "high")},
		{name: "effort the model does not list", pattern: "gpt-5.6", effort: "max",
			wantErr: `openai/gpt-5.6 has no reasoning effort "max"; the catalog lists ["none" "low" "medium" "high" "xhigh"]`},
		{name: "model without efforts", pattern: "gpt-5.5-pro", effort: "high",
			wantErr: `openai/gpt-5.5-pro has no reasoning effort "high"; the catalog lists []`},
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
		agenttest.Provider{ID: chatgpt.ProviderID, Gateway: &agenttest.Gateway{}})

	if d := a.Models().DefaultEffort(agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6"}); d != "medium" {
		t.Errorf("Models.DefaultEffort = %q, want medium", d)
	}
	if v, _ := a.Models().Resolve("openai/gpt-5.6"); v.Effort != "medium" {
		t.Errorf("gpt-5.6 runs at %q, want medium", v.Effort)
	}
	// A chosen level wins over the default.
	if v, _ := a.Models().Resolve("openai/gpt-5.6:low"); v.Effort != "low" {
		t.Errorf("gpt-5.6:low runs at %q, want low", v.Effort)
	}
	// A model must name its provider: flow defaults to none.
	if _, err := a.Models().Resolve("gpt-5.6"); !errors.Is(err, ErrModelFormat) {
		t.Errorf("Resolve of a model without a provider = %v, want ErrModelFormat", err)
	}
}

// TestAnthropicDefaultEffortPerModel checks that a Claude model that names
// no effort runs at the catalog's default effort for it, high for Opus 4.8,
// or else at medium, as Opus 5.5, which the catalog gives none.
func TestAnthropicDefaultEffortPerModel(t *testing.T) {
	const doc = `{"anthropic":{"id":"anthropic","models":{
  "claude-opus-4-8":{"id":"claude-opus-4-8","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]},
  "claude-opus-5-5":{"id":"claude-opus-5-5","reasoning":true,
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}}}`
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, doc)})
	a := newCatalogApp(t, dir, "")
	a.providers = agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: dir})}, messages.New(messages.Init{Name: string(claude.ProviderID)}))

	for model, want := range map[string]agent.ReasoningEffort{
		"anthropic/claude-opus-5-5": "medium",
		"anthropic/claude-opus-4-8": "high",
	} {
		if v, err := a.Models().Resolve(model); v.Effort != want || err != nil {
			t.Errorf("Models.Resolve(%s) runs at %q, %v, want %q", model, v.Effort, err, want)
		}
	}
}
