// Package claude logs in to Anthropic with Claude sign-in. The requests that
// its tokens authorize are Anthropic Messages, which package messages sends.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent"
)

// ProviderID identifies Anthropic in the catalog: the provider whose
// accounts a Claude login signs in to.
const ProviderID agent.ProviderID = "anthropic"

const (
	clientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	authURL      = "https://claude.ai/oauth/authorize"
	tokenURL     = "https://console.anthropic.com/v1/oauth/token"
	deviceURL    = "https://claude.ai/oauth/device/code"
	scopes       = "org:create_api_key user:profile user:inference user:sessions:claude_code"
	callbackPort = 19532
	userAgent    = "claude-cli/1.0.56 (external, cli)"
)

// OAuth implements agent.LoginFlow for Claude sign-in.
type OAuth struct {
	// ClientID identifies the OAuth client.
	ClientID string
	// AuthURL is the browser authorization endpoint.
	AuthURL string
	// TokenURL is the token endpoint.
	TokenURL string
	// DeviceURL is the device authorization endpoint.
	DeviceURL string
	// Scopes contains requested OAuth scopes.
	Scopes []string
	// CallbackPort is the local port of the browser redirect.
	CallbackPort int
	// Client sends OAuth requests. Nil means http.DefaultClient.
	Client *http.Client
}

// NewOAuth returns an OAuth using Anthropic's endpoints.
func NewOAuth() *OAuth {
	return &OAuth{
		ClientID:     clientID,
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		DeviceURL:    deviceURL,
		Scopes:       strings.Fields(scopes),
		CallbackPort: callbackPort,
	}
}

// Browser logs in through the browser and a redirect to the local port.
func (a *OAuth) Browser(ctx context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	pkce := agent.NewPKCE()
	state := agent.NewOAuthState()
	cb, err := agent.ListenOAuthCallback(a.CallbackPort, "/auth/callback", state)
	if err != nil {
		return agent.OAuthToken{}, err
	}

	u := a.AuthURL + "?" + url.Values{
		"code":                  {"true"},
		"response_type":         {"code"},
		"client_id":             {a.ClientID},
		"redirect_uri":          {cb.RedirectURI},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {strings.Join(a.Scopes, " ")},
		"state":                 {state},
	}.Encode()
	show(agent.LoginPrompt{URL: u})
	if err := agent.OpenBrowser(u); err != nil {
		slog.Info("open browser", "err", err)
	}

	code, err := cb.Wait(ctx)
	if err != nil {
		return agent.OAuthToken{}, err
	}
	// Drop fragments that copying the code from the page can append.
	code, _, _ = strings.Cut(code, "#")
	code, _, _ = strings.Cut(code, "&")

	tok, err := a.requestToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"state":         state,
		"client_id":     a.ClientID,
		"redirect_uri":  cb.RedirectURI,
		"code_verifier": pkce.Verifier,
	})
	if err != nil {
		return agent.OAuthToken{}, fmt.Errorf("token exchange: %w", err)
	}
	return tok, nil
}

// Device logs in with the device authorization grant (RFC 8628).
func (a *OAuth) Device(ctx context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	body, err := a.postForm(ctx, a.DeviceURL, url.Values{
		"client_id": {a.ClientID},
		"scope":     {strings.Join(a.Scopes, " ")},
	})
	if err != nil {
		return agent.OAuthToken{}, fmt.Errorf("device code request: %w", err)
	}
	if err := json.Unmarshal(body, &dc); err != nil {
		return agent.OAuthToken{}, fmt.Errorf("device code response: %w", err)
	}
	show(agent.LoginPrompt{URL: dc.VerificationURI, Code: dc.UserCode})

	interval := time.Duration(dc.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	expiresIn := time.Duration(dc.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, expiresIn)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return agent.OAuthToken{}, agent.ErrDeviceCodeExpired
			}
			return agent.OAuthToken{}, ctx.Err()
		case <-time.After(interval):
		}

		tok, err := a.pollDevice(ctx, dc.DeviceCode)
		switch {
		case errors.Is(err, errPending):
			continue
		case errors.Is(err, errSlowDown):
			interval += 5 * time.Second
			continue
		case err != nil:
			return agent.OAuthToken{}, err
		}
		return tok, nil
	}
}

// Refresh renews t with its refresh token.
func (a *OAuth) Refresh(ctx context.Context, t agent.OAuthToken) (agent.OAuthToken, error) {
	return a.requestToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": t.Refresh,
		"client_id":     a.ClientID,
	})
}

var (
	// errPending reports a device login that is not approved yet.
	errPending = errors.New("authorization pending")
	// errSlowDown asks a device login to poll less often.
	errSlowDown = errors.New("slow down")
)

// pollDevice checks once whether the device login was approved.
func (a *OAuth) pollDevice(ctx context.Context, deviceCode string) (agent.OAuthToken, error) {
	body, err := a.postForm(ctx, a.TokenURL, url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {a.ClientID},
	})
	var grantErr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &grantErr) == nil {
		switch grantErr.Error {
		case "":
		case "authorization_pending":
			return agent.OAuthToken{}, errPending
		case "slow_down":
			return agent.OAuthToken{}, errSlowDown
		default:
			return agent.OAuthToken{}, fmt.Errorf("%w: %s", agent.ErrTokenEndpoint, grantErr.Error)
		}
	}
	if err != nil {
		return agent.OAuthToken{}, err
	}
	return agent.ParseOAuthToken(body)
}

// requestToken posts params as JSON to the token endpoint.
func (a *OAuth) requestToken(ctx context.Context, params map[string]string) (agent.OAuthToken, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return agent.OAuthToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenURL, bytes.NewReader(body))
	if err != nil {
		return agent.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Origin", "https://claude.ai")
	return agent.RequestOAuthToken(a.client(), req)
}

// postForm posts form to endpoint and returns the response body. A
// non-200 status is an error that still returns the body.
func (a *OAuth) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("%w (%d): %s", agent.ErrTokenEndpoint, resp.StatusCode, body)
	}
	return body, nil
}

// client returns the HTTP client for OAuth requests.
func (a *OAuth) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}
