//go:build darwin || freebsd || netbsd

package engine

import (
	"io/fs"
	"syscall"
	"time"
)

// sysStat returns the inode change time and inode number of info, which must
// come from Lstat.
func sysStat(info fs.FileInfo) (ctime time.Time, inode uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, 0
	}
	return time.Unix(st.Ctimespec.Unix()), uint64(st.Ino)
}
