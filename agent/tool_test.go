package agent

import (
	"slices"
	"testing"
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
