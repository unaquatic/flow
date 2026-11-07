// Package pubsub delivers published events to every current subscriber.
package pubsub

import (
	"context"
	"maps"
	"slices"
	"sync"
)

// subscriberBuffer is the number of events a subscriber can lag before
// blocking publication.
const subscriberBuffer = 256

// Broker delivers each event of type T to every current subscriber.
type Broker[T any] struct {
	mu   sync.Mutex
	subs map[int]*subscriber[T]
	next int
}

type subscriber[T any] struct {
	ch   chan T
	done chan struct{}
}

// NewBroker creates an empty broker.
func NewBroker[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[int]*subscriber[T])}
}

// Subscribe returns a channel receiving events published until ctx is done.
// The returned channel is not closed when the subscription ends.
func (b *Broker[T]) Subscribe(ctx context.Context) <-chan T {
	sub := &subscriber[T]{
		ch:   make(chan T, subscriberBuffer),
		done: make(chan struct{}),
	}
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = sub
	b.mu.Unlock()

	context.AfterFunc(ctx, func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		close(sub.done)
	})
	return sub.ch
}

// Publish sends ev to every current subscriber. It blocks for each subscriber
// until the event is accepted or the subscription ends.
func (b *Broker[T]) Publish(ev T) {
	b.mu.Lock()
	subs := slices.Collect(maps.Values(b.subs))
	b.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		case <-sub.done:
		}
	}
}
