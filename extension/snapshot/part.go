package snapshot

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
)

// TypeSnapshot identifies a SnapshotPart.
const TypeSnapshot message.ContentType = "snapshot"

// Hash names a tree the engine recorded.
type Hash = engine.Hash

// ParseHash parses the hexadecimal name of a tree, as Hash.String prints it.
func ParseHash(s string) (Hash, error) { return engine.ParseHash(s) }

// SnapshotPart records the workspace snapshot a message ends with: a
// prompt's is the workspace as the prompt was sent, and a reply's the
// workspace once its tools ran, so a reply's changes are the difference
// between the tree of the message before it and its own. It is the last part
// of its message, and a message holds none when snapshots are off. It is
// message.Metadata: it is stored with the message but never sent to a
// provider, and shows only while it is pending.
type SnapshotPart struct {
	// ID identifies the part.
	ID message.PartID `json:"id,omitempty"`
	// Tree is the hash of the workspace's root tree, a tree object the
	// engine stored, or the zero Hash when tracking it failed or while the
	// part is pending.
	Tree Hash `json:"tree,omitzero"`
	// Pending reports that the workspace is still being recorded. A part
	// that takes long to record starts pending, so that its message shows
	// the snapshot being taken, and ends with its tree. A stored part is
	// never pending.
	Pending bool `json:"pending,omitempty"`
}

func init() { message.RegisterPart[SnapshotPart](TypeSnapshot) }

// PartType returns TypeSnapshot.
func (SnapshotPart) PartType() message.ContentType { return TypeSnapshot }

// PartID returns the part's ID.
func (p SnapshotPart) PartID() message.PartID { return p.ID }

// WithID returns a copy of the part with its ID replaced by id.
func (p SnapshotPart) WithID(id message.PartID) SnapshotPart {
	p.ID = id
	return p
}

// Metadata marks the part as message.Metadata.
func (SnapshotPart) Metadata() {}

// TreeIn returns the workspace tree that the SnapshotPart among parts
// records, or the zero Hash when parts hold none or tracking the tree
// failed.
func TreeIn(parts []message.ContentPart) Hash {
	p, _ := message.LastPart[SnapshotPart](parts)
	return p.Tree
}
