package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/alert"
	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/sse"
	"github.com/bcnelson/cadence/internal/store"
)

// recordedWebhook is a sink the e2e test points its alert channels at.
// It captures POST bodies so the test can assert on alert payloads without
// touching the network.
type recordedWebhook struct {
	mu    sync.Mutex
	posts [][]byte
	srv   *httptest.Server
}

func newRecordedWebhook(t *testing.T) *recordedWebhook {
	t.Helper()
	rw := &recordedWebhook{}
	rw.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rw.mu.Lock()
		rw.posts = append(rw.posts, body)
		rw.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rw.srv.Close)
	return rw
}

func (r *recordedWebhook) waitFor(t *testing.T, predicate func([][]byte) bool, timeout time.Duration, label string) [][]byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		snapshot := append([][]byte{}, r.posts...)
		r.mu.Unlock()
		if predicate(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: predicate not satisfied within %v (have %d posts)", label, timeout, len(snapshot))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *recordedWebhook) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte{}, r.posts...)
}

// settableClock is a clock that the test advances manually.
type settableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *settableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *settableClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *settableClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Wire-format mirrors of payloads the test asserts on. Defined here so
// renames in production show up as compile errors against the test.

type webhookCheckSummary struct {
	Slug   string   `json:"slug"`
	Name   string   `json:"name,omitempty"`
	UUID   string   `json:"uuid"`
	Tags   []string `json:"tags,omitempty"`
	Status string   `json:"status"`
}

type webhookPayload struct {
	Event  string              `json:"event"`
	Check  webhookCheckSummary `json:"check"`
	From   string              `json:"from"`
	To     string              `json:"to"`
	At     string              `json:"at"`
	Reason string              `json:"reason,omitempty"`
}

type ssePayload struct {
	CheckUUID string `json:"check_uuid"`
	Slug      string `json:"slug"`
	From      string `json:"from"`
	To        string `json:"to"`
	At        string `json:"at"`
	Reason    string `json:"reason,omitempty"`
}

type checkView struct {
	Name      string  `json:"name,omitempty"`
	Slug      string  `json:"slug"`
	Tags      string  `json:"tags"`
	Status    string  `json:"status"`
	Started   bool    `json:"started"`
	LastPing  *string `json:"last_ping,omitempty"`
	NextPing  *string `json:"next_ping,omitempty"`
	Grace     int64   `json:"grace"`
	Schedule  string  `json:"schedule,omitempty"`
	Timezone  string  `json:"timezone,omitempty"`
	Timeout   int64   `json:"timeout,omitempty"`
	NPings    int     `json:"n_pings"`
	PingURL   *string `json:"ping_url,omitempty"`
	Channels  *string `json:"channels,omitempty"`
	UniqueKey string  `json:"unique_key,omitempty"`
}

// e2eHarness wires the full daemon stack against in-process httptest
// servers. Both e2e tests share it.
type e2eHarness struct {
	serverURL string
	reg       *config.Registry
	engine    *engine.Engine
	bus       *sse.Bus
	clk       *settableClock
	rw        *recordedWebhook
}

const e2eConfigTemplate = `
server:
  uuid_salt: "e2e-salt"
  api_keys:
    read_write: ["rw-key"]
    read_only:  ["ro-key"]
ping_keys:
  - { name: ops, key: "ops-secret" }
channels:
  - { name: hook, type: webhook, url: %q }
defaults:
  channels: [hook]
checks:
  - { slug: api, name: "API Daemon", period: 10m, grace: 5m, ping_keys: [ops], tags: [web, prod] }
`

