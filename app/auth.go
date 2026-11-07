package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/unaquatic/flow/agent"
)

// Accounts are the accounts of the providers the app sends requests to,
// which App.Accounts returns: their status, logging in and out, saving a key,
// and choosing the account a provider uses. Each change publishes
// EventAccountsChanged.
type Accounts struct{ app *App }

// Accounts returns the app's accounts.
func (a *App) Accounts() Accounts { return Accounts{app: a} }

// Providers returns the IDs of the registered providers, in ID order.
func (c Accounts) Providers() []agent.ProviderID {
	return c.app.providers.Providers()
}

// Status returns the accounts of provider t and which one is active.
func (c Accounts) Status(ctx context.Context, t agent.ProviderID) (agent.AuthStatus, error) {
	if err := c.app.registered(t); err != nil {
		return agent.AuthStatus{}, err
	}
	return c.app.accounts.Status(ctx, t)
}

// Login logs in to provider t with method m and, once the login ends,
// returns the ID of the account it saved, which becomes active. An empty id
// adds a new account; otherwise id must be a saved account of t, whose
// credential the login replaces. Once the login starts, Login publishes
// EventLoginStarted, EventLoginPrompt with what the user must do, and
// EventLoginSucceeded followed by EventAccountsChanged, or EventLoginFailed.
func (c Accounts) Login(ctx context.Context, t agent.ProviderID, id agent.AccountID, m agent.LoginMethod) (agent.AccountID, error) {
	id, err := c.app.target(ctx, t, id)
	if err != nil {
		return "", err
	}
	return id, c.app.login(ctx, t, id, m)
}

// StartLogin checks t and id like Login and returns the ID of the account to
// be saved, then logs in from a new goroutine, which ends at the latest when
// the app closes. The login's progress and outcome arrive as events.
func (c Accounts) StartLogin(t agent.ProviderID, id agent.AccountID, m agent.LoginMethod) (agent.AccountID, error) {
	id, err := c.app.target(c.app.workerCtx, t, id)
	if err != nil {
		return "", err
	}
	c.app.wg.Go(func() {
		if err := c.app.login(c.app.workerCtx, t, id, m); err != nil {
			slog.Info("login", "provider", t, "account", id, "err", err)
		}
	})
	return id, nil
}

// login logs in to account id of provider t and publishes its events.
func (a *App) login(ctx context.Context, t agent.ProviderID, id agent.AccountID, m agent.LoginMethod) error {
	a.publish(EventLoginStarted{EventBase: a.eventBase(), Provider: t, Account: id, Method: m})
	show := func(p agent.LoginPrompt) {
		a.publish(EventLoginPrompt{EventBase: a.eventBase(), Provider: t, Account: id, URL: p.URL, Code: p.Code})
	}
	if err := a.accounts.Login(ctx, t, id, m, show); err != nil {
		a.publishLoginFailed(t, id, err)
		return err
	}
	a.publish(EventLoginSucceeded{EventBase: a.eventBase(), Provider: t, Account: id})
	a.publishAccountsChanged(ctx, t)
	return nil
}

// SetKey saves key as an API key of provider t and returns the ID of the
// account it saved, which becomes active. An empty id adds a new account;
// otherwise id must be a saved account of t, whose credential SetKey replaces.
func (c Accounts) SetKey(ctx context.Context, t agent.ProviderID, id agent.AccountID, key agent.APIKey) (agent.AccountID, error) {
	id, err := c.app.target(ctx, t, id)
	if err != nil {
		return "", err
	}
	if _, err := c.app.changeAccounts(ctx, t, func(accounts *agent.Accounts) error { return accounts.SetKey(ctx, t, id, key) }); err != nil {
		return "", err
	}
	return id, nil
}

// Use makes account id the active account of provider t and returns the
// resulting status.
func (c Accounts) Use(ctx context.Context, t agent.ProviderID, id agent.AccountID) (agent.AuthStatus, error) {
	return c.app.changeAccounts(ctx, t, func(accounts *agent.Accounts) error { return accounts.Use(ctx, t, id) })
}

// Logout removes account id of provider t and returns the resulting status,
// which reports the account that became active in its place.
func (c Accounts) Logout(ctx context.Context, t agent.ProviderID, id agent.AccountID) (agent.AuthStatus, error) {
	return c.app.changeAccounts(ctx, t, func(accounts *agent.Accounts) error { return accounts.Logout(ctx, t, id) })
}

// target returns the account that a login or key of provider t saves to: a
// new account when id is empty, and otherwise id, which must be a saved
// account of t.
func (a *App) target(ctx context.Context, t agent.ProviderID, id agent.AccountID) (agent.AccountID, error) {
	if err := a.registered(t); err != nil {
		return "", err
	}
	if id == "" {
		return agent.NewAccountID(), nil
	}
	id, err := agent.ParseAccountID(string(id))
	if err != nil {
		return "", err
	}
	if id == agent.ConfigAccount {
		return "", agent.ErrReadOnlyAccount
	}
	st, err := a.accounts.Status(ctx, t)
	if err != nil {
		return "", err
	}
	saved := func(s agent.AccountStatus) bool { return s.ID == id && s.Source == agent.CredentialSourceStore }
	if !slices.ContainsFunc(st.Accounts, saved) {
		return "", fmt.Errorf("%w: %s account %s", agent.ErrUnknownAccount, t, id)
	}
	return id, nil
}

// changeAccounts applies change to the accounts of provider t, publishes
// EventAccountsChanged, and returns the resulting status.
func (a *App) changeAccounts(ctx context.Context, t agent.ProviderID, change func(*agent.Accounts) error) (agent.AuthStatus, error) {
	if err := a.registered(t); err != nil {
		return agent.AuthStatus{}, err
	}
	if err := change(a.accounts); err != nil {
		return agent.AuthStatus{}, err
	}
	st, err := a.accounts.Status(ctx, t)
	if err != nil {
		return agent.AuthStatus{}, err
	}
	a.publish(EventAccountsChanged{EventBase: a.eventBase(), Provider: t, Active: st.Active})
	return st, nil
}

// registered reports an error unless provider t is registered.
func (a *App) registered(t agent.ProviderID) error {
	if _, ok := a.providers.Get(t); !ok {
		return fmt.Errorf("%w: %s", agent.ErrProviderNotRegistered, t)
	}
	return nil
}

func (a *App) publishLoginFailed(pt agent.ProviderID, id agent.AccountID, err error) {
	a.publish(EventLoginFailed{EventBase: a.eventBase(), Provider: pt, Account: id, Error: NewUnknownError(err.Error())})
}

// publishAccountsChanged publishes the active account of provider t.
func (a *App) publishAccountsChanged(ctx context.Context, t agent.ProviderID) {
	st, err := a.accounts.Status(ctx, t)
	if err != nil {
		slog.Warn("auth status", "provider", t, "err", err)
		return
	}
	a.publish(EventAccountsChanged{EventBase: a.eventBase(), Provider: t, Active: st.Active})
}
