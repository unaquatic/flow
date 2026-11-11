package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// The providers of testCatalog: provider-a speaks the Responses format, and
// provider-b the Messages format.
const (
	providerA agent.ProviderID = "provider-a"
	providerB agent.ProviderID = "provider-b"
)

// testLab returns the lab testCatalog ties the models of provider t to:
// lab-a for provider-a, and lab-b for provider-b.
func testLab(t agent.ProviderID) string {
	return strings.Replace(string(t), "provider-", "lab-", 1)
}

// testCatalog is the api.json of the catalog the tests run on,
// which protocoltest.FromAPI makes the catalog of: provider-a serves the
// models of lab-a, of which model-a takes none through max and has a fast
// tier and a pro mode, model-c takes minimal through high, model-c-pro
// takes high alone, and model-b has a fast tier; and provider-b serves
// those of lab-b, of which model-f takes none through max and model-g low
// through max.
const testCatalog = `{
"provider-a":{"id":"provider-a","name":"Provider A","format":"responses","models":{
  "lab-a/model-c":{"id":"lab-a/model-c","name":"Model C","reasoning":true,"release_date":"2025-08-07",
    "limit":{"context":400000,"input":272000,"output":128000},
    "cost":{"input":1.25,"output":10,"cache_read":0.125},
    "reasoning_options":[{"type":"effort","values":["minimal","low","medium","high"]}]},
  "lab-a/model-c-pro":{"id":"lab-a/model-c-pro","name":"Model C Pro","reasoning":true,"release_date":"2025-10-06",
    "limit":{"context":400000,"output":272000},
    "reasoning_options":[{"type":"effort","values":["high"]}]},
  "lab-a/model-a":{"id":"lab-a/model-a","name":"Model A","reasoning":true,"release_date":"2026-07-09",
    "limit":{"context":1050000,"input":922000,"output":128000},
    "cost":{"input":2,"output":10,"cache_read":0.2},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh","max"]}],
    "experimental":{"modes":{
      "fast":{"cost":{"input":4,"output":20,"cache_read":0.4},"provider":{"body":{"service_tier":"priority"}}},
      "pro":{"provider":{"body":{"reasoning":{"mode":"pro"}}}}}}},
  "lab-a/model-b":{"id":"lab-a/model-b","name":"Model B","reasoning":true,"release_date":"2026-07-09",
    "limit":{"context":400000,"output":128000},
    "cost":{"input":0.2,"output":1.2},
    "reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}],
    "experimental":{"modes":{"fast":{"cost":{"input":0.4,"output":2.4},"provider":{"body":{"service_tier":"priority"}}}}}}
}},
"provider-b":{"id":"provider-b","name":"Provider B","format":"messages","models":{
  "lab-b/model-d":{"id":"lab-b/model-d","name":"Model D","limit":{"context":200000,"output":64000},
    "cost":{"input":3,"output":15}},
  "lab-b/model-e":{"id":"lab-b/model-e","name":"Model E","reasoning":true,"limit":{"context":200000,"output":64000},
    "reasoning_options":[{"type":"budget_tokens","min":1024}]},
  "lab-b/model-f":{"id":"lab-b/model-f","name":"Model F","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"toggle"},{"type":"effort","values":["low","medium","high","xhigh","max"]}]},
  "lab-b/model-g":{"id":"lab-b/model-g","name":"Model G","reasoning":true,"limit":{"context":1000000,"output":128000},
    "reasoning_options":[{"type":"effort","values":["low","medium","high","xhigh","max"]}]}
}}}`

// writeTestCatalog caches the catalog of testCatalog in home, as the
// app's catalog store keeps a download there, with high the default effort
// of model-f, as the catalog's rules give one to the models whose labs
// document it, which an api.json cannot.
func writeTestCatalog(t *testing.T, home string) {
	t.Helper()
	cat := protocoltest.FromAPI(t, testCatalog)
	cat.Providers[providerB].Models["lab-b/model-f"].Reasoning.DefaultEffort = "high"
	gatewaytest.WriteCache(t, home, cat)
}

// loginTestFlows is the extension that logs in to the providers of
// testCatalog, with flows that never sign in.
type loginTestFlows struct{}

// Name identifies the extension.
func (loginTestFlows) Name() string { return "test_login_flows" }

// LoginFlows returns a flow of provider-a and one of provider-b.
func (loginTestFlows) LoginFlows() map[agent.ProviderID]agent.LoginFlow {
	return map[agent.ProviderID]agent.LoginFlow{providerA: noLogin{}, providerB: noLogin{}}
}

// loadLoginTestFlows loads loginTestFlows.
func loadLoginTestFlows(context.Context, extension.Host) (extension.Extension, error) {
	return loginTestFlows{}, nil
}

// noLogin is a LoginFlow that never signs in.
type noLogin struct{}

func (noLogin) Device(context.Context, func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	return agent.OAuthToken{}, errors.ErrUnsupported
}

func (noLogin) Refresh(context.Context, agent.OAuthToken) (agent.OAuthToken, error) {
	return agent.OAuthToken{}, errors.ErrUnsupported
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
// by the catalog's model ID, of the lab testCatalog ties t's models to, or,
// when the catalog does not list it, by its own ID, as a model of no
// variants.
func listing(cat *gateway.Catalog, t agent.ProviderID, ids ...string) app.ModelListing {
	l := app.ModelListing{Provider: t}
	for _, id := range ids {
		model := testLab(t) + "/" + id
		if off, ok := cat.Providers[t].Models[gateway.ModelID(model)]; ok {
			l.Models = append(l.Models, agent.Listing{Model: model, Offering: off})
			continue
		}
		l.Models = append(l.Models, agent.Listing{Model: id, Offering: gateway.Offering{Name: id,
			ProviderOptions: gateway.ProviderOptions{ID: id}}})
	}
	return l
}
