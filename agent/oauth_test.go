package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestChallenge(t *testing.T) {
	// The example of RFC 7636, appendix B.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	if got, want := challenge(verifier), "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"; got != want {
		t.Errorf("challenge = %q, want %q", got, want)
	}
}

func TestNewPKCE(t *testing.T) {
	p := NewPKCE()
	// RFC 7636 section 4.1 bounds the verifier length.
	if n := len(p.Verifier); n < 43 || n > 128 {
		t.Errorf("verifier length = %d, want 43 to 128", n)
	}
	if p.Challenge != challenge(p.Verifier) {
		t.Error("challenge does not match verifier")
	}
	if NewPKCE().Verifier == p.Verifier {
		t.Error("two proof keys share a verifier")
	}
}

func TestCallback(t *testing.T) {
	tests := []struct {
		name     string
		query    url.Values
		wantCode string
		wantErr  bool
		wantPage string // text the page must contain
	}{
		{
			name:     "code",
			query:    url.Values{"state": {"s1"}, "code": {"c1"}},
			wantCode: "c1",
			wantPage: "Login Successful",
		},
		{
			name:     "unexpected state",
			query:    url.Values{"state": {"forged"}, "code": {"c1"}},
			wantErr:  true,
			wantPage: "Session expired",
		},
		{
			name:     "provider error is escaped",
			query:    url.Values{"state": {"s1"}, "error": {"access_denied"}, "error_description": {"<script>x</script>"}},
			wantErr:  true,
			wantPage: "&lt;script&gt;",
		},
		{
			name:     "missing code",
			query:    url.Values{"state": {"s1"}},
			wantErr:  true,
			wantPage: "Missing authorization code",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, err := ListenOAuthCallback(0, "/cb", "s1")
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.Get(cb.RedirectURI + "?" + tt.query.Encode())
			if err != nil {
				t.Fatal(err)
			}
			page, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(page), tt.wantPage) {
				t.Errorf("page lacks %q:\n%s", tt.wantPage, page)
			}
			if strings.Contains(string(page), "<script>") {
				t.Error("page contains unescaped markup from the query")
			}

			code, err := cb.Wait(t.Context())
			if (err != nil) != tt.wantErr || code != tt.wantCode {
				t.Errorf("Wait = %q, %v; want %q, error %v", code, err, tt.wantCode, tt.wantErr)
			}
		})
	}
}

func TestIconPath(t *testing.T) {
	// "█" sets both pixels of its column, "▀" the top one, "▄" the bottom
	// one, and a space neither.
	got := IconPath("█ \n▀▄")
	want := "M0 0h1v1h-1zM0 1h1v1h-1zM0 2h1v1h-1zM1 3h1v1h-1z"
	if got != want {
		t.Errorf("IconPath = %q, want %q", got, want)
	}
}

func TestCallbackWaitHonorsContext(t *testing.T) {
	cb, err := ListenOAuthCallback(0, "/cb", "s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cb.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait error = %v, want context.Canceled", err)
	}
}

func TestParseOAuthToken(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		want       OAuthToken
		wantExpiry bool
		wantErr    error
	}{
		{
			name:       "full",
			body:       `{"access_token":"a","refresh_token":"r","expires_in":3600}`,
			want:       OAuthToken{Access: "a", Refresh: "r"},
			wantExpiry: true,
		},
		{
			name: "no expiry",
			body: `{"access_token":"a"}`,
			want: OAuthToken{Access: "a"},
		},
		{name: "no access token", body: `{"refresh_token":"r"}`, wantErr: ErrTokenEndpoint},
		{name: "not JSON", body: `<html>`, wantErr: ErrTokenEndpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOAuthToken([]byte(tt.body))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseOAuthToken error = %v, want %v", err, tt.wantErr)
			}
			if got.Access != tt.want.Access || got.Refresh != tt.want.Refresh {
				t.Errorf("ParseOAuthToken = %+v, want %+v", got, tt.want)
			}
			if hasExpiry := !got.Expiry.IsZero(); hasExpiry != tt.wantExpiry {
				t.Errorf("expiry set = %v, want %v", hasExpiry, tt.wantExpiry)
			}
			if tt.wantExpiry && got.Expiring(time.Now()) {
				t.Error("fresh token already expiring")
			}
		})
	}
}
