package agent_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// The providers of the tests' catalogs: provider-a speaks the Responses format
// and provider-b the Messages format, as their api.json entries say.
const (
	providerA agent.ProviderID = "provider-a"
	providerB agent.ProviderID = "provider-b"
)

// variantDoc is an api.json with the reasoning controls and modes that
// models have: effort lists, token budgets, a toggle, and fast and pro
// modes, and a model-d whose effort list holds levels flow does not know,
// light and xxxhigh, and the nil and "default" values that name none. A
// gateway, router, serves lab-a/model-a too, the model provider-a serves.
const variantDoc = `{
"provider-b":{"id":"provider-b","format":"messages","models":{
  "lab-b/model-f":{"id":"lab-b/model-f","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"budget_tokens","min":1024}]},
  "lab-b/model-g":{"id":"lab-b/model-g","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"toggle"},{"type":"effort","values":["low","medium","high"]},{"type":"budget_tokens","min":1024}]},
  "lab-b/model-h":{"id":"lab-b/model-h","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","max"]},{"type":"budget_tokens","min":1024}]},
  "lab-b/model-i":{"id":"lab-b/model-i","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}],
    "cost":{"input":5,"output":25},
    "experimental":{"modes":{"fast":{"cost":{"input":10,"output":50},
      "provider":{"body":{"speed":"fast"},"headers":{"provider-beta":"fast-mode-2026-02-01"}}}}}},
  "lab-b/model-j":{"id":"lab-b/model-j","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"toggle"},{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}},
"provider-a":{"id":"provider-a","format":"responses","models":{
  "lab-a/model-e":{"id":"lab-a/model-e","limit":{"context":1000000,"output":32768}},
  "lab-a/model-b":{"id":"lab-a/model-b","reasoning":true,"limit":{"context":400000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["minimal","low","medium","high"]}]},
  "lab-a/model-c-pro":{"id":"lab-a/model-c-pro","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["medium","high","xhigh"]}]},
  "lab-a/model-c":{"id":"lab-a/model-c","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "lab-a/model-a":{"id":"lab-a/model-a","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh","max"]}],
    "experimental":{"modes":{
      "fast":{"provider":{"body":{"service_tier":"priority"}}},
      "pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "lab-a/model-d":{"id":"lab-a/model-d","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["light","low","medium","high","max","xxxhigh",null,"default"]}]}
}},
"router":{"id":"router","models":{
  "lab-a/model-a":{"id":"lab-a/model-a","reasoning":true,
    "limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high"]}]}
}}}`

// variantCatalog returns the catalog of variantDoc, and of a gateway,
// router, that serves model-a under provider-a's ID.
func variantCatalog(t *testing.T) *gateway.Catalog {
	t.Helper()
	return protocoltest.FromAPI(t, variantDoc)
}

// catalogModel returns the offering of model id, without its lab, of
// provider p in variantDoc, whose models of provider-a are lab-a's, and
// of provider-b lab-b's.
func catalogModel(t *testing.T, p agent.ProviderID, id string) gateway.Offering {
	t.Helper()
	model := strings.Replace(string(p), "provider-", "lab-", 1) + "/" + id
	off, ok := agent.Lookup(variantCatalog(t), agent.Variant{Provider: p, Model: model})
	if !ok {
		t.Fatalf("variantDoc lists no %s at %s", model, p)
	}
	return off
}

