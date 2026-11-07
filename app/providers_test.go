package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/flow/internal/catalog"
)

// providersCatalog is an api.json of two providers with login flows, a
// gateway whose base URL names its host in an environment variable beside
// its key, a provider of the generateContent format, and two whose formats the
// catalog does not know, so leaves them out.
const providersCatalog = `{
"provider-b":{"id":"provider-b","format":"messages","models":{}},
"provider-a":{"id":"provider-a","format":"responses","models":{}},
"gw":{"id":"gw","format":"chat","api":"https://${GW_HOST}/v1","env":["GW_HOST","GW_API_KEY"],"models":{}},
"provider-c":{"id":"provider-c","format":"content","env":["PROVIDER_C_API_KEY"],"models":{}},
"provider-d":{"id":"provider-d","npm":"unknown-package","env":["PROVIDER_D_ACCESS_KEY","PROVIDER_D_REGION"],"models":{}},
"provider-e":{"id":"provider-e","npm":"other-package","env":["PROVIDER_E_TOKEN"],"models":{}}
}`

// loginExtension is the protocol extension, which dials the providers of
// the formats it speaks, with login flows of provider-a and provider-b in
// place of its own.
type loginExtension struct{ *protocol.Extension }

// LoginFlows returns a flow of provider-a and one of provider-b.
func (loginExtension) LoginFlows() map[agent.ProviderID]agent.LoginFlow {
	return map[agent.ProviderID]agent.LoginFlow{providerA: noLogin{}, providerB: noLogin{}}
}

// noLogin is a LoginFlow that never logs in.
type noLogin struct{}

func (noLogin) Device(context.Context, func(agent.LoginPrompt)) (agent.OAuthToken, error) {
	return agent.OAuthToken{}, errors.ErrUnsupported
}

func (noLogin) Refresh(context.Context, agent.OAuthToken) (agent.OAuthToken, error) {
	return agent.OAuthToken{}, errors.ErrUnsupported
}

// TestNew_MakesProvidersOfExtensions checks that an app registers every
// provider of the catalog whose format an extension's DialHook speaks, and
// not the ones the configuration disables, and none without such an
// extension.
func TestNew_MakesProvidersOfExtensions(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"catalog_source":"http://127.0.0.1:1","provider":{"provider-a":{"disabled":true}}}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, providersCatalog))
	registered := func(loads ...extension.Load) ([]agent.ProviderID, []agent.ProviderID) {
		t.Helper()
		a, err := New(t.Context(), config.NewService(home), Options{Extensions: loads})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		return a.Accounts().Providers(), a.Accounts().LoginProviders()
	}

	got, logins := registered(loadOf(loginExtension{protocol.New(protocol.Options{})}))
	if want := []agent.ProviderID{"gw", providerB, "provider-c"}; !slices.Equal(got, want) {
		t.Errorf("Providers = %v, want %v: not the disabled provider-a, not provider-d or provider-e, which the catalog leaves out", got, want)
	}
	if want := []agent.ProviderID{providerB}; !slices.Equal(logins, want) {
		t.Errorf("LoginProviders = %v, want %v: not API-key-only providers or the disabled provider-a", logins, want)
	}
	got, logins = registered()
	if len(got) != 0 {
		t.Errorf("Providers without an extension that dials = %v, want none", got)
	}
	if len(logins) != 0 {
		t.Errorf("LoginProviders without extensions = %v, want none", logins)
	}
}

// TestConfigKeys checks that a provider's key is the configuration's, or
// else the first environment variable the catalog names for it that its base
// URL does not take, and that holds a key.
func TestConfigKeys(t *testing.T) {
	home := t.TempDir()
	settings := `{"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, providersCatalog))
	conf := config.NewService(home)
	keys := configKeys(conf, catalog.NewStore(gateway.StoreOptions{Dir: home}))

	t.Setenv("GW_HOST", "gw.example")
	t.Setenv("GW_API_KEY", "sk-gw")
	if k, err := keys("gw"); k != "sk-gw" || err != nil {
		t.Errorf("gw key = %q, %v, want sk-gw, not the host its base URL takes", k, err)
	}
	t.Setenv("GW_API_KEY", "")
	if k, err := keys("gw"); k != "" || err != nil {
		t.Errorf("gw key without GW_API_KEY = %q, %v, want none", k, err)
	}

	settings = `{"catalog_source":"http://127.0.0.1:1","provider":{"gw":{"api_key":"sk-config"}}}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GW_API_KEY", "sk-gw")
	if k, err := keys("gw"); k != "sk-config" || err != nil {
		t.Errorf("gw key = %q, %v, want the configuration's, sk-config", k, err)
	}
}
