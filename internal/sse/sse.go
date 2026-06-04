// Package sse fans out engine.Transition events to HTTP Server-Sent
// Events subscribers. Implements engine.EventBus.
//
// Backpressure: each subscriber has a small bounded queue. If a
// subscriber falls behind, additional events are dropped for THAT
// subscriber only (a "missed N events" SSE is emitted on the next slot
// they free up). The engine never blocks waiting for slow clients.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/bcnelson/cadence/internal/engine"
)

const (
	// subscriberQueueDepth caps how many in-flight events one subscriber
	// can have queued before the bus starts dropping for them. Small but
	// not tiny — bursty transitions (e.g. config reload that flips many
	// checks at once) shouldn't immediately blow past it.
	subscriberQueueDepth = 32
)

// Bus is an in-memory pub/sub for transitions. Safe for concurrent use.
type Bus struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

func NewBus() *Bus {
	return &Bus{subs: make(map[*subscription]struct{})}
}

// subscription is one connected client. The bus enqueues events into ch;
// missed counts events dropped due to a full queue so the client gets a
// resync hint when they next have room.
type subscription struct {
	ch     chan engine.Transition
	missed atomic.Int64
}

// Publish fans an event out to every subscriber. Per the engine.EventBus
// contract, this must not block. Slow subscribers see drops, not delays.
func (b *Bus) Publish(t *engine.Transition) {
	if t == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- *t:
		default:
			s.missed.Add(1)
		}
	}
}

func (b *Bus) subscribe() *subscription {
	s := &subscription{ch: make(chan engine.Transition, subscriberQueueDepth)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *Bus) unsubscribe(s *subscription) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
	close(s.ch)
}

// Subscribers returns the current count. Used by tests and for the
// dashboard's "connected clients" gauge.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Handler returns an http.Handler that streams transitions to one client
// over SSE. Each client gets its own goroutine; disconnect (context
// cancel) cleans up the subscription.
func (b *Bus) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable nginx's response buffering if it ever fronts this.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		// An immediate flush makes the connection visible to clients
		// (and to test code) without waiting for the first event.
		flusher.Flush()

		s := b.subscribe()
		defer b.unsubscribe(s)

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-s.ch:
				if !ok {
					return
				}
				if missed := s.missed.Swap(0); missed > 0 {
					// Inform the client of dropped events so they can
					// resync from the read API if they care.
					_, _ = fmt.Fprintf(w, "event: missed\ndata: {\"count\": %d}\n\n", missed)
				}
				payload, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "event: transition\ndata: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}
