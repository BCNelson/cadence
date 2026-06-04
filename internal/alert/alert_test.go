package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// receivingChannel is a test webhook target that captures every received
// request body so tests can assert on payload shape.
type receivingChannel struct {
	mu       sync.Mutex
	requests []receivedRequest
	statusOK int // override with non-2xx to simulate webhook failure
}

type receivedRequest struct {
	method  string
	headers http.Header
	body    []byte
}

func (rc *receivingChannel) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.requests = append(rc.requests, receivedRequest{
			method:  r.Method,
			headers: r.Header.Clone(),
			body:    body,
		})
		status := rc.statusOK
		rc.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}
}

func (rc *receivingChannel) count() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.requests)
}

func (rc *receivingChannel) last() receivedRequest {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.requests[len(rc.requests)-1]
}

func TestWebhookDownPayloadShape(t *testing.T) {
	rc := &receivingChannel{}
	ts := httptest.NewServer(rc.handler())
	defer ts.Close()

	channels := map[string]config.Channel{
		"hook": {Name: "hook", Type: "webhook", URL: ts.URL},
	}
	w := New(channels, Options{})

	check := &config.ResolvedCheck{
		Slug:     "api",
		Name:     "API",
		UUID:     uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Tags:     []string{"web", "prod"},
		Channels: []string{"hook"},
	}
	at := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	trans := &engine.Transition{
		CheckUUID: check.UUID,
		Slug:      check.Slug,
		From:      store.StatusUp,
		To:        store.StatusDown,
		At:        at,
		Reason:    "grace exhausted",
	}
	if err := w.Down(context.Background(), check, trans); err != nil {
		t.Fatalf("Down: %v", err)
	}

	if rc.count() != 1 {
		t.Fatalf("expected 1 webhook hit, got %d", rc.count())
	}
	req := rc.last()
	if req.method != "POST" {
		t.Errorf("method: got %q", req.method)
	}
	if got := req.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type: got %q", got)
	}

	var p map[string]any
	if err := json.Unmarshal(req.body, &p); err != nil {
		t.Fatalf("payload not json: %v / %s", err, req.body)
	}
	if p["event"] != "down" {
		t.Errorf("event: got %v", p["event"])
	}
	if p["from"] != "up" || p["to"] != "down" {
		t.Errorf("from/to: got %v/%v", p["from"], p["to"])
	}
	if p["reason"] != "grace exhausted" {
		t.Errorf("reason: got %v", p["reason"])
	}
	check2 := p["check"].(map[string]any)
	if check2["slug"] != "api" {
		t.Errorf("check.slug: got %v", check2["slug"])
	}
	if check2["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("check.uuid: got %v", check2["uuid"])
	}
}

func TestWebhookRecoverPayload(t *testing.T) {
	rc := &receivingChannel{}
	ts := httptest.NewServer(rc.handler())
	defer ts.Close()

	channels := map[string]config.Channel{
		"hook": {Name: "hook", URL: ts.URL},
	}
	w := New(channels, Options{})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"hook"},
	}
	trans := &engine.Transition{From: store.StatusDown, To: store.StatusUp, At: time.Now()}
	if err := w.Recover(context.Background(), check, trans); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal(rc.last().body, &p)
	if p["event"] != "recover" {
		t.Errorf("event: got %v", p["event"])
	}
}

func TestWebhookMultiChannelFanout(t *testing.T) {
	rc1 := &receivingChannel{}
	rc2 := &receivingChannel{}
	ts1 := httptest.NewServer(rc1.handler())
	ts2 := httptest.NewServer(rc2.handler())
	defer ts1.Close()
	defer ts2.Close()

	channels := map[string]config.Channel{
		"a": {Name: "a", URL: ts1.URL},
		"b": {Name: "b", URL: ts2.URL},
	}
	w := New(channels, Options{})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"a", "b"},
	}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	if err := w.Down(context.Background(), check, trans); err != nil {
		t.Fatal(err)
	}
	if rc1.count() != 1 || rc2.count() != 1 {
		t.Errorf("fanout: got %d / %d", rc1.count(), rc2.count())
	}
}

func TestWebhookErrorsAggregated(t *testing.T) {
	good := &receivingChannel{}
	bad := &receivingChannel{statusOK: http.StatusInternalServerError}
	tsGood := httptest.NewServer(good.handler())
	tsBad := httptest.NewServer(bad.handler())
	defer tsGood.Close()
	defer tsBad.Close()

	channels := map[string]config.Channel{
		"good": {Name: "good", URL: tsGood.URL},
		"bad":  {Name: "bad", URL: tsBad.URL},
	}
	w := New(channels, Options{})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"good", "bad"},
	}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	err := w.Down(context.Background(), check, trans)
	if err == nil {
		t.Fatal("expected error from bad channel")
	}
	if !strings.Contains(err.Error(), "bad") || !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention bad channel and status: %v", err)
	}
	// Good channel still got the request — failures don't block siblings.
	if good.count() != 1 {
		t.Errorf("good channel skipped: count=%d", good.count())
	}
}

