package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
)

type mgmtHarness struct {
	reg    *config.Registry
	store  *store.Store
	engine *engine.Engine
	mux    *http.ServeMux
}

func newMgmtHarness(t *testing.T, yaml string) *mgmtHarness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{cfgPath}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "store"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	eng, err := engine.New(reg, st, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	mh := NewMgmtHandler(reg, eng, st, nil)
	mux := http.NewServeMux()
	for _, r := range mh.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}
	return &mgmtHarness{reg: reg, store: st, engine: eng, mux: mux}
}

func (h *mgmtHarness) do(method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, http.NoBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.mux.ServeHTTP(rr, req)
	return rr
}

const sampleConfig = `
server:
  uuid_salt: "s"
  base_url: "https://cadence.example.com"
  api_keys:
    read_write: ["rw-key"]
    read_only: ["ro-key"]
ping_keys:
  - { name: ops, key: "k" }
channels:
  - { name: hook, type: webhook, url: "https://example.com/hook" }
checks:
  - { slug: api,    name: "API",     period: 1h, grace: 5m, ping_keys: [ops], channels: [hook], tags: [web, prod] }
  - { slug: backup, name: "Backup",  cron: "0 2 * * *", grace: 10m, ping_keys: [ops] }
`

func TestMgmtAuthRequired(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	rr := h.do("GET", "/api/v3/checks/", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", rr.Code)
	}
	rr = h.do("GET", "/api/v3/checks/", map[string]string{"X-Api-Key": "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("bad key: got %d", rr.Code)
	}
	// Query-string form is accepted (browser EventSource and other
	// header-less clients depend on this).
	rr = h.do("GET", "/api/v3/checks/?api_key=ro-key", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("query-key: got %d", rr.Code)
	}
}

func TestMgmtListReadWriteIncludesPingURLAndChannels(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	rr := h.do("GET", "/api/v3/checks/", map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("list: got %d", rr.Code)
	}
	var resp struct{ Checks []checkView }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(resp.Checks))
	}
	// Sorted by slug.
	if resp.Checks[0].Slug != "api" || resp.Checks[1].Slug != "backup" {
		t.Errorf("order: %v", []string{resp.Checks[0].Slug, resp.Checks[1].Slug})
	}
	api := resp.Checks[0]
	if api.PingURL == nil || !strings.HasSuffix(*api.PingURL, "/ping/api") {
		t.Errorf("ping_url missing or wrong: %v", api.PingURL)
	}
	if api.Channels == nil || *api.Channels != "hook" {
		t.Errorf("channels: %v", api.Channels)
	}
	if api.UniqueKey != "" {
		t.Errorf("unique_key should be omitted on r/w: %q", api.UniqueKey)
	}
	if api.Tags != "web prod" {
		t.Errorf("tags formatting: %q", api.Tags)
	}
	if api.Grace != 300 {
		t.Errorf("grace seconds: %d", api.Grace)
	}
}

func TestMgmtListReadOnlyOmitsPingURLAndIncludesUniqueKey(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	rr := h.do("GET", "/api/v3/checks/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var resp struct{ Checks []checkView }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	api := resp.Checks[0]
	if api.PingURL != nil {
		t.Errorf("ping_url should be omitted on r/o: %v", api.PingURL)
	}
	if api.Channels != nil {
		t.Errorf("channels should be omitted on r/o: %v", api.Channels)
	}
	if api.UniqueKey == "" {
		t.Errorf("unique_key missing on r/o")
	}
}

func TestMgmtGetByUUIDAndUniqueKey(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	uk := uniqueKey(c.UUID)

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusOK {
		t.Errorf("by uuid: got %d", rr.Code)
	}
	rr = h.do("GET", "/api/v3/checks/"+uk, map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Errorf("by unique_key: got %d", rr.Code)
	}
	rr = h.do("GET", "/api/v3/checks/bogus-id", map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("bogus: got %d", rr.Code)
	}
}

func TestMgmtStatusVocabularyMatchesHCIO(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	// Move the check into late by pinging then ticking past deadline.
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	base := time.Unix(1_700_000_000, 0).UTC()
	h.engine.Tick(base.Add(70 * time.Minute)) // period 1h + 10m > grace 5m, so down
	// Actually we want late, not down. Period 1h + grace 5m = 65m. Use 63m.
	h.engine.Tick(base.Add(63 * time.Minute))

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	// After the second tick we're in down (the 70m tick already pushed
	// the state past grace). Verify the down vocabulary.
	if v.Status != "down" {
		t.Errorf("status: got %q, want %q", v.Status, "down")
	}
}

func TestMgmtLateMapsToGrace(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	base := time.Unix(1_700_000_000, 0).UTC()
	// 63m: past period (60m) but inside grace (5m).
	h.engine.Tick(base.Add(63 * time.Minute))

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if v.Status != "grace" {
		t.Errorf("late -> grace mapping: got %q", v.Status)
	}
}

func TestMgmtWriteEndpointsRejectWith409(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	hdrs := map[string]string{"X-Api-Key": "rw-key"}

	cases := []struct {
		method, path, op string
	}{
		{"POST", "/api/v3/checks/", "create"},
		{"POST", "/api/v3/checks/" + c.UUID.String(), "update"},
		{"DELETE", "/api/v3/checks/" + c.UUID.String(), "delete"},
		{"POST", "/api/v3/checks/" + c.UUID.String() + "/pause", "pause"},
		{"POST", "/api/v3/checks/" + c.UUID.String() + "/resume", "resume"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			rr := h.do(tc.method, tc.path, hdrs)
			if rr.Code != http.StatusConflict {
				t.Errorf("%s %s: got %d, want 409", tc.method, tc.path, rr.Code)
			}
			var body map[string]string
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if !strings.Contains(body["error"], "config file") {
				t.Errorf("error msg should mention config file: %q", body["error"])
			}
		})
	}
}

