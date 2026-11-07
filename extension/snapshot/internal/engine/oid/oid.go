// Package oid defines the name Git gives an object. It depends on nothing
// but the standard library, so packages that only record object names, such
// as message, need not import snapshot and its storage.
package oid

import (
	"crypto/sha1"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
)

// Hash is the SHA-1 name of an object, computed as Git computes it, so a
// snapshot's tree hash equals the one Git would give the same files. The zero
// Hash names no object.
type Hash [sha1.Size]byte

// Parse parses a hexadecimal object name.
func Parse(s string) (Hash, error) {
	var h Hash
	if len(s) != hex.EncodedLen(len(h)) {
		return Hash{}, fmt.Errorf("oid: invalid hash %q", s)
	}
	if _, err := hex.Decode(h[:], []byte(s)); err != nil {
		return Hash{}, fmt.Errorf("oid: invalid hash %q: %w", s, err)
	}
	return h, nil
}

// String returns h in hexadecimal.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether h is the zero Hash, which names no object. It lets
// an omitzero JSON field leave the zero Hash out.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// MarshalText implements [encoding.TextMarshaler], encoding h in
// hexadecimal.
func (h Hash) MarshalText() ([]byte, error) {
	return hex.AppendEncode(nil, h[:]), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler], parsing text as Parse
// does.
func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

// Scan implements [database/sql.Scanner] for a 20-byte blob.
func (h *Hash) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok || len(b) != len(h) {
		return fmt.Errorf("oid: cannot scan %T of length %d into a hash", src, len(b))
	}
	copy(h[:], b)
	return nil
}

// Value implements [database/sql/driver.Valuer].
func (h Hash) Value() (driver.Value, error) {
	return h[:], nil
}
