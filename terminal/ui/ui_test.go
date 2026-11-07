package ui

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/unaquatic/flow/app"
)

// TestAppEventsAfter checks that a function After takes runs on the event
// loop behind every event published before the call, even those the
// forwarder has not read yet. A history move's notice relies on it: the
// transcript the move published clears every row, so a notice applied before
// it would vanish.
func TestAppEventsAfter(t *testing.T) {
	// Run picks at random between an event and a function that are both
	// ready, so each order is tried many times over.
	for range 50 {
		synctest.Test(t, func(t *testing.T) {
			events := make(chan app.Event, 4)
			done := make(chan struct{})
			defer close(done)
			var ran []string
			q := NewAppEvents(events, func(ev app.Event) { ran = append(ran, fmt.Sprintf("%T", ev)) },
				func(fn func()) { fn() }, done)

			// The move has published, and its events wait in the buffer.
			events <- app.EventAgentEnded{}
			events <- app.EventSessionLoaded{}
			go q.Run()
			reported := make(chan struct{})
			q.After(func() {
				ran = append(ran, "report")
				close(reported)
			})
			<-reported

			want := []string{"app.EventAgentEnded", "app.EventSessionLoaded", "report"}
			if !slices.Equal(ran, want) {
				t.Fatalf("ran %q, want %q", ran, want)
			}
		})
	}
}

// TestAppEventsAfterStopped checks that After returns, running nothing, once
// Run has ended.
func TestAppEventsAfterStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		q := NewAppEvents(make(chan app.Event), func(app.Event) {}, func(fn func()) { fn() }, done)
		go q.Run()
		close(done)
		synctest.Wait()
		q.After(func() { t.Error("ran after Run ended") })
	})
}
