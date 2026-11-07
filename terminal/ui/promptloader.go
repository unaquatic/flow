package ui

import (
	"context"
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// PromptLoadDelay is how long PromptLoader waits for the selection to settle
// before it loads a session's prompts, so that holding an arrow down through
// the session list loads only the session it stops on.
const PromptLoadDelay = 80 * time.Millisecond

// PromptLoader loads the prompts the session list previews off the event
// loop, so that a session whose branch is long to walk does not hold up the
// keys that move through the list. It loads one session at a time, the one
// selected last: a request drops the one before it, and cancels its query.
// It keeps what it loaded until Reset, so that moving back to a session
// shows its prompts at once.
//
// Its methods run on the event loop.
type PromptLoader struct {
	// Load reads the preview of session id, as Service.SessionPrompts gives
	// it. It runs off the event loop, and stops early once ctx is done.
	Load func(ctx context.Context, id message.SessionID) []UIEvent
	// Enqueue runs a function on the event loop, as Renderer.Enqueue does.
	Enqueue func(func())
	// Apply shows a loaded preview, on the event loop.
	Apply func([]UIEvent)
	// Loading, when set, shows that a preview is loading, in place of the
	// one shown before, which belongs to another session.
	Loading func()
	// Delay is how long a request waits before it loads, as PromptLoadDelay.
	Delay time.Duration
	// Loaded holds the previews loaded since the last Reset, by session.
	Loaded map[message.SessionID][]UIEvent
	// Want is the session whose preview the last request asked for, or ""
	// after Reset.
	Want message.SessionID
	// Cancel stops the load in progress, or is nil when none is.
	Cancel context.CancelFunc
}

// Request shows the preview of session id: at once when it is loaded, and
// otherwise, after Loading, once Delay has passed with id still wanted and
// the load is done. It cancels the load of the session wanted before.
func (l *PromptLoader) Request(id message.SessionID) {
	l.Stop()
	l.Want = id
	if events, ok := l.Loaded[id]; ok {
		l.Apply(events)
		return
	}
	if l.Loading != nil {
		l.Loading()
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.Cancel = cancel
	timer := time.AfterFunc(l.Delay, func() {
		events := l.Load(ctx, id)
		if ctx.Err() != nil {
			return
		}
		l.Enqueue(func() {
			if ctx.Err() != nil || l.Want != id {
				return
			}
			cancel()
			l.Cancel = nil
			if l.Loaded == nil {
				l.Loaded = make(map[message.SessionID][]UIEvent)
			}
			l.Loaded[id] = events
			l.Apply(events)
		})
	})
	// Cancelling before the timer fires stops it, so that no load starts.
	context.AfterFunc(ctx, func() { timer.Stop() })
}

// Stop cancels the load in progress, if any, and leaves what is shown.
func (l *PromptLoader) Stop() {
	if l.Cancel != nil {
		l.Cancel()
		l.Cancel = nil
	}
}

// Reset stops the load in progress and forgets the loaded previews, as the
// session list closes, since the sessions may change before it opens again.
func (l *PromptLoader) Reset() {
	l.Stop()
	l.Want = ""
	clear(l.Loaded)
}
