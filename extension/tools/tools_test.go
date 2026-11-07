package tools

import (
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
)

// TestLoad checks that the extension loads with bash, the one tool flow
// offers the model, as a tool hook.
func TestLoad(t *testing.T) {
	e, err := Load(t.Context(), extension.Host{})
	if err != nil {
		t.Fatal(err)
	}
	h, ok := e.(extension.ToolHook)
	if !ok {
		t.Fatalf("loaded %T, want an extension.ToolHook", e)
	}
	var names []string
	for _, tl := range h.Tools() {
		names = append(names, tl.Name)
	}
	if len(names) != 1 || names[0] != "bash" {
		t.Errorf("tools = %v, want [bash]", names)
	}
}

// TestTools_ReturnsACopy checks that changing the slice Tools returns leaves
// the extension's tools as they were.
func TestTools_ReturnsACopy(t *testing.T) {
	e := New(agent.Tool{Name: "a"})
	e.Tools()[0].Name = "b"
	if got := e.Toolset[0].Name; got != "a" {
		t.Errorf("Toolset[0].Name = %q after changing Tools' slice, want a", got)
	}
}
