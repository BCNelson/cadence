package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// startBusServer brings up a Bus + http.Server and returns the bus, a
// dial URL, and a cleanup func.
func startBusServer(t *testing.T) (bus *Bus, url string, cleanup func()) {
	t.Helper()
	bus = NewBus()
	srv := httptest.NewServer(bus.Handler())
	return bus, srv.URL, srv.Close
}

// readSSEEvent reads one full SSE event (terminated by a blank line)
// from r. Returns the event name and its data line, or "" on EOF.
func readSSEEvent(t *testing.T, r *bufio.Reader) (eventName, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", ""
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if eventName != "" || data != "" {
				return eventName, data
			}
			// Blank line before any field is a heartbeat; keep going.
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestSSEPublishReceived(t *testing.T) {
	bus, url, cleanup := startBusServer(t)
	defer cleanup()

	// Open a streaming connection.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)

	// Wait for the subscription to be registered before publishing.
	deadline := time.Now().Add(time.Second)
	for bus.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.Subscribers() == 0 {
		t.Fatal("subscriber never registered")
	}

	bus.Publish(&engine.Transition{
		CheckUUID: uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Slug:      "api",
		From:      store.StatusUp,
		To:        store.StatusDown,
		At:        time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC),
		Reason:    "grace exhausted",
	})

	ev, data := readSSEEvent(t, br)
	if ev != "transition" {
		t.Errorf("event: got %q", ev)
	}
	var got engine.Transition
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("data not json: %v / %s", err, data)
	}
	if got.Slug != "api" || got.To != store.StatusDown {
		t.Errorf("payload: %+v", got)
	}
}

func TestSSEFanout(t *testing.T) {
	bus, url, cleanup := startBusServer(t)
	defer cleanup()

	const N = 3
	ctxs := make([]context.CancelFunc, N)
	readers := make([]*bufio.Reader, N)
	defer func() {
		for _, c := range ctxs {
			c()
		}
	}()
	for i := 0; i < N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[i] = cancel
		req, _ := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		readers[i] = bufio.NewReader(resp.Body)
	}
	// Wait for all subscribers.
	deadline := time.Now().Add(time.Second)
	for bus.Subscribers() < N && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := bus.Subscribers(); got != N {
		t.Fatalf("subscribers: got %d, want %d", got, N)
	}

	bus.Publish(&engine.Transition{Slug: "x", From: store.StatusUp, To: store.StatusDown})

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(r *bufio.Reader) {
			defer wg.Done()
			ev, _ := readSSEEvent(t, r)
			if ev != "transition" {
				t.Errorf("client got %q", ev)
			}
		}(readers[i])
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("not all clients received the event")
	}
}

func TestSSEDisconnectCleansSubscription(t *testing.T) {
	bus, url, cleanup := startBusServer(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for bus.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.Subscribers() != 1 {
		t.Fatalf("setup: %d subs", bus.Subscribers())
	}

	cancel()
	_ = resp.Body.Close()

	deadline = time.Now().Add(time.Second)
	for bus.Subscribers() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bus.Subscribers() != 0 {
		t.Errorf("subscription leaked: %d", bus.Subscribers())
	}
}

func TestSSEBackpressureDrops(t *testing.T) {
	bus := NewBus()
	// Skip the HTTP layer: directly subscribe, never read, and verify
	// publishes get dropped instead of blocking the bus.
	sub := bus.subscribe()
	defer bus.unsubscribe(sub)

	// Fill the queue, then overshoot.
	for i := 0; i < subscriberQueueDepth+5; i++ {
		bus.Publish(&engine.Transition{Slug: "x"})
	}
	if got := sub.missed.Load(); got < 5 {
		t.Errorf("missed counter not advanced: %d", got)
	}
}

func TestSSEPublishNilSafe(t *testing.T) {
	bus := NewBus()
	bus.Publish(nil) // must not panic
}
