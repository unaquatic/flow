package agent_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
)

// variantDoc is an api.json with the reasoning controls and modes
// of real models: effort lists, token budgets, a toggle, and fast and pro
// modes, and a gpt-7 whose effort list holds levels flow does not know,
// light and xxxhigh, and the nil and "default" values that name none. A
// gateway, router, serves gpt-5.6 too, which it ties to OpenAI's.
const variantDoc = `{
"anthropic":{"id":"anthropic","models":{
  "claude-haiku-4-5":{"id":"claude-haiku-4-5","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"budget_tokens","min":1024}]},
  "claude-opus-4-5":{"id":"claude-opus-4-5","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high"]},{"type":"budget_tokens","min":1024}]},
  "claude-opus-4-6":{"id":"claude-opus-4-6","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","max"]},{"type":"budget_tokens","min":1024}]},
  "claude-opus-4-8":{"id":"claude-opus-4-8","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}],
    "cost":{"input":5,"output":25},
    "experimental":{"modes":{"fast":{"cost":{"input":10,"output":50},
      "provider":{"body":{"speed":"fast"},"headers":{"anthropic-beta":"fast-mode-2026-02-01"}}}}}},
  "claude-sonnet-5":{"id":"claude-sonnet-5","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"toggle"},{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}},
"openai":{"id":"openai","models":{
  "gpt-4.1":{"id":"gpt-4.1","limit":{"context":1000000,"output":32768}},
  "gpt-5":{"id":"gpt-5","reasoning":true,"limit":{"context":400000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["minimal","low","medium","high"]}]},
  "gpt-5.5-pro":{"id":"gpt-5.5-pro","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["medium","high","xhigh"]}]},
  "gpt-5.5":{"id":"gpt-5.5","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "gpt-5.6":{"id":"gpt-5.6","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh","max"]}],
    "experimental":{"modes":{
      "fast":{"provider":{"body":{"service_tier":"priority"}}},
      "pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "gpt-7":{"id":"gpt-7","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["light","low","medium","high","max","xxxhigh",null,"default"]}]}
}},
"router":{"id":"router","models":{
  "openai/gpt-5.6":{"id":"openai/gpt-5.6","canonical_model_id":"openai/gpt-5.6","reasoning":true,
    "limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high"]}]}
}}}`

// variantCatalog returns the catalog of variantDoc, and of a gateway,
// router, that serves gpt-5.6 under OpenAI's ID.
func variantCatalog(t *testing.T) *gateway.Catalog {
	t.Helper()
	return gatewaytest.FromAPI(t, variantDoc)
}

