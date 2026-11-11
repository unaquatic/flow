// Package adc authorizes requests to a cloud's AI platform with OAuth
// access tokens from its application default credentials: the file that
// CredentialsEnv names, or the one that LoginCommand writes.
package adc

import (
	"cmp"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/unaquatic/flow/agent"
)

// DefaultTokenURL is the cloud's OAuth token endpoint.
const DefaultTokenURL = "https://oauth2.googleapis.com/token"

// Scope is the OAuth scope of the tokens Credentials makes, which covers
// the cloud's AI platform.
const Scope = "https://www.googleapis.com/auth/cloud-platform"

// CredentialsEnv is the environment variable that names the application
// default credentials file.
const CredentialsEnv = "GOOGLE_APPLICATION_CREDENTIALS"

// LoginCommand is the command of the cloud's CLI that writes the user's
// application default credentials, in ConfigDir.
const LoginCommand = "gcloud auth application-default login"

// ConfigDir is the name of the cloud CLI's configuration directory, in the
// user's configuration directory.
const ConfigDir = "gcloud"

// QuotaHeader is the request header that names the project that quota and
// billing go to.
const QuotaHeader = "X-Goog-User-Project"

// expiryMargin is how long before a token expires that Credentials makes a
// new one, so that no request goes with a token about to expire.
const expiryMargin = time.Minute

// File is an application default credentials file: a service account's
// key, or a user's refresh token.
type File struct {
	// Type is "service_account" or "authorized_user".
	Type string `json:"type"`

	// ClientEmail, PrivateKey and PrivateKeyID are a service account's.
	ClientEmail  string `json:"client_email,omitempty"`
	PrivateKey   string `json:"private_key,omitempty"`
	PrivateKeyID string `json:"private_key_id,omitempty"`
	// ProjectID is the project of a service account.
	ProjectID string `json:"project_id,omitempty"`

	// ClientID, ClientSecret and RefreshToken are a user's.
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	// TokenURI is where tokens are made, DefaultTokenURL when empty.
	TokenURI string `json:"token_uri,omitempty"`
	// QuotaProject is the project that quota and billing go to, which
	// requests made with a user's credentials name.
	QuotaProject string `json:"quota_project_id,omitempty"`
}

// Credentials authorizes requests with OAuth access tokens made from the
// application default credentials of the environment, which it reads anew
// for every request, so that a new login takes effect at once. It keeps a
// token until it is about to expire. It is safe for concurrent use.
type Credentials struct {
	// Env reads environment variables. Nil reads the process's.
	Env func(string) string
	// Client sends the token requests. Nil is http.DefaultClient.
	Client *http.Client
	// Now returns the current time. Nil is time.Now.
	Now func() time.Time

	mu sync.Mutex
	// token is the last token made, for the credentials file as source
	// names it.
	token  string
	expiry time.Time
	source string
}

