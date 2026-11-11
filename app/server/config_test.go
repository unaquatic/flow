package server

import (
	"net/http"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
)

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	var got Health
	if status := doJSON(t, "GET", ts.URL+"/health", nil, &got); status != http.StatusOK || got.Version != product.Version() {
		t.Errorf("GET /health = %d, %+v, want 200 and version %s", status, got, product.Version())
	}
}

// TestConfig checks that GET /config reports the default provider and
// model of the config file, and that PATCH /config saves those it names
// there, with the other as the file sets it, keeping the file's other
// settings, changes nothing when it names none, and refuses an empty
// provider or model, an unknown provider, or a setting it cannot change.
func TestConfig(t *testing.T) {
	var home string
	ts, _ := newTestServerWith(t, func(cfg *config.Config) {
		home = cfg.HomeDir
		cfg.LogLevel = config.LogDebug
	})

	var got Config
	if status := doJSON(t, "GET", ts.URL+"/config", nil, &got); status != http.StatusOK ||
		got != (Config{DefaultProvider: "provider-b", DefaultModel: "lab-b/model-y"}) {
		t.Fatalf("GET /config = %d, %+v, want 200 and lab-b/model-y at provider-b", status, got)
	}

	want := Config{DefaultProvider: "provider-a", DefaultModel: "lab-a/model-x"}
	provider, model := want.DefaultProvider, want.DefaultModel
	update := ConfigUpdate{DefaultProvider: &provider, DefaultModel: &model}
	if status := doJSON(t, "PATCH", ts.URL+"/config", update, &got); status != http.StatusOK || got != want {
		t.Fatalf("PATCH /config = %d, %+v, want 200 and %+v", status, got, want)
	}
	if doJSON(t, "GET", ts.URL+"/config", nil, &got); got != want {
		t.Errorf("GET /config after PATCH = %+v, want %+v", got, want)
	}
	saved, err := config.LoadDir(home)
	if err != nil || saved.DefaultProvider != "provider-a" || saved.DefaultModel != "lab-a/model-x" || saved.LogLevel != config.LogDebug {
		t.Errorf("config file = provider %q, model %q, log level %q, %v, want lab-a/model-x at provider-a and the kept log level",
			saved.DefaultProvider, saved.DefaultModel, saved.LogLevel, err)
	}
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{}, &got); status != http.StatusOK || got != want {
		t.Errorf("PATCH /config with no setting = %d, %+v, want 200 and %+v unchanged", status, got, want)
	}

	// A model alone runs at the provider the file sets.
	other := "lab-a/model-z"
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{DefaultModel: &other}, &got); status != http.StatusOK ||
		got != (Config{DefaultProvider: "provider-a", DefaultModel: other}) {
		t.Errorf("PATCH /config with a model alone = %d, %+v, want 200 and %s at provider-a", status, got, other)
	}
	// A provider alone keeps the model the file sets.
	providerB := agent.ProviderID("provider-b")
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{DefaultProvider: &providerB}, &got); status != http.StatusOK ||
		got != (Config{DefaultProvider: providerB, DefaultModel: other}) {
		t.Errorf("PATCH /config with a provider alone = %d, %+v, want 200 and %s at provider-b", status, got, other)
	}
	if status := doJSON(t, "PATCH", ts.URL+"/config", update, &got); status != http.StatusOK || got != want {
		t.Fatalf("PATCH /config back = %d, %+v, want 200 and %+v", status, got, want)
	}

	empty, unknown := agent.ProviderID(""), agent.ProviderID("nope")
	noModel := ""
	tests := []struct {
		name       string
		body       any
		wantStatus int
	}{
		{name: "empty provider", body: ConfigUpdate{DefaultProvider: &empty}, wantStatus: http.StatusBadRequest},
		{name: "empty model", body: ConfigUpdate{DefaultModel: &noModel}, wantStatus: http.StatusBadRequest},
		{name: "unknown provider", body: ConfigUpdate{DefaultProvider: &unknown}, wantStatus: http.StatusNotFound},
		{name: "older model setting", body: map[string]string{"model": "provider-a/lab-a/model-x"}, wantStatus: http.StatusBadRequest},
		{name: "setting it cannot change", body: map[string]string{"log_level": "warn"}, wantStatus: http.StatusBadRequest},
		{name: "not an object", body: "model", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		if status := doJSON(t, "PATCH", ts.URL+"/config", tt.body, nil); status != tt.wantStatus {
			t.Errorf("%s: PATCH /config = %d, want %d", tt.name, status, tt.wantStatus)
		}
	}
	if doJSON(t, "GET", ts.URL+"/config", nil, &got); got != want {
		t.Errorf("config after refused updates = %+v, want %+v", got, want)
	}
	if saved, _ := config.LoadDir(home); saved.LogLevel != config.LogDebug {
		t.Errorf("log level after a refused update = %q, want it kept", saved.LogLevel)
	}
}
