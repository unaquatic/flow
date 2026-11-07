// Package ui shows workspace snapshots in the terminal UI, as the terminal
// side of the snapshot extension: a row like a tool call's while a snapshot
// is being taken (StepBox), and under each reply the changes it made
// (PatchBox).
package ui

import (
	"context"
	"log/slog"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
)

// Extension adds the widgets of snapshots to the transcript. It implements
// ui.Extension.
type Extension struct {
	// Snapshots is the extension that records the snapshots, whose patches
	// PatchBox shows.
	Snapshots *snapshot.Extension
	// ReadPatch reads the patch from one tree to another, as the Patch of
	// Snapshots does.
	ReadPatch func(ctx context.Context, from, to snapshot.Hash) (string, error)
	// Patches holds the patches LoadPatch read, by the trees they go between,
	// which it shows again without reading them again.
	Patches map[[2]snapshot.Hash]string
	// ctx is the context patches are read in.
	ctx context.Context
}

var _ ui.Extension = (*Extension)(nil)

// New returns the widgets of the snapshots that s records, whose patches are
// read in ctx.
func New(ctx context.Context, s *snapshot.Extension) *Extension {
	return &Extension{Snapshots: s, ReadPatch: s.Patch, Patches: make(map[[2]snapshot.Hash]string), ctx: ctx}
}

// NewPartView returns the StepBox of a step part, and nil for another part.
func (e *Extension) NewPartView(part message.ContentPart, host ui.ViewHost) ui.PartView {
	step, ok := part.(snapshot.StepPart)
	if !ok {
		return nil
	}
	box := NewStepBox(step)
	box.SetViewStyle(host.Style)
	return box
}

// NewSegmentView returns the PatchBox that shows the changes msg made, which
// stays hidden until they are known, and while there are none.
func (e *Extension) NewSegmentView(msg ui.ChatMessage, host ui.ViewHost) ui.SegmentView {
	box := NewPatchBox(host.Theme, host.Fg)
	if host.Enqueue != nil {
		box.Load = func(from, to snapshot.Hash) { e.LoadPatch(box, from, to, host) }
	}
	box.SetMessage(msg)
	return box
}

// LoadPatch shows in box the patch from one tree to another, and lays the
// bubble out again. It reads the patch at once, on the event loop, the first
// time, and keeps it in Patches. Every changes box of a transcript is then
// whole as the transcript is written, and one written again, as a resize
// writes it, reads none. A patch that arrived later would change a row that
// may have scrolled into the scrollback, which keeps the row as it was.
func (e *Extension) LoadPatch(box *PatchBox, from, to snapshot.Hash, host ui.ViewHost) {
	key := [2]snapshot.Hash{from, to}
	patch, ok := e.Patches[key]
	if !ok {
		var err error
		if patch, err = e.ReadPatch(e.ctx, from, to); err != nil {
			slog.Warn("step patch", "from", from, "to", to, "err", err)
			return
		}
		e.Patches[key] = patch
	}
	box.SetPatch(patch)
	host.Relayout()
}
