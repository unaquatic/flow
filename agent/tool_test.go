package agent

import (
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

// TestTools_WireToolsSorted checks that the definitions come sorted by
// name, whatever the order of registration, so the prompt prefix that holds
// them stays the same across requests.
func TestTools_WireToolsSorted(t *testing.T) {
	s := NewTools()
	for _, name := range []string{"write", "bash", "read", "grep", "edit", "glob"} {
		s.Register(Tool{Name: name})
	}
	want := []string{"bash", "edit", "glob", "grep", "read", "write"}
	for range 20 {
		var got []string
		for _, w := range s.WireTools() {
			got = append(got, w.Name)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}

// TestToolsIn checks that the tools in force are those of the last system
// message that holds a set, that a system message of text alone leaves them
// as they were, that an empty set leaves none, and that a history without a
// set reports none in force.
func TestToolsIn(t *testing.T) {
	read := ToolDefinition{Name: "read", Description: "Read a file.", InputSchema: InputSchema{Type: "object"}}
	grep := ToolDefinition{Name: "grep", InputSchema: InputSchema{Type: "object"}}
	system := func(parts ...message.ContentPart) Message { return Message{Role: message.RoleSystem, Content: parts} }
	opened := system(message.TextPart{Text: "Be brief."}, message.ToolSetPart{Tools: []ToolDefinition{read}})
	changed := system(message.ToolSetPart{Tools: []ToolDefinition{grep, read}})
	emptied := system(message.ToolSetPart{Tools: []ToolDefinition{}})

	for _, tt := range []struct {
		name    string
		history []Message
		want    []ToolDefinition
		ok      bool
	}{
		{"no system message", []Message{NewUserMessage("hi")}, nil, false},
		{"text without tools", []Message{NewSystemMessage("Be brief."), NewUserMessage("hi")}, nil, false},
		{"opening set", []Message{opened, NewUserMessage("hi")}, []ToolDefinition{read}, true},
		{"later set", []Message{opened, NewUserMessage("hi"), NewAgentMessage("hello"), changed}, []ToolDefinition{grep, read}, true},
		{"text alone", []Message{opened, NewUserMessage("hi"), NewSystemMessage("Be terse.")}, []ToolDefinition{read}, true},
		{"empty set", []Message{opened, NewUserMessage("hi"), emptied}, nil, true},
	} {
		got, ok := ToolsIn(tt.history)
		if ok != tt.ok || !SameTools(got, tt.want) {
			t.Errorf("%s: ToolsIn = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

// TestSameTools checks that tools are the same when a provider would be
// sent the same definitions, in the same order.
func TestSameTools(t *testing.T) {
	read := ToolDefinition{Name: "read", InputSchema: InputSchema{Type: "object", Required: []string{}}}
	stored := ToolDefinition{Name: "read", InputSchema: InputSchema{Type: "object"}}
	grep := ToolDefinition{Name: "grep", InputSchema: InputSchema{Type: "object"}}
	described := read
	described.Description = "Read a file."
	for _, tt := range []struct {
		name string
		a, b []ToolDefinition
		want bool
	}{
		{"none", nil, []ToolDefinition{}, true},
		{"empty and absent lists", []ToolDefinition{read}, []ToolDefinition{stored}, true},
		{"a description", []ToolDefinition{read}, []ToolDefinition{described}, false},
		{"order", []ToolDefinition{grep, read}, []ToolDefinition{read, grep}, false},
		{"one more", []ToolDefinition{read}, []ToolDefinition{read, grep}, false},
	} {
		if got := SameTools(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: SameTools = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestTools_RegisterReplaces checks that a tool replaces the one of the
// same name registered before it, and that List holds each name once.
func TestTools_RegisterReplaces(t *testing.T) {
	s := NewTools()
	s.Register(Tool{Name: "read", Description: "first"})
	s.Register(Tool{Name: "read", Description: "second"})
	if got, ok := s.Get("read"); !ok || got.Description != "second" {
		t.Errorf("Get(read) = %+v, %v; want the second", got, ok)
	}
	if n := len(s.List()); n != 1 {
		t.Errorf("List holds %d tools, want 1", n)
	}
	if _, ok := s.Get("bash"); ok {
		t.Error("Get(bash) found a tool that was never registered")
	}
}