func setupE2E(t *testing.T) *e2eHarness {
	t.Helper()
	rw := newRecordedWebhook(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(e2eConfigTemplate, rw.srv.URL)), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{cfgPath}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "store"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	clk := &settableClock{now: time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)}
	bus := sse.NewBus()
	alerter := alert.New(reg.Channels, alert.Options{Timeout: 2 * time.Second})
	eng, err := engine.New(reg, st, engine.Options{
		Bus:     bus,
		Alerter: alerter,
		Now:     clk.Now,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	mux := http.NewServeMux()
	registerRoutes(mux, reg, eng, st, bus)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &e2eHarness{
		serverURL: srv.URL,
		reg:       reg,
		engine:    eng,
		bus:       bus,
		clk:       clk,
		rw:        rw,
	}
}

// waitForSubscribers blocks until the bus reports at least n subscribers.
// The SSE handler subscribes only after sending its response headers, so
// the client's `Do()` can return before the server-side subscription is
// established — this closes the race for tests that need to publish events
// the new subscriber will see.
func (h *e2eHarness) waitForSubscribers(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if h.bus.Subscribers() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitForSubscribers: want >=%d, have %d after %v", n, h.bus.Subscribers(), timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestE2EHappyPathPingTickDownRecover(t *testing.T) {
	h := setupE2E(t)
	apiCheck := h.reg.CheckBySlug("api")
	uuidStr := apiCheck.UUID.String()

	// /healthz sanity.
	resp := mustDo(t, http.MethodGet, h.serverURL+"/healthz", nil, "")
	if resp.code != http.StatusOK || resp.body != "ok" {
		t.Errorf("healthz: code=%d body=%q", resp.code, resp.body)
	}

	// First ping: response shape (Ping-Body-Limit header, body=OK), engine
	// state transitions to up.
	resp = mustDo(t, http.MethodGet, h.serverURL+"/ping/api", http.Header{"X-Ping-Key": []string{"ops-secret"}}, "")
	if resp.code != http.StatusOK {
		t.Fatalf("ping: code=%d body=%q", resp.code, resp.body)
	}
	if resp.body != "OK" {
		t.Errorf("ping body: got %q, want OK", resp.body)
	}
	if got := resp.headers.Get("Ping-Body-Limit"); got != strconv.Itoa(store.DefaultMaxBodyBytes) {
		t.Errorf("Ping-Body-Limit: got %q, want %d", got, store.DefaultMaxBodyBytes)
	}
	if snap, _ := h.engine.Snapshot(apiCheck.UUID); snap.Status != store.StatusUp {
		t.Fatalf("engine after ping: status %q, want up", snap.Status)
	}

	// Mgmt GET (single check, read-write key): every documented wire field.
	var view checkView
	mustGetJSON(t, h.serverURL+"/api/v3/checks/"+uuidStr, "rw-key", &view)
	if view.Slug != "api" {
		t.Errorf("view.slug: %q", view.Slug)
	}
	if view.Name != "API Daemon" {
		t.Errorf("view.name: %q", view.Name)
	}
	if view.Tags != "web prod" {
		t.Errorf("view.tags (space-joined HC.io convention): got %q", view.Tags)
	}
	if view.Status != "up" {
		t.Errorf("view.status after first ping: %q", view.Status)
	}
	if view.Started {
		t.Error("view.started should be false after success ping")
	}
	if view.Grace != int64((5 * time.Minute).Seconds()) {
		t.Errorf("view.grace: %d", view.Grace)
	}
	if view.Timeout != int64((10 * time.Minute).Seconds()) {
		t.Errorf("view.timeout (period seconds): %d", view.Timeout)
	}
	if view.NPings < 1 {
		t.Errorf("view.n_pings: %d", view.NPings)
	}
	if view.LastPing == nil {
		t.Error("view.last_ping should be populated after first ping")
	} else if _, err := time.Parse(time.RFC3339, *view.LastPing); err != nil {
		t.Errorf("view.last_ping not RFC3339: %q (%v)", *view.LastPing, err)
	}
	if view.NextPing == nil {
		t.Error("view.next_ping should be populated when last_ping is set")
	}
	if view.PingURL == nil {
		t.Error("view.ping_url should be present for read-write key")
	} else if !strings.Contains(*view.PingURL, "api") {
		t.Errorf("view.ping_url should reference slug: %q", *view.PingURL)
	}
	if view.Channels == nil || *view.Channels != "hook" {
		t.Errorf("view.channels for read-write key: got %v", view.Channels)
	}
	if view.UniqueKey != "" {
		t.Error("view.unique_key should be empty for read-write key (read-only-only field)")
	}
	firstPingAt := *view.LastPing

	// Read-only key: ping_url and channels are stripped, unique_key is added.
	var viewRO checkView
	mustGetJSON(t, h.serverURL+"/api/v3/checks/"+uuidStr, "ro-key", &viewRO)
	if viewRO.PingURL != nil || viewRO.Channels != nil {
		t.Errorf("read-only must not include ping_url/channels: %+v / %+v", viewRO.PingURL, viewRO.Channels)
	}
	if viewRO.UniqueKey == "" {
		t.Error("read-only must include unique_key")
	}

	// Mgmt LIST returns our check.
	var listResp struct {
		Checks []checkView `json:"checks"`
	}
	mustGetJSON(t, h.serverURL+"/api/v3/checks/", "rw-key", &listResp)
	if len(listResp.Checks) != 1 {
		t.Fatalf("list len: %d", len(listResp.Checks))
	}
	if listResp.Checks[0].Slug != "api" || listResp.Checks[0].Status != "up" {
		t.Errorf("list[0]: %+v", listResp.Checks[0])
	}

	// Open SSE subscription before causing the transition. /events is
	// auth-gated; the test passes the read-only key via query string
	// (the form browser EventSource also uses).
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	events := subscribeSSE(t, sseCtx, h.serverURL+"/events?api_key=ro-key")
	h.waitForSubscribers(t, 1, 2*time.Second)

	// Advance the clock past period+grace and tick → down.
	tickTime := h.clk.Advance(16 * time.Minute)
	h.engine.Tick(h.clk.Now())
	if snap, _ := h.engine.Snapshot(apiCheck.UUID); snap.Status != store.StatusDown {
		t.Fatalf("engine after grace: %q", snap.Status)
	}

	// Down webhook payload — every documented field.
	posts := h.rw.waitFor(t, func(p [][]byte) bool { return countEvent(p, "down") == 1 }, 2*time.Second, "waiting for down webhook")
	down := decodeFirstWebhook(t, posts, "down")
	if down.Check.Slug != "api" {
		t.Errorf("webhook check.slug: %q", down.Check.Slug)
	}
	if down.Check.Name != "API Daemon" {
		t.Errorf("webhook check.name: %q", down.Check.Name)
	}
	if down.Check.UUID != uuidStr {
		t.Errorf("webhook check.uuid: %q", down.Check.UUID)
	}
	if down.Check.Status != "down" {
		t.Errorf("webhook check.status: %q", down.Check.Status)
	}
	wantTags := map[string]bool{"web": true, "prod": true}
	gotTags := map[string]bool{}
	for _, tag := range down.Check.Tags {
		gotTags[tag] = true
	}
	for tag := range wantTags {
		if !gotTags[tag] {
			t.Errorf("webhook check.tags missing %q: got %v", tag, down.Check.Tags)
		}
	}
	if down.From != "up" || down.To != "down" {
		t.Errorf("webhook from/to: %s -> %s", down.From, down.To)
	}
	if down.Reason == "" {
		t.Error("webhook reason should be set on down transition")
	}
	if at, err := time.Parse(time.RFC3339, down.At); err != nil {
		t.Errorf("webhook at not RFC3339: %q (%v)", down.At, err)
	} else if at.Sub(tickTime).Abs() > time.Second {
		t.Errorf("webhook at %v far from tickTime %v", at, tickTime)
	}

	// Down SSE event shape.
	downEv := waitForTransition(t, events, "up", "down", 2*time.Second)
	if downEv.CheckUUID != uuidStr {
		t.Errorf("sse check_uuid: %q", downEv.CheckUUID)
	}
	if downEv.Slug != "api" {
		t.Errorf("sse slug: %q", downEv.Slug)
	}
	if downEv.Reason == "" {
		t.Error("sse reason should be set on down transition")
	}
	if _, err := time.Parse(time.RFC3339, downEv.At); err != nil {
		t.Errorf("sse at not RFC3339: %q", downEv.At)
	}

	// Mgmt list still works; the check now reports down via the
	// HC.io-compatible status vocabulary (no translation here since down
	// has the same name in both worlds).
	var viewDown checkView
	mustGetJSON(t, h.serverURL+"/api/v3/checks/"+uuidStr, "rw-key", &viewDown)
	if viewDown.Status != "down" {
		t.Errorf("mgmt view after down: status %q", viewDown.Status)
	}
	if viewDown.LastPing == nil || *viewDown.LastPing != firstPingAt {
		t.Errorf("last_ping should still reflect the original ping after down: got %v, want %q", viewDown.LastPing, firstPingAt)
	}

	// Recover ping. The recover webhook + SSE event are observable.
	h.clk.Advance(1 * time.Minute)
	resp = mustDo(t, http.MethodGet, h.serverURL+"/ping/api", http.Header{"X-Ping-Key": []string{"ops-secret"}}, "")
	if resp.code != http.StatusOK {
		t.Fatalf("recover ping: code=%d body=%q", resp.code, resp.body)
	}
	if snap, _ := h.engine.Snapshot(apiCheck.UUID); snap.Status != store.StatusUp {
		t.Fatalf("engine after recover: %q", snap.Status)
	}

	posts = h.rw.waitFor(t, func(p [][]byte) bool { return countEvent(p, "recover") == 1 }, 2*time.Second, "waiting for recover webhook")
	rec := decodeFirstWebhook(t, posts, "recover")
	if rec.Check.Status != "up" {
		t.Errorf("recover webhook check.status: %q", rec.Check.Status)
	}
	if rec.From != "down" || rec.To != "up" {
		t.Errorf("recover webhook from/to: %s -> %s", rec.From, rec.To)
	}

	recEv := waitForTransition(t, events, "down", "up", 2*time.Second)
	if recEv.Slug != "api" {
		t.Errorf("recover sse slug: %q", recEv.Slug)
	}

	// After recovery the mgmt API reflects new last_ping AND down isn't fired again.
	var viewRecover checkView
	mustGetJSON(t, h.serverURL+"/api/v3/checks/"+uuidStr, "rw-key", &viewRecover)
	if viewRecover.Status != "up" {
		t.Errorf("mgmt status after recover: %q", viewRecover.Status)
	}
	if viewRecover.LastPing == nil {
		t.Error("recover last_ping nil")
	} else if *viewRecover.LastPing == firstPingAt {
		t.Errorf("recover did not advance last_ping: still %q", firstPingAt)
	}
	if viewRecover.NPings < 2 {
		t.Errorf("n_pings should reflect both pings: got %d", viewRecover.NPings)
	}

	// Repeat-down suppression: only one down post total across the whole run.
	if got := countEvent(h.rw.snapshot(), "down"); got != 1 {
		t.Errorf("repeat-down suppression broken: got %d down posts", got)
	}
}

func TestE2EAuthBoundaries(t *testing.T) {
	h := setupE2E(t)
	apiCheck := h.reg.CheckBySlug("api")

	// Slug-form ping auth: missing key, wrong key, and an unknown slug all
	// return 404 — non-enumerable namespace per the spec.
	for _, tc := range []struct {
		name    string
		url     string
		headers http.Header
	}{
		{"no key", h.serverURL + "/ping/api", nil},
		{"wrong key header", h.serverURL + "/ping/api", http.Header{"X-Ping-Key": []string{"nope"}}},
		{"wrong key query", h.serverURL + "/ping/api?ping_key=nope", nil},
		{"unknown slug", h.serverURL + "/ping/no-such-check", http.Header{"X-Ping-Key": []string{"ops-secret"}}},
	} {
		t.Run("ping/"+tc.name, func(t *testing.T) {
			resp := mustDo(t, http.MethodGet, tc.url, tc.headers, "")
			if resp.code != http.StatusNotFound {
				t.Errorf("got %d, want 404 (body=%q)", resp.code, resp.body)
			}
		})
	}

	// UUID-form ping against a closed (key-protected) check must NOT bypass
	// the allow-list. Returns 404.
	t.Run("ping/uuid-form on closed check", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/ping/"+apiCheck.UUID.String(), nil, "")
		if resp.code != http.StatusNotFound {
			t.Errorf("got %d, want 404", resp.code)
		}
	})

	// Mgmt auth: missing key 401, wrong key 401, read-only key works for reads.
	t.Run("mgmt no api key", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/api/v3/checks/", nil, "")
		if resp.code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.code)
		}
	})
	t.Run("mgmt wrong api key", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/api/v3/checks/", http.Header{"X-Api-Key": []string{"nope"}}, "")
		if resp.code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.code)
		}
	})
	t.Run("mgmt read-only key works for reads", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/api/v3/checks/", http.Header{"X-Api-Key": []string{"ro-key"}}, "")
		if resp.code != http.StatusOK {
			t.Errorf("got %d, want 200", resp.code)
		}
	})
	t.Run("mgmt accepts api_key query param", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/api/v3/checks/?api_key=ro-key", nil, "")
		if resp.code != http.StatusOK {
			t.Errorf("got %d, want 200", resp.code)
		}
	})

	// SSE auth: same allow-list as mgmt; the streamed success path is
	// exercised by TestE2EHappyPathPingTickDownRecover (which connects
	// with ?api_key=ro-key). Here we only assert the 401 boundary
	// because mustDo would hang reading a never-ending 200 body.
	t.Run("sse no api key", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/events", nil, "")
		if resp.code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.code)
		}
	})
	t.Run("sse wrong api key", func(t *testing.T) {
		resp := mustDo(t, http.MethodGet, h.serverURL+"/events?api_key=nope", nil, "")
		if resp.code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.code)
		}
	})

	// Writes return 409 with an explanatory message — config is the source of truth.
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"create", http.MethodPost, "/api/v3/checks/"},
		{"update", http.MethodPost, "/api/v3/checks/" + apiCheck.UUID.String()},
		{"delete", http.MethodDelete, "/api/v3/checks/" + apiCheck.UUID.String()},
		{"pause", http.MethodPost, "/api/v3/checks/" + apiCheck.UUID.String() + "/pause"},
		{"resume", http.MethodPost, "/api/v3/checks/" + apiCheck.UUID.String() + "/resume"},
	} {
		t.Run("mgmt write rejected: "+tc.name, func(t *testing.T) {
			resp := mustDo(t, tc.method, h.serverURL+tc.path, http.Header{"X-Api-Key": []string{"rw-key"}}, "")
			if resp.code != http.StatusConflict {
				t.Fatalf("got %d, want 409 (body=%q)", resp.code, resp.body)
			}
			if !strings.Contains(resp.body, "YAML") && !strings.Contains(resp.body, "config") {
				t.Errorf("409 body should explain why: got %q", resp.body)
			}
		})
	}
}

