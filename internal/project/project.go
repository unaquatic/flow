// Package project finds the folders that hold a working directory's project,
// which the app reads the project's instructions from, and an extension its
// own files, such as skills.
package project

import (
	"iter"
	"os"
	"path/filepath"
)

// Dirs yields the folders that hold workingDir's project, nearest first, by
// their absolute paths: workingDir, and each folder above it up to the root
// of the git repository that holds it, the folder with a .git entry. Outside
// a repository it yields workingDir alone, so that a folder above it that
// has nothing to do with it is never read, and for an empty workingDir
// nothing.
func Dirs(workingDir string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if workingDir == "" {
			return
		}
		start, err := filepath.Abs(workingDir)
		if err != nil {
			return
		}
		var dirs []string
		for dir := start; ; {
			dirs = append(dirs, dir)
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				// No repository holds workingDir.
				dirs = dirs[:1]
				break
			}
			dir = parent
		}
		for _, dir := range dirs {
			if !yield(dir) {
				return
			}
		}
	}
}
