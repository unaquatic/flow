package agent

import (
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
)

// Variant names a model as a request runs it: its provider, the catalog's
// model, the variant of the provider's offering of it, and the reasoning
// effort. Its string form is "provider/model:option:effort", the model
// without its lab when the provider is the lab, as
// "openai/gpt-6.1-sol:fast:high" and "openrouter/anthropic/claude-opus-5-5",
// leaving out the option and the effort it does not set.
type Variant struct {
	// Provider identifies the provider, by its catalog ID.
	Provider ProviderID
	// Model is the catalog's model ID, such as "openai/gpt-6.1-sol", or the
	// provider's own ID of a model the catalog leaves unlisted or does not
	// know.
	Model string
	// Option is what the catalog's variant ID adds to the model ID, such as
	// "fast" of "openai/gpt-6.1-sol:fast", or empty for the default
	// variant.
	Option string
	// Effort is the reasoning effort, or empty to leave it to the provider.
	Effort ReasoningEffort
}

// ID returns the catalog's variant ID of v: its model ID, and its option
// after a colon.
func (v Variant) ID() string {
	if v.Option == "" {
		return v.Model
	}
	return v.Model + ":" + v.Option
}

// ModelName returns v's variant ID as its string form names it: without the
// lab when the provider is the lab, such as "gpt-6.1-sol:fast".
func (v Variant) ModelName() string {
	return strings.TrimPrefix(v.ID(), string(v.Provider)+"/")
}

// String returns v in "provider/model:option:effort" format, the form
// ParseVariant reads.
func (v Variant) String() string {
	s := FormatModel(v.Provider, v.ModelName())
	if v.Effort != "" {
		s += ":" + string(v.Effort)
	}
	return s
}

// WithEffort returns v at reasoning effort t.
func (v Variant) WithEffort(t ReasoningEffort) Variant {
	v.Effort = t
	return v
}

// ParseVariant reads s, a model in the form Variant.String writes. A model
// without a provider has none, as ParseModel has it. The rest names a
// variant that cat lists for the provider, by its ID or, when the provider
// is its model's lab, by its ID without the lab, and may end in ":effort",
// naming a level that cat lists for the model. A variant ID that cat lists
// is never cut, so an option or model ID that ends like an effort is not
// mistaken for one. A model cat does not know is kept whole, by the ID s
// names.
func ParseVariant(s string, cat *gateway.Catalog) Variant {
	t, rest := ParseModel(s)
	if v, ok := findVariant(cat, t, rest); ok {
		return v
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		e := ParseReasoningEffort(rest[i+1:])
		if v, ok := findVariant(cat, t, rest[:i]); ok {
			if off, ok := Lookup(cat, v); ok && slices.Contains(ReasoningEfforts(off.Reasoning), e) {
				return v.WithEffort(e)
			}
		}
	}
	return Variant{Provider: t, Model: rest}
}

// findVariant returns the variant of provider t whose ID is id, or the
// lab's ID followed by id, and reports whether cat lists one.
func findVariant(cat *gateway.Catalog, t ProviderID, id string) (Variant, bool) {
	for _, full := range []string{id, string(t) + "/" + id} {
		if model, _, option, ok := cat.Lookup(t, full); ok {
			return Variant{Provider: t, Model: model, Option: option}, true
		}
	}
	return Variant{}, false
}

// Lookup returns the offering of v's model at its provider in cat, listed or
// unlisted, and reports whether cat lists it.
func Lookup(cat *gateway.Catalog, v Variant) (gateway.Offering, bool) {
	p, ok := cat.Providers[v.Provider]
	if !ok {
		return gateway.Offering{}, false
	}
	if o, ok := p.Models[gateway.ModelID(v.Model)]; ok {
		return o, true
	}
	o, ok := p.Unlisted[v.Model]
	return o, ok
}

// Variants returns the variants of the offering of model of provider t:
// its own call, and then each of its variants, in the order the catalog
// lists them.
func Variants(t ProviderID, model string, off gateway.Offering) []Variant {
	out := []Variant{{Provider: t, Model: model}}
	for _, key := range off.VariantKeys() {
		out = append(out, Variant{Provider: t, Model: model, Option: key})
	}
	return out
}
