package request

import (
	"net/http"
	"reflect"
	"testing"
)

func TestMergeBody(t *testing.T) {
	dst := map[string]any{"model": "gpt-5.6", "reasoning": map[string]any{"effort": "high"}}
	src := map[string]any{"reasoning": map[string]any{"mode": "pro"}, "service_tier": "priority"}
	got := MergeBody(dst, src)
	want := map[string]any{
		"model":        "gpt-5.6",
		"reasoning":    map[string]any{"effort": "high", "mode": "pro"},
		"service_tier": "priority",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeBody = %v, want %v", got, want)
	}
	// The objects taken from src are copies.
	got["reasoning"].(map[string]any)["mode"] = "changed"
	if src["reasoning"].(map[string]any)["mode"] != "pro" {
		t.Error("MergeBody shares objects with src")
	}
}

func TestAddHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", "oauth-2025-04-20")
	AddHeaders(h, map[string]string{"anthropic-beta": "fast-mode-2026-02-01", "X-Other": "1"})
	if got := h.Get("anthropic-beta"); got != "oauth-2025-04-20,fast-mode-2026-02-01" {
		t.Errorf("anthropic-beta = %q", got)
	}
	if got := h.Get("x-other"); got != "1" {
		t.Errorf("x-other = %q", got)
	}
}
