// Package realpath resolves the symbolic links of paths, which may not
// exist yet, so that every path leading to one folder names it the same way.
package realpath

import "path/filepath"

// Of returns the absolute path p with its symbolic links resolved. The part
// of p that does not exist yet is kept as it is, below the resolved part
// that does.
func Of(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(Of(parent), filepath.Base(p))
}
