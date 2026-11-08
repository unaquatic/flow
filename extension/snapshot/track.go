package snapshot

import (
	"context"
	"log/slog"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension"
)

// DefaultIndexingDelay is how long recording the workspace may take before
// the snapshot part that records it starts pending. A quicker snapshot shows
// nothing, so that the end of every message does not flash an indexing row,
// and a slower one shows that it is being taken.
const DefaultIndexingDelay = 150 * time.Millisecond

// OnPrompt ends the prompt with a snapshot part recording the workspace as the
// prompt was sent, which the run's first reply starts from, as Track adds it.
func (e *Extension) OnPrompt(ctx context.Context, _ extension.Prompt, sink agent.PartSink) {
	e.Track(ctx, sink)
}

// OnTurnEnd ends the turn's message with a snapshot part recording the
// workspace as the turn left it, as Track adds it. Tools may have changed
// files however the turn ended, so a stop does not keep the workspace from
// being recorded.
//
// The trees of the message before the turn and of its snapshot part describe
// everything the turn changed, so no separate record of changed files is
// kept: a diff of the pair reproduces it on demand.
func (e *Extension) OnTurnEnd(ctx context.Context, _ agent.Turn, sink agent.PartSink) {
	e.Track(ctx, sink)
}

// Track records the workspace in a new snapshot part, which it starts and ends
// through sink. When recording takes longer than IndexingDelay, it starts
// the part pending, so that its message shows the snapshot being taken, and
// ends it with the tree once recorded. A quicker snapshot starts the part
// with its tree. TrackDelay counts as part of the recording time. It records nothing, and adds no part, while snapshots are
// disabled. A failed snapshot fails neither a turn nor a prompt: it is
// logged, and the part ends with the zero Hash, which names no tree.
func (e *Extension) Track(ctx context.Context, sink agent.PartSink) {
	if e.Disabled() {
		return
	}
	id := message.NewPartID()
	recorded := make(chan SnapshotPart, 1)
	go func() {
		h, err := e.Service.Track(ctx)
		if err != nil {
			slog.Warn("track snapshot", "part", id, "err", err)
		}
		e.hold(ctx)
		recorded <- SnapshotPart{ID: id, Tree: h}
	}()
	if e.IndexingDelay > 0 {
		timer := time.NewTimer(e.IndexingDelay)
		defer timer.Stop()
		select {
		case step := <-recorded:
			sink.Start(step)
			sink.End(step)
			return
		case <-timer.C:
		}
	}
	sink.Start(SnapshotPart{ID: id, Pending: true})
	sink.End(<-recorded)
}

// hold waits out TrackDelay, or until ctx ends, whichever is first.
func (e *Extension) hold(ctx context.Context) {
	if e.TrackDelay <= 0 {
		return
	}
	timer := time.NewTimer(e.TrackDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
