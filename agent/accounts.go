package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// APIKeyFunc returns the API key set for provider t in the configuration or
// environment, or "" when none is.
type APIKeyFunc func(t ProviderID) (APIKey, error)

// AccountsConfig configures an Accounts.
type AccountsConfig struct {
	// Store holds the accounts saved by login and SetKey.
	Store AuthFile
	// Keys returns the API keys set in the configuration or environment,
	// which the config account holds. It is called whenever the accounts of
	// a provider are read. Nil means none are set.
	Keys APIKeyFunc
	// Flows holds the OAuth protocol of each provider that supports login.
	Flows map[ProviderID]LoginFlow
}

// Accounts resolves provider credentials and manages provider accounts. It
// keeps no credential in memory. Accounts is safe for concurrent use.
//
// The active account of a provider is the one auth.json marks active. When it
// marks none, or one the provider does not have, the config account is
// active if it exists, and otherwise the saved account with the lowest ID,
// which is the oldest.
type Accounts struct {
	store AuthFile
	keys  APIKeyFunc
	flows map[ProviderID]LoginFlow

	mu     sync.Mutex
	logins map[ProviderID]*login
}

// login is a login in progress.
type login struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewAccounts returns an Accounts configured by cfg.
func NewAccounts(cfg AccountsConfig) *Accounts {
	return &Accounts{
		store:  cfg.Store,
		keys:   cfg.Keys,
		flows:  maps.Clone(cfg.Flows),
		logins: make(map[ProviderID]*login),
	}
}

// For returns the Authorizer of provider t's requests.
func (s *Accounts) For(t ProviderID) Authorizer {
	return authorizer{s: s, t: t}
}

// authorizer is the Authorizer of one provider.
type authorizer struct {
	s *Accounts
	t ProviderID
}

func (a authorizer) Authorize(ctx context.Context, rejected *Authorization) (Authorization, error) {
	return a.s.Authorize(ctx, a.t, rejected)
}

// Authorize returns the credential of the active account of provider t for
// one request. It reports ErrNotAuthenticated when t has no account.
//
// An OAuth token about to expire is refreshed and saved first. A non-nil
// rejected is a credential that t refused as unauthorized: when the active
// account still holds it, its OAuth token is refreshed and saved, and a
// rejected API key is an error; when the account holds another credential,
// because another caller replaced it or the account changed, that one is
// returned. When callers in several goroutines or processes need the same
// refresh, one performs it and the others use the result.
func (s *Accounts) Authorize(ctx context.Context, t ProviderID, rejected *Authorization) (Authorization, error) {
	sel, err := s.selected(ctx, t)
	if err != nil {
		return Authorization{}, err
	}
	tok, isToken := sel.cred.(OAuthToken)
	switch {
	case rejected != nil && *rejected == sel.cred.Authorization() && !isToken:
		return Authorization{}, fmt.Errorf("%w: %s rejected the API key of account %s", ErrNotAuthenticated, t, sel.id)
	case rejected != nil && *rejected == sel.cred.Authorization():
		return s.refresh(ctx, t, sel.id, func(tok OAuthToken) bool { return tok.Access == rejected.Token })
	case isToken && tok.Expiring(time.Now()):
		return s.refresh(ctx, t, sel.id, func(tok OAuthToken) bool { return tok.Expiring(time.Now()) })
	}
	return sel.cred.Authorization(), nil
}

// Status returns the accounts of provider t and which one is active.
func (s *Accounts) Status(ctx context.Context, t ProviderID) (AuthStatus, error) {
	accts, err := s.accounts(t)
	if err != nil {
		return AuthStatus{}, err
	}
	st := AuthStatus{Provider: t}
	if sel, ok := accts.selected(); ok {
		st.Active = sel.id
	}
	if accts.key != "" {
		st.Accounts = append(st.Accounts, AccountStatus{ID: ConfigAccount, Kind: CredentialAPIKey, Source: CredentialSourceConfig})
	}
	for id, c := range accts.entry.Accounts {
		st.Accounts = append(st.Accounts, AccountStatus{ID: id, Kind: c.Authorization().Kind, Source: CredentialSourceStore})
	}
	slices.SortFunc(st.Accounts, func(x, y AccountStatus) int { return cmp.Compare(x.ID, y.ID) })
	return st, nil
}

