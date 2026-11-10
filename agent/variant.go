package agent

import (
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
)

// Variant names a model as a request runs it: its provider, the catalog's
// model, the variant of the provider's offering of it, and the reasoning
// effort. Its string form is "provider/model:option:effort", the provider
// and then Ref, as "provider-a/lab-a/model-a:fast:high", leaving out the
// option and the effort it does not set.
type Variant struct {
	// Provider identifies the provider, by its catalog ID.
	Provider ProviderID
	// Model is the catalog's model ID, lab included, such as
	// "lab-a/model-a", or, for a model the catalog leaves unlisted or does
	// not know, the provider's own ID of it.
	Model string
	// Option is what the catalog's variant ID adds to the model ID, such as
	// "fast" of "lab-a/model-a:fast", or empty for the default variant.
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

// String returns v in "provider/model:option:effort" form, its provider and
// then its Ref, the form ParseVariant reads.
func (v Variant) String() string {
	return FormatModel(v.Provider, v.Ref())
}

// Ref returns v without its provider, in "model:option:effort" form: its ID,
// and its effort after a colon, as "lab-a/model-a:fast:high". The config
// file and the session store save a variant as its Ref and its provider
// apart, which ParseVariantAt reads back.
func (v Variant) Ref() string {
	if v.Effort == "" {
		return v.ID()
	}
	return v.ID() + ":" + string(v.Effort)
}

// WithEffort returns v at reasoning effort t.
func (v Variant) WithEffort(t ReasoningEffort) Variant {
	v.Effort = t
	return v
}

// ParseVariant reads s, a model in the form Variant.String writes: the
// provider, the part before the first slash, as ParseModel has it, and the
// rest as ParseVariantAt reads it.
func ParseVariant(s string, cat *gateway.Catalog) Variant {
	t, rest := ParseModel(s)
	return ParseVariantAt(t, rest, cat)
}

// ParseVariantAt reads s, a variant of provider t in the form Variant.Ref
// writes: a variant ID that cat lists for t, which may end in ":effort",
// naming a level that cat lists for the model. A variant ID that cat lists
// is never cut, so an option or model ID that ends like an effort is not
// mistaken for one. A model cat does not know is kept whole, by the ID s
// names.
func ParseVariantAt(t ProviderID, s string, cat *gateway.Catalog) Variant {
	if v, ok := findVariant(cat, t, s); ok {
		return v
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		e := ParseReasoningEffort(s[i+1:])
		if v, ok := findVariant(cat, t, s[:i]); ok {
			if off, ok := Lookup(cat, v); ok && slices.Contains(ReasoningEfforts(off.Reasoning), e) {
				return v.WithEffort(e)
			}
		}
	}
	return Variant{Provider: t, Model: s}
}

// findVariant returns the variant of provider t whose ID is id, and reports
// whether cat lists one.
func findVariant(cat *gateway.Catalog, t ProviderID, id string) (Variant, bool) {
	model, _, option, ok := cat.Lookup(t, id)
	if !ok {
		return Variant{}, false
	}
	return Variant{Provider: t, Model: model, Option: option}, true
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
