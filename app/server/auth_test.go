package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
)

// storedKey is the wire form of a saved API-key account.
func storedKey(id agent.AccountID) AccountStatus {
	return AccountStatus{ID: id, Kind: agent.CredentialAPIKey, Source: agent.CredentialSourceStore}
}

// configKey is the wire form of the config account.
var configKey = AccountStatus{ID: agent.ConfigAccount, Kind: agent.CredentialAPIKey, Source: agent.CredentialSourceConfig}

func equalAuthStatus(a, b AuthStatus) bool {
	return a.Provider == b.Provider && a.Active == b.Active && slices.Equal(a.Accounts, b.Accounts)
}

// addKey adds an OpenAI account with an API key through the API and returns
// its ID. The account becomes active.
func addKey(t *testing.T, ts *httptest.Server) agent.AccountID {
	t.Helper()
	var resp AccountResponse
	if status := doJSON(t, "POST", ts.URL+"/auth/openai/accounts", NewAccountRequest{Key: "sk"}, &resp); status != http.StatusCreated {
		t.Fatalf("add key: status %d", status)
	}
	return resp.ID
}

// accountURL returns the URL of an OpenAI account.
func accountURL(ts *httptest.Server, id agent.AccountID) string {
	return ts.URL + "/auth/openai/accounts/" + string(id)
}

func TestAuthList(t *testing.T) {
	ts := newTestServer(t)
	older, newer := addKey(t, ts), addKey(t, ts)

	var got []AuthStatus
	if status := doJSON(t, "GET", ts.URL+"/auth", nil, &got); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	want := []AuthStatus{
		{Provider: claude.ProviderID, Accounts: []AccountStatus{}},
		{Provider: chatgpt.ProviderID, Active: newer, Accounts: []AccountStatus{storedKey(older), storedKey(newer)}},
	}
	if !slices.EqualFunc(got, want, equalAuthStatus) {
		t.Errorf("GET /auth = %+v, want %+v", got, want)
	}
}

func TestAuthCodeValidation(t *testing.T) {
	ts := newTestServer(t)
	id := addKey(t, ts)
	for _, tt := range []struct {
		name   string
		id     agent.AccountID
		body   any
		status int
	}{
		{"missing code", id, map[string]string{}, http.StatusBadRequest},
		{"invalid account", "not-a-uuid", map[string]string{"code": "code#state"}, http.StatusBadRequest},
		{"missing proof", id, map[string]string{"code": "code#state"}, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := doJSON(t, "POST", accountURL(ts, tt.id)+"/code", tt.body, nil)
			if status != tt.status {
				t.Errorf("status = %d, want %d", status, tt.status)
			}
		})
	}
}

func TestAuthAdd(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		body       any
		wantStatus int
	}{
		{name: "key", provider: "openai", body: NewAccountRequest{Key: " sk-1 "}, wantStatus: http.StatusCreated},
		{name: "method and key", provider: "openai", body: NewAccountRequest{Method: agent.LoginDevice, Key: "sk"}, wantStatus: http.StatusBadRequest},
		{name: "neither", provider: "openai", body: NewAccountRequest{}, wantStatus: http.StatusBadRequest},
		{name: "empty key", provider: "openai", body: NewAccountRequest{Key: "  "}, wantStatus: http.StatusBadRequest},
		{name: "unknown method", provider: "openai", body: map[string]string{"method": "carrier-pigeon"}, wantStatus: http.StatusBadRequest},
		{name: "unregistered provider, key", provider: "bogus", body: NewAccountRequest{Key: "sk"}, wantStatus: http.StatusNotFound},
		{name: "unregistered provider, login", provider: "bogus", body: NewAccountRequest{Method: agent.LoginDevice}, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			var resp AccountResponse
			if status := doJSON(t, "POST", ts.URL+"/auth/"+tt.provider+"/accounts", tt.body, &resp); status != tt.wantStatus {
				t.Fatalf("status %d, want %d", status, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusCreated {
				return
			}
			if _, err := agent.ParseAccountID(string(resp.ID)); err != nil {
				t.Fatalf("new account ID %q: %v", resp.ID, err)
			}
			var got []AuthStatus
			doJSON(t, "GET", ts.URL+"/auth", nil, &got)
			want := AuthStatus{Provider: chatgpt.ProviderID, Active: resp.ID, Accounts: []AccountStatus{storedKey(resp.ID)}}
			if !equalAuthStatus(got[1], want) {
				t.Errorf("OpenAI accounts = %+v, want %+v", got[1], want)
			}
		})
	}
}

