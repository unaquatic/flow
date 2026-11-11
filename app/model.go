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
	ErrNoModel = errors.New("no model")

	// ErrAmbiguousModel reports a model named without its lab that models
	// of more than one lab have.
	ErrAmbiguousModel = errors.New("ambiguous model")

	// ErrModelFormat reports a model not named in "provider/model" format,
	// or a default model without its provider or its model.
	ErrModelFormat = errors.New("invalid model")

	// ErrNoModelSet reports a request that names no model when the config
	// file sets no default model either.
	ErrNoModelSet = errors.New("no model selected and no default model set")
)

// Models is the model catalog, and the model the app sends a prompt to that
// names none, which App.Models returns: resolving and selecting a model,
// fitting its reasoning effort, listing the catalog and refreshing it, and
// sending a request outside any session.
type Models struct{ app *App }

// Models returns the app's models.
func (a *App) Models() Models { return Models{app: a} }

// DefaultModel is the default model of the config file, which a request
// that names no model runs: a provider, and a model of it apart, in the
// form agent.Variant.Ref writes, such as "lab-a/model-a:fast:high".
type DefaultModel struct {
	// Provider is the provider the model runs at, by its catalog ID.
	Provider agent.ProviderID
	// Model is the model without its provider, as agent.Variant.Ref
	// writes it.
	Model string
}

// DefaultModel returns the default provider and model as the config file
// sets them now, so that SaveDefaultModel applies at once. Either is empty
// when the file does not set it. It fails when the config file cannot be
// read.
func (m Models) DefaultModel() (DefaultModel, error) {
	cfg, err := m.app.config.Get()
	if err != nil {
		return DefaultModel{}, err
	}
	return DefaultModel{Provider: agent.ProviderID(cfg.DefaultProvider), Model: cfg.DefaultModel}, nil
}

// Default returns the default model, as DefaultModel reads it, as a variant
// in the form Resolve reads, its provider and then its model. It is empty
// when the file sets no model, and the model alone when it sets no
// provider, which Resolve refuses. A caller that picks its own model, as
// the terminal does, passes it with each request instead.
func (m Models) Default() (string, error) {
	d, err := m.DefaultModel()
	if err != nil || d.Model == "" || d.Provider == "" {
		return d.Model, err
	}
	return agent.FormatModel(d.Provider, d.Model), nil
}

// SaveDefaultModel saves d as the default provider and model of the config
// file, its model as agent.ParseVariantAt reads it at its provider and
// Variant.Ref writes it, once its provider is registered. It fails with
// ErrModelFormat when d lacks either. The model need not be in the catalog.
// DefaultModel reads them from there, so they apply at once, and to the
// other processes as they next read them.
func (m Models) SaveDefaultModel(d DefaultModel) error {
	v, err := m.defaultVariant(d)
	if err != nil {
		return err
	}
	return m.app.config.SaveDefaultModel(string(v.Provider), v.Ref())
}

// UpdateDefaultModel applies change to the default provider and model as
// DefaultModel reads them, and saves the result as SaveDefaultModel saves
// it, in one write under the config file's lock, so that a change another
// process makes to the file meanwhile, as of the other of the two, is not
// lost. Nothing is saved when the result cannot be.
func (m Models) UpdateDefaultModel(change func(*DefaultModel)) error {
	return m.app.config.UpdateDefaultModel(func(provider, model string) (string, string, error) {
		d := DefaultModel{Provider: agent.ProviderID(provider), Model: model}
		change(&d)
		v, err := m.defaultVariant(d)
		if err != nil {
			return "", "", err
		}
		return string(v.Provider), v.Ref(), nil
	})
}

// defaultVariant returns d as a variant, its model as agent.ParseVariantAt
// reads it at its provider. It fails with ErrModelFormat when d lacks its
// provider or its model, and when its provider is not registered.
func (m Models) defaultVariant(d DefaultModel) (agent.Variant, error) {
	if d.Provider == "" || d.Model == "" {
		return agent.Variant{}, fmt.Errorf("default model: %w: %s/%s; use provider/model", ErrModelFormat, d.Provider, d.Model)
	}
	if err := m.app.registered(d.Provider); err != nil {
		return agent.Variant{}, err
	}
	return agent.ParseVariantAt(d.Provider, d.Model, m.Catalog()), nil
}

// SaveDefault saves model, a variant in the form Resolve reads, as the
// default model, its provider and the rest apart, as SaveDefaultModel
// saves them.
func (m Models) SaveDefault(model string) error {
	t, id, ok := strings.Cut(model, "/")
	if !ok || t == "" || id == "" {
		return fmt.Errorf("%w: %s; use provider/model", ErrModelFormat, model)
	}
	return m.SaveDefaultModel(DefaultModel{Provider: agent.ProviderID(t), Model: id})
}

// Resolve returns the model variant that model names, in the form
// agent.Variant.String writes, such as "provider-a/lab-a/model-a:fast:high", or the
// default model when model is empty, which fails when the config file
// cannot be read. ParseVariant reads it, and FitEffort fits its
// effort to the model, or sets the default effort when it names none, so the
// effort it reports is the one the model runs at. A model that names no
// provider fails with ErrModelFormat, and an empty model when the config
// file sets no default with ErrNoModelSet.
func (m Models) Resolve(model string) (agent.Variant, error) {
	if model == "" {
		var err error
		if model, err = m.Default(); err != nil {
			return agent.Variant{}, err
		}
		if model == "" {
			return agent.Variant{}, ErrNoModelSet
		}
	}
	v := m.ParseVariant(model)
	if v.Provider == "" {
		return agent.Variant{}, fmt.Errorf("%w: %s; use provider/model", ErrModelFormat, model)
	}
	return m.FitEffort(v), nil
}

