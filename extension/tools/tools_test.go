package tools

import (
	"slices"
	"strings"
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

// TestSelection checks that a Selection offers DefaultTools when it names
// none, the built-in tools that Only names or matches in place of them,
// even those Load leaves out, without those Exclude names, in the order All
// returns them, and none for an empty Only.
func TestSelection(t *testing.T) {
	for _, tt := range []struct {
		name string
		sel  Selection
		want []string
	}{
		{"zero", Selection{}, DefaultTools},
		{"only", Selection{Only: []string{"grep", "read"}}, []string{"read", "grep"}},
		{"exclude", Selection{Exclude: []string{"bash", "write"}}, []string{"read", "edit", "todo"}},
		{"pattern", Selection{Only: []string{"*"}, Exclude: []string{"[bw]*"}}, []string{"read", "edit", "todo", "grep", "glob", "ls"}},
		{"none", Selection{Only: []string{}}, nil},
	} {
		ts, err := tt.sel.Tools()
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var names []string
		for _, tl := range ts {
			names = append(names, tl.Name)
		}
		if !slices.Equal(names, tt.want) {
			t.Errorf("%s: tools = %v, want %v", tt.name, names, tt.want)
		}
	}
}

// TestSelectionUnknown checks that a Selection fails on a name or pattern
// that matches no built-in tool, naming them, and on a malformed pattern.
func TestSelectionUnknown(t *testing.T) {
	for _, sel := range []Selection{{Only: []string{"read", "model-x"}}, {Exclude: []string{"model-x"}}} {
		_, err := sel.Tools()
		if err == nil || !strings.Contains(err.Error(), `unknown tool "model-x"`) || !strings.Contains(err.Error(), "bash, edit") {
			t.Errorf("%+v: err = %v, want it to name the unknown tool and the built-in ones", sel, err)
		}
	}
	if _, err := (Selection{Only: []string{"["}}).Tools(); err == nil {
		t.Error("malformed pattern: no error")
	}
}
