package agent

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent/request"
)

// Settings is how the configuration sets up one provider.
type Settings struct {
	// Disabled leaves the provider out.
	Disabled bool
	// BaseURL moves the provider's endpoints: a host alone, such as
	// https://proxy.example, takes every endpoint there with its own path;
	// a base URL with a path, such as https://proxy.example/anthropic/v1,
	// replaces the provider's own.
	BaseURL string
}

// Dialer returns the provider that sends provider t's requests to endpoint
// ep, whose base URL is set, authorized by az, and reports whether flow
// speaks ep's request format and authentication. az is nil when nothing
// authorizes t's requests, and an endpoint may authorize them otherwise, as
// with Google's application default credentials.
type Dialer func(t ProviderID, ep gateway.Endpoint, az Authorizer) (Provider, bool)

// ProviderOptions configures a Providers.
type ProviderOptions struct {
	// Catalog holds the catalog, flow's source of truth for providers,
	// models and how to call them.
	Catalog *gateway.Store
	// Dial makes the provider of an endpoint. Nil registers none of the
	// catalog's providers, only those given in their place.
	Dial Dialer
	// Auth returns the authorizer of provider t's accounts and API keys,
	// which Dial is given. Nil gives it none.
	Auth func(t ProviderID) Authorizer
	// Settings returns the configuration of provider t. Nil leaves every
	// provider enabled at its endpoints.
	Settings func(t ProviderID) Settings
	// Env reads the environment variables that base URLs name as ${NAME}.
	// Nil reads the process's.
	Env func(name string) string
}

// Providers gives access to the providers of the catalog and their models: a
// provider whose request format flow speaks is registered unless the
// configuration disables it, and each request goes to the endpoint the
// catalog lists for its variant.
type Providers struct {
	opts      ProviderOptions
	overrides map[ProviderID]Provider
}

// NewProviders returns a Providers configured by opts, with overrides in place
// of the catalog's providers of their names.
func NewProviders(opts ProviderOptions, overrides ...Provider) *Providers {
	s := &Providers{opts: opts, overrides: make(map[ProviderID]Provider, len(overrides))}
	for _, p := range overrides {
		s.overrides[ProviderID(p.Name())] = p
	}
	return s
}

// WithProvider returns a Providers like s, reading the same catalog, with p in
// place of the provider of its name, as the demo puts its model in place of
// a real provider's.
func (s *Providers) WithProvider(p Provider) *Providers {
	o := maps.Clone(s.overrides)
	o[ProviderID(p.Name())] = p
	return &Providers{opts: s.opts, overrides: o}
}

// Catalog returns the catalog as the store has it now.
func (s *Providers) Catalog() *gateway.Catalog {
	return s.opts.Catalog.Get()
}

// settings returns the configuration of provider t.
func (s *Providers) settings(t ProviderID) Settings {
	if s.opts.Settings == nil {
		return Settings{}
	}
	return s.opts.Settings(t)
}

// Get returns provider t, at its catalog endpoint, and reports whether it
// is registered: in place of the catalog's, or listed by the catalog at
// an endpoint flow speaks and not disabled.
func (s *Providers) Get(t ProviderID) (Provider, bool) {
	if p, ok := s.overrides[t]; ok {
		return p, true
	}
	prov, ok := s.Catalog().Providers[t]
	if !ok || s.opts.Dial == nil {
		return nil, false
	}
	set := s.settings(t)
	if set.Disabled {
		return nil, false
	}
	ep := prov.Endpoint
	ep.API = rebase(ep.API, set.BaseURL, true)
	return s.dial(t, ep)
}

// dial makes the provider of endpoint ep for provider t with Dial, giving it
// t's authorizer.
func (s *Providers) dial(t ProviderID, ep gateway.Endpoint) (Provider, bool) {
	var az Authorizer
	if s.opts.Auth != nil {
		az = s.opts.Auth(t)
	}
	return s.opts.Dial(t, ep, az)
}

// Ambient reports whether provider t is registered and the environment
// holds credentials that authorize its requests without an account, as an
// AmbientProvider reports them.
func (s *Providers) Ambient(t ProviderID) bool {
	p, ok := s.Get(t)
	if !ok {
		return false
	}
	a, ok := p.(AmbientProvider)
	return ok && a.AmbientCredentials()
}

