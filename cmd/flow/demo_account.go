package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
)

// The providers and accounts of the demo app, as newDemoApp makes it.
const (
	// demoLoginProvider takes a login in a browser or with a device code,
	// and holds demoAccount, which is active, and demoKeyAccount.
	demoLoginProvider agent.ProviderID = "provider-a"
	// demoKeyProvider takes an API key alone, and holds no account.
	demoKeyProvider agent.ProviderID = "provider-b"
	// demoAccount is the account of demoLoginProvider that a login saved.
	demoAccount agent.AccountID = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e01"
	// demoKeyAccount is the account of demoLoginProvider that an API key
	// saved.
	demoKeyAccount agent.AccountID = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e02"
	// demoKey is the API key that the key login demo types.
	demoKey = "sk-demo-0123456789"
	// demoNowhere is the catalog server of the demo app, where no server
	// listens, so that it downloads nothing.
	demoNowhere = "http://127.0.0.1:1"
)

// withDemoApp returns the run of a demo case that passes run a demo app, as
// newDemoApp makes it, and closes it once run returns.
func withDemoApp(run func(ctx context.Context, w io.Writer, a *app.App) error) func(context.Context, io.Writer, bool) error {
	return func(ctx context.Context, w io.Writer, _ bool) error {
		a, closeApp, err := newDemoApp(ctx)
		if err != nil {
			return err
		}
		defer closeApp()
		return run(ctx, w, a)
	}
}

// newDemoApp returns an app over a new temporary data directory, which
// downloads no catalog and loads no extension, with demoLoginProvider and
// demoKeyProvider in place of the catalog's providers, demoLoginProvider's
// login flow a demoLoginFlow, and its accounts saved, and the func that
// closes it and removes the directory. Nothing of the user's is read or
// changed.
func newDemoApp(ctx context.Context) (*app.App, func(), error) {
	dir, err := os.MkdirTemp("", product.ID+"-demo-")
	if err != nil {
		return nil, nil, err
	}
	a, err := openDemoApp(ctx, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	return a, func() { a.Close(); os.RemoveAll(dir) }, nil
}

// openDemoApp opens the demo app of newDemoApp over data directory dir.
func openDemoApp(ctx context.Context, dir string) (*app.App, error) {
	settings := `{"snapshot":false,"catalog_source":"` + demoNowhere + `"}`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(settings), 0o600); err != nil {
		return nil, err
	}
	accounts := agent.NewAccounts(agent.AccountsConfig{
		Store: agent.AuthFile{Dir: dir},
		Flows: map[agent.ProviderID]agent.LoginFlow{demoLoginProvider: demoLoginFlow{}},
	})
	err := errors.Join(
		accounts.Login(ctx, demoLoginProvider, demoAccount, agent.LoginDevice, func(agent.LoginPrompt) {}),
		accounts.SetKey(ctx, demoLoginProvider, demoKeyAccount, demoKey),
		accounts.Use(ctx, demoLoginProvider, demoAccount),
	)
	if err != nil {
		return nil, err
	}
	providers := agent.NewProviders(
		agent.ProviderOptions{Catalog: gateway.NewStore(gateway.StoreOptions{Dir: dir, Source: demoNowhere})},
		demoProvider(demoLoginProvider), demoProvider(demoKeyProvider),
	)
	return app.New(ctx, config.NewService(dir), app.Options{Accounts: accounts, Providers: providers, BuiltIn: []agent.ProviderID{}})
}

// demoProvider is a provider of the demo app, by its ID, whose models
// answer no request.
type demoProvider agent.ProviderID

// Name returns p's ID.
func (p demoProvider) Name() string { return string(p) }

// Model returns a model that fails each request with errors.ErrUnsupported.
func (p demoProvider) Model(string) agent.Model { return demoModel{} }

// demoModel is the model of every demoProvider.
type demoModel struct{}

// Stream fails with errors.ErrUnsupported.
func (demoModel) Stream(context.Context, agent.Request) (agent.StreamResponse, error) {
	return nil, errors.ErrUnsupported
}

// demoLoginFlow is the login flow of demoLoginProvider: it shows the prompt
// of a real one and is approved at once, with demoToken.
type demoLoginFlow struct{}

// demoToken is the OAuth token every demoLoginFlow login gets.
var demoToken = agent.OAuthToken{Access: "demo-access-token", Refresh: "demo-refresh-token"}

// Device shows a device code to enter at a login page, and is approved.
func (demoLoginFlow) Device(_ context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	show(agent.LoginPrompt{URL: "https://login.example/device", Code: "WDJB-MJHT"})
	return demoToken, nil
}

// Browser shows the authorization page, and is approved.
func (demoLoginFlow) Browser(_ context.Context, show func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	show(agent.LoginPrompt{URL: "https://login.example/authorize"})
	return demoToken, nil
}

