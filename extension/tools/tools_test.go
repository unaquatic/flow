package tools

import (
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
)

// TestLoad checks that the extension loads, as a tool hook, with the tools
// flow offers the model: read, write, edit, bash, and todo.
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
	if want := []string{"read", "write", "edit", "bash", "todo"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
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
