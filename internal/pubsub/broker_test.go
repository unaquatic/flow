package pubsub

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestBroker_FanOut(t *testing.T) {
	b := NewBroker[string]()
	ctx := t.Context()
	first := b.Subscribe(ctx)
	second := b.Subscribe(ctx)

	b.Publish("ses_1")

	for name, ch := range map[string]<-chan string{"first": first, "second": second} {
		select {
		case ev := <-ch:
			if ev != "ses_1" {
				t.Errorf("%s: got %q, want ses_1", name, ev)
			}
		default:
			t.Errorf("%s: no event delivered", name)
		}
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBroker[string]()
		ctx, cancel := context.WithCancel(t.Context())
		ch := b.Subscribe(ctx)

		cancel()
		synctest.Wait()

		b.Publish("ses_1")
		select {
		case ev := <-ch:
			t.Errorf("received %q after unsubscribe", ev)
		default:
		}
	})
}

func TestBroker_FullUnblocksOnUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBroker[string]()
		ctx, cancel := context.WithCancel(t.Context())
		b.Subscribe(ctx)

		done := make(chan struct{})
		go func() {
			defer close(done)
			// The final event exceeds the subscriber buffer.
			for range subscriberBuffer + 1 {
				b.Publish("ses_1")
			}
		}()

		synctest.Wait()
		cancel()
		<-done
	})
}

// TestBroker_DroppableNeverBlocks checks that a droppable subscriber that
// stops reading holds up no publisher: once its buffer is full, the broker
// drops it and ends its subscription, while a subscriber of Subscribe that
// reads still gets every event.
func TestBroker_DroppableNeverBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBroker[int]()
		stalled, ended := b.SubscribeDroppable(t.Context())
		reader := b.Subscribe(t.Context())

		const n = subscriberBuffer + 10
		got := make(chan int)
		go func() {
			count := 0
			for range n {
				<-reader
				count++
			}
			got <- count
		}()

		published := make(chan struct{})
		go func() {
			defer close(published)
			for i := range n {
				b.Publish(i)
			}
		}()
		synctest.Wait()
		select {
		case <-published:
		default:
			t.Fatal("Publish is held up by a droppable subscriber that stopped reading")
		}

		if count := <-got; count != n {
			t.Errorf("reading subscriber got %d events, want %d", count, n)
		}
		select {
		case <-ended:
		default:
			t.Fatal("the stalled subscription did not end")
		}
		// The events buffered before the drop can still be read, in order,
		// and none came after.
		if len(stalled) != subscriberBuffer {
			t.Fatalf("stalled subscriber holds %d events, want %d", len(stalled), subscriberBuffer)
		}
		for i := range subscriberBuffer {
			if ev := <-stalled; ev != i {
				t.Fatalf("buffered event %d = %d, want %d", i, ev, i)
			}
		}
		b.Publish(n)
		if len(stalled) != 0 {
			t.Error("a dropped subscriber received an event")
		}
	})
}

// TestBroker_DroppableEndsWithContext checks that a droppable subscription
// ends, closing its ended channel, when its context does.
func TestBroker_DroppableEndsWithContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBroker[string]()
		ctx, cancel := context.WithCancel(t.Context())
		events, ended := b.SubscribeDroppable(ctx)
		cancel()
		synctest.Wait()
		select {
		case <-ended:
		default:
			t.Fatal("subscription did not end with its context")
		}
		b.Publish("ses_1")
		if len(events) != 0 {
			t.Error("received an event after the subscription ended")
		}
	})
}