// Providers returns the registered providers, in ID order.
func (s *Providers) Providers() []ProviderID {
	ids := slices.Collect(maps.Keys(s.overrides))
	for id := range s.Catalog().Providers {
		if _, over := s.overrides[id]; over {
			continue
		}
		if _, ok := s.Get(id); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// Listing is a model a provider offers, as ListModels lists it.
type Listing struct {
	// Model is the catalog's model ID, or the provider's own ID of a model
	// the catalog lists only at the provider.
	Model string `json:"model"`
	// Unlisted reports a model the catalog lists only at the provider.
	Unlisted bool `json:"unlisted,omitempty"`
	gateway.Offering
}

// Variants returns the variants of l, in the catalog's order.
func (l Listing) Variants(t ProviderID) []Variant {
	return Variants(t, l.Model, l.Offering)
}

// ListModels returns the models the catalog lists for provider t that flow
// can call: those of the catalog's models it offers, in model ID order, and
// then the unlisted ones, in ID order. It leaves out the offerings and
// variants whose endpoint of their own takes a request format or
// authentication flow does not speak, as Callable finds them.
func (s *Providers) ListModels(_ context.Context, t ProviderID) ([]Listing, error) {
	if _, ok := s.Get(t); !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotRegistered, t)
	}
	p := s.Catalog().Providers[t]
	out := make([]Listing, 0, len(p.Models)+len(p.Unlisted))
	add := func(l Listing) {
		if off, ok := s.Callable(t, l.Offering); ok {
			l.Offering = off
			out = append(out, l)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(p.Models)) {
		add(Listing{Model: string(id), Offering: p.Models[id]})
	}
	for _, id := range slices.Sorted(maps.Keys(p.Unlisted)) {
		add(Listing{Model: id, Unlisted: true, Offering: p.Unlisted[id]})
	}
	return out, nil
}

// Callable returns off, an offering of provider t, without the variants
// whose endpoint of their own flow does not speak, as Dial finds it, and
// reports false when flow cannot call the offering itself: when its own
// endpoint is one flow does not speak. An offering without one calls the
// provider's endpoint, which flow speaks once t is registered. It copies
// what it changes, and returns off as it is when t stands in for a catalog
// provider or no Dial is set.
func (s *Providers) Callable(t ProviderID, off gateway.Offering) (gateway.Offering, bool) {
	if _, over := s.overrides[t]; over || s.opts.Dial == nil {
		return off, true
	}
	speaks := func(ep *gateway.Endpoint) bool {
		if ep == nil {
			return true
		}
		_, ok := s.dial(t, *ep)
		return ok
	}
	if !speaks(off.ProviderOptions.Endpoint) {
		return gateway.Offering{}, false
	}
	vs, copied := off.Variants, false
	for k, v := range off.Variants {
		if !speaks(v.ProviderOptions.Endpoint) {
			if !copied {
				vs, copied = maps.Clone(off.Variants), true
			}
			delete(vs, k)
		}
	}
	off.Variants = vs
	return off, true
}

// Offering returns the catalog's offering of v's model at its provider, and
// reports whether the catalog lists it.
func (s *Providers) Offering(v Variant) (gateway.Offering, bool) {
	return Lookup(s.Catalog(), v)
}

// DefaultReasoningEffort returns the reasoning effort the model of v runs
// at when none is chosen: the catalog's default effort of its offering, or
// else FallbackEffort, medium.
func (s *Providers) DefaultReasoningEffort(v Variant) ReasoningEffort {
	if off, ok := s.Offering(v); ok && off.Reasoning != nil && off.Reasoning.DefaultEffort != "" {
		return ReasoningEffort(off.Reasoning.DefaultEffort)
	}
	return FallbackEffort
}

// ReasoningEfforts returns the effort levels the catalog lists for the model
// of v, in its order, or none when it lists none or the model.
func (s *Providers) ReasoningEfforts(v Variant) []ReasoningEffort {
	off, ok := s.Offering(v)
	if !ok {
		return nil
	}
	return ReasoningEfforts(off.Reasoning)
}

// ModelsDue reports whether a refresh of the catalog is due, and if so
// leaves it to the caller, which is to call RefreshModels.
func (s *Providers) ModelsDue() (bool, error) {
	return s.opts.Catalog.Due()
}

// RefreshModels downloads the catalog now and stores it. Unlike the other
// reads, it waits for the download.
func (s *Providers) RefreshModels(ctx context.Context) error {
	_, err := s.opts.Catalog.Refresh(ctx)
	return err
}

// Stream starts a streaming model request to model variant v, as the
// catalog resolves it: to its provider's model ID, at its endpoint, with its
// request fields and headers, under the caller's own. A request without
// MaxTokens gets the model's output limit, so it can use all of its output,
// and one without Reasoning gets the reasoning of v's reasoning effort, as
// ReasoningFor sets it. A model the catalog does not list goes to its
// provider's endpoint under the ID v names, with no reasoning settings. A
// variant the catalog does not list fails with ErrVariantNotFound, and an
// endpoint flow does not speak with ErrFormatNotSupported.
//
// The reasoning of messages that another model wrote is dropped, as
// OwnReasoning drops it.
func (s *Providers) Stream(ctx context.Context, v Variant, req Request) (StreamResponse, error) {
	cat := s.Catalog()
	r, resolved := cat.Resolve(v.Provider, v.ID())
	off, listed := Lookup(cat, v)
	if listed && resolved {
		if req.MaxTokens == 0 {
			req.MaxTokens = off.Limit.Output
		}
		if req.Reasoning == nil {
			req.Reasoning = ReasoningFor(off, v.Effort)
		}
		// The caller's own fields and headers win over the variant's.
		req.Body = request.MergeBody(request.MergeBody(nil, r.ProviderOptions.Body), req.Body)
		if len(r.ProviderOptions.Headers) > 0 {
			headers := maps.Clone(r.ProviderOptions.Headers)
			maps.Copy(headers, req.Headers)
			req.Headers = headers
		}
	}
	req.Messages = ExpandToolParts(OwnReasoning(req.Messages, v.Provider, v.Model))

	if p, ok := s.overrides[v.Provider]; ok {
		return p.Model(cmp.Or(r.ProviderOptions.ID, v.Model)).Stream(ctx, req)
	}
	prov, ok := cat.Providers[v.Provider]
	if !ok || s.settings(v.Provider).Disabled {
		slog.Error("provider not registered", "provider", v.Provider)
		return nil, fmt.Errorf("%w: %q", ErrProviderNotRegistered, v.Provider)
	}
	ep, send := prov.Endpoint, v.Model
	switch {
	case resolved:
		ep, send = *r.ProviderOptions.Endpoint, r.ProviderOptions.ID
	case v.Option != "":
		return nil, fmt.Errorf("%w: %s", ErrVariantNotFound, v)
	}
	// An endpoint flow does not speak fails as such, whatever variables
	// its base URL lacks, as setting them would not make it work.
	ep, err := s.Endpoint(v.Provider, ep)
	var p Provider
	if s.opts.Dial != nil {
		p, ok = s.dial(v.Provider, ep)
	}
	if p == nil || !ok {
		return nil, fmt.Errorf("%w: %s takes %s", ErrFormatNotSupported, v, cmp.Or(string(ep.Format), "an unknown format"))
	}
	if err != nil {
		return nil, err
	}
	return p.Model(send).Stream(ctx, req)
}

// Generate sends a model request and returns the whole response, which it
// collects from the stream, so it parses responses as Stream does.
func (s *Providers) Generate(ctx context.Context, v Variant, req Request) (*Response, error) {
	stream, err := s.Stream(ctx, v, req)
	if err != nil {
		return nil, err
	}
	return stream.Collect()
}

// variable matches a ${NAME} variable of a base URL.
var variable = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// Endpoint returns ep, one of provider t's endpoints, as t's requests reach
// it: moved to the configured base URL, as Settings.BaseURL moves it, and
// with the ${NAME} variables of its base URL replaced by the environment's,
// as gateway.Lookup finds them. It fails, with ErrMissingVariable, naming
// the variables the environment does not set.
func (s *Providers) Endpoint(t ProviderID, ep gateway.Endpoint) (gateway.Endpoint, error) {
	own := ep.Equal(s.Catalog().Providers[t].Endpoint)
	ep.API = rebase(ep.API, s.settings(t).BaseURL, own)
	env := s.opts.Env
	if env == nil {
		env = os.Getenv
	}
	var missing []string
	ep.API = variable.ReplaceAllStringFunc(ep.API, func(m string) string {
		name := variable.FindStringSubmatch(m)[1]
		if v := gateway.Lookup(name, env); v != "" {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return ep, fmt.Errorf("%w: %s needs %s", ErrMissingVariable, t, strings.Join(slices.Compact(missing), ", "))
	}
	return ep, nil
}

// rebase returns api, the base URL of one of a provider's endpoints, moved
// to base, the base URL the configuration sets for the provider, or api as
// it is when base is empty:
//
//   - base names a host alone, such as "https://proxy": base's scheme and
//     host with api's path, so that "https://api.anthropic.com/v1" becomes
//     "https://proxy/v1" and each endpoint of a provider with several, such
//     as OpenCode's Claude and GPT ones, keeps its own path;
//   - base names a path too: base itself for the provider's own endpoint,
//     when own is set, and base's scheme and host with api's path for an
//     entry's endpoint of its own.
//
// Either URL may name ${NAME} variables.
func rebase(api, base string, own bool) string {
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return api
	}
	origin, path := splitURL(base)
	if path != "" && own {
		return base
	}
	_, apiPath := splitURL(api)
	return origin + apiPath
}

// splitURL splits u, which may name ${NAME} variables, into its scheme and
// host, and its path, without a trailing slash.
func splitURL(u string) (origin, path string) {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		scheme, rest = "", u
	}
	host, p, _ := strings.Cut(rest, "/")
	if p = strings.TrimSuffix(p, "/"); p != "" {
		p = "/" + p
	}
	if ok {
		return scheme + "://" + host, p
	}
	return host, p
}
