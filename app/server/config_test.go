package server

import (
	"io"
	"net/http"
	"testing"

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

// TestConfig checks that GET /config reports the default model of the config
// file, and that PATCH /config saves the model it names there, keeping the
// file's other settings, changes nothing when it names none, and refuses a
// model not named as provider/model, of an unknown provider, or a setting it
// cannot change.
func TestConfig(t *testing.T) {
	var home string
	ts, _ := newTestServerWith(t, func(cfg *config.Config) {
		home = cfg.HomeDir
		cfg.LogLevel = config.LogDebug
	})

	var got Config
	if status := doJSON(t, "GET", ts.URL+"/config", nil, &got); status != http.StatusOK || got.Model != "test/model" {
		t.Fatalf("GET /config = %d, %+v, want 200 and test/model", status, got)
	}

	model := "provider-a/model-x"
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{Model: &model}, &got); status != http.StatusOK || got.Model != model {
		t.Fatalf("PATCH /config = %d, %+v, want 200 and %s", status, got, model)
	}
	if doJSON(t, "GET", ts.URL+"/config", nil, &got); got.Model != model {
		t.Errorf("GET /config after PATCH = %q, want %s", got.Model, model)
	}
	saved, err := config.LoadDir(home)
	if err != nil || saved.Model != model || saved.LogLevel != config.LogDebug {
		t.Errorf("config file = model %q, log level %q, %v, want %s and the kept log level", saved.Model, saved.LogLevel, err, model)
	}
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{}, &got); status != http.StatusOK || got.Model != model {
		t.Errorf("PATCH /config with no setting = %d, %+v, want 200 and %s unchanged", status, got, model)
	}

	unqualified, unknown := "model-x", "nope/x"
	tests := []struct {
		name       string
		body       any
		wantStatus int
	}{
		{name: "model without provider", body: ConfigUpdate{Model: &unqualified}, wantStatus: http.StatusBadRequest},
		{name: "unknown provider", body: ConfigUpdate{Model: &unknown}, wantStatus: http.StatusNotFound},
		{name: "setting it cannot change", body: map[string]string{"log_level": "warn"}, wantStatus: http.StatusBadRequest},
		{name: "not an object", body: "model", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		if status := doJSON(t, "PATCH", ts.URL+"/config", tt.body, nil); status != tt.wantStatus {
			t.Errorf("%s: PATCH /config = %d, want %d", tt.name, status, tt.wantStatus)
		}
	}
	if doJSON(t, "GET", ts.URL+"/config", nil, &got); got.Model != model {
		t.Errorf("model after refused updates = %q, want %s", got.Model, model)
	}
	if saved, _ := config.LoadDir(home); saved.LogLevel != config.LogDebug {
		t.Errorf("log level after a refused update = %q, want it kept", saved.LogLevel)
	}
}

// TestRequestsUseTheConfigModel checks that once PATCH /config sets the
// default model, a generate request and a prompt that name no model go to
// it.
func TestRequestsUseTheConfigModel(t *testing.T) {
	ts, _, bodies := newGenerateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, messagesReply)
	})
	model := "provider-b/model-y"
	if status := doJSON(t, "PATCH", ts.URL+"/config", ConfigUpdate{Model: &model}, nil); status != http.StatusOK {
		t.Fatalf("PATCH /config: status %d", status)
	}

	var got GenerateResponse
	body := GenerateRequest{Messages: []GenerateMessage{{Role: "user", Content: "hi"}}, MaxTokens: 10}
	if status := doJSON(t, "POST", ts.URL+"/generate", body, &got); status != http.StatusOK || got.Model != model {
		t.Errorf("POST /generate = %d, %q, want 200 from %s", status, got.Model, model)
	}

	sess := createSession(t, ts)
	msgURL := ts.URL + "/session/" + string(sess.ID) + "/message"
	if status := doJSON(t, "POST", msgURL, SendRequest{Text: "hello"}, nil); status != http.StatusAccepted {
		t.Fatalf("send: status %d", status)
	}
	waitForMessages(t, ts, msgURL)
	var msgs []struct {
		Model string `json:"model"`
	}
	doJSON(t, "GET", msgURL, nil, &msgs)
	if len(msgs) == 0 || msgs[0].Model != model {
		t.Errorf("prompt = %+v, want it sent to %s", msgs, model)
	}
	if len(bodies()) < 2 {
		t.Errorf("%s got %d requests, want the generate request and the prompt's", model, len(bodies()))
	}
}
