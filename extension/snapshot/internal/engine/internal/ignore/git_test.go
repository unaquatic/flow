package ignore

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitCases are worktrees whose ignored paths are compared with git
// check-ignore.
var gitCases = []struct {
	name    string
	exclude string            // contents of .git/info/exclude
	ignores map[string]string // .gitignore path to contents
	files   []string
}{
	{
		name:    "base names",
		ignores: map[string]string{".gitignore": "*.log\n"},
		files:   []string{"a.log", "dir/b.log", "dir/c.txt", "keep.txt"},
	},
	{
		name:    "anchored",
		ignores: map[string]string{".gitignore": "/build\ndocs/*.md\na/*.c\n"},
		files:   []string{"build/out.o", "sub/build/out.o", "docs/a.md", "docs/deep/b.md", "sub/docs/c.md", "a/x.c", "a/b/x.c"},
	},
	{
		name:    "directory only",
		ignores: map[string]string{".gitignore": "tmp/\nnode_modules\n"},
		files:   []string{"tmp/x", "sub/tmp", "node_modules/x", "a/node_modules/y", "node_modules.txt"},
	},
	{
		name:    "double star",
		ignores: map[string]string{".gitignore": "**/cache\nlogs/**\na/**/z\n"},
		files:   []string{"cache/f", "x/y/cache/f", "logs/one", "logs/deep/two", "logs.txt", "a/z", "a/b/c/z", "a/b/y"},
	},
	{
		name:    "negation",
		ignores: map[string]string{".gitignore": "*.txt\n!keep.txt\n"},
		files:   []string{"a.txt", "keep.txt", "sub/keep.txt", "sub/b.txt"},
	},
	{
		name:    "no re-include inside ignored directory",
		ignores: map[string]string{".gitignore": "out/\n!out/keep.txt\n"},
		files:   []string{"out/keep.txt", "out/drop.txt"},
	},
	{
		name:    "re-include among directory contents",
		ignores: map[string]string{".gitignore": "out/*\n!out/keep.txt\n"},
		files:   []string{"out/keep.txt", "out/drop.txt"},
	},
	{
		name: "nested gitignore",
		ignores: map[string]string{
			".gitignore":     "*.gen\n",
			"sub/.gitignore": "!x.gen\n/local\n",
		},
		files: []string{"x.gen", "sub/x.gen", "sub/y.gen", "sub/local", "sub/deeper/local", "local"},
	},
	{
		name: "ignored directory hides its gitignore",
		ignores: map[string]string{
			".gitignore":        "vendor/\n",
			"vendor/.gitignore": "!*\n",
		},
		files: []string{"vendor/a.go", "main.go"},
	},
	{
		name:    "info exclude below gitignore",
		exclude: "*.bak\nnotes.md\n",
		ignores: map[string]string{".gitignore": "!keep.bak\n"},
		files:   []string{"a.bak", "keep.bak", "notes.md", "sub/notes.md"},
	},
	{
		name:    "quoting and spaces",
		ignores: map[string]string{".gitignore": "\\#hash\n\\!bang\ntrail   \nspace\\ \n# comment\n"},
		files:   []string{"#hash", "!bang", "trail", "space ", "# comment", "other"},
	},
	{
		name:    "bracket expressions",
		ignores: map[string]string{".gitignore": "[abc].c\n[!a].o\n[a-c]x\n[]]y\n[[:digit:]]*.tmp\n?.q\n"},
		files:   []string{"a.c", "d.c", "a.o", "b.o", "bx", "dx", "]y", "7a.tmp", "a7.tmp", "x.q", "xy.q"},
	},
	{
		name:    "byte order mark and CRLF",
		ignores: map[string]string{".gitignore": "\ufeff*.log\r\nbuild/\r\n"},
		files:   []string{"a.log", "build/x", "c.txt"},
	},
}

func TestMatcherAgreesWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	for _, tt := range gitCases {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init", "--quiet")
			writeFile(t, root, ".git/info/exclude", tt.exclude)
			for name, data := range tt.ignores {
				writeFile(t, root, name, data)
			}
			for _, name := range tt.files {
				writeFile(t, root, name, "x")
			}

			paths := worktreePaths(t, root)
			want := gitIgnored(t, root, paths)
			excluded := walkExcluded(t, root, Parse([]byte(tt.exclude), ""))
			for _, name := range paths {
				if got := isIgnored(excluded, name); got != want[name] {
					t.Errorf("%q: ignored = %t; git says %t", name, got, want[name])
				}
			}
		})
	}
}

// walkExcluded walks root as the snapshot walker does: it reads each
// directory's .gitignore on entry and never descends into an ignored
// directory. It returns the paths m excluded.
func walkExcluded(t *testing.T, root string, m Matcher) map[string]bool {
	t.Helper()
	excluded := make(map[string]bool)
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := slashRel(t, root, name)
		switch {
		case rel == ".git":
			return fs.SkipDir
		case rel != "" && m.Match(rel, d.Type()):
			excluded[rel] = true
			if d.IsDir() {
				return fs.SkipDir
			}
		case d.IsDir():
			m = append(m, readIgnoreFile(t, name, rel)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return excluded
}

// isIgnored reports whether name or one of its parent directories was
// excluded.
func isIgnored(excluded map[string]bool, name string) bool {
	for {
		if excluded[name] {
			return true
		}
		i := strings.LastIndexByte(name, '/')
		if i < 0 {
			return false
		}
		name = name[:i]
	}
}

// gitIgnored asks git check-ignore which of paths are ignored.
func gitIgnored(t *testing.T, root string, paths []string) map[string]bool {
	t.Helper()
	cmd := gitCommand(t, root, "check-ignore", "--no-index", "--verbose", "--non-matching", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	// Exit status 1 means that no path is ignored.
	if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		t.Fatalf("git check-ignore: %v", err)
	}

	// Each record is source, line number, pattern, and path.
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(fields)%4 != 0 {
		t.Fatalf("git check-ignore: malformed output %q", out)
	}
	ignored := make(map[string]bool, len(paths))
	for record := range slices.Chunk(fields, 4) {
		pattern, name := record[2], record[3]
		ignored[name] = pattern != "" && !strings.HasPrefix(pattern, "!")
	}
	return ignored
}

// worktreePaths returns every file and directory below root outside .git.
func worktreePaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch rel := slashRel(t, root, name); rel {
		case ".git":
			return fs.SkipDir
		case "":
		default:
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// readIgnoreFile parses the .gitignore in the directory name, whose
// worktree-relative path is dir. A missing file holds no patterns.
func readIgnoreFile(t *testing.T, name, dir string) []Pattern {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(name, ".gitignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return Parse(data, dir)
}

// slashRel returns name relative to root with forward slashes. The root
// itself is "".
func slashRel(t *testing.T, root, name string) string {
	t.Helper()
	rel, err := filepath.Rel(root, name)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// gitCommand returns a git command run in dir, isolated from the user's and
// the system's configuration so no outside excludes file applies. Matching
// is case-sensitive, like Matcher.
func gitCommand(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("git", append([]string{"-c", "core.ignorecase=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	return cmd
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := gitCommand(t, dir, args...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, root, name, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
