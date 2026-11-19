package main

import (
	"math"
	"runtime/debug"
	"testing"
)

// TestLimitMemory checks that flow sets its own soft memory limit only when
// GOMEMLIMIT sets none.
func TestLimitMemory(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(old) })
	tests := []struct {
		name, env string
		want      int64
	}{
		{name: "unset", env: "", want: defaultMemoryLimit},
		{name: "set", env: "1GiB", want: math.MaxInt64},
		{name: "off", env: "off", want: math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOMEMLIMIT", tt.env)
			debug.SetMemoryLimit(math.MaxInt64)
			limitMemory()
			if got := debug.SetMemoryLimit(-1); got != tt.want {
				t.Errorf("memory limit = %d, want %d", got, tt.want)
			}
		})
	}
}
