package claude

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/unaquatic/flow/agent"
)

func TestParseCode(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		wantErr     bool
	}{
		{"manual", " code#state \n", false},
		{"callback URL", redirectURI + "?code=code&state=state", false},
		{"query", "code=code&state=state", false},
		{"bare code", "code", true},
		{"missing code", "#state", true},
		{"missing state", "code#", true},
		{"wrong state", "code#other", true},
		{"extra fragment", "code#state#extra", true},
		{"URL without state", redirectURI + "?code=code", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, err := ParseCode(tt.input, "state")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCode error = %v, want error %v", err, tt.wantErr)
			}
			if !tt.wantErr && code != "code" {
				t.Errorf("code = %q", code)
			}
		})
	}
}

func TestBrowser(t *testing.T) {
	for _, format := range []string{"manual", "URL", "query", "wrong state", "retry", "cancel"} {
		t.Run(format, func(t *testing.T) {
			var challenge, state string
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for key, want := range map[string]string{
					"grant_type": "authorization_code", "code": "authorization-code", "state": state,
					"client_id": clientID, "redirect_uri": redirectURI,
				} {
					if body[key] != want {
						t.Errorf("%s = %q, want %q", key, body[key], want)
					}
				}
				sum := sha256.Sum256([]byte(body["code_verifier"]))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
					t.Error("code verifier does not match challenge")
				}
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
					t.Error("token exchange must POST JSON")
				}
				fmt.Fprint(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
			}))
			defer server.Close()
			oauth := NewOAuth()
			if got := oauth.Scopes; len(got) != 4 {
				t.Fatalf("unexpected scopes: %v", got)
			}
			if oauth.TokenURL != "https://platform.claude.com/v1/oauth/token" {
				t.Fatalf("token URL = %s", oauth.TokenURL)
			}
			oauth.TokenURL = server.URL
			oauth.OpenBrowser = func(string) error { return nil }
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := oauth.BeginBrowser()
			u, err := url.Parse(p.URL)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			state, challenge = q.Get("state"), q.Get("code_challenge")
			if len(state) != 32 || challenge == "" || p.State == nil || p.State.Verifier == "" || p.State.State != state {
				t.Fatalf("incomplete authorization prompt: %+v", p)
			}
			for key, want := range map[string]string{
				"redirect_uri": redirectURI, "client_id": clientID, "scope": scopes,
				"code_challenge_method": "S256", "response_type": "code", "code": "true",
			} {
				if q.Get(key) != want {
					t.Errorf("authorization %s = %q, want %q", key, q.Get(key), want)
				}
			}
			var pasted string
			switch format {
			case "manual":
				pasted = "authorization-code#" + state
			case "URL":
				pasted = redirectURI + "?code=authorization-code&state=" + state
			case "query":
				pasted = "code=authorization-code&state=" + state
			case "wrong state":
				pasted = "authorization-code#other"
			case "retry":
				for _, invalid := range []string{"", " \n", "authorization-code", "#", "code#", redirectURI + "?code=code"} {
					if _, err := oauth.CompleteBrowser(ctx, *p.State, invalid); !errors.Is(err, agent.ErrInvalidLoginCode) {
						t.Fatalf("retry input %q: error = %v", invalid, err)
					}
				}
				pasted = "authorization-code#" + state
			case "cancel":
				cancel()
				pasted = "authorization-code#" + state
			}
			// Completion uses only the proof, not the OAuth instance that began it.
			oauth = NewOAuth()
			oauth.TokenURL = server.URL
			tok, err := oauth.CompleteBrowser(ctx, *p.State, pasted)
			if format == "wrong state" || format == "cancel" {
				if err == nil || requests != 0 {
					t.Fatalf("invalid input: error %v, token requests %d", err, requests)
				}
				if format == "cancel" && !errors.Is(err, context.Canceled) {
					t.Errorf("cancel error = %v", err)
				}
				return
			}
			if err != nil || tok.Access != "access" || tok.Refresh != "refresh" || tok.Expiry.IsZero() || requests != 1 {
				t.Fatalf("Browser = %+v, %v; token requests %d", tok, err, requests)
			}
		})
	}
}
