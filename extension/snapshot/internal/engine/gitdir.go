package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// repoCommonDir returns the common directory of the Git repository holding
// dir, searching dir and then its parents, or "" when dir is in none. The
// common directory holds the repository-wide files, config and info/exclude
// among them.
func repoCommonDir(dir string) (string, error) {
	for {
		gitDir, err := resolveGitDir(filepath.Join(dir, ".git"))
		if err != nil {
			return "", err
		}
		if gitDir != "" {
			return commonDir(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// resolveGitDir returns the Git directory that dotGit, a .git entry, stands
// for: dotGit itself when it is a directory, or the target of its "gitdir:"
// line when it is a file, as in a linked worktree or a submodule. It returns
// "" when dotGit is missing or is not a valid .git file.
func resolveGitDir(dotGit string) (string, error) {
	info, err := os.Stat(dotGit)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return dotGit, nil
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", err
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", nil
	}
	return resolvePath(filepath.Dir(dotGit), strings.TrimSpace(target)), nil
}

// commonDir returns the directory holding the repository-wide files of
// gitDir. It differs from gitDir in a linked worktree, whose gitDir names it
// in a commondir file.
func commonDir(gitDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return gitDir, nil
	}
	if err != nil {
		return "", err
	}
	return resolvePath(gitDir, strings.TrimSpace(string(data))), nil
}

// resolvePath returns p, joined to base when p is relative.
func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
