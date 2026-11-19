package main

import (
	"context"
	"os"
	"runtime/debug"

	"github.com/unaquatic/flow/internal/cmdline"
)

// defaultMemoryLimit is the soft memory limit flow runs under unless the
// GOMEMLIMIT environment variable sets another, off included. Nearing it, the
// garbage collector runs more often and returns memory to the operating
// system sooner, so that recording large files does not leave flow holding
// hundreds of megabytes. A heap that needs more still gets it.
const defaultMemoryLimit = 32 << 20

func main() {
	limitMemory()
	cmdline.Main(context.Background(), rootCmd)
}

// limitMemory sets defaultMemoryLimit as the soft memory limit of the
// process, unless GOMEMLIMIT has set one.
func limitMemory() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(defaultMemoryLimit)
	}
}
