package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/internal/fuzzy"
)

var (
	// ErrNoModel reports that no model matches a pattern.
	ErrNoModel = errors.New("no model matches")

	// ErrModelFormat reports a model not named in "provider/model" format.
	ErrModelFormat = errors.New("model must be provider/model")
)

// Models is the model catalog, and the model the app sends a prompt to that
// names none, which App.Models returns: resolving and selecting a model,
// fitting its reasoning effort, listing the catalog and refreshing it, and
// sending a request outside any session.
type Models struct{ app *App }

// Models returns the app's models.
func (a *App) Models() Models { return Models{app: a} }

// Default returns the default model, a variant in the form Resolve reads,
// of a request that names none, as the config file sets it now, so that
// SaveDefault applies at once. It fails when the config file cannot be read. A
// caller that picks its own model, as the terminal does, passes it with each
// request instead.
func (m Models) Default() (string, error) {
	cfg, err := m.app.config.Get()
	if err != nil {
		return "", err
	}
	return cfg.Model, nil
}

// SaveDefault saves model, a variant in the form Resolve reads, as the
// default model in the config file, once its provider is registered. The
// model need not be in the catalog. Default reads it from there, so it applies
// at once, and to the other processes as they next read it.
func (m Models) SaveDefault(model string) error {
	t, id, ok := strings.Cut(model, "/")
	if !ok || t == "" || id == "" {
		return fmt.Errorf("%w: %q", ErrModelFormat, model)
	}
	if err := m.app.registered(agent.ProviderID(t)); err != nil {
		return err
	}
	return m.app.config.SaveModel(model)
}

// Resolve returns the model variant that model names, in the form
// agent.Variant.String writes, such as "openai/gpt-5.6:fast:high", or the
// default model when model is empty, which fails when the config file
// cannot be read. ParseVariant reads it, and FitEffort fits its
// effort to the model, or sets the default effort when it names none, so the
// effort it reports is the one the model runs at. A model that names no
// provider fails with ErrModelFormat.
func (m Models) Resolve(model string) (agent.Variant, error) {
	if model == "" {
		var err error
		if model, err = m.Default(); err != nil {
			return agent.Variant{}, err
		}
	}
	v := m.ParseVariant(model)
	if v.Provider == "" {
		return agent.Variant{}, fmt.Errorf("%w: %q", ErrModelFormat, model)
	}
	return m.FitEffort(v), nil
}

// Select returns the model variant that pattern names among the models
// of provider t, or of every registered provider when t is empty, as
// Find finds it, or the default model when pattern is empty. Its effort
// is effort when that is not empty, else the one pattern names, fitted to the
// model as FitEffort fits it. It fails when the catalog does not
// list effort for the model.
func (m Models) Select(ctx context.Context, t agent.ProviderID, pattern string, effort agent.ReasoningEffort) (agent.Variant, error) {
	var model string
	var err error
	if pattern != "" {
		model, err = m.Find(ctx, t, pattern)
	} else {
		model, err = m.Default()
	}
	if err != nil {
		return agent.Variant{}, err
	}
	v := m.ParseVariant(model)
	if effort != "" {
		levels := m.Efforts(v)
		if !slices.Contains(levels, effort) {
			return agent.Variant{}, fmt.Errorf("%s has no reasoning effort %q; the catalog lists %q", v.WithEffort(""), effort, levels)
		}
		v = v.WithEffort(effort)
	}
	return m.FitEffort(v), nil
}

// FitEffort returns v at the effort its model runs at: v's effort
// when the catalog lists it for the model, or else its default effort, as
// DefaultEffort gives it, when the catalog lists that, or else
// none, which leaves it to the provider.
func (m Models) FitEffort(v agent.Variant) agent.Variant {
	return v.WithEffort(agent.Fit(m.Efforts(v), v.Effort, m.DefaultEffort(v)))
}

// DefaultEffort returns the effort the model of v runs at when none
// is chosen: its catalog offering's default effort, or else medium.
func (m Models) DefaultEffort(v agent.Variant) agent.ReasoningEffort {
	return m.app.providers.DefaultReasoningEffort(v)
}

// Efforts returns the effort levels the catalog lists for the model
// of v, in its order, or none.
func (m Models) Efforts(v agent.Variant) []agent.ReasoningEffort {
	return m.app.providers.ReasoningEfforts(v)
}

