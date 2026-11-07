package ui

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// ThemeJSON is the file that TestThemeJSON writes: DefaultTheme as JSON. The
// design editor, "fluid design" in fluid next to this repository, offers the
// theme's roles in its palette when -theme names this file.
const ThemeJSON = "testdata/theme.json"

// TestThemeJSON checks that ThemeJSON holds DefaultTheme. -update writes it.
func TestThemeJSON(t *testing.T) {
	want, err := json.MarshalIndent(DefaultTheme(), "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		if err := os.WriteFile(ThemeJSON, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(ThemeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not DefaultTheme; run go test ./terminal/ui/ -run ThemeJSON -update", ThemeJSON)
	}
}
