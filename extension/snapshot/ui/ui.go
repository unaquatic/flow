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

// maxPatchLoads is the number of patches fetched at once, so that loading a
// long session does not diff every step at once.
const maxPatchLoads = 4

// Extension adds the widgets of snapshots to the transcript. It implements
// ui.Extension.
type Extension struct {
	// Snapshots is the extension that records the snapshots, whose patches
	// PatchBox shows.
	Snapshots *snapshot.Extension
	// ctx is the context patches are read in.
	ctx context.Context
	// slots bounds the patches fetched at once.
	slots chan struct{}
}

var _ ui.Extension = (*Extension)(nil)

// New returns the widgets of the snapshots that s records, whose patches are
// read in ctx.
func New(ctx context.Context, s *snapshot.Extension) *Extension {
	return &Extension{Snapshots: s, ctx: ctx, slots: make(chan struct{}, maxPatchLoads)}
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

// LoadPatch reads the patch from one tree to another off the event loop,
// since it reads the snapshot database, and shows it in box back on the
// loop, laying out the bubble again.
func (e *Extension) LoadPatch(box *PatchBox, from, to snapshot.Hash, host ui.ViewHost) {
	go func() {
		e.slots <- struct{}{}
		patch, err := e.Snapshots.Patch(e.ctx, from, to)
		<-e.slots
		if err != nil {
			slog.Warn("step patch", "from", from, "to", to, "err", err)
			return
		}
		host.Enqueue(func() {
			box.SetPatch(patch)
			host.Relayout()
		})
	}()
}
