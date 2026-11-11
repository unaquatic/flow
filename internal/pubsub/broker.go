// Package pubsub delivers published events to every current subscriber.
package pubsub

import (
	"context"
	"sync"
)

// subscriberBuffer is the number of events a subscriber can lag before
// Publish waits for it, or drops it when it subscribed with
// SubscribeDroppable.
const subscriberBuffer = 256

// Broker delivers each event of type T to every current subscriber.
type Broker[T any] struct {
	mu   sync.Mutex
	subs map[int]*subscriber[T]
	next int
}

// subscriber is one subscription to a Broker.
type subscriber[T any] struct {
	ch chan T
	// done is closed once the subscription ends, as its context ends or
	// the broker drops it.
	done chan struct{}
	// droppable makes Publish drop the subscription, rather than wait for
	// it, when ch is full.
	droppable bool
	// end closes done once.
	end sync.Once
}

// NewBroker creates an empty broker.
func NewBroker[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[int]*subscriber[T])}
}

// Subscribe returns a channel receiving every event published until ctx is
// done. Publish waits for the subscriber while it lags subscriberBuffer
// events behind, so it misses none, but a subscriber that stops reading
// stalls every publisher: Subscribe suits subscribers of the same process
// that keep reading, such as a UI. The returned channel is not closed when
// the subscription ends.
func (b *Broker[T]) Subscribe(ctx context.Context) <-chan T {
	sub := b.subscribe(ctx, false)
	return sub.ch
}

// SubscribeDroppable returns a channel receiving the events published until
// ctx is done or the broker drops the subscription, and a channel closed
// when either happens. Publish never waits for the subscriber: once it lags
// subscriberBuffer events behind, the broker drops it, and it receives no
// later event, so that a subscriber that stalls, such as a network client
// that stops reading, stalls no publisher. A dropped subscriber has missed
// events, and has to read again what it follows. The events channel is not
// closed when the subscription ends; the events still buffered in it can
// be read.
func (b *Broker[T]) SubscribeDroppable(ctx context.Context) (events <-chan T, ended <-chan struct{}) {
	sub := b.subscribe(ctx, true)
	return sub.ch, sub.done
}

// subscribe adds a subscriber, which ctx ending removes.
func (b *Broker[T]) subscribe(ctx context.Context, droppable bool) *subscriber[T] {
	sub := &subscriber[T]{
		ch:        make(chan T, subscriberBuffer),
		done:      make(chan struct{}),
		droppable: droppable,
	}
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = sub
	b.mu.Unlock()

	context.AfterFunc(ctx, func() { b.remove(id, sub) })
	return sub
}

// remove ends subscription id, sub, unless it has ended already.
func (b *Broker[T]) remove(id int, sub *subscriber[T]) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
	sub.end.Do(func() { close(sub.done) })
}

// Publish sends ev to every current subscriber. For a subscriber of
// Subscribe, it waits until the event is accepted or the subscription
// ends. A subscriber of SubscribeDroppable whose buffer is full it drops.
func (b *Broker[T]) Publish(ev T) {
	type entry struct {
		id  int
		sub *subscriber[T]
	}
	b.mu.Lock()
	subs := make([]entry, 0, len(b.subs))
	for id, sub := range b.subs {
		subs = append(subs, entry{id, sub})
	}
	b.mu.Unlock()

	for _, e := range subs {
		if e.sub.droppable {
			select {
			case e.sub.ch <- ev:
			case <-e.sub.done:
			default:
				b.remove(e.id, e.sub)
			}
			continue
		}
		select {
		case e.sub.ch <- ev:
		case <-e.sub.done:
		}
	}
}