// ---- HTTP helpers ----

type httpResult struct {
	code    int
	body    string
	headers http.Header
}

func mustDo(t *testing.T, method, url string, headers http.Header, body string) httpResult {
	t.Helper()
	var reqBody io.Reader = http.NoBody
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return httpResult{code: resp.StatusCode, body: string(raw), headers: resp.Header}
}

func mustGetJSON(t *testing.T, url, apiKey string, into any) {
	t.Helper()
	resp := mustDo(t, http.MethodGet, url, http.Header{"X-Api-Key": []string{apiKey}}, "")
	if resp.code != http.StatusOK {
		t.Fatalf("GET %s: code=%d body=%q", url, resp.code, resp.body)
	}
	if err := json.Unmarshal([]byte(resp.body), into); err != nil {
		t.Fatalf("decode JSON from %s: %v\n---\n%s", url, err, resp.body)
	}
}

// ---- Webhook decoding ----

func decodeFirstWebhook(t *testing.T, posts [][]byte, event string) webhookPayload {
	t.Helper()
	for _, p := range posts {
		var got webhookPayload
		if err := json.Unmarshal(p, &got); err != nil {
			t.Fatalf("decode webhook: %v\n---\n%s", err, p)
		}
		if got.Event == event {
			return got
		}
	}
	t.Fatalf("no webhook with event=%q in %d posts", event, len(posts))
	return webhookPayload{}
}

