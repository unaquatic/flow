package engine

import (
	"runtime/debug"
	"runtime/metrics"
)

// ReleaseThreshold is how many bytes a snapshot operation must allocate for
// the Service to return the memory to the operating system as it ends.
// Recording or restoring a file reads it whole, so large files leave as much
// garbage behind. Without a release, the process keeps that memory until the
// next collection, which in an idle process may be minutes away, and gives
// it back to the operating system only gradually after that.
const ReleaseThreshold = 32 << 20

// HeapAllocs returns the bytes the process has allocated on the heap since it
// started.
func HeapAllocs() uint64 {
	s := []metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

// ReleaseMemory collects garbage and returns as much memory as it can to the
// operating system, if the process has allocated ReleaseThreshold bytes or
// more since [HeapAllocs] returned since.
func ReleaseMemory(since uint64) {
	if HeapAllocs()-since >= ReleaseThreshold {
		debug.FreeOSMemory()
	}
}
