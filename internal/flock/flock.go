// Package flock takes shared and exclusive locks on open files. Locks
// conflict between open files, so they exclude goroutines of one process as
// well as other processes, and the operating system releases them when the
// process holding them exits. On Unix the locks are advisory: programs that
// use the file without them are not excluded.
package flock

import (
	"context"
	"os"
	"time"
)

// retry is how long Lock waits between attempts to take a busy lock.
const retry = 10 * time.Millisecond

// Lock takes a shared or exclusive lock on the open file f. While another
// open file holds a conflicting lock, Lock retries until ctx is done.
func Lock(ctx context.Context, f *os.File, exclusive bool) error {
	for {
		ok, err := TryLock(f, exclusive)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retry):
		}
	}
}