// Refresh returns t as it is.
func (demoLoginFlow) Refresh(_ context.Context, t agent.OAuthToken) (agent.OAuthToken, error) {
	return t, nil
}

// accountDemos are the cases of flow account, each on a demo app of its own.
var accountDemos = []demoCase{
	{name: "list", desc: "Every provider's accounts, the active one marked", line: "account",
		run: withDemoApp(func(ctx context.Context, w io.Writer, a *app.App) error { return listAccounts(ctx, w, a) })},
	{name: "current", desc: "The active account of each provider that has one", line: "account current",
		run: accountDemo(runAccountCurrent)},
	{name: "current-none", desc: "The active account of a provider that has none", line: "account current " + string(demoKeyProvider),
		run: accountDemo(runAccountCurrent, string(demoKeyProvider))},
	{name: "use", desc: "Another account made the active one", line: "account use " + string(demoLoginProvider) + " " + string(demoKeyAccount),
		run: accountDemo(runAccountUse, string(demoLoginProvider), string(demoKeyAccount))},
	{name: "use-invalid", desc: "An account ID that is not one", line: "account use " + string(demoLoginProvider) + " 123",
		run: accountDemo(runAccountUse, string(demoLoginProvider), "123")},
}

// accountDemo returns the run of a case of flow account that runs run on a
// demo app with args.
func accountDemo(run func(context.Context, io.Writer, *app.App, cmdline.NoOptions, []string) error, args ...string) func(context.Context, io.Writer, bool) error {
	return withDemoApp(func(ctx context.Context, w io.Writer, a *app.App) error {
		return run(ctx, w, a, cmdline.NoOptions{}, args)
	})
}

// loginDemos are the cases of flow login, each on a demo app of its own.
var loginDemos = []demoCase{
	{name: "browser", desc: "A new account, logged in to in the browser", line: "login --provider " + string(demoLoginProvider),
		run: loginDemo(loginOptions{method: loginMethod(agent.LoginBrowser), provider: demoLoginProvider}, "")},
	{name: "device", desc: "A saved account, logged in to again with a device code", line: "login --provider " + string(demoLoginProvider) + " --method device --account " + string(demoAccount),
		run: loginDemo(loginOptions{method: loginMethod(agent.LoginDevice), provider: demoLoginProvider, account: demoAccount}, "")},
	{name: "key", desc: "An API key, typed in", line: "login --provider " + string(demoKeyProvider) + " --method key",
		run: loginDemo(loginOptions{method: keyMethod, provider: demoKeyProvider}, demoKey+"\n")},
	{name: "no-login", desc: "A provider that takes an API key alone", line: "login --provider " + string(demoKeyProvider),
		run: loginDemo(loginOptions{method: loginMethod(agent.LoginBrowser), provider: demoKeyProvider}, "")},
	{name: "no-provider", desc: "No provider named", line: "login",
		run: func(ctx context.Context, _ io.Writer, _ bool) error { return loginCmd.Run(ctx, loginOptions{}, nil) }},
}

// loginDemo returns the run of a case of flow login with options o on a
// demo app, which reads input as typed in: echoed after the prompt, as the
// terminal shows it.
func loginDemo(o loginOptions, input string) func(context.Context, io.Writer, bool) error {
	return withDemoApp(func(ctx context.Context, w io.Writer, a *app.App) error {
		return login(ctx, w, io.TeeReader(strings.NewReader(input), w), a, o)
	})
}

// logoutDemos are the cases of flow logout, each on a demo app of its own.
var logoutDemos = []demoCase{
	{name: "active", desc: "The active account, another taking its place", line: "logout --provider " + string(demoLoginProvider) + " --account " + string(demoAccount),
		run: logoutDemo(demoAccount)},
	{name: "last", desc: "The last account of its provider", line: "logout --provider " + string(demoLoginProvider) + " --account " + string(demoAccount),
		run: logoutDemo(demoAccount, demoKeyAccount)},
	{name: "no-account", desc: "No account named", line: "logout --provider " + string(demoLoginProvider),
		run: func(ctx context.Context, _ io.Writer, _ bool) error {
			return logoutCmd.Run(ctx, logoutOptions{provider: demoLoginProvider}, nil)
		}},
}

// logoutDemo returns the run of a case of flow logout that removes account
// id of demoLoginProvider on a demo app, once the accounts of before are
// gone.
func logoutDemo(id agent.AccountID, before ...agent.AccountID) func(context.Context, io.Writer, bool) error {
	return withDemoApp(func(ctx context.Context, w io.Writer, a *app.App) error {
		for _, gone := range before {
			if _, err := a.Accounts().Logout(ctx, demoLoginProvider, gone); err != nil {
				return err
			}
		}
		return runLogout(ctx, w, a, logoutOptions{provider: demoLoginProvider, account: id}, nil)
	})
}
