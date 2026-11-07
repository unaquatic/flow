// Package backendflow logs in to the provider ProviderID names with its
// browser and device sign-in, and says where the requests that its tokens
// authorize go: Responses requests, which package responses sends, to the
// provider's backend at BaseURL rather than its platform API.
package backendflow

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
	"github.com/unaquatic/flow/product"
)

// ProviderID identifies, by its catalog ID, the provider whose accounts
// this login signs in to.
const ProviderID agent.ProviderID = "openai"

// BaseURL is the base URL of the backend's Responses API, which this
// login's requests reach instead of the provider's platform API.
const BaseURL = "https://chatgpt.com/backend-api/codex"

const (
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	issuer       = "https://auth.openai.com"
	oauthScopes  = "openid profile email offline_access"
	callbackPort = 1455
)

// userAgent identifies the application in OAuth requests.
var userAgent = product.Name + "/" + product.Version()

// deviceLoginTimeout bounds how long a device login waits for approval.
const deviceLoginTimeout = 15 * time.Minute

// OAuth implements agent.LoginFlow for the provider's sign-in.
type OAuth struct {
	// ClientID identifies the OAuth client.
	ClientID string
	// Issuer is the OAuth server base URL.
	Issuer string
	// Scopes contains requested OAuth scopes.
	Scopes []string
	// CallbackPort is the local port of the browser redirect.
	CallbackPort int
	// Client sends OAuth requests. Nil means http.DefaultClient.
	Client *http.Client
}

// NewOAuth returns an OAuth using the provider's endpoints.
func NewOAuth() *OAuth {
	return &OAuth{
		ClientID:     clientID,
		Issuer:       issuer,
		Scopes:       strings.Fields(oauthScopes),
		CallbackPort: callbackPort,
	}
}

// Browser logs in through the browser and a redirect to the local port.
func (o *OAuth) Browser(ctx context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	pkce := agent.NewPKCE()
	state := agent.NewOAuthState()
	cb, err := agent.ListenOAuthCallback(o.CallbackPort, "/auth/callback", state)
	if err != nil {
		return agent.OAuthToken{}, err
	}

	authURL := o.Issuer + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {o.ClientID},
		"redirect_uri":          {cb.RedirectURI},
		"scope":                 {strings.Join(o.Scopes, " ")},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()
	show(agent.LoginPrompt{URL: authURL})
	if err := agent.OpenBrowser(authURL); err != nil {
		slog.Info("open browser", "err", err)
	}

	code, err := cb.Wait(ctx)
	if err != nil {
		return agent.OAuthToken{}, err
	}
	return o.exchange(ctx, code, pkce.Verifier, cb.RedirectURI)
}

// Device logs in with a code entered at the device page of the issuer.
func (o *OAuth) Device(ctx context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	var dc struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     int    `json:"interval"`
	}
	if err := o.postJSON(ctx, "/api/accounts/deviceauth/usercode", map[string]string{"client_id": o.ClientID}, &dc); err != nil {
		return agent.OAuthToken{}, fmt.Errorf("device code request: %w", err)
	}
	show(agent.LoginPrompt{URL: o.Issuer + "/codex/device", Code: dc.UserCode})

	interval := time.Duration(dc.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, deviceLoginTimeout)
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

		grant, err := o.pollDevice(ctx, dc.DeviceAuthID, dc.UserCode)
		if errors.Is(err, errDevicePending) {
			continue
		}
		if err != nil {
			return agent.OAuthToken{}, err
		}
		return o.exchange(ctx, grant.AuthorizationCode, grant.CodeVerifier, o.Issuer+"/deviceauth/callback")
	}
}

// Refresh renews t with its refresh token.
func (o *OAuth) Refresh(ctx context.Context, t agent.OAuthToken) (agent.OAuthToken, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": t.Refresh,
		"client_id":     o.ClientID,
	})
	if err != nil {
		return agent.OAuthToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Issuer+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return agent.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return agent.RequestOAuthToken(o.client(), req)
}

// errDevicePending reports a device login that is not approved yet.
var errDevicePending = errors.New("authorization pending")

// deviceGrant is the result of an approved device login.
type deviceGrant struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

// pollDevice checks once whether the device login was approved.
func (o *OAuth) pollDevice(ctx context.Context, deviceAuthID, userCode string) (deviceGrant, error) {
	var grant deviceGrant
	err := o.postJSON(ctx, "/api/accounts/deviceauth/token", map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}, &grant)
	if statusErr, ok := errors.AsType[*statusError](err); ok &&
		(statusErr.code == http.StatusForbidden || statusErr.code == http.StatusNotFound) {
		return deviceGrant{}, errDevicePending
	}
	return grant, err
}

// exchange trades an authorization code for a token.
func (o *OAuth) exchange(ctx context.Context, code, verifier, redirectURI string) (agent.OAuthToken, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {o.ClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return agent.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	tok, err := agent.RequestOAuthToken(o.client(), req)
	if err != nil {
		return agent.OAuthToken{}, fmt.Errorf("token exchange: %w", err)
	}
	return tok, nil
}

// statusError reports an unsuccessful response of the issuer.
type statusError struct {
	code int
	body []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%v (%d): %s", agent.ErrTokenEndpoint, e.code, e.body)
}

func (e *statusError) Unwrap() error { return agent.ErrTokenEndpoint }

// postJSON posts in as JSON to path on the issuer and decodes the response
// into out.
func (o *OAuth) postJSON(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Issuer+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode, body: data}
	}
	return json.Unmarshal(data, out)
}

// client returns the HTTP client for OAuth requests.
func (o *OAuth) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return http.DefaultClient
}