// Login logs in to provider t with method m, saves the token as account id,
// adding the account if t has none with that ID, and makes it the active
// account. show receives what the user must do. A new login to t cancels the
// one in progress and waits for it to end, since both need the same callback
// port.
func (s *Accounts) Login(ctx context.Context, t ProviderID, id AccountID, m LoginMethod, show func(LoginPrompt)) error {
	id, err := writable(id)
	if err != nil {
		return err
	}
	flow, ok := s.flows[t]
	if !ok {
		return fmt.Errorf("login to %s: %w", t, errors.ErrUnsupported)
	}
	var run func(context.Context, func(LoginPrompt)) (OAuthToken, error)
	switch m {
	case LoginBrowser:
		run = flow.Browser
	case LoginDevice:
		run = flow.Device
	default:
		return fmt.Errorf("unknown login method %q", m)
	}

	ctx, done, err := s.begin(ctx, t)
	if err != nil {
		return err
	}
	defer done()

	tok, err := run(ctx, show)
	if err != nil {
		return err
	}
	return s.save(ctx, t, id, tok)
}

// SetKey saves key as the API key of account id of provider t, adding the
// account if t has none with that ID, and makes it the active account.
// Surrounding white space is trimmed; ErrEmptyKey reports that nothing
// remains.
func (s *Accounts) SetKey(ctx context.Context, t ProviderID, id AccountID, key APIKey) error {
	id, err := writable(id)
	if err != nil {
		return err
	}
	key = APIKey(strings.TrimSpace(string(key)))
	if key == "" {
		return ErrEmptyKey
	}
	return s.save(ctx, t, id, key)
}

// Use makes account id the active account of provider t.
func (s *Accounts) Use(ctx context.Context, t ProviderID, id AccountID) error {
	if id == ConfigAccount {
		key, err := s.configKey(t)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("%w: %s has no API key in the configuration or environment", ErrUnknownAccount, t)
		}
	}
	return s.store.Update(ctx, t, func(e *AuthEntry) error {
		if _, ok := e.Accounts[id]; !ok && id != ConfigAccount {
			return fmt.Errorf("%w: %s account %s", ErrUnknownAccount, t, id)
		}
		e.Active = id
		return nil
	})
}

// Logout removes account id of provider t. When it was active, the remaining
// saved account with the lowest ID, the oldest, becomes active; when none
// remains, the config account does if it exists.
func (s *Accounts) Logout(ctx context.Context, t ProviderID, id AccountID) error {
	if id == ConfigAccount {
		return ErrReadOnlyAccount
	}
	return s.store.Update(ctx, t, func(e *AuthEntry) error {
		if _, ok := e.Accounts[id]; !ok {
			return fmt.Errorf("%w: %s account %s", ErrUnknownAccount, t, id)
		}
		delete(e.Accounts, id)
		if e.Active == id {
			e.Active = ""
			if len(e.Accounts) > 0 {
				e.Active = slices.Min(slices.Collect(maps.Keys(e.Accounts)))
			}
		}
		return nil
	})
}

// save saves c as account id of provider t and makes it active.
func (s *Accounts) save(ctx context.Context, t ProviderID, id AccountID, c Credential) error {
	return s.store.Update(ctx, t, func(e *AuthEntry) error {
		e.Accounts[id] = c
		e.Active = id
		return nil
	})
}

// writable returns id in canonical form, or an error unless login and SetKey
// can save it.
func writable(id AccountID) (AccountID, error) {
	id, err := ParseAccountID(string(id))
	if err != nil {
		return "", err
	}
	if id == ConfigAccount {
		return "", ErrReadOnlyAccount
	}
	return id, nil
}

// accounts are the accounts of a provider: the saved ones and the key of the
// config account.
type accounts struct {
	entry AuthEntry
	key   APIKey
}

// selection is the active account of a provider.
type selection struct {
	id   AccountID
	cred Credential
}

