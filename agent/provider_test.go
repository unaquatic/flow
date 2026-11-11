package agent

import "testing"

// TestValidModelID checks that a model ID is lab/model or model alone, with
// an option and an effort or not, and that one with an empty part, a part
// more, or white space names no model.
func TestValidModelID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"model-x", true},
		{"lab-a/model-a", true},
		{"lab-a/model-a:fast:high", true},
		{"", false},
		{"///-6-luna:medium", false},
		{"/-da-d-s-sd", false},
		{"lab-a/", false},
		{"lab-a//model-a", false},
		{"lab-a/model-a/extra", false},
		{"model x", false},
		{"model-x\t", false},
	}
	for _, tt := range tests {
		if got := ValidModelID(tt.id); got != tt.want {
			t.Errorf("ValidModelID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
