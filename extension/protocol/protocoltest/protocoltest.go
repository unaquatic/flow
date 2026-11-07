// Package protocoltest builds the catalogs of flow's tests from api.json
// documents, as gatewaytest.FromAPI does, in which a provider, or the
// provider override of a model, may name its request format in a "format"
// field by the protocol package of flow that speaks it:
//
//   - "messages", of package messages, with an API key in a header;
//   - "responses", of package responses, with a bearer token;
//   - "chat", of package completions, with a bearer token, which a
//     provider that names no format takes too;
//   - "content", of package content, with an API key in a header.
package protocoltest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/extension/protocol"
)

// formats maps the formats a document names to the endpoints' formats and
// the ways their API keys go.
var formats = map[string]struct {
	format gateway.Format
	auth   gateway.Auth
}{
	"messages":  {protocol.FormatMessages, gateway.Auth{Scheme: gateway.AuthHeader}},
	"responses": {protocol.FormatResponses, gateway.Auth{Scheme: gateway.AuthBearer}},
	"chat":      {protocol.FormatChat, gateway.Auth{Scheme: gateway.AuthBearer}},
	"content":   {protocol.FormatContent, gateway.Auth{Scheme: gateway.AuthHeader}},
}

// FromAPI returns the catalog of doc, an api.json document whose formats
// are named as the package doc says, as gatewaytest.FromAPI builds it.
func FromAPI(t testing.TB, doc string) *gateway.Catalog {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(doc))
	d.UseNumber()
	var api map[string]map[string]any
	if err := d.Decode(&api); err != nil {
		t.Fatalf("protocoltest: decode api.json: %v", err)
	}
	// The format of each provider, and of each model whose override names
	// one, by the provider's own model ID.
	providers, models := map[gateway.ProviderID]string{}, map[gateway.ProviderID]map[string]string{}
	for pid, p := range api {
		if f := takeFormat(t, p); f != "" {
			providers[gateway.ProviderID(pid)] = f
		}
		ms, _ := p["models"].(map[string]any)
		for mid, m := range ms {
			m, _ := m.(map[string]any)
			o, _ := m["provider"].(map[string]any)
			if f := takeFormat(t, o); f != "" {
				if models[gateway.ProviderID(pid)] == nil {
					models[gateway.ProviderID(pid)] = map[string]string{}
				}
				models[gateway.ProviderID(pid)][mid] = f
			}
		}
	}
	b, err := json.Marshal(api)
	if err != nil {
		t.Fatal(err)
	}
	cat := gatewaytest.FromAPI(t, string(b))
	for pid, p := range cat.Providers {
		if f, ok := providers[pid]; ok {
			setFormat(&p.Endpoint, f)
		}
		for _, offs := range []map[string]gateway.Offering{offerings(p.Models), p.Unlisted} {
			for _, off := range offs {
				if f, ok := models[pid][off.ProviderOptions.ID]; ok && off.ProviderOptions.Endpoint != nil {
					setFormat(off.ProviderOptions.Endpoint, f)
				}
			}
		}
		cat.Providers[pid] = p
	}
	return cat
}

// Store returns gatewaytest.Store(t, FromAPI(t, doc)): a Store over a new
// temporary directory, which holds the catalog of doc as its cached
// download, and the directory.
func Store(t testing.TB, doc string) (*gateway.Store, string) {
	t.Helper()
	return gatewaytest.Store(t, FromAPI(t, doc))
}

// takeFormat removes the "format" field of obj, which may be nil, and
// returns it, or "" when obj names none.
func takeFormat(t testing.TB, obj map[string]any) string {
	t.Helper()
	f, ok := obj["format"]
	if !ok {
		return ""
	}
	delete(obj, "format")
	name, _ := f.(string)
	if _, ok := formats[name]; !ok {
		t.Fatalf("protocoltest: unknown format %v", f)
	}
	return name
}

// setFormat makes ep an endpoint of format f.
func setFormat(ep *gateway.Endpoint, f string) {
	ep.Format, ep.Auth = formats[f].format, formats[f].auth
}

// offerings returns offs keyed by strings, as a provider's unlisted
// offerings are.
func offerings(offs map[gateway.ModelID]gateway.Offering) map[string]gateway.Offering {
	out := make(map[string]gateway.Offering, len(offs))
	for id, off := range offs {
		out[string(id)] = off
	}
	return out
}
