// Package claude logs in to Anthropic with Claude sign-in. The requests that
// its tokens authorize are Anthropic Messages, which package messages sends.
package claude

import (
	"bytes"
	"context"
	"crypto/rand"
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
	clientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	authURL     = "https://claude.ai/oauth/authorize"
	tokenURL    = "https://platform.claude.com/v1/oauth/token"
	redirectURI = "https://platform.claude.com/oauth/code/callback"
	deviceURL   = "https://claude.ai/oauth/device/code"
	scopes      = "org:create_api_key user:profile user:inference user:sessions:claude_code"
	userAgent   = "axios/1.13.6"
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
	// Client sends OAuth requests. Nil means http.DefaultClient.
	Client *http.Client
	// OpenBrowser opens the authorization URL. Nil means agent.OpenBrowser.
	OpenBrowser func(string) error
}

// NewOAuth returns an OAuth using Anthropic's endpoints.
func NewOAuth() *OAuth {
	return &OAuth{
		ClientID:  clientID,
		AuthURL:   authURL,
		TokenURL:  tokenURL,
		DeviceURL: deviceURL,
		Scopes:    strings.Fields(scopes),
	}
}

// BeginBrowser opens Anthropic's authorization page and returns the temporary
// state the caller needs to complete sign-in. It does not wait for user input.
func (a *OAuth) BeginBrowser() agent.LoginPrompt {
	pkce := agent.NewPKCE()
	var nonce [16]byte
	rand.Read(nonce[:])
	state := fmt.Sprintf("%x", nonce)

	u := a.AuthURL + "?" + url.Values{
		"code":                  {"true"},
		"response_type":         {"code"},
		"client_id":             {a.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {strings.Join(a.Scopes, " ")},
		"state":                 {state},
	}.Encode()
	open := a.OpenBrowser
	if open == nil {
		open = agent.OpenBrowser
	}
	if err := open(u); err != nil {
		slog.Info("open browser", "err", err)
	}

	return agent.LoginPrompt{URL: u, State: &agent.LoginState{Verifier: pkce.Verifier, State: state}}
}

// CompleteBrowser exchanges a pasted result using the proof from BeginBrowser.
func (a *OAuth) CompleteBrowser(ctx context.Context, state agent.LoginState, pasted string) (agent.OAuthToken, error) {
	if state.Verifier == "" || state.State == "" {
		return agent.OAuthToken{}, errors.New("missing OAuth login state; please log in again")
	}
	code, err := ParseCode(pasted, state.State)
	if err != nil {
		return agent.OAuthToken{}, err
	}

	tok, err := a.requestToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"state":         state.State,
		"client_id":     a.ClientID,
		"redirect_uri":  redirectURI,
		"code_verifier": state.Verifier,
	})
	if err != nil {
		return agent.OAuthToken{}, fmt.Errorf("token exchange: %w", err)
	}
	return tok, nil
}

// ParseCode extracts an authorization code and verifies its state. It accepts
// Anthropic's code#state format, a callback URL, or URL query parameters.
func ParseCode(input, expectedState string) (string, error) {
	input = strings.TrimSpace(input)
	var code, state string
	if u, err := url.Parse(input); err == nil && u.IsAbs() {
		code, state = u.Query().Get("code"), u.Query().Get("state")
	} else if before, after, ok := strings.Cut(input, "#"); ok {
		code, state = before, after
	} else if q, err := url.ParseQuery(input); err == nil {
		code, state = q.Get("code"), q.Get("state")
	}
	if code == "" || state == "" {
		return "", fmt.Errorf("%w: paste the complete code (code#state) or callback URL", agent.ErrInvalidLoginCode)
	}
	if state != expectedState {
		return "", errors.New("OAuth authorization has unexpected state; please log in again")
	}
	return code, nil
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
	req.Header.Set("User-Agent", userAgent)
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
