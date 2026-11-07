//go:build !darwin && !freebsd && !netbsd && !linux && !openbsd && !dragonfly && !solaris

package engine

import (
	"io/fs"
	"time"
)

// sysStat reports neither an inode change time nor an inode number, which
// this platform does not expose portably. The index then compares only the
// mode, size, and modification time.
func sysStat(fs.FileInfo) (ctime time.Time, inode uint64) {
	return time.Time{}, 0
}
