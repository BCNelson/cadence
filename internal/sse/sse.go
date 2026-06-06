// Package sse fans out engine.Transition events to HTTP Server-Sent
// Events subscribers. Implements engine.EventBus.
//
// Backpressure: each subscriber has a small bounded queue. If a
// subscriber falls behind, additional events are dropped for THAT
// subscriber only (a "missed" SSE is emitted with the dropped sequence
// range so the client can re-fetch just the affected window from the
// read API). The engine never blocks waiting for slow clients.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bcnelson/cadence/internal/engine"
)

const (
	// subscriberQueueDepth caps how many in-flight events one subscriber
	// can have queued before the bus starts dropping for them. Small but
	// not tiny — bursty transitions (e.g. config reload that flips many
	// checks at once) shouldn't immediately blow past it.
	subscriberQueueDepth = 32

	// defaultHeartbeatInterval is how often Handler writes an SSE comment
	// line to an otherwise idle connection. Sits comfortably under common
	// proxy / browser idle timeouts (often 60s) so silent streams don't
	// get torn down between transitions.
	defaultHeartbeatInterval = 20 * time.Second
)

// Bus is an in-memory pub/sub for transitions. Safe for concurrent use.
type Bus struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}

	// seq is a process-monotonic counter stamped onto every published
	// transition. Clients use it to detect gaps after a `missed` event
	// and refetch only the affected window from the read API.
	seq atomic.Uint64

	heartbeatInterval time.Duration
}

// Option customizes a Bus at construction.
type Option func(*Bus)

// WithHeartbeatInterval overrides how often Handler writes an SSE keepalive
// comment. Primarily for tests; production should use the default.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(b *Bus) { b.heartbeatInterval = d }
}

func NewBus(opts ...Option) *Bus {
	b := &Bus{
		subs:              make(map[*subscription]struct{}),
		heartbeatInterval: defaultHeartbeatInterval,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// envelope is what the bus enqueues for each subscriber: the transition
// plus its monotonic sequence number.
type envelope struct {
	seq   uint64
	trans engine.Transition
}

// subscription is one connected client. The bus enqueues envelopes into
// ch; missedFrom / missedTo bound the seq range of events that didn't
// fit so the client gets a precise resync hint on the next slot.
type subscription struct {
	ch         chan envelope
	missedMu   sync.Mutex
	missedFrom uint64
	missedTo   uint64
	missedAny  bool
}

// Publish fans an event out to every subscriber. Per the engine.EventBus
// contract, this must not block. Slow subscribers see drops, not delays.
func (b *Bus) Publish(t *engine.Transition) {
	if t == nil {
		return
	}
	seq := b.seq.Add(1)
	env := envelope{seq: seq, trans: *t}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- env:
		default:
			s.recordMissed(seq)
		}
	}
}

func (s *subscription) recordMissed(seq uint64) {
	s.missedMu.Lock()
	defer s.missedMu.Unlock()
	if !s.missedAny {
		s.missedFrom = seq
		s.missedAny = true
	}
	s.missedTo = seq
}

// takeMissed returns and clears the pending missed-range, if any.
func (s *subscription) takeMissed() (from, to uint64, ok bool) {
	s.missedMu.Lock()
	defer s.missedMu.Unlock()
	if !s.missedAny {
		return 0, 0, false
	}
	from, to = s.missedFrom, s.missedTo
	s.missedAny = false
	s.missedFrom, s.missedTo = 0, 0
	return from, to, true
}

func (b *Bus) subscribe() *subscription {
	s := &subscription{ch: make(chan envelope, subscriberQueueDepth)}
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

		// Heartbeat: SSE comment lines (starting with ":") are ignored by
		// EventSource but keep the TCP connection warm so proxies and
		// browser idle detection don't tear down a silent stream between
		// transitions.
		ticker := time.NewTicker(b.heartbeatInterval)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case env, ok := <-s.ch:
				if !ok {
					return
				}
				if from, to, has := s.takeMissed(); has {
					// Inform the client of the dropped sequence range so
					// they can refetch the affected window from the
					// management API instead of doing a full re-list.
					count := to - from + 1
					_, _ = fmt.Fprintf(w, "event: missed\ndata: {\"from\":%d,\"to\":%d,\"count\":%d}\n\n", from, to, count)
				}
				payload, err := json.Marshal(&transitionWithSeq{seq: env.seq, Transition: env.trans})
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "id: %d\nevent: transition\ndata: %s\n\n", env.seq, payload)
				flusher.Flush()
			}
		}
	}
}

// transitionWithSeq adds the `seq` JSON field to engine.Transition so
// clients reading the data payload don't have to parse the SSE `id:`
// line themselves. The seq value also lands on the SSE `id:` for
// EventSource's built-in Last-Event-ID semantics.
type transitionWithSeq struct {
	engine.Transition
	seq uint64
}

func (t *transitionWithSeq) MarshalJSON() ([]byte, error) {
	// Embed the seq into the Transition payload alongside the existing
	// fields. Using a local alias avoids infinite recursion through
	// MarshalJSON.
	type alias engine.Transition
	wrap := struct {
		Seq uint64 `json:"seq"`
		alias
	}{
		Seq:   t.seq,
		alias: alias(t.Transition),
	}
	return json.Marshal(wrap)
}
