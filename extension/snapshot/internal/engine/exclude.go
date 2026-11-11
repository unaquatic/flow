package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/internal/ignore"
)

// loadExcludes returns the patterns that apply to the whole worktree dir,
// lowest precedence first: the global excludes file, then the info/exclude
// file of the repository holding dir. Missing files hold no patterns.
func loadExcludes(dir string) (ignore.Matcher, error) {
	common, err := repoCommonDir(dir)
	if err != nil {
		return nil, err
	}
	global, err := globalExcludeFile(dir, common)
	if err != nil {
		return nil, err
	}
	files := []string{global}
	if common != "" {
		files = append(files, filepath.Join(common, "info", "exclude"))
	}
	var m ignore.Matcher
	for _, file := range files {
		if file == "" {
			continue
		}
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot: read excludes: %w", err)
		}
		m = m.With(ignore.Parse(data, "")...)
	}
	return m, nil
}

// globalExcludeFile returns the global excludes file for the worktree dir:
// the file named by the last core.excludesFile in the user's Git
// configuration or in the configuration of the repository whose common
// directory is common, or else Git's default. It returns "" when no file can
// be located.
func globalExcludeFile(dir, common string) (string, error) {
	home := gitHome()
	name := ""
	for _, file := range configFiles(home, common) {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			continue // Git passes over a configuration file it cannot read
		}
		if err != nil {
			return "", fmt.Errorf("snapshot: read git config: %w", err)
		}
		if v, ok := configValue(data, "core", "excludesfile"); ok {
			name = v
		}
	}
	if name == "" {
		return xdgGitFile(home, "ignore"), nil
	}
	return expandPath(name, home, dir), nil
}

// gitHome returns the home directory Git reads the user's configuration
// from: $HOME, which Git for Windows prefers to the profile directory as
// well, or else the user's home directory, or "" when neither is known, so
// that only the environment and the repository can name a file.
func gitHome() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}

// configFiles returns the Git configuration files that can set
// core.excludesFile, lowest precedence first: the user's global files, or the
// one GIT_CONFIG_GLOBAL names instead, then the repository's own. System-wide
// configuration is not read.
func configFiles(home, common string) []string {
	var files []string
	if global := os.Getenv("GIT_CONFIG_GLOBAL"); global != "" {
		files = append(files, global)
	} else {
		if xdg := xdgGitFile(home, "config"); xdg != "" {
			files = append(files, xdg)
		}
		if home != "" {
			files = append(files, filepath.Join(home, ".gitconfig"))
		}
	}
	if common != "" {
		files = append(files, filepath.Join(common, "config"))
	}
	return files
}

// xdgGitFile returns the path of name in Git's XDG configuration directory,
// or "" when neither XDG_CONFIG_HOME nor home locates it.
func xdgGitFile(home, name string) string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "git", name)
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "git", name)
}

// expandPath resolves a path from Git's configuration: a leading "~/" stands
// for home, and a relative path is relative to the worktree dir, where Git
// ran for the git-based snapshots.
func expandPath(p, home, dir string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok && home != "" {
		return filepath.Join(home, rest)
	}
	return resolvePath(dir, p)
}