// Select returns the model variant that pattern names among the models
// of provider t, or of every registered provider when t is empty, as
// Find finds it, or the default model when pattern is empty. Its effort
// is effort when that is not empty, else the one pattern names, fitted to the
// model as FitEffort fits it. It fails when the catalog does not
// list effort for the model, and with ErrNoModelSet when pattern is empty
// and the config file sets no default model.
func (m Models) Select(ctx context.Context, t agent.ProviderID, pattern string, effort agent.ReasoningEffort) (agent.Variant, error) {
	var model string
	var err error
	if pattern != "" {
		model, err = m.Find(ctx, t, pattern)
	} else if model, err = m.Default(); err == nil && model == "" {
		err = ErrNoModelSet
	}
	if err != nil {
		return agent.Variant{}, err
	}
	v := m.ParseVariant(model)
	if effort != "" {
		levels := m.Efforts(v)
		if !slices.Contains(levels, effort) {
			return agent.Variant{}, fmt.Errorf("%s: unknown reasoning effort: %s; use one of %v", v.WithEffort(""), effort, levels)
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
	// "provider-a/lab-a/model-a:fast".
	Name string
	// Released is the model's release date, as YYYY-MM-DD, or "" when the
	// catalog has none.
	Released string
}

// Find returns, in the form agent.Variant.String writes, the catalog
// variant that pattern names among the models of provider t, or of every
// registered provider when t is empty, as MatchModel picks it. A ":effort"
// suffix naming a level the catalog lists for the model found is kept, so
// "large:fast:high" finds the fast tier of the newest model whose ID names
// large, such as model-b-large-5, at the high level.
// When none matches, it downloads the catalog and looks again, as the model
// may be newer than the cached catalog. The error wraps
// [ErrNoModel] when none matches still, and the download's error too when
// the download fails, and [ErrAmbiguousModel] when pattern, or it without
// its suffix, names the models of more than one lab without their lab.
func (m Models) Find(ctx context.Context, t agent.ProviderID, pattern string) (string, error) {
	if i := strings.LastIndexByte(pattern, ':'); i >= 0 {
		level := agent.ParseReasoningEffort(pattern[i+1:])
		name, err := m.Match(ctx, t, pattern[:i])
		if errors.Is(err, ErrAmbiguousModel) {
			return "", err
		}
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
	if name, err := MatchModel(pattern, choices); !errors.Is(err, ErrNoModel) {
		return name, err
	}
	if err := m.Refresh(ctx); err != nil {
		return "", fmt.Errorf("%w: %s; refresh the model catalog: %w", ErrNoModel, pattern, err)
	}
	if choices, err = m.Choices(ctx, t); err != nil {
		return "", err
	}
	return MatchModel(pattern, choices)
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
// case: the choice whose name or model ID is pattern, or else the one whose
// model ID without its lab is, or else the best fuzzy match, as the model
// menu ranks them, and the newest release among equal matches. A pattern
// that names, without the lab, the models of more than one lab fails with
// ErrAmbiguousModel, and one that matches none with ErrNoModel.
func MatchModel(pattern string, choices []ModelChoice) (string, error) {
	key := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	query := key(pattern)
	if query == "" {
		return "", fmt.Errorf("%w: %s", ErrNoModel, pattern)
	}
	var bare []ModelChoice // the choices whose model ID without its lab is query
	for _, c := range choices {
		name := key(c.Name)
		_, id := agent.ParseModel(name)
		if name == query || id == query {
			return c.Name, nil
		}
		if _, short, ok := strings.Cut(id, "/"); ok && short == query {
			bare = append(bare, c)
		}
	}
	if len(bare) > 0 {
		ids := make([]string, 0, len(bare))
		for _, c := range bare {
			_, id := agent.ParseModel(key(c.Name))
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) > 1 {
			return "", fmt.Errorf("%w: %s matches %s; name its lab", ErrAmbiguousModel, pattern, strings.Join(ids, ", "))
		}
		return bare[0].Name, nil
	}
	// Equal matches keep this order, so the newest release ranks first.
	newest := slices.Clone(choices)
	slices.SortStableFunc(newest, func(x, y ModelChoice) int { return cmp.Compare(y.Released, x.Released) })
	for c := range fuzzy.Rank(query, newest, func(c ModelChoice) string { return key(c.Name) }) {
		return c.Name, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoModel, pattern)
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
// in, or that credentials of the environment authorize, as a cloud's
// application default credentials do its AI platform, or of provider t alone when
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

// ProviderName returns the catalog's name of provider t, such as
// "Provider A", or its ID when the catalog gives it none.
func (m Models) ProviderName(t agent.ProviderID) string {
	if p, ok := m.Catalog().Providers[t]; ok && p.Name != "" {
		return p.Name
	}
	return string(t)
}

// Listing returns provider t's offering of the model that id, the catalog's
// model ID, names, as ParseVariant reads "t/id". Like List, it starts a
// refresh of the
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
			e := ErrorOf(err)
			ev.Error = &e
		}
		ev.Listings = listings
		m.app.publish(ev)
	}()
}