// Path returns the application default credentials file: the one
// CredentialsEnv names, or else the cloud CLI's, in CLOUDSDK_CONFIG or the
// user's ConfigDir. It returns "" when there is no such file.
func (c *Credentials) Path() string {
	env := c.env()
	path := env(CredentialsEnv)
	if path == "" {
		dir := env("CLOUDSDK_CONFIG")
		if dir == "" {
			home := cmp.Or(env("HOME"), func() string { h, _ := os.UserHomeDir(); return h }())
			dir = filepath.Join(home, ".config", ConfigDir)
			if runtime.GOOS == "windows" && env("APPDATA") != "" {
				dir = filepath.Join(env("APPDATA"), ConfigDir)
			}
		}
		path = filepath.Join(dir, "application_default_credentials.json")
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Available reports whether the environment holds application default
// credentials.
func (c *Credentials) Available() bool { return c.Path() != "" }

// Load returns the application default credentials file, as Path finds it.
func (c *Credentials) Load() (File, string, error) {
	path := c.Path()
	if path == "" {
		return File{}, "", fmt.Errorf("%w: no application default credentials: run %q or set %s", agent.ErrNotAuthenticated, LoginCommand, CredentialsEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, "", fmt.Errorf("%w: read %s: %w", agent.ErrNotAuthenticated, path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, "", fmt.Errorf("%w: decode %s: %w", agent.ErrNotAuthenticated, path, err)
	}
	return f, path + "\x00" + string(data), nil
}

// Authorize returns an OAuth access token, as a agent.CredentialOAuth,
// made from the application default credentials: the token it made last,
// unless that is about to expire, was made from other credentials, or is
// rejected, or else a new one. It fails with agent.ErrNotAuthenticated
// when there are no credentials, or no token can be made from them.
func (c *Credentials) Authorize(ctx context.Context, rejected *agent.Authorization) (agent.Authorization, error) {
	f, source, err := c.Load()
	if err != nil {
		return agent.Authorization{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.token != "" && c.source == source && now.Add(expiryMargin).Before(c.expiry) &&
		(rejected == nil || rejected.Token != c.token) {
		return agent.Authorization{Kind: agent.CredentialOAuth, Token: c.token}, nil
	}
	tok, ttl, err := c.mint(ctx, f, now)
	if err != nil {
		return agent.Authorization{}, fmt.Errorf("%w: access token: %w", agent.ErrNotAuthenticated, err)
	}
	c.token, c.expiry, c.source = tok, now.Add(ttl), source
	return agent.Authorization{Kind: agent.CredentialOAuth, Token: tok}, nil
}

// mint makes a new access token from f at time now, and returns it with how
// long it lasts.
func (c *Credentials) mint(ctx context.Context, f File, now time.Time) (string, time.Duration, error) {
	form := url.Values{}
	switch f.Type {
	case "service_account":
		jwt, err := Assertion(f, now)
		if err != nil {
			return "", 0, err
		}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		form.Set("assertion", jwt)
	case "authorized_user":
		form.Set("grant_type", "refresh_token")
		form.Set("client_id", f.ClientID)
		form.Set("client_secret", f.ClientSecret)
		form.Set("refresh_token", f.RefreshToken)
	default:
		return "", 0, fmt.Errorf("unsupported credential type: %s; use a service account key or run %s", f.Type, LoginCommand)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cmp.Or(f.TokenURI, DefaultTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", 0, fmt.Errorf("token endpoint answered %d: %s", resp.StatusCode, cmp.Or(strings.TrimSpace(out.Error+": "+out.Description), string(raw)))
	}
	return out.AccessToken, time.Duration(cmp.Or(out.ExpiresIn, 3600)) * time.Second, nil
}

// Assertion returns the signed JWT with which service account f asks for a
// token at time now, valid for an hour.
func Assertion(f File, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(f.PrivateKey))
	if block == nil {
		return "", errors.New("the service account's private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("the service account's private key is not RSA")
		}
		key = rk
	} else if key, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return "", fmt.Errorf("parse the service account's private key: %w", err)
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if f.PrivateKeyID != "" {
		header["kid"] = f.PrivateKeyID
	}
	claims := map[string]any{
		"iss": f.ClientEmail, "scope": Scope, "aud": cmp.Or(f.TokenURI, DefaultTokenURL),
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	signing := enc(header) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Transport returns an http.RoundTripper that sends requests with base, or
// http.DefaultTransport when base is nil, naming the quota project of the
// application default credentials, when they have one, in QuotaHeader, as
// the cloud's client libraries do.
func (c *Credentials) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return TransportFunc(func(req *http.Request) (*http.Response, error) {
		f, _, err := c.Load()
		if err != nil || f.QuotaProject == "" || req.Header.Get(QuotaHeader) != "" {
			return base.RoundTrip(req)
		}
		out := req.Clone(req.Context())
		out.Header.Set(QuotaHeader, f.QuotaProject)
		return base.RoundTrip(out)
	})
}

// TransportFunc is an http.RoundTripper of a function.
type TransportFunc func(*http.Request) (*http.Response, error)

// RoundTrip returns f(req).
func (f TransportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// env returns c.Env, or os.Getenv when it is nil.
func (c *Credentials) env() func(string) string {
	if c.Env == nil {
		return os.Getenv
	}
	return c.Env
}

// now returns the current time, as c.Now gives it.
func (c *Credentials) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}