func TestMgmtNextPingComputed(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if v.LastPing == nil || v.NextPing == nil {
		t.Fatalf("last_ping/next_ping: %v / %v", v.LastPing, v.NextPing)
	}
	last, _ := time.Parse(time.RFC3339, *v.LastPing)
	next, _ := time.Parse(time.RFC3339, *v.NextPing)
	if next.Sub(last) != time.Hour {
		t.Errorf("next - last: got %v, want 1h", next.Sub(last))
	}
}

func TestMgmtNeverPingedNoTimestamps(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	rr := h.do("GET", "/api/v3/checks/", map[string]string{"X-Api-Key": "ro-key"})
	var resp struct{ Checks []checkView }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	for _, v := range resp.Checks {
		if v.LastPing != nil || v.NextPing != nil {
			t.Errorf("%s should have nil timestamps: %+v", v.Slug, v)
		}
		if v.Status != "new" {
			t.Errorf("%s should be new: %q", v.Slug, v.Status)
		}
	}
}

func TestMgmtFlipsReturnsHCIOShape(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	// new -> up via a ping, then up -> down via /fail.
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingFail})

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String()+"/flips/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("flips: got %d body=%q", rr.Code, rr.Body.String())
	}
	var flips []flipView
	if err := json.Unmarshal(rr.Body.Bytes(), &flips); err != nil {
		t.Fatalf("decode flips: %v body=%q", err, rr.Body.String())
	}
	// Expect at least the up->down transition. The new->up flip is also
	// recorded because `new` collapses to !up on the binary axis.
	if len(flips) < 1 {
		t.Fatalf("flips count: %d, want >=1, got: %+v", len(flips), flips)
	}
	// Newest first: first entry is the up->down flip (up=0).
	if flips[0].Up != 0 {
		t.Errorf("newest flip: up=%d, want 0 (down)", flips[0].Up)
	}
	if flips[0].Timestamp == "" {
		t.Error("flip timestamp empty")
	}
}

func TestMgmtFlipsAuthAnd404(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String()+"/flips/", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", rr.Code)
	}
	rr = h.do("GET", "/api/v3/checks/bogus/flips/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("bogus id: got %d", rr.Code)
	}
}

func TestMgmtPingsReturnsHistory(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingStart, UserAgent: "curl/8", RemoteAddr: "10.0.0.1"})
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingExit, ExitCode: 7})

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String()+"/pings/", map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("pings: got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct{ Pings []pingView }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Pings) != 2 {
		t.Fatalf("pings count: got %d, want 2: %+v", len(resp.Pings), resp.Pings)
	}
	// Newest first.
	if resp.Pings[0].Type != "exitstatus" {
		t.Errorf("newest type: got %q, want exitstatus", resp.Pings[0].Type)
	}
	if resp.Pings[0].ExitStatus == nil || *resp.Pings[0].ExitStatus != 7 {
		t.Errorf("exitstatus: %v", resp.Pings[0].ExitStatus)
	}
	if resp.Pings[1].Type != "start" {
		t.Errorf("oldest type: got %q, want start", resp.Pings[1].Type)
	}
	if resp.Pings[1].UA != "curl/8" {
		t.Errorf("ua: %q", resp.Pings[1].UA)
	}
}

func TestMgmtResolveBySlug(t *testing.T) {
	// Slug lookups are a cadence extension to the HC.io URL surface so the
	// SPA can route by user-facing names rather than UUID/unique_key.
	h := newMgmtHarness(t, sampleConfig)
	rr := h.do("GET", "/api/v3/checks/api", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("by slug: got %d body=%q", rr.Code, rr.Body.String())
	}
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if v.Slug != "api" {
		t.Errorf("slug: got %q, want api", v.Slug)
	}
}

