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