// selected returns the active account among accts, and reports false when
// there are no accounts.
func (accts accounts) selected() (selection, bool) {
	e := accts.entry
	if e.Active == ConfigAccount && accts.key != "" {
		return selection{ConfigAccount, accts.key}, true
	}
	if c, ok := e.Accounts[e.Active]; ok {
		return selection{e.Active, c}, true
	}
	if accts.key != "" {
		return selection{ConfigAccount, accts.key}, true
	}
	if len(e.Accounts) == 0 {
		return selection{}, false
	}
	oldest := slices.Min(slices.Collect(maps.Keys(e.Accounts)))
	return selection{oldest, e.Accounts[oldest]}, true
}

// accounts reads the accounts of provider t.
func (s *Accounts) accounts(t ProviderID) (accounts, error) {
	key, err := s.configKey(t)
	if err != nil {
		return accounts{}, err
	}
	e, err := s.store.Load(t)
	if err != nil {
		return accounts{}, err
	}
	return accounts{entry: e, key: key}, nil
}

// selected returns the active account of provider t. It reports
// ErrNotAuthenticated when t has no account.
func (s *Accounts) selected(ctx context.Context, t ProviderID) (selection, error) {
	accts, err := s.accounts(t)
	if err != nil {
		return selection{}, err
	}
	sel, ok := accts.selected()
	if !ok {
		return selection{}, fmt.Errorf("%w: no %s account", ErrNotAuthenticated, t)
	}
	return sel, nil
}

// configKey returns the API key set for provider t in the configuration or
// environment.
func (s *Accounts) configKey(t ProviderID) (APIKey, error) {
	if s.keys == nil {
		return "", nil
	}
	key, err := s.keys(t)
	if err != nil {
		return "", fmt.Errorf("configured %s API key: %w", t, err)
	}
	return key, nil
}

// refresh renews the OAuth token of account id of provider t while holding
// the lock on auth.json, when stale reports that the saved token needs it.
// Holding the lock, it reads the saved token again, so a caller that waited
// for another refresh uses that result instead of refreshing a second time.
// Every other write of auth.json waits for the refresh to end, while reads
// see the token from before it until it is saved. It returns the account's
// credential after the refresh.
func (s *Accounts) refresh(ctx context.Context, t ProviderID, id AccountID, stale func(OAuthToken) bool) (Authorization, error) {
	flow, ok := s.flows[t]
	if !ok {
		return Authorization{}, fmt.Errorf("refresh %s token: %w", t, errors.ErrUnsupported)
	}
	var cred Credential
	err := s.store.Update(ctx, t, func(e *AuthEntry) error {
		c, ok := e.Accounts[id]
		if !ok {
			return fmt.Errorf("%w: %s account %s", ErrUnknownAccount, t, id)
		}
		cred = c
		tok, ok := c.(OAuthToken)
		if !ok || !stale(tok) {
			return nil
		}
		if tok.Refresh == "" {
			return fmt.Errorf("%w: the %s token of account %s expired; log in again", ErrNotAuthenticated, t, id)
		}
		fresh, err := flow.Refresh(ctx, tok)
		if err != nil {
			return fmt.Errorf("refresh %s token of account %s: %w", t, id, err)
		}
		if fresh.Refresh == "" {
			fresh.Refresh = tok.Refresh
		}
		e.Accounts[id], cred = fresh, fresh
		return nil
	})
	if err != nil {
		return Authorization{}, err
	}
	return cred.Authorization(), nil
}

// begin registers a login of provider t. It first cancels the login of t in
// progress and waits for it to end. The returned done ends the registration.
func (s *Accounts) begin(ctx context.Context, t ProviderID) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	l := &login{cancel: cancel, done: make(chan struct{})}

	s.mu.Lock()
	prev := s.logins[t]
	s.logins[t] = l
	s.mu.Unlock()

	done := func() {
		cancel()
		s.mu.Lock()
		if s.logins[t] == l {
			delete(s.logins, t)
		}
		s.mu.Unlock()
		close(l.done)
	}
	if prev != nil {
		prev.cancel()
		<-prev.done
	}
	if err := ctx.Err(); err != nil {
		done()
		return nil, nil, err
	}
	return ctx, done, nil
}
