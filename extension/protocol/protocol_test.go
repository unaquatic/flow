package protocol

import (
	"slices"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
)

// TestLoad checks that the extension loads as a dial and a login hook, which
// dials the endpoints of the formats it speaks and none other, and logs in to
// Anthropic and OpenAI.
func TestLoad(t *testing.T) {
	e, err := Load(t.Context(), extension.Host{})
	if err != nil {
		t.Fatal(err)
	}
	dial, ok := e.(extension.DialHook)
	if !ok {
		t.Fatalf("loaded %T, want an extension.DialHook", e)
	}
	login, ok := e.(extension.LoginHook)
	if !ok {
		t.Fatalf("loaded %T, want an extension.LoginHook", e)
	}

	ep := gateway.Endpoint{Format: gateway.FormatAnthropicMessages, API: "https://a/v1", Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-api-key"}}
	if p, ok := dial.Dial(claude.ProviderID, ep, nil); !ok || p == nil {
		t.Errorf("Dial of an Anthropic endpoint = %v, %v; want a provider", p, ok)
	}
	if p, ok := dial.Dial("other", gateway.Endpoint{API: "https://u"}, nil); ok {
		t.Errorf("Dial of an endpoint of no known format = %v, true; want none", p)
	}

	var got []agent.ProviderID
	for pt, flow := range login.LoginFlows() {
		if flow == nil {
			t.Errorf("login flow of %s is nil", pt)
		}
		got = append(got, pt)
	}
	slices.Sort(got)
	if want := []agent.ProviderID{claude.ProviderID, chatgpt.ProviderID}; !slices.Equal(got, want) {
		t.Errorf("logins = %v, want %v", got, want)
	}
}
