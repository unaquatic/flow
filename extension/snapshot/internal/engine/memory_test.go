package engine

import (
	"runtime/metrics"
	"strings"
	"testing"
)

// forcedCollections returns how many garbage collections the process has
// forced, by runtime.GC or debug.FreeOSMemory.
func forcedCollections() uint64 {
	s := []metrics.Sample{{Name: "/gc/cycles/forced:gc-cycles"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

// TestTrack_ReleasesMemory checks that Track returns memory to the operating
// system after reading a file of ReleaseThreshold bytes, and not after
// reading only small ones.
func TestTrack_ReleasesMemory(t *testing.T) {
	s, dir := newTestService(t)
	writeFile(t, dir, "small.txt", "small\n")

	before := forcedCollections()
	if _, err := s.Track(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := forcedCollections(); got != before {
		t.Errorf("Track of a small file forced %d collections, want 0", got-before)
	}

	writeFile(t, dir, "big.bin", strings.Repeat("x", ReleaseThreshold))
	before = forcedCollections()
	if _, err := s.Track(t.Context()); err != nil {
		t.Fatal(err)
	}
	if forcedCollections() == before {
		t.Error("Track read a large file and released no memory")
	}
}