// catalogModel returns the offering of model id, without its lab, of
// provider p in variantDoc.
func catalogModel(t *testing.T, p agent.ProviderID, id string) gateway.Offering {
	t.Helper()
	off, ok := agent.Lookup(variantCatalog(t), agent.Variant{Provider: p, Model: string(p) + "/" + id})
	if !ok {
		t.Fatalf("variantDoc lists no %s/%s", p, id)
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
		{chatgpt.ProviderID, "gpt-5.6", []agent.ReasoningEffort{"none", "low", "medium", "high", "xhigh", "max"}},
		{chatgpt.ProviderID, "gpt-5", []agent.ReasoningEffort{"minimal", "low", "medium", "high"}},
		{chatgpt.ProviderID, "gpt-7", []agent.ReasoningEffort{"light", "low", "medium", "high", "max", "xxxhigh"}},
		// The rules say Opus 4.5's reasoning can be turned off, so the
		// catalog lists none.
		{claude.ProviderID, "claude-opus-4-5", []agent.ReasoningEffort{"none", "low", "medium", "high"}},
		{claude.ProviderID, "claude-haiku-4-5", nil},
		{chatgpt.ProviderID, "gpt-4.1", nil},
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
		{name: "no reasoning", provider: chatgpt.ProviderID, model: "gpt-4.1", level: "high", wantNil: true},
		{name: "no level", provider: chatgpt.ProviderID, model: "gpt-5.6"},
		{name: "listed", provider: chatgpt.ProviderID, model: "gpt-5.6", level: "xhigh", wantLevel: "xhigh"},
		{name: "none listed", provider: chatgpt.ProviderID, model: "gpt-5.6", level: "none", wantLevel: "none"},
		{name: "not listed", provider: chatgpt.ProviderID, model: "gpt-5", level: "max"},
		{name: "level flow does not know", provider: chatgpt.ProviderID, model: "gpt-7", level: "xxxhigh", wantLevel: "xxxhigh"},
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
		{"openai/gpt-5.6", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6"}},
		{"openai/gpt-5.6:high", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Effort: "high"}},
		{"openai/gpt-5.6:fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Option: "fast"}},
		// A pro mode is a model of its own.
		{"openai/gpt-5.6-pro:none", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6-pro", Effort: "none"}},
		{"openai/gpt-5.6:pro", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-5.6:pro"}},
		// The variant's whole ID names it too.
		{"openai/openai/gpt-5.6:fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Option: "fast"}},
		// A gateway's model is the lab's, named whole.
		{"router/openai/gpt-5.6:high", agent.Variant{Provider: "router", Model: "openai/gpt-5.6", Effort: "high"}},
		{"anthropic/claude-opus-4-8:fast:max", agent.Variant{Provider: claude.ProviderID, Model: "anthropic/claude-opus-4-8", Option: "fast", Effort: "max"}},
		// A model without a provider names none, and stays whole.
		{"claude-opus-4-8:fast:max", agent.Variant{Model: "claude-opus-4-8:fast:max"}},
		// The catalog's IDs are lower case, and name a variant whatever its
		// case.
		{"openai/GPT-5.6:Fast:HIGH", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6", Option: "fast", Effort: "high"}},
		// A model named by the provider's own ID and a mode, as no variant
		// is, is not the catalog's.
		{"openai/gpt-5.6-fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-5.6-fast"}},
		// A level the model does not list is part of the ID.
		{"openai/gpt-5:none", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-5:none"}},
		// A listed ID is never cut, though it ends in a mode's name.
		{"openai/gpt-5.5-pro", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.5-pro"}},
		// A variant the model lacks, and a model not listed, stay whole.
		{"openai/gpt-5-fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-5-fast"}},
		{"openai/gpt-5:fast", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-5:fast"}},
		// A suffix that names no level is part of the ID.
		{"openai/ft:gpt-5:org", agent.Variant{Provider: chatgpt.ProviderID, Model: "ft:gpt-5:org"}},
		// A level the catalog lists is one, though flow does not know it.
		{"openai/gpt-7:XXXHIGH", agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-7", Effort: "xxxhigh"}},
		{"openai/gpt-7:bogus", agent.Variant{Provider: chatgpt.ProviderID, Model: "gpt-7:bogus"}},
	}
	for _, tt := range tests {
		got := agent.ParseVariant(tt.in, cat)
		if got != tt.want {
			t.Errorf("ParseVariant(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestVariantString(t *testing.T) {
	cat := variantCatalog(t)
	for _, s := range []string{
		"openai/gpt-5.6",
		"openai/gpt-5.6:fast",
		"openai/gpt-5.6-pro:xhigh",
		"anthropic/claude-sonnet-5:high",
		"router/openai/gpt-5.6:high",
	} {
		if got := agent.ParseVariant(s, cat).String(); got != s {
			t.Errorf("ParseVariant(%q).String() = %q", s, got)
		}
	}
	for in, want := range map[string]string{
		// Levels are written in lower case, as the catalog lists them.
		"openai/gpt-5.6:NONE": "openai/gpt-5.6:none",
		// The variant's whole ID is written without the lab.
		"openai/openai/gpt-5.6:fast": "openai/gpt-5.6:fast",
	} {
		if got := agent.ParseVariant(in, cat).String(); got != want {
			t.Errorf("String of %s = %q, want %q", in, got, want)
		}
	}
}

// TestVariants checks that a listing's variants follow the catalog's
// order, each tier and mode after the variant it is of.
func TestVariants(t *testing.T) {
	cat := variantCatalog(t)
	v := agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6"}
	off, _ := agent.Lookup(cat, v)
	var got []string
	for _, w := range agent.Variants(v.Provider, v.Model, off) {
		got = append(got, w.String())
	}
	if want := []string{"openai/gpt-5.6", "openai/gpt-5.6:fast"}; !slices.Equal(got, want) {
		t.Errorf("Variants = %q, want %q", got, want)
	}
	// The pro model runs gpt-5.6 with the pro mode's fields, as its call.
	pro := agent.Variant{Provider: chatgpt.ProviderID, Model: "openai/gpt-5.6-pro"}
	off, ok := agent.Lookup(cat, pro)
	if got := agent.Variants(pro.Provider, pro.Model, off); !ok || len(got) != 1 || off.ProviderOptions.ID != "gpt-5.6" || off.ProviderOptions.Body["reasoning"] == nil {
		t.Errorf("pro variants = %v, offering %+v", got, off)
	}
}
