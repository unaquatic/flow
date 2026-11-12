package app

import (
	"maps"
	"os"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/internal/catalog"
)

// newAccounts returns the accounts of the providers, which authorize their
// requests from the auth.json file in the data directory of conf, the
// configuration's API keys and the environment variables the catalog that
// cat returns names for each provider, all read anew for every request,
// with the login flows that the extension.LoginHook of each of exts adds, in
// order.
func newAccounts(conf *config.Service, cat func() *gateway.Catalog, exts []extension.Extension) *agent.Accounts {
	flows := make(map[agent.ProviderID]agent.LoginFlow)
	for h := range extension.Hooks[extension.LoginHook](exts) {
		maps.Copy(flows, h.LoginFlows())
	}
	return agent.NewAccounts(agent.AccountsConfig{
		Store: agent.AuthFile{Dir: conf.Dir},
		Keys:  configKeys(conf, cat),
		Flows: flows,
	})
}

// newProviders returns the providers of the catalog in store, with the
// models file of overlay over it, whose request formats an
// extension.DialHook of exts speaks, unless the configuration of conf
// disables them, at their catalog endpoints or the configuration's base
// URLs, authorized by accounts.
func newProviders(conf *config.Service, store *gateway.Store, overlay *catalog.Overlay, accounts *agent.Accounts, exts []extension.Extension) *agent.Providers {
	return agent.NewProviders(agent.ProviderOptions{
		Catalog:  store,
		Overlay:  overlay.Apply,
		Dial:     dialer(exts),
		Auth:     accounts.For,
		Settings: providerSettings(conf),
	})
}

// newCatalog returns the store of the catalog, flow's source of truth for
// providers, models and how to call them, which a catalog server serves at
// the configuration's catalog source and the data directory of conf caches,
// and the overlay of the models file of that directory, which holds the
// user's own providers and models over the catalog.
func newCatalog(conf *config.Service, cfg *config.Config) (*gateway.Store, *catalog.Overlay) {
	return catalog.NewStore(gateway.StoreOptions{Dir: conf.Dir, Source: cfg.CatalogSource}), catalog.NewOverlay(conf.Dir)
}

// dialer returns the agent.Dialer that asks the extension.DialHook of each
// of exts, in order, for the provider of an endpoint, and takes the first
// one's that speaks it, or nil when no extension dials.
func dialer(exts []extension.Extension) agent.Dialer {
	var hooks []extension.DialHook
	for h := range extension.Hooks[extension.DialHook](exts) {
		hooks = append(hooks, h)
	}
	if len(hooks) == 0 {
		return nil
	}
	return func(t agent.ProviderID, ep gateway.Endpoint, az agent.Authorizer) (agent.Provider, bool) {
		for _, h := range hooks {
			if p, ok := h.Dial(t, ep, az); ok {
				return p, true
			}
		}
		return nil, false
	}
}

// providerSettings returns the configuration of each provider, read anew
// from conf on every call.
func providerSettings(conf *config.Service) func(agent.ProviderID) agent.Settings {
	return func(t agent.ProviderID) agent.Settings {
		cfg, err := conf.Get()
		if err != nil {
			return agent.Settings{}
		}
		pc := cfg.Provider[string(t)]
		if pc == nil {
			return agent.Settings{}
		}
		return agent.Settings{Disabled: pc.Disabled, BaseURL: pc.BaseURL, ToolChanges: pc.ToolChanges}
	}
}

// configKeys returns an APIKeyFunc that reads the API key of a provider, on
// every call: the configuration's, from conf, or else the first of the
// environment variables the catalog that cat returns names for the provider that
// holds a key and is set, as gateway.Endpoint.KeyEnv lists them, leaving
// out a cloud resource's name and the cloud credentials that requests are signed
// or authorized with.
func configKeys(conf *config.Service, cat func() *gateway.Catalog) agent.APIKeyFunc {
	return func(t agent.ProviderID) (agent.APIKey, error) {
		cfg, err := conf.Get()
		if err != nil {
			return "", err
		}
		if pc := cfg.Provider[string(t)]; pc != nil && pc.APIKey != "" {
			return agent.APIKey(pc.APIKey), nil
		}
		p, ok := cat().Providers[t]
		if !ok {
			return "", nil
		}
		for _, name := range p.Endpoint.KeyEnv() {
			if v := os.Getenv(name); v != "" {
				return agent.APIKey(v), nil
			}
		}
		return "", nil
	}
}
