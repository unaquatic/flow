package engine

import (
	"bytes"
	"cmp"
	"crypto/sha1"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/oid"
)

// Hash is the SHA-1 name of an object, as [oid.Hash] defines it.
type Hash = oid.Hash

// ShortHashLen is the number of hexadecimal digits a hash is abbreviated to
// wherever it is shown, as in the index line of a patch and the snapshot a
// message ends with.
const ShortHashLen = 8

// ShortHash returns h abbreviated to its first ShortHashLen digits.
func ShortHash(h Hash) string {
	return h.String()[:ShortHashLen]
}

// ParseHash parses a hexadecimal object name.
func ParseHash(s string) (Hash, error) {
	return oid.Parse(s)
}

// Mode is the file mode of a tree entry. Only the modes below are valid.
type Mode uint32

const (
	ModeDir     Mode = 0o40000  // directory
	ModeFile    Mode = 0o100644 // regular file
	ModeExec    Mode = 0o100755 // executable file
	ModeSymlink Mode = 0o120000 // symbolic link; the blob holds the target
)

// TreeEntry is one name in a tree.
type TreeEntry struct {
	// Name is a single path element.
	Name string
	Mode Mode
	Hash Hash
}

// ObjectType is the type of a stored object.
type ObjectType uint8

const (
	BlobType ObjectType = 1 // file content or a symbolic link's target
	TreeType ObjectType = 2 // directory listing
)

// String returns "blob" or "tree".
func (t ObjectType) String() string {
	switch t {
	case BlobType:
		return "blob"
	case TreeType:
		return "tree"
	}
	return "object type " + strconv.Itoa(int(t))
}

// HashObject returns the Git object name of data stored as type t.
func HashObject(t ObjectType, data []byte) Hash {
	d := sha1.New()
	fmt.Fprintf(d, "%s %d\x00", t, len(data))
	d.Write(data)
	var h Hash
	d.Sum(h[:0])
	return h
}

// EncodeTree returns the Git encoding of a tree holding entries, which may
// be in any order. It reports an error for an invalid mode or name, or for a
// name used twice.
func EncodeTree(entries []TreeEntry) ([]byte, error) {
	seen := make(map[string]bool, len(entries))
	var b []byte
	for _, e := range slices.SortedFunc(slices.Values(entries), compareEntries) {
		if err := checkEntry(e); err != nil {
			return nil, err
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("snapshot: tree entry %q appears twice", e.Name)
		}
		seen[e.Name] = true
		b = strconv.AppendUint(b, uint64(e.Mode), 8)
		b = append(b, ' ')
		b = append(b, e.Name...)
		b = append(b, 0)
		b = append(b, e.Hash[:]...)
	}
	return b, nil
}

// DecodeTree parses the Git encoding of a tree.
func DecodeTree(data []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	for len(data) > 0 {
		e, rest, err := decodeEntry(data)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
		data = rest
	}
	return entries, nil
}

// decodeEntry parses the tree entry at the start of data and returns the
// data after it.
func decodeEntry(data []byte) (TreeEntry, []byte, error) {
	mode, rest, ok := bytes.Cut(data, []byte{' '})
	if !ok {
		return TreeEntry{}, nil, errors.New("snapshot: malformed tree entry: no mode")
	}
	name, rest, ok := bytes.Cut(rest, []byte{0})
	if !ok || len(rest) < sha1.Size {
		return TreeEntry{}, nil, fmt.Errorf("snapshot: malformed tree entry %q: truncated", name)
	}
	m, err := strconv.ParseUint(string(mode), 8, 32)
	if err != nil {
		return TreeEntry{}, nil, fmt.Errorf("snapshot: malformed tree entry %q: %w", name, err)
	}
	e := TreeEntry{Name: string(name), Mode: Mode(m), Hash: Hash(rest[:sha1.Size])}
	if err := checkEntry(e); err != nil {
		return TreeEntry{}, nil, err
	}
	return e, rest[sha1.Size:], nil
}

// checkEntry reports whether e has a valid mode and a single-element name.
func checkEntry(e TreeEntry) error {
	switch e.Mode {
	case ModeDir, ModeFile, ModeExec, ModeSymlink:
	default:
		return fmt.Errorf("snapshot: tree entry %q has invalid mode %o", e.Name, e.Mode)
	}
	if e.Name == "" || e.Name == "." || e.Name == ".." || strings.ContainsAny(e.Name, "/\x00") {
		return fmt.Errorf("snapshot: invalid tree entry name %q", e.Name)
	}
	return nil
}

// compareEntries orders tree entries as Git does: by name, with a directory
// compared as if its name ended in a slash.
func compareEntries(a, b TreeEntry) int {
	n := min(len(a.Name), len(b.Name))
	if c := strings.Compare(a.Name[:n], b.Name[:n]); c != 0 {
		return c
	}
	return cmp.Compare(sortByte(a, n), sortByte(b, n))
}

// sortByte returns the byte at index i of e's name as Git sorts it: past the
// end, a directory has a slash and anything else has nothing.
func sortByte(e TreeEntry, i int) int {
	switch {
	case i < len(e.Name):
		return int(e.Name[i])
	case e.Mode == ModeDir:
		return '/'
	}
	return -1
}