func TestParseReasoningEffort(t *testing.T) {
	for in, want := range map[string]agent.ReasoningEffort{"high": "high", " XHigh ": "xhigh", "": ""} {
		if got := agent.ParseReasoningEffort(in); got != want {
			t.Errorf("ParseReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReasoningEfforts checks that a model's levels are the effort values
// its catalog offering lists, in its order, without the null and "default"
// values that name none, and that a model with no effort list has none.
func TestReasoningEfforts(t *testing.T) {
	tests := []struct {
		provider agent.ProviderID
		model    string
		want     []agent.ReasoningEffort
	}{
		{providerA, "model-a", []agent.ReasoningEffort{"none", "low", "medium", "high", "xhigh", "max"}},
		{providerA, "model-b", []agent.ReasoningEffort{"minimal", "low", "medium", "high"}},
		{providerA, "model-d", []agent.ReasoningEffort{"light", "low", "medium", "high", "max", "xxxhigh"}},
		// model-g's toggle says its reasoning can be turned off, so the
		// catalog lists none.
		{providerB, "model-g", []agent.ReasoningEffort{"none", "low", "medium", "high"}},
		{providerB, "model-f", nil},
		{providerA, "model-e", nil},
	}
	for _, tt := range tests {
		if got := agent.ReasoningEfforts(catalogModel(t, tt.provider, tt.model).Reasoning); !slices.Equal(got, tt.want) {
			t.Errorf("ReasoningEfforts(%s) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestFit(t *testing.T) {
	levels := []agent.ReasoningEffort{"low", "medium", "high"}
	tests := []struct {
		chosen, def, want agent.ReasoningEffort
	}{
		{"low", "medium", "low"},
		{"max", "medium", "medium"},
		{"", "medium", "medium"},
		{"max", "xhigh", ""},
	}
	for _, tt := range tests {
		if got := agent.Fit(levels, tt.chosen, tt.def); got != tt.want {
			t.Errorf("Fit(%q, %q) = %q, want %q", tt.chosen, tt.def, got, tt.want)
		}
	}
}

// TestReasoningFor checks that a request reasons at a level the model's
// catalog offering lists, at none when it lists another, and not at all for
// a model that does not reason, with the offering's reasoning controls.
func TestReasoningFor(t *testing.T) {
	tests := []struct {
		name      string
		provider  agent.ProviderID
		model     string
		level     agent.ReasoningEffort
		wantNil   bool
		wantLevel agent.ReasoningEffort
	}{
		{name: "no reasoning", provider: providerA, model: "model-e", level: "high", wantNil: true},
		{name: "no level", provider: providerA, model: "model-a"},
		{name: "listed", provider: providerA, model: "model-a", level: "xhigh", wantLevel: "xhigh"},
		{name: "none listed", provider: providerA, model: "model-a", level: "none", wantLevel: "none"},
		{name: "not listed", provider: providerA, model: "model-b", level: "max"},
		{name: "level flow does not know", provider: providerA, model: "model-d", level: "xxxhigh", wantLevel: "xxxhigh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off := catalogModel(t, tt.provider, tt.model)
			got := agent.ReasoningFor(off, tt.level)
			if got == nil || tt.wantNil {
				if (got == nil) != tt.wantNil {
					t.Errorf("ReasoningFor = %+v, want nil %v", got, tt.wantNil)
				}
				return
			}
			if got.Effort != tt.wantLevel || !reflect.DeepEqual(got.Controls, *off.Reasoning) {
				t.Errorf("level, controls = %q, %+v, want %q and the model's", got.Effort, got.Controls, tt.wantLevel)
			}
		})
	}
}

func TestParseVariant(t *testing.T) {
	cat := variantCatalog(t)
	tests := []struct {
		in   string
		want agent.Variant
	}{
		{"provider-a/lab-a/model-a", agent.Variant{Provider: providerA, Model: "lab-a/model-a"}},
		{"provider-a/lab-a/model-a:high", agent.Variant{Provider: providerA, Model: "lab-a/model-a", Effort: "high"}},
		{"provider-a/lab-a/model-a:fast", agent.Variant{Provider: providerA, Model: "lab-a/model-a", Option: "fast"}},
		// A pro mode is a model of its own.
		{"provider-a/lab-a/model-a-pro:none", agent.Variant{Provider: providerA, Model: "lab-a/model-a-pro", Effort: "none"}},
		{"provider-a/lab-a/model-a:pro", agent.Variant{Provider: providerA, Model: "lab-a/model-a:pro"}},
		// A model ID without its lab is not the catalog's.
		{"provider-a/model-a:fast", agent.Variant{Provider: providerA, Model: "model-a:fast"}},
		{"router/lab-a/model-a:high", agent.Variant{Provider: "router", Model: "lab-a/model-a", Effort: "high"}},
		{"provider-b/lab-b/model-i:fast:max", agent.Variant{Provider: providerB, Model: "lab-b/model-i", Option: "fast", Effort: "max"}},
		// A model without a provider names none, and stays whole.
		{"model-i:fast:max", agent.Variant{Model: "model-i:fast:max"}},
		// The catalog's IDs are lower case, and name a variant whatever its
		// case.
		{"provider-a/LAB-A/MODEL-A:Fast:HIGH", agent.Variant{Provider: providerA, Model: "lab-a/model-a", Option: "fast", Effort: "high"}},
		// A model named with a mode, as no variant is, is not the catalog's.
		{"provider-a/lab-a/model-a-fast", agent.Variant{Provider: providerA, Model: "lab-a/model-a-fast"}},
		// A level the model does not list is part of the ID.
		{"provider-a/lab-a/model-b:none", agent.Variant{Provider: providerA, Model: "lab-a/model-b:none"}},
		// A listed ID is never cut, though it ends in a mode's name.
		{"provider-a/lab-a/model-c-pro", agent.Variant{Provider: providerA, Model: "lab-a/model-c-pro"}},
		// A variant the model lacks stays whole.
		{"provider-a/lab-a/model-b:fast", agent.Variant{Provider: providerA, Model: "lab-a/model-b:fast"}},
		// A suffix that names no level is part of the ID.
		{"provider-a/ft:model-b:org", agent.Variant{Provider: providerA, Model: "ft:model-b:org"}},
		// A level the catalog lists is one, though flow does not know it.
		{"provider-a/lab-a/model-d:XXXHIGH", agent.Variant{Provider: providerA, Model: "lab-a/model-d", Effort: "xxxhigh"}},
		{"provider-a/lab-a/model-d:bogus", agent.Variant{Provider: providerA, Model: "lab-a/model-d:bogus"}},
	}
	for _, tt := range tests {
		got := agent.ParseVariant(tt.in, cat)
		if got != tt.want {
			t.Errorf("ParseVariant(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

// TestVariantString checks that String writes the provider and then Ref,
// the form ParseVariant reads back.
func TestVariantString(t *testing.T) {
	cat := variantCatalog(t)
	for _, s := range []string{
		"provider-a/lab-a/model-a",
		"provider-a/lab-a/model-a:fast",
		"provider-a/lab-a/model-a-pro:xhigh",
		"provider-b/lab-b/model-j:high",
		"router/lab-a/model-a:high",
	} {
		v := agent.ParseVariant(s, cat)
		if got := v.String(); got != s || got != string(v.Provider)+"/"+v.Ref() {
			t.Errorf("ParseVariant(%q).String() = %q, Ref %q", s, got, v.Ref())
		}
	}
	// Levels are written in lower case, as the catalog lists them.
	if got := agent.ParseVariant("provider-a/lab-a/model-a:NONE", cat).String(); got != "provider-a/lab-a/model-a:none" {
		t.Errorf("String = %q, want provider-a/lab-a/model-a:none", got)
	}
	// No model writes as none, not "/", and reads back as none.
	if got := (agent.Variant{}).String(); got != "" {
		t.Errorf("Variant{}.String() = %q, want \"\"", got)
	}
	if v := agent.ParseVariant("", cat); v != (agent.Variant{}) {
		t.Errorf("ParseVariant(\"\") = %+v, want the zero Variant", v)
	}
	// A model without a provider keeps its slash, so that a model ID with
	// a lab in it is not read back as a provider.
	v := agent.Variant{Model: "lab-a/model-a"}
	if got := v.String(); got != "/lab-a/model-a" || agent.ParseVariant(got, cat) != v {
		t.Errorf("String() = %q, read back as %+v, want /lab-a/model-a and %+v", got, agent.ParseVariant(got, cat), v)
	}
}

// TestVariantRef checks that Ref writes a variant without its provider, as
// its ID and its effort, and that ParseVariantAt reads it back at that
// provider.
func TestVariantRef(t *testing.T) {
	cat := variantCatalog(t)
	tests := []struct {
		provider agent.ProviderID
		ref      string
	}{
		{providerA, "lab-a/model-a"},
		{providerA, "lab-a/model-a:fast"},
		{providerA, "lab-a/model-a-pro:xhigh"},
		{providerB, "lab-b/model-j:high"},
		{"router", "lab-a/model-a:high"},
		// A model the catalog does not list keeps the ID it is named by.
		{providerA, "model-b:fast"},
	}
	for _, tt := range tests {
		v := agent.ParseVariantAt(tt.provider, tt.ref, cat)
		if v.Provider != tt.provider || v.Ref() != tt.ref {
			t.Errorf("ParseVariantAt(%s, %q) = %+v, Ref %q; want it back", tt.provider, tt.ref, v, v.Ref())
		}
	}
	if v := agent.ParseVariantAt("", "", cat); v != (agent.Variant{}) {
		t.Errorf("ParseVariantAt of no model = %+v, want the zero Variant", v)
	}
}

// TestVariants checks that a listing's variants follow the catalog's
// order, each tier and mode after the variant it is of.
func TestVariants(t *testing.T) {
	cat := variantCatalog(t)
	v := agent.Variant{Provider: providerA, Model: "lab-a/model-a"}
	off, _ := agent.Lookup(cat, v)
	var got []string
	for _, w := range agent.Variants(v.Provider, v.Model, off) {
		got = append(got, w.String())
	}
	if want := []string{"provider-a/lab-a/model-a", "provider-a/lab-a/model-a:fast"}; !slices.Equal(got, want) {
		t.Errorf("Variants = %q, want %q", got, want)
	}
	// The pro model runs model-a with the pro mode's fields, as its call.
	pro := agent.Variant{Provider: providerA, Model: "lab-a/model-a-pro"}
	off, ok := agent.Lookup(cat, pro)
	if got := agent.Variants(pro.Provider, pro.Model, off); !ok || len(got) != 1 || off.ProviderOptions.ID != "lab-a/model-a" || off.ProviderOptions.Body["reasoning"] == nil {
		t.Errorf("pro variants = %v, offering %+v", got, off)
	}
}