// ParseVariant returns the model variant that model names, as
// agent.ParseVariant reads it against the catalog as it is now.
func (m Models) ParseVariant(model string) agent.Variant {
	return agent.ParseVariant(model, m.Catalog())
}

// ModelChoice is a model that a pattern can name.
type ModelChoice struct {
	// Name is the variant in the form agent.Variant.String writes, as in
	// "openai/gpt-6.1-sol:fast".
	Name string
	// Released is the model's release date, as YYYY-MM-DD, or "" when the
	// catalog has none.
	Released string
}

// Find returns, in the form agent.Variant.String writes, the catalog
// variant that pattern names among the models of provider t, or of every
// registered provider when t is empty, as MatchModel picks it. A ":effort"
// suffix naming a level the catalog lists for the model found is kept, so
// "opus:fast:high" finds the fast tier of the newest Opus at the high level.
// When none matches, it downloads the catalog and looks again, as the model
// may be newer than the cached catalog. The error wraps
// [ErrNoModel] when none matches still, and the download's error too when
// the download fails.
func (m Models) Find(ctx context.Context, t agent.ProviderID, pattern string) (string, error) {
	if i := strings.LastIndexByte(pattern, ':'); i >= 0 {
		level := agent.ParseReasoningEffort(pattern[i+1:])
		name, err := m.Match(ctx, t, pattern[:i])
		if err == nil && slices.Contains(m.Efforts(m.ParseVariant(name)), level) {
			return name + ":" + string(level), nil
		}
	}
	return m.Match(ctx, t, pattern)
}

// Match returns the catalog variant that pattern names, as
// Find finds it, without a reasoning effort.
func (m Models) Match(ctx context.Context, t agent.ProviderID, pattern string) (string, error) {
	choices, err := m.Choices(ctx, t)
	if err != nil {
		return "", err
	}
	if name, ok := MatchModel(pattern, choices); ok {
		return name, nil
	}
	if err := m.Refresh(ctx); err != nil {
		return "", fmt.Errorf("%w %q, and the model catalog could not be refreshed: %w", ErrNoModel, pattern, err)
	}
	if choices, err = m.Choices(ctx, t); err != nil {
		return "", err
	}
	if name, ok := MatchModel(pattern, choices); ok {
		return name, nil
	}
	return "", fmt.Errorf("%w %q", ErrNoModel, pattern)
}

// Choices returns the variants of the catalog's models at provider t,
// or at every registered provider when t is empty, in the catalog's order.
// It fails when the models of one cannot be read. Unlike List, it
// starts no refresh of the catalog, as Find refreshes the catalog
// itself when it needs to.
func (m Models) Choices(ctx context.Context, t agent.ProviderID) ([]ModelChoice, error) {
	providers := m.app.Accounts().Providers()
	slices.Sort(providers)
	if t != "" {
		if err := m.app.registered(t); err != nil {
			return nil, err
		}
		providers = []agent.ProviderID{t}
	}
	var choices []ModelChoice
	for _, pt := range providers {
		models, err := m.app.providers.ListModels(ctx, pt)
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			for _, v := range m.Variants(pt) {
				choices = append(choices, ModelChoice{Name: v.String(), Released: m.Released})
			}
		}
	}
	return choices, nil
}

// MatchModel returns the name of the choice that pattern names, ignoring
// case: the choice whose name or model ID is pattern, or else the best fuzzy
// match, as the model menu ranks them, and the newest release among equal
// matches. It reports false when none matches.
func MatchModel(pattern string, choices []ModelChoice) (string, bool) {
	key := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	query := key(pattern)
	if query == "" {
		return "", false
	}
	for _, c := range choices {
		name := key(c.Name)
		_, id := agent.ParseModel(name)
		if name == query || id == query {
			return c.Name, true
		}
	}
	// Equal matches keep this order, so the newest release ranks first.
	newest := slices.Clone(choices)
	slices.SortStableFunc(newest, func(x, y ModelChoice) int { return cmp.Compare(y.Released, x.Released) })
	for c := range fuzzy.Rank(query, newest, func(c ModelChoice) string { return key(c.Name) }) {
		return c.Name, true
	}
	return "", false
}

