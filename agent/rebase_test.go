package agent

import "testing"

func TestRebase(t *testing.T) {
	tests := []struct {
		api, base string
		own       bool
		want      string
	}{
		{"https://api.provider-a.example/v1", "", true, "https://api.provider-a.example/v1"},
		// A host alone takes every endpoint there, each with its own path.
		{"https://api.provider-a.example/v1", "https://proxy.example/", true, "https://proxy.example/v1"},
		{"https://provider-b.example/inference/lab-a/v1", "https://proxy.example", false, "https://proxy.example/inference/lab-a/v1"},
		{"https://${PROVIDER_C_ENDPOINT}/v1/projects/${PROVIDER_C_PROJECT}/locations/global/publishers/lab-c", "http://proxy.example", true,
			"http://proxy.example/v1/projects/${PROVIDER_C_PROJECT}/locations/global/publishers/lab-c"},
		// A base URL with a path replaces the provider's own endpoint, and
		// moves an entry's own to its host.
		{"https://provider-b.example/inference/lab-b/v1", "https://proxy.example/pb/v1", true, "https://proxy.example/pb/v1"},
		{"https://provider-b.example/inference/lab-a/v1", "https://proxy.example/pb/v1", false, "https://proxy.example/inference/lab-a/v1"},
	}
	for _, tt := range tests {
		if got := rebase(tt.api, tt.base, tt.own); got != tt.want {
			t.Errorf("rebase(%s, %q, %v) = %s, want %s", tt.api, tt.base, tt.own, got, tt.want)
		}
	}
}
