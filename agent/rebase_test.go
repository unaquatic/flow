package agent

import "testing"

func TestRebase(t *testing.T) {
	tests := []struct {
		api, base string
		own       bool
		want      string
	}{
		{"https://api.anthropic.com/v1", "", true, "https://api.anthropic.com/v1"},
		// A host alone takes every endpoint there, each with its own path.
		{"https://api.anthropic.com/v1", "https://proxy.example/", true, "https://proxy.example/v1"},
		{"https://opencode.ai/inference/anthropic/v1", "https://proxy.example", false, "https://proxy.example/inference/anthropic/v1"},
		{"https://${GOOGLE_VERTEX_ENDPOINT}/v1/projects/${GOOGLE_VERTEX_PROJECT}/locations/global/publishers/google", "http://proxy.example", true,
			"http://proxy.example/v1/projects/${GOOGLE_VERTEX_PROJECT}/locations/global/publishers/google"},
		// A base URL with a path replaces the provider's own endpoint, and
		// moves an entry's own to its host.
		{"https://opencode.ai/inference/openai/v1", "https://proxy.example/oc/v1", true, "https://proxy.example/oc/v1"},
		{"https://opencode.ai/inference/anthropic/v1", "https://proxy.example/oc/v1", false, "https://proxy.example/inference/anthropic/v1"},
	}
	for _, tt := range tests {
		if got := rebase(tt.api, tt.base, tt.own); got != tt.want {
			t.Errorf("rebase(%s, %q, %v) = %s, want %s", tt.api, tt.base, tt.own, got, tt.want)
		}
	}
}