func TestWebhookCustomMethodAndHeaders(t *testing.T) {
	rc := &receivingChannel{}
	ts := httptest.NewServer(rc.handler())
	defer ts.Close()

	channels := map[string]config.Channel{
		"hook": {
			Name:    "hook",
			URL:     ts.URL,
			Method:  "PUT",
			Headers: map[string]string{"X-Custom": "value"},
		},
	}
	w := New(channels, Options{})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"hook"},
	}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	if err := w.Down(context.Background(), check, trans); err != nil {
		t.Fatal(err)
	}
	req := rc.last()
	if req.method != "PUT" {
		t.Errorf("method: got %q", req.method)
	}
	if got := req.headers.Get("X-Custom"); got != "value" {
		t.Errorf("custom header: got %q", got)
	}
}

func TestWebhookUserAgentHeader(t *testing.T) {
	rc := &receivingChannel{}
	ts := httptest.NewServer(rc.handler())
	defer ts.Close()

	channels := map[string]config.Channel{
		"hook": {Name: "hook", URL: ts.URL},
	}
	w := New(channels, Options{})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"hook"},
	}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	if err := w.Down(context.Background(), check, trans); err != nil {
		t.Fatal(err)
	}
	if got := rc.last().headers.Get("User-Agent"); got != "cadence/v1" {
		t.Errorf("User-Agent: got %q, want cadence/v1", got)
	}
}

func TestWebhookNetworkErrorSurfaces(t *testing.T) {
	// Point the channel at a server we've already closed, so the client gets
	// a connection-refused on dial.
	rc := &receivingChannel{}
	ts := httptest.NewServer(rc.handler())
	ts.Close() // immediately close to free the port

	channels := map[string]config.Channel{
		"dead": {Name: "dead", URL: ts.URL},
	}
	w := New(channels, Options{Timeout: time.Second})
	check := &config.ResolvedCheck{
		Slug: "api", UUID: uuid.New(), Channels: []string{"dead"},
	}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	err := w.Down(context.Background(), check, trans)
	if err == nil {
		t.Fatal("expected network error from unreachable server")
	}
	if !strings.Contains(err.Error(), "dead") {
		t.Errorf("error should mention channel name: %v", err)
	}
}

func TestWebhookHonorsTimeout(t *testing.T) {
	// Server hangs forever so the only thing that returns is the client
	// timeout firing.
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	// Order matters: ts.Close waits for in-flight handlers, so release the
	// blocked handler before closing the server. defers run LIFO, so register
	// ts.Close first (runs last) and close(release) second (runs first).
	defer ts.Close()
	defer close(release)

	// Each case configures Options.Timeout and asserts the actual elapsed
	// dispatch time lands within max(10% * T, 10ms) of T. The cases span
	// the regime where 10ms dominates (50ms) up to where the percentage
	// dominates (250ms).
	cases := []time.Duration{
		50 * time.Millisecond,
		100 * time.Millisecond,
		250 * time.Millisecond,
	}
	for _, target := range cases {
		t.Run(target.String(), func(t *testing.T) {
			tol := target / 10
			if tol < 10*time.Millisecond {
				tol = 10 * time.Millisecond
			}
			lower := target - tol
			upper := target + tol

			w := New(map[string]config.Channel{
				"slow": {Name: "slow", URL: ts.URL},
			}, Options{Timeout: target})
			check := &config.ResolvedCheck{
				Slug: "api", UUID: uuid.New(), Channels: []string{"slow"},
			}
			trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}

			start := time.Now()
			err := w.Down(context.Background(), check, trans)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatalf("target=%v: expected timeout error from slow server", target)
			}
			// Error must look like a timeout (http.Client.Timeout or context deadline).
			msg := err.Error()
			if !strings.Contains(msg, "Timeout") && !strings.Contains(msg, "deadline") {
				t.Errorf("target=%v: error should look like a timeout: got %v", target, err)
			}
			if elapsed < lower {
				t.Errorf("target=%v: dispatch too fast (%v < %v) — did the timeout actually drive it?", target, elapsed, lower)
			}
			if elapsed > upper {
				t.Errorf("target=%v: dispatch did not honor timeout (%v > %v)", target, elapsed, upper)
			}
		})
	}
}

func TestWebhookCheckWithNoChannelsIsNoop(t *testing.T) {
	w := New(nil, Options{})
	check := &config.ResolvedCheck{Slug: "api", UUID: uuid.New()}
	trans := &engine.Transition{From: store.StatusUp, To: store.StatusDown, At: time.Now()}
	if err := w.Down(context.Background(), check, trans); err != nil {
		t.Errorf("no-channels check should not error: %v", err)
	}
}