func countEvent(posts [][]byte, event string) int {
	n := 0
	for _, p := range posts {
		var got webhookPayload
		if err := json.Unmarshal(p, &got); err == nil && got.Event == event {
			n++
		}
	}
	return n
}

// ---- SSE consumption ----

// subscribeSSE opens an SSE stream and pushes decoded transitions to a
// channel. The channel closes when ctx is done or the stream ends.
func subscribeSSE(t *testing.T, ctx context.Context, url string) <-chan ssePayload {
	t.Helper()
	out := make(chan ssePayload, 32)
	ready := make(chan struct{})
	go func() {
		defer close(out)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			close(ready)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		close(ready)

		reader := bufio.NewReader(resp.Body)
		var dataBuf bytes.Buffer
		var eventName string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				// Event boundary: emit transitions; skip meta events ("missed").
				if eventName == "transition" && dataBuf.Len() > 0 {
					var p ssePayload
					if jerr := json.Unmarshal(dataBuf.Bytes(), &p); jerr == nil {
						select {
						case out <- p:
						case <-ctx.Done():
							return
						}
					}
				}
				dataBuf.Reset()
				eventName = ""
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				eventName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if dataBuf.Len() > 0 {
					dataBuf.WriteByte('\n')
				}
				dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	<-ready
	return out
}

func waitForTransition(t *testing.T, events <-chan ssePayload, from, to string, timeout time.Duration) ssePayload {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for transition %s -> %s", from, to)
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("SSE channel closed before transition %s -> %s", from, to)
			}
			if ev.From == from && ev.To == to {
				return ev
			}
		}
	}
}