// ModelVariants returns the variants of listing m of provider t, in the
// catalog's order: the offering's own call, and then each of its
// variants.
func ModelVariants(t agent.ProviderID, m agent.Listing) []agent.Variant {
	return m.Variants(t)
}

// List returns catalog models published for provider t, as the
// catalog cache has them now. When a refresh of the catalog is due, it starts
// one in the background; see StartRefresh.
func (m Models) List(ctx context.Context, t agent.ProviderID) ([]agent.Listing, error) {
	m.StartRefresh()
	return m.app.providers.ListModels(ctx, t)
}

// StartRefresh refreshes the model catalog in the background when a
// refresh is due. The refresh stops when the app closes.
func (m Models) StartRefresh() {
	due, err := m.app.providers.ModelsDue()
	if err != nil {
		slog.Warn("refresh models", "err", err)
		return
	}
	if !due {
		return
	}
	go func() {
		if err := m.Refresh(m.app.workerCtx); err != nil {
			slog.Warn("refresh models", "err", err)
		}
	}()
}

// Listings returns the catalog models of each provider that is signed
// in, or that credentials of the environment authorize, as Google's
// application default credentials do Vertex AI, or of provider t alone when
// t is set, ordered by
// provider. It fails when the accounts or the models of one cannot be read.
// Like List, it starts a refresh of the catalog when one is due.
func (m Models) Listings(ctx context.Context, t agent.ProviderID) ([]ModelListing, error) {
	providers := m.app.Accounts().Providers()
	slices.Sort(providers)
	if t != "" {
		if err := m.app.registered(t); err != nil {
			return nil, err
		}
		providers = []agent.ProviderID{t}
	}
	m.StartRefresh()
	listings := []ModelListing{}
	for _, pt := range providers {
		st, err := m.app.Accounts().Status(ctx, pt)
		if err != nil {
			return nil, err
		}
		if !st.Authenticated() && !m.app.providers.Ambient(pt) {
			continue
		}
		models, err := m.app.providers.ListModels(ctx, pt)
		if err != nil {
			return nil, err
		}
		listings = append(listings, NewModelListing(pt, models))
	}
	return listings, nil
}

// Refresh downloads the model catalog now, stores it in the catalog
// cache, and publishes EventModelsRefreshed, whether or not the catalog
// changed. Every refresh of the catalog runs through it. The error wraps
// gateway.ErrSource when the catalog server is at fault.
func (m Models) Refresh(ctx context.Context) error {
	if err := m.app.providers.RefreshModels(ctx); err != nil {
		return err
	}
	m.app.publish(EventModelsRefreshed{EventBase: m.app.eventBase()})
	return nil
}

// Catalog returns the model catalog as it is now: the latest download, or
// the snapshot built in. Unlike Listing, it starts no refresh.
func (m Models) Catalog() *gateway.Catalog {
	return m.app.providers.Catalog()
}

// Listing returns provider t's offering of the model that id names, as
// ParseVariant reads "t/id": by the catalog's model ID, or by it without
// the lab when t is the lab. Like List, it starts a refresh of the
// catalog when one is due. It fails when t is not registered, with
// agent.ErrModelNotFound when the catalog lists no such model at t.
func (m Models) Listing(t agent.ProviderID, id string) (agent.Listing, error) {
	if err := m.app.registered(t); err != nil {
		return agent.Listing{}, err
	}
	m.StartRefresh()
	v := m.ParseVariant(agent.FormatModel(t, id))
	off, ok := m.app.providers.Offering(v)
	if !ok {
		return agent.Listing{}, fmt.Errorf("%w: %s", agent.ErrModelNotFound, agent.FormatModel(t, id))
	}
	_, listed := m.Catalog().Providers[t].Models[gateway.ModelID(v.Model)]
	return agent.Listing{Model: v.Model, Unlisted: !listed, Offering: off}, nil
}

// StartListings lists models from authenticated providers from a new
// goroutine, and publishes the listings, or why they could not be read.
func (m Models) StartListings(ctx context.Context) {
	go func() {
		ev := EventModelsListed{EventBase: m.app.eventBase()}
		listings, err := m.Listings(ctx, "")
		if err != nil {
			e := NewUnknownError(err.Error())
			ev.Error = &e
		}
		ev.Listings = listings
		m.app.publish(ev)
	}()
}
