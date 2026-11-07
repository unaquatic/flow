package snapshot

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
)

// TypeStep identifies a StepPart.
const TypeStep message.ContentType = "step"

// Hash names a tree the engine recorded.
type Hash = engine.Hash

// ParseHash parses the hexadecimal name of a tree, as Hash.String prints it.
func ParseHash(s string) (Hash, error) { return engine.ParseHash(s) }

// StepPart records the workspace snapshot a message ends with: a prompt's is
// the workspace as the prompt was sent, and a reply's the workspace once its
// tools ran, so a reply's changes are the difference between the snapshot of
// the message before it and its own. It is the last part of its message, and
// a message holds none when snapshots are off. It is message.Metadata: it is
// stored with the message but never sent to a provider, and shows only while
// it is pending.
type StepPart struct {
	// ID identifies the part.
	ID message.PartID `json:"id,omitempty"`
	// Snapshot is the workspace tree, or the zero Hash when tracking it
	// failed or while the part is pending.
	Snapshot Hash `json:"snapshot,omitzero"`
	// Pending reports that the workspace is still being recorded. A part
	// that takes long to record starts pending, so that its message shows
	// the snapshot being taken, and ends with its tree. A stored part is
	// never pending.
	Pending bool `json:"pending,omitempty"`
}

func init() { message.RegisterPart[StepPart](TypeStep) }

// PartType returns TypeStep.
func (StepPart) PartType() message.ContentType { return TypeStep }

// PartID returns the part's ID.
func (p StepPart) PartID() message.PartID { return p.ID }

// WithID returns a copy of the part with its ID replaced by id.
func (p StepPart) WithID(id message.PartID) StepPart {
	p.ID = id
	return p
}

// Metadata marks the part as message.Metadata.
func (StepPart) Metadata() {}

// TreeIn returns the workspace tree that the StepPart among parts records, or
// the zero Hash when parts hold none or tracking the tree failed.
func TreeIn(parts []message.ContentPart) Hash {
	step, _ := message.LastPart[StepPart](parts)
	return step.Snapshot
}
