package builtin

import (
	"math"
	"testing"
	"time"
)

// TestBashTimeout checks the timeout a call runs under for each form of
// timeout_ms, as decoded from JSON.
func TestBashTimeout(t *testing.T) {
	tests := map[string]struct {
		args map[string]any
		want time.Duration
	}{
		"omitted":       {args: map[string]any{}, want: DefaultBashTimeout},
		"nil args":      {args: nil, want: DefaultBashTimeout},
		"zero":          {args: map[string]any{"timeout_ms": float64(0)}, want: DefaultBashTimeout},
		"negative":      {args: map[string]any{"timeout_ms": float64(-5)}, want: DefaultBashTimeout},
		"not a number":  {args: map[string]any{"timeout_ms": "soon"}, want: DefaultBashTimeout},
		"json number":   {args: map[string]any{"timeout_ms": float64(1500)}, want: 1500 * time.Millisecond},
		"decimal text":  {args: map[string]any{"timeout_ms": "120000"}, want: 2 * time.Minute},
		"above the cap": {args: map[string]any{"timeout_ms": float64(2 * time.Hour / time.Millisecond)}, want: MaxBashTimeout},
		"overflow":      {args: map[string]any{"timeout_ms": int64(math.MaxInt64)}, want: MaxBashTimeout},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := BashTimeout(tt.args); got != tt.want {
				t.Errorf("BashTimeout(%v) = %v; want %v", tt.args, got, tt.want)
			}
		})
	}
}
