package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"
)

var (
	// ErrEmptyKey reports an API key that is empty or all white space.
	ErrEmptyKey = errors.New("empty API key")

	// ErrInvalidAccountID reports an account ID that is not a UUID.
	ErrInvalidAccountID = errors.New("invalid account id")

	// ErrUnknownAccount reports an account that the provider does not have.
	ErrUnknownAccount = errors.New("unknown account")

	// ErrReadOnlyAccount reports a change to the config account, which the
	// configuration and environment define.
	ErrReadOnlyAccount = errors.New("the config account is set in the configuration or environment")

	// ErrNoAuthCode reports an OAuth redirect without an authorization code.
	ErrNoAuthCode = errors.New("no authorization code provided")

	// ErrInvalidLoginCode reports malformed input that the user can correct
	// without restarting the pending login.
	ErrInvalidLoginCode = errors.New("invalid authorization code")

	// ErrDeviceCodeExpired reports a device login that was not approved in
	// time.
	ErrDeviceCodeExpired = errors.New("device code expired")

	// ErrTokenEndpoint reports a failed OAuth token request.
	ErrTokenEndpoint = errors.New("token endpoint error")
)

// Credential is a credential saved for a provider: an [APIKey] or an
// [OAuthToken].
type Credential interface {
	// Authorization returns the credential in the form sent with a request.
	Authorization() Authorization

	credential()
}

// APIKey is a static provider API key.
type APIKey string

// Authorization returns k as an API-key authorization.
func (k APIKey) Authorization() Authorization {
	return Authorization{Kind: CredentialAPIKey, Token: string(k)}
}

func (APIKey) credential() {}

// refreshMargin is how long before its expiry an OAuth token is renewed.
const refreshMargin = 30 * time.Second

// OAuthToken is an OAuth access token and the refresh token that renews it.
type OAuthToken struct {
	// Access is sent with requests.
	Access string
	// Refresh renews Access. It is empty when the server issued none.
	Refresh string
	// Expiry is when Access expires. The zero time means it does not.
	Expiry time.Time
}

// Authorization returns t as an OAuth authorization.
func (t OAuthToken) Authorization() Authorization {
	return Authorization{Kind: CredentialOAuth, Token: t.Access}
}

func (OAuthToken) credential() {}

// Expiring reports whether t expires within 30 seconds of now.
func (t OAuthToken) Expiring(now time.Time) bool {
	return !t.Expiry.IsZero() && now.After(t.Expiry.Add(-refreshMargin))
}

// LoginMethod selects an OAuth login flow.
type LoginMethod string

const (
	// LoginBrowser logs in through the browser and a local redirect.
	LoginBrowser LoginMethod = "browser"
	// LoginDevice logs in with a code entered on another device, for headless
	// and SSH sessions.
	LoginDevice LoginMethod = "device"
)

// ParseLoginMethod returns the LoginMethod named s.
func ParseLoginMethod(s string) (LoginMethod, error) {
	switch m := LoginMethod(s); m {
	case LoginBrowser, LoginDevice:
		return m, nil
	}
	return "", fmt.Errorf("unknown login method %q", s)
}

// UnmarshalText sets m to the LoginMethod named by text.
func (m *LoginMethod) UnmarshalText(text []byte) error {
	parsed, err := ParseLoginMethod(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// LoginPrompt is what the user must do to finish a login.
type LoginPrompt struct {
	// URL is the page to open.
	URL string
	// Code is the code to enter on the page. The browser flow has none.
	Code string
	// State holds the parameters needed to complete a manual browser login.
	State *LoginState `json:"state,omitempty"`
}

// LoginState is the temporary proof of a manual OAuth authorization attempt.
// The caller keeps it until completion or cancellation; it must not be persisted.
type LoginState struct {
	// Verifier is the PKCE secret used for the token exchange.
	Verifier string `json:"verifier"`
	// State is the nonce that must match the authorization result.
	State string `json:"state"`
}

// String returns the instruction for the user.
func (p LoginPrompt) String() string {
	if p.Code == "" {
		return "open " + p.URL + " in your browser"
	}
	return "open " + p.URL + " and enter code " + p.Code
}

// AccountID identifies one account of a provider. It is a UUID in canonical
// form, lowercase with hyphens. Accounts that login and SetKey add get a
// version 7 UUID, so their IDs sort in the order they were added.
type AccountID string

// ConfigAccount is the ID of the read-only account holding the API key set in
// the configuration or environment: the Nil UUID, which no added account gets.
const ConfigAccount AccountID = "00000000-0000-0000-0000-000000000000"

// NewAccountID returns the ID of a new account, a version 7 UUID.
func NewAccountID() AccountID {
	return AccountID(uuid.NewV7().String())
}

// ParseAccountID returns the account ID that s holds in any form uuid.Parse
// accepts, in canonical form.
func ParseAccountID(s string) (AccountID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w %q", ErrInvalidAccountID, s)
	}
	return AccountID(u.String()), nil
}

// MarshalText returns id as text, the counterpart of UnmarshalText.
func (id *AccountID) MarshalText() ([]byte, error) {
	return []byte(*id), nil
}

// UnmarshalText sets id to the account ID that text holds.
func (id *AccountID) UnmarshalText(text []byte) error {
	parsed, err := ParseAccountID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// CredentialSource identifies where the credential of an account comes from.
type CredentialSource string

const (
	// CredentialSourceConfig is an API key set in the configuration files or the
	// environment, which the config account holds.
	CredentialSourceConfig CredentialSource = "config"
	// CredentialSourceStore is a credential saved in auth.json by login or SetKey.
	CredentialSourceStore CredentialSource = "store"
)

// AccountStatus describes one account of a provider.
type AccountStatus struct {
	// ID identifies the account.
	ID AccountID
	// Kind is the kind of the account's credential.
	Kind CredentialKind
	// Source is where the credential comes from.
	Source CredentialSource
}

// AuthStatus is the authentication state of a provider.
type AuthStatus struct {
	// Provider identifies the provider.
	Provider ProviderID
	// Active identifies the account whose credential requests use, or is
	// empty when the provider has no account.
	Active AccountID
	// Accounts lists the provider's accounts by ID.
	Accounts []AccountStatus
}

// Authenticated reports whether the provider has an active account.
func (s AuthStatus) Authenticated() bool { return s.Active != "" }

// LoginFlow is the OAuth protocol of one provider.
type LoginFlow interface {
	// Device logs in with a device code. It passes the page and the code to
	// enter to show, then polls until the login is approved or expires, or
	// ctx is done.
	Device(ctx context.Context, show func(LoginPrompt)) (OAuthToken, error)

	// Refresh renews t. The returned token may lack a refresh token when
	// the server keeps the old one valid.
	Refresh(ctx context.Context, t OAuthToken) (OAuthToken, error)
}

// BrowserLoginFlow receives an automatic browser redirect to finish a login.
type BrowserLoginFlow interface {
	// Browser shows the authorization page and waits for its redirect.
	Browser(context.Context, func(LoginPrompt)) (OAuthToken, error)
}

// CodeLoginFlow completes browser authorization from a pasted result. Neither
// method keeps a pending operation; the caller owns the returned state.
type CodeLoginFlow interface {
	// BeginBrowser creates the authorization URL and its temporary proof.
	BeginBrowser() LoginPrompt
	// CompleteBrowser validates the result and exchanges it for credentials.
	CompleteBrowser(context.Context, LoginState, string) (OAuthToken, error)
}
