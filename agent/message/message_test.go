package message

import (
	"errors"
	"testing"
)

// TestParseRole_RoundTripsAndRefusesUnknownNames checks that ParseRole reads
// back every name String writes, and fails on any other name rather than
// taking it for RoleAgent.
func TestParseRole_RoundTripsAndRefusesUnknownNames(t *testing.T) {
	for _, r := range []Role{RoleAgent, RoleUser, RoleSystem} {
		if got, err := ParseRole(r.String()); err != nil || got != r {
			t.Errorf("ParseRole(%q) = %v, %v; want %v", r.String(), got, err, r)
		}
	}
	for _, name := range []string{"", "assistant", "Agent", "unknown", Role(99).String()} {
		if _, err := ParseRole(name); !errors.Is(err, ErrUnknownRole) {
			t.Errorf("ParseRole(%q): err = %v, want ErrUnknownRole", name, err)
		}
	}
}