func TestAuthKey(t *testing.T) {
	tests := []struct {
		name       string
		path       func(ts *httptest.Server, id agent.AccountID) string
		body       any
		wantStatus int
	}{
		{
			name:       "replaces the key",
			path:       func(ts *httptest.Server, id agent.AccountID) string { return accountURL(ts, id) + "/key" },
			body:       KeyRequest{Key: "sk-2"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty key",
			path:       func(ts *httptest.Server, id agent.AccountID) string { return accountURL(ts, id) + "/key" },
			body:       KeyRequest{Key: "  "},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown account",
			path: func(ts *httptest.Server, _ agent.AccountID) string {
				return accountURL(ts, agent.NewAccountID()) + "/key"
			},
			body:       KeyRequest{Key: "sk-2"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "account ID not a UUID",
			path:       func(ts *httptest.Server, _ agent.AccountID) string { return ts.URL + "/auth/openai/accounts/work/key" },
			body:       KeyRequest{Key: "sk-2"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "config account",
			path: func(ts *httptest.Server, _ agent.AccountID) string {
				return accountURL(ts, agent.ConfigAccount) + "/key"
			},
			body:       KeyRequest{Key: "sk-2"},
			wantStatus: http.StatusConflict,
		},
		{
			name: "unregistered provider",
			path: func(ts *httptest.Server, id agent.AccountID) string {
				return ts.URL + "/auth/bogus/accounts/" + string(id) + "/key"
			},
			body:       KeyRequest{Key: "sk-2"},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			id, other := addKey(t, ts), addKey(t, ts)

			var got AuthStatus
			if status := doJSON(t, "PUT", tt.path(ts, id), tt.body, &got); status != tt.wantStatus {
				t.Fatalf("status %d, want %d", status, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			want := AuthStatus{Provider: chatgpt.ProviderID, Active: id, Accounts: []AccountStatus{storedKey(id), storedKey(other)}}
			if !equalAuthStatus(got, want) {
				t.Errorf("response = %+v, want %+v", got, want)
			}
		})
	}
}

func TestAuthUse(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string // OPENAI_API_KEY
		body       func(older agent.AccountID) any
		wantStatus int
		wantActive func(older agent.AccountID) agent.AccountID
	}{
		{
			name:       "saved account",
			body:       func(older agent.AccountID) any { return UseRequest{ID: older} },
			wantStatus: http.StatusOK,
			wantActive: func(older agent.AccountID) agent.AccountID { return older },
		},
		{
			name:       "config account",
			envKey:     "sk-env",
			body:       func(agent.AccountID) any { return UseRequest{ID: agent.ConfigAccount} },
			wantStatus: http.StatusOK,
			wantActive: func(agent.AccountID) agent.AccountID { return agent.ConfigAccount },
		},
		{name: "config account without key", body: func(agent.AccountID) any { return UseRequest{ID: agent.ConfigAccount} }, wantStatus: http.StatusNotFound},
		{name: "unknown account", body: func(agent.AccountID) any { return UseRequest{ID: agent.NewAccountID()} }, wantStatus: http.StatusNotFound},
		{name: "account ID not a UUID", body: func(agent.AccountID) any { return map[string]string{"id": "work"} }, wantStatus: http.StatusBadRequest},
		{name: "missing account ID", body: func(agent.AccountID) any { return map[string]string{} }, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			older := addKey(t, ts)
			addKey(t, ts)
			t.Setenv("OPENAI_API_KEY", tt.envKey)

			var got AuthStatus
			if status := doJSON(t, "PUT", ts.URL+"/auth/openai/active", tt.body(older), &got); status != tt.wantStatus {
				t.Fatalf("status %d, want %d", status, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && got.Active != tt.wantActive(older) {
				t.Errorf("active account = %q, want %q", got.Active, tt.wantActive(older))
			}
		})
	}
}

func TestAuthLogout(t *testing.T) {
	t.Run("active account passes to the other saved one", func(t *testing.T) {
		ts := newTestServer(t)
		older, newer := addKey(t, ts), addKey(t, ts)
		// The server reads the environment for every request.
		t.Setenv("OPENAI_API_KEY", "sk-env")

		var got AuthStatus
		if status := doJSON(t, "DELETE", accountURL(ts, newer), nil, &got); status != http.StatusOK {
			t.Fatalf("status %d", status)
		}
		want := AuthStatus{Provider: chatgpt.ProviderID, Active: older, Accounts: []AccountStatus{configKey, storedKey(older)}}
		if !equalAuthStatus(got, want) {
			t.Errorf("response = %+v, want %+v", got, want)
		}
	})

	t.Run("last account passes to the config account", func(t *testing.T) {
		ts := newTestServer(t)
		id := addKey(t, ts)
		t.Setenv("OPENAI_API_KEY", "sk-env")

		var got AuthStatus
		if status := doJSON(t, "DELETE", accountURL(ts, id), nil, &got); status != http.StatusOK {
			t.Fatalf("status %d", status)
		}
		want := AuthStatus{Provider: chatgpt.ProviderID, Active: agent.ConfigAccount, Accounts: []AccountStatus{configKey}}
		if !equalAuthStatus(got, want) {
			t.Errorf("response = %+v, want %+v", got, want)
		}
	})

	tests := []struct {
		name       string
		url        func(ts *httptest.Server) string
		wantStatus int
	}{
		{"unknown account", func(ts *httptest.Server) string { return accountURL(ts, agent.NewAccountID()) }, http.StatusNotFound},
		{"account ID not a UUID", func(ts *httptest.Server) string { return ts.URL + "/auth/openai/accounts/work" }, http.StatusBadRequest},
		{"config account", func(ts *httptest.Server) string { return accountURL(ts, agent.ConfigAccount) }, http.StatusConflict},
		{"unregistered provider", func(ts *httptest.Server) string {
			return ts.URL + "/auth/bogus/accounts/" + string(agent.NewAccountID())
		}, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			addKey(t, ts)
			if status := doJSON(t, "DELETE", tt.url(ts), nil, nil); status != tt.wantStatus {
				t.Errorf("status %d, want %d", status, tt.wantStatus)
			}
		})
	}
}

// TestAuthLoginValidation covers the rejected requests only: an accepted one
// starts a real login that opens the browser.
func TestAuthLoginValidation(t *testing.T) {
	tests := []struct {
		name       string
		url        func(ts *httptest.Server, id agent.AccountID) string
		body       any
		wantStatus int
	}{
		{
			name:       "unknown method",
			url:        func(ts *httptest.Server, id agent.AccountID) string { return accountURL(ts, id) + "/login" },
			body:       map[string]string{"method": "carrier-pigeon"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing method",
			url:        func(ts *httptest.Server, id agent.AccountID) string { return accountURL(ts, id) + "/login" },
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown account",
			url: func(ts *httptest.Server, _ agent.AccountID) string {
				return accountURL(ts, agent.NewAccountID()) + "/login"
			},
			body:       LoginRequest{Method: agent.LoginDevice},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "account ID not a UUID",
			url: func(ts *httptest.Server, _ agent.AccountID) string {
				return ts.URL + "/auth/openai/accounts/work/login"
			},
			body:       LoginRequest{Method: agent.LoginDevice},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "config account",
			url: func(ts *httptest.Server, _ agent.AccountID) string {
				return accountURL(ts, agent.ConfigAccount) + "/login"
			},
			body:       LoginRequest{Method: agent.LoginDevice},
			wantStatus: http.StatusConflict,
		},
		{
			name: "unregistered provider",
			url: func(ts *httptest.Server, id agent.AccountID) string {
				return ts.URL + "/auth/bogus/accounts/" + string(id) + "/login"
			},
			body:       LoginRequest{Method: agent.LoginDevice},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			id := addKey(t, ts)
			if status := doJSON(t, "POST", tt.url(ts, id), tt.body, nil); status != tt.wantStatus {
				t.Errorf("status %d, want %d", status, tt.wantStatus)
			}
		})
	}
}
