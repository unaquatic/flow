package engine

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// mustParseHash parses s or fails the test.
func mustParseHash(t *testing.T, s string) Hash {
	t.Helper()
	h, err := ParseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashObject_MatchesKnownGitNames(t *testing.T) {
	tests := []struct {
		name string
		typ  ObjectType
		data string
		want string
	}{
		{"empty blob", BlobType, "", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"hello blob", BlobType, "hello\n", "ce013625030ba8dba906f756967f9e9ca394464a"},
		{"empty tree", TreeType, "", "4b825dc642cb6eb9a060e54bf8d69288fbee4904"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HashObject(tt.typ, []byte(tt.data)).String(); got != tt.want {
				t.Errorf("hashObject = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestEncodeTree_SortsAsGitDoes(t *testing.T) {
	var h Hash
	entries := []TreeEntry{
		{Name: "a0", Mode: ModeFile, Hash: h},
		{Name: "a", Mode: ModeDir, Hash: h},
		{Name: "a.b", Mode: ModeExec, Hash: h},
		{Name: "a-b", Mode: ModeSymlink, Hash: h},
	}
	data, err := EncodeTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTree(data)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range decoded {
		got = append(got, e.Name)
	}
	// The directory "a" sorts as "a/", after "-" and "." and before "0".
	if want := []string{"a-b", "a.b", "a", "a0"}; !slices.Equal(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestEncodeTree_RejectsInvalidEntries(t *testing.T) {
	var h Hash
	tests := []struct {
		name    string
		entries []TreeEntry
	}{
		{"empty name", []TreeEntry{{Name: "", Mode: ModeFile, Hash: h}}},
		{"dot", []TreeEntry{{Name: ".", Mode: ModeDir, Hash: h}}},
		{"dot dot", []TreeEntry{{Name: "..", Mode: ModeDir, Hash: h}}},
		{"slash", []TreeEntry{{Name: "a/b", Mode: ModeFile, Hash: h}}},
		{"NUL", []TreeEntry{{Name: "a\x00b", Mode: ModeFile, Hash: h}}},
		{"gitlink mode", []TreeEntry{{Name: "sub", Mode: 0o160000, Hash: h}}},
		{"zero mode", []TreeEntry{{Name: "a", Hash: h}}},
		{"file and directory share a name", []TreeEntry{
			{Name: "a", Mode: ModeFile, Hash: h},
			{Name: "a.txt", Mode: ModeFile, Hash: h},
			{Name: "a", Mode: ModeDir, Hash: h},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := EncodeTree(tt.entries); err == nil {
				t.Error("encodeTree succeeded")
			}
		})
	}
}

func TestDecodeTree_RejectsMalformedData(t *testing.T) {
	hash := string(make([]byte, 20))
	tests := map[string]string{
		"no space":        "100644",
		"no NUL":          "100644 name",
		"truncated hash":  "100644 name\x00" + hash[:19],
		"non-octal mode":  "100649 name\x00" + hash,
		"gitlink mode":    "160000 name\x00" + hash,
		"invalid name":    "100644 a/b\x00" + hash,
		"trailing junk":   "100644 name\x00" + hash + "40000",
		"empty mode text": " name\x00" + hash,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeTree([]byte(data)); err == nil {
				t.Errorf("decodeTree(%q) succeeded", data)
			}
		})
	}
}

// TestObjects_MatchGit checks blob and tree names against git hash-object and
// git mktree, which sorts its input itself.
func TestObjects_MatchGit(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	gitOutput(t, repo, nil, "init", "--quiet")
	rng := rand.New(rand.NewChaCha8([32]byte{}))

	blob := make([]byte, 4096)
	for i := range blob {
		blob[i] = byte(rng.Uint32())
	}
	want := gitOutput(t, repo, blob, "hash-object", "--stdin")
	if got := HashObject(BlobType, blob).String(); got != want {
		t.Errorf("blob name = %s, git says %s", got, want)
	}

	// The directory "a" is a prefix of its neighbours, so it only sorts
	// correctly when compared as "a/".
	var entries []TreeEntry
	var listing strings.Builder
	for _, e := range []TreeEntry{
		{Name: "z", Mode: ModeFile},
		{Name: "a0", Mode: ModeExec},
		{Name: "a", Mode: ModeDir},
		{Name: "a.b", Mode: ModeSymlink},
		{Name: "a-b", Mode: ModeFile},
		{Name: "b c", Mode: ModeDir},
		{Name: "ünïcode", Mode: ModeFile},
		{Name: "UPPER", Mode: ModeExec},
	} {
		for j := range e.Hash {
			e.Hash[j] = byte(rng.Uint32())
		}
		entries = append(entries, e)
		typ := "blob"
		if e.Mode == ModeDir {
			typ = "tree"
		}
		fmt.Fprintf(&listing, "%06o %s %s\t%s\n", e.Mode, typ, e.Hash, e.Name)
	}
	data, err := EncodeTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	want = gitOutput(t, repo, []byte(listing.String()), "mktree", "--missing")
	if got := HashObject(TreeType, data).String(); got != want {
		t.Errorf("tree name = %s, git says %s", got, want)
	}
}

// gitOutput runs git in dir with stdin and returns its trimmed output.
func gitOutput(t *testing.T, dir string, stdin []byte, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
