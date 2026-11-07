package ui

import (
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
)

// testCatalog is the api.json of the catalog the tests run on,
// which gatewaytest.FromAPI makes the catalog of: gpt-5.6 takes none
// through max and has a fast tier and a pro mode, gpt-5 takes minimal
// through high, gpt-5-pro takes high alone, and gpt-5.6-luna has a fast
// tier; and the Claude models, Opus taking low through max.
const testCatalog = `{
"openai":{"id":"openai","models":{
  "gpt-5":{"id":"gpt-5","name":"GPT-5","reasoning":true,"release_date":"2025-08-07",
    "limit":{"context":400000,"input":272000,"output":128000},
    "cost":{"input":1.25,"output":10,"cache_read":0.125},
    "reasoning_options":[{"type":"effort","values":["minimal","low","medium","high"]}]},
  "gpt-5-pro":{"id":"gpt-5-pro","name":"GPT-5 Pro","reasoning":true,"release_date":"2025-10-06",
    "limit":{"context":400000,"output":272000},
    "reasoning_options":[{"type":"effort","values":["high"]}]},
  "gpt-5.6":{"id":"gpt-5.6","name":"GPT-5.6","reasoning":true,"release_date":"2026-07-09",
    "limit":{"context":1050000,"input":922000,"output":128000},
    "cost":{"input":2,"output":10,"cache_read":0.2},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh","max"]}],
    "experimental":{"modes":{
      "fast":{"cost":{"input":4,"output":20,"cache_read":0.4},"provider":{"body":{"service_tier":"priority"}}},
      "pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "gpt-5.6-luna":{"id":"gpt-5.6-luna","name":"GPT-5.6 Luna","reasoning":true,"release_date":"2026-07-09",
    "limit":{"context":400000,"output":128000},
    "cost":{"input":0.2,"output":1.2},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"fast":{"cost":{"input":0.4,"output":2.4},"provider":{"body":{"service_tier":"priority"}}}}}}
}},
"anthropic":{"id":"anthropic","models":{
  "claude-sonnet-4":{"id":"claude-sonnet-4","name":"Claude Sonnet 4","limit":{"context":200000,"output":64000},
    "cost":{"input":3,"output":15}},
  "claude-haiku-4-5":{"id":"claude-haiku-4-5","name":"Claude Haiku 4.5","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"budget_tokens","min":1024}]},
  "claude-opus-4-8":{"id":"claude-opus-4-8","name":"Claude Opus 4.8","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]},
  "claude-opus-5-5":{"id":"claude-opus-5-5","name":"Claude Opus 5.5","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}}}`

// writeTestCatalog caches the catalog of testCatalog in home, as the
// app's catalog store keeps a download there.
func writeTestCatalog(t *testing.T, home string) {
	t.Helper()
	gatewaytest.WriteCache(t, home, gatewaytest.FromAPI(t, testCatalog))
}

// offeringOf returns the catalog's offering of model, a variant's model in
// "provider/model" form, in a's catalog.
func offeringOf(t *testing.T, a *app.App, model string) gateway.Offering {
	t.Helper()
	off, ok := agent.Lookup(a.Models().Catalog(), a.Models().ParseVariant(model))
	if !ok {
		t.Fatalf("the catalog does not list %s", model)
	}
	return off
}

// listing returns the listing of provider t's models ids in cat, each named
// by the catalog's model ID or, when the catalog does not list it, as an
// unlisted model of no variants.
func listing(cat *gateway.Catalog, t agent.ProviderID, ids ...string) app.ModelListing {
	l := app.ModelListing{Provider: t}
	for _, id := range ids {
		model := string(t) + "/" + id
		if off, ok := cat.Providers[t].Models[gateway.ModelID(model)]; ok {
			l.Models = append(l.Models, agent.Listing{Model: model, Offering: off})
			continue
		}
		l.Models = append(l.Models, agent.Listing{Model: id, Unlisted: true, Offering: gateway.Offering{Name: id,
			ProviderOptions: gateway.ProviderOptions{ID: id}}})
	}
	return l
}
