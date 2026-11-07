package config

import (
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/product"
)

// TestEnvFlags checks that the variables that turn a feature on or off read
// any value but "", "0" and "false", in any case, as on.
func TestEnvFlags(t *testing.T) {
	for name, flag := range map[string]func() bool{
		EnvDisableTerminalTitle: TerminalTitleDisabled,
		EnvWidthProbe:           WidthProbe,
	} {
		for value, want := range map[string]bool{
			"": false, "0": false, "false": false, "FALSE": false,
			"1": true, "true": true, "yes": true,
		} {
			t.Setenv(name, value)
			if got := flag(); got != want {
				t.Errorf("%s=%q: %v, want %v", name, value, got, want)
			}
		}
	}
}

// TestSnapshotDelay checks that EnvSnapshotDelay reads a duration, reads as
// no delay when empty, and is an error when it is not a duration or is
// negative.
func TestSnapshotDelay(t *testing.T) {
	for _, tt := range []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: ""},
		{value: "0"},
		{value: "500ms", want: 500 * time.Millisecond},
		{value: "2s", want: 2 * time.Second},
		{value: "soon", wantErr: true},
		{value: "5", wantErr: true},
		{value: "-1s", wantErr: true},
	} {
		t.Setenv(EnvSnapshotDelay, tt.value)
		got, err := SnapshotDelay()
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("%s=%q: %v, %v; want %v, error = %v", EnvSnapshotDelay, tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}

// TestEnvVars checks that help lists every variable of flow's own, and that
// those named after flow carry the product's ID.
func TestEnvVars(t *testing.T) {
	listed := map[string]bool{}
	for _, v := range EnvVars() {
		if v.Usage == "" {
			t.Errorf("%s has no usage", v.Name)
		}
		listed[v.Name] = true
	}
	for _, name := range []string{EnvDataDir, EnvDebug, EnvSnapshotDelay, EnvDisableTerminalTitle, EnvWidthProbe} {
		if !listed[name] {
			t.Errorf("EnvVars does not list %s", name)
		}
	}
	prefix := strings.ToUpper(product.ID) + "_"
	for _, name := range []string{EnvDataDir, EnvSnapshotDelay, EnvDisableTerminalTitle} {
		if !strings.HasPrefix(name, prefix) {
			t.Errorf("%s does not start with %s", name, prefix)
		}
	}
}
