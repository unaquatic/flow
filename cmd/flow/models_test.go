package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// TestWriteModelRows checks that each variant is a line of its own, and
// that the reasoning efforts of a model that takes them follow its name in
// a column.
func TestWriteModelRows(t *testing.T) {
	listing := app.ModelListing{
		Provider:         "provider-a",
		Models:           []agent.Listing{{Model: "lab-a/model-a"}, {Model: "lab-a/model-bb"}},
		ReasoningEfforts: map[string][]agent.ReasoningEffort{"lab-a/model-a": {"low", "high"}},
	}
	var out strings.Builder
	if err := writeModelRows(&out, modelRows([]app.ModelListing{listing})); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "provider-a/lab-a/model-a   low, high\nprovider-a/lab-a/model-bb\n"; got != want {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// TestModels checks that flow models fails with errNoModels while no
// provider is signed in, and then lists the catalog's model of the
// provider an API key signs in to, by itself or named.
func TestModels(t *testing.T) {
	a := newModelsTestApp(t)
	var out strings.Builder
	if err := runModels(t.Context(), &out, a, ModelsOptions{Format: OutputText}, nil); !errors.Is(err, errNoModels) {
		t.Errorf("models with no provider signed in: err = %v, want %v", err, errNoModels)
	}
	if err := runModels(t.Context(), &out, a, ModelsOptions{Format: OutputJSON}, nil); err != nil || out.String() != "[]\n" {
		t.Errorf("models as JSON with no provider signed in = %q, %v; want []", out.String(), err)
	}

	if _, err := a.Accounts().SetKey(t.Context(), testProvider, "", agent.APIKey("sk-test")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {string(testProvider)}} {
		out.Reset()
		if err := runModels(t.Context(), &out, a, ModelsOptions{Format: OutputText}, args); err != nil {
			t.Fatal(err)
		}
		if got, want := out.String(), "provider-a/lab-a/model-a\n"; got != want {
			t.Errorf("models %q = %q, want %q", args, got, want)
		}
	}
}

// newModelsTestApp returns an app as newTestApp does, whose catalog lists
// one model, lab-a/model-a, at testProvider.
func newModelsTestApp(t *testing.T) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv(keyEnv, "")
	home := t.TempDir()
	settings := `{"snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, `{"provider-a":{"id":"provider-a","format":"responses","env":["`+keyEnv+`"],`+
		`"models":{"lab-a/model-a":{"id":"lab-a/model-a","limit":{"context":1000,"output":100}}}}}`))
	a, err := app.New(t.Context(), config.NewService(home), app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}
