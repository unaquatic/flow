package realpath

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOf checks that a path through a symbolic link resolves to the folder
// the link leads to, that the part of a path that does not exist is kept
// below the resolved part that does, and that a path none of which exists is
// kept as it is.
func TestOf(t *testing.T) {
	// The temporary directory may lie behind links itself, as on macOS.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink: %v", err)
	}

	for p, want := range map[string]string{
		dir:                                 dir,
		link:                                dir,
		filepath.Join(link, "new", "a.txt"): filepath.Join(dir, "new", "a.txt"),
		filepath.FromSlash("/work/a"):       filepath.FromSlash("/work/a"),
	} {
		if got := Of(p); got != want {
			t.Errorf("Of(%s) = %s, want %s", p, got, want)
		}
	}
}
