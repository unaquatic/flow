package app

import (
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
	"github.com/unaquatic/flow/internal/catalog"
)

// providersCatalog is an api.json of the built-in providers, a gateway
// whose base URL names its host in an environment variable beside its key,
// Google's Gemini API, and Amazon Bedrock and GitLab, whose formats the
// catalog does not know, so leaves them out.
const providersCatalog = `{
"anthropic":{"id":"anthropic","models":{}},
"openai":{"id":"openai","models":{}},
"gw":{"id":"gw","npm":"@ai-sdk/openai-compatible","api":"https://${GW_HOST}/v1","env":["GW_HOST","GW_API_KEY"],"models":{}},
"google":{"id":"google","npm":"@ai-sdk/google","env":["GEMINI_API_KEY"],"models":{}},
"amazon-bedrock":{"id":"amazon-bedrock","npm":"@ai-sdk/amazon-bedrock","env":["AWS_ACCESS_KEY_ID","AWS_SECRET_ACCESS_KEY","AWS_REGION","AWS_BEARER_TOKEN_BEDROCK"],"models":{}},
"gitlab":{"id":"gitlab","npm":"@gitlab/gitlab-ai-provider","env":["GITLAB_TOKEN"],"models":{}}
}`

// TestNew_MakesProvidersOfExtensions checks that an app registers every
// provider of the catalog whose format an extension's DialHook speaks, and
// not the ones the configuration disables, and none without such an
// extension.
func TestNew_MakesProvidersOfExtensions(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"catalog_source":"http://127.0.0.1:1","provider":{"openai":{"disabled":true}}}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, gatewaytest.FromAPI(t, providersCatalog))
	registered := func(loads ...extension.Load) []agent.ProviderID {
		t.Helper()
		a, err := New(t.Context(), config.NewService(home), Options{Extensions: loads})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		return a.providers.Providers()
	}

	if got, want := registered(protocol.Load), []agent.ProviderID{"anthropic", "google", "gw"}; !slices.Equal(got, want) {
		t.Errorf("Providers = %v, want %v: not the disabled openai, not Bedrock or GitLab, which the catalog leaves out", got, want)
	}
	if got := registered(); len(got) != 0 {
		t.Errorf("Providers without an extension that dials = %v, want none", got)
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
	gatewaytest.WriteCache(t, home, gatewaytest.FromAPI(t, providersCatalog))
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
