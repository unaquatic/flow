//go:build unix || windows

package logging

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureStderr(t *testing.T) {
	name := filepath.Join(t.TempDir(), "test.log")
	before := stderrFile(t)

	restore := CaptureStderr(name)
	if stderrFile(t) == before {
		restore()
		t.Fatal("standard error was not captured")
	}
	fmt.Fprintln(os.Stderr, "captured line")
	println("runtime line") // Written as crash reports are, by the runtime.
	restore()
	restore() // A second call does nothing.

	if stderrFile(t) != before {
		t.Error("standard error was not restored")
	}
	log, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"captured line\n", "runtime line\n"} {
		if !bytes.Contains(log, []byte(want)) {
			t.Errorf("log = %q, want %q written while captured", log, want)
		}
	}
}