func TestMgmtGetPingReturnsDetailWithBody(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	body := []byte("captured-payload\nline two")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{
		Kind:       store.PingSuccess,
		Body:       body,
		UserAgent:  "curl/8",
		RemoteAddr: "10.0.0.1",
	})

	// Pull the ID off the list response so the test mirrors how the SPA
	// constructs the detail URL (rather than reaching into store internals).
	rr := h.do("GET", "/api/v3/checks/api/pings/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("list pings: got %d body=%q", rr.Code, rr.Body.String())
	}
	var listResp struct{ Pings []pingView }
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	if len(listResp.Pings) != 1 {
		t.Fatalf("want 1 ping, got %d", len(listResp.Pings))
	}
	pid := listResp.Pings[0].ID
	if pid == "" {
		t.Fatal("ping id missing from list response")
	}
	if !listResp.Pings[0].HasBody {
		t.Error("has_body should be true on list row when body was captured")
	}

	rr = h.do("GET", "/api/v3/checks/api/pings/"+pid, map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("get ping: got %d body=%q", rr.Code, rr.Body.String())
	}
	var detail pingDetailView
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Body != string(body) {
		t.Errorf("body: got %q, want %q", detail.Body, string(body))
	}
	if detail.RemoteAddr != "10.0.0.1" || detail.UA != "curl/8" {
		t.Errorf("remote/ua passthrough: %+v", detail)
	}
}

func TestMgmtGetPingAuthAnd404s(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	// Unauthenticated.
	rr := h.do("GET", "/api/v3/checks/api/pings/1", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", rr.Code)
	}
	// Unknown check.
	rr = h.do("GET", "/api/v3/checks/bogus/pings/1", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("bogus check: got %d", rr.Code)
	}
	// Non-numeric ping id.
	rr = h.do("GET", "/api/v3/checks/api/pings/not-a-number", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad id: got %d", rr.Code)
	}
	// Numeric but no such ping.
	rr = h.do("GET", "/api/v3/checks/api/pings/1", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("absent ping: got %d", rr.Code)
	}
}

func TestMgmtChannelsRequiresReadWrite(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)

	// Read-only key is insufficient — channel definitions can carry secrets.
	rr := h.do("GET", "/api/v3/channels/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("ro key: got %d, want 401", rr.Code)
	}

	rr = h.do("GET", "/api/v3/channels/", map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("rw key: got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct{ Channels []channelView }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Channels) != 1 || resp.Channels[0].Name != "hook" || resp.Channels[0].Kind != "webhook" {
		t.Errorf("channels: %+v", resp.Channels)
	}
}

func TestMgmtBadgesPublicAndShapesPerSlug(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)

	// Public — no API key needed.
	rr := h.do("GET", "/api/v3/badges/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("badges: got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Badges map[string]badgeURLs `json:"badges"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Badges) != 2 {
		t.Fatalf("badges count: got %d, want 2 (one per check)", len(resp.Badges))
	}
	apiBadges, ok := resp.Badges["api"]
	if !ok {
		t.Fatal("badges missing 'api' slug")
	}
	if !strings.HasPrefix(apiBadges.SVG, "https://cadence.example.com/badge/") || !strings.HasSuffix(apiBadges.SVG, ".svg") {
		t.Errorf("svg URL: %q", apiBadges.SVG)
	}
	if !strings.Contains(apiBadges.SVG3, "-3.svg") {
		t.Errorf("svg3 URL: %q", apiBadges.SVG3)
	}
	if !strings.HasSuffix(apiBadges.Shields, ".shields") {
		t.Errorf("shields URL: %q", apiBadges.Shields)
	}
}

func TestMgmtCheckViewIncludesBadgeURL(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if !strings.HasPrefix(v.BadgeURL, "https://cadence.example.com/badge/") {
		t.Errorf("badge_url: %q", v.BadgeURL)
	}
}

func TestMgmtCheckViewLastDuration(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")

	// Open a run, then close it — engine.HandlePing uses the engine's
	// fixed `now` so start.at == end.at; the duration is zero but the
	// field should still be populated (run completed).
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingStart})
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if v.LastDuration == nil {
		t.Errorf("last_duration should be set after a closed run, got nil")
	}
}

func TestMgmtCheckViewLastDurationNilWithoutRun(t *testing.T) {
	h := newMgmtHarness(t, sampleConfig)
	c := h.reg.CheckBySlug("api")
	// Plain success ping (no /start) — no completed run.
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	rr := h.do("GET", "/api/v3/checks/"+c.UUID.String(), map[string]string{"X-Api-Key": "ro-key"})
	var v checkView
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	if v.LastDuration != nil {
		t.Errorf("last_duration should be nil without a /start-closed run, got %d", *v.LastDuration)
	}
}
