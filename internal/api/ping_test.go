package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
)

// pingHarness wires the engine + store + handler for a single test against
// a small inline YAML, plus exposes a callable mux.
type pingHarness struct {
	reg    *config.Registry
	store  *store.Store
	engine *engine.Engine
	mux    *http.ServeMux
}

func newPingHarness(t *testing.T, yaml string) *pingHarness {
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
	h := NewPingHandler(reg, eng, st)
	mux := http.NewServeMux()
	for _, r := range h.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}
	return &pingHarness{reg: reg, store: st, engine: eng, mux: mux}
}

func (h *pingHarness) do(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.mux.ServeHTTP(rr, req)
	return rr
}

func TestPingSlugSuccessRequiresKey(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "ops-secret" }
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	// Missing key -> 404.
	if rr := h.do("GET", "/ping/api", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("no key: got %d, want 404", rr.Code)
	}
	// Wrong key -> 404.
	if rr := h.do("GET", "/ping/api", "", map[string]string{"X-Ping-Key": "wrong"}); rr.Code != http.StatusNotFound {
		t.Errorf("wrong key: got %d, want 404", rr.Code)
	}
	// Correct key -> 200, body limit advertised.
	rr := h.do("GET", "/ping/api", "", map[string]string{"X-Ping-Key": "ops-secret"})
	if rr.Code != http.StatusOK {
		t.Fatalf("correct key: got %d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Ping-Body-Limit"); got != strconv.Itoa(store.DefaultMaxBodyBytes) {
		t.Errorf("Ping-Body-Limit: got %q", got)
	}
	// Engine should now be up.
	c := h.reg.CheckBySlug("api")
	if snap, _ := h.engine.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("check status after ping: %q", snap.Status)
	}
}

func TestPingQueryKeyFallback(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "ops-secret" }
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	rr := h.do("GET", "/ping/api?ping_key=ops-secret", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("query key: got %d", rr.Code)
	}
}

func TestPingUUIDFormOpenCheck(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: open, period: 1h, ping_keys: [] }
`)
	c := h.reg.CheckBySlug("open")
	// UUID-form pinging an open check is allowed.
	if rr := h.do("GET", "/ping/"+c.UUID.String(), "", nil); rr.Code != http.StatusOK {
		t.Errorf("open uuid: got %d", rr.Code)
	}
	// Slug-form pinging an open check is rejected — spec rule.
	if rr := h.do("GET", "/ping/open", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("slug-form on open check: got %d, want 404", rr.Code)
	}
}

func TestPingUUIDFormClosedCheckRejected(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "ops-secret" }
checks:
  - { slug: closed, period: 1h, ping_keys: [ops] }
`)
	c := h.reg.CheckBySlug("closed")
	// UUID-form on a check with a ping_keys allow-list is not a bypass.
	if rr := h.do("GET", "/ping/"+c.UUID.String(), "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("uuid on closed check: got %d, want 404", rr.Code)
	}
}

func TestPingUUIDFormPinnedSecret(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "ops-secret" }
checks:
  - { slug: pinned, period: 1h, ping_keys: [ops], uuid: "11111111-2222-3333-4444-555555555555" }
`)
	// Pinned uuid authorizes regardless of ping_keys.
	if rr := h.do("GET", "/ping/11111111-2222-3333-4444-555555555555", "", nil); rr.Code != http.StatusOK {
		t.Errorf("pinned uuid: got %d", rr.Code)
	}
}

func TestPingStartActionsAndExitCode(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "k" }
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	hdrs := map[string]string{"X-Ping-Key": "k"}
	c := h.reg.CheckBySlug("api")

	if rr := h.do("POST", "/ping/api/start", "", hdrs); rr.Code != http.StatusOK {
		t.Errorf("/start: got %d", rr.Code)
	}
	snap, _ := h.engine.Snapshot(c.UUID)
	if !snap.Started {
		t.Error("/start did not open a run")
	}

	if rr := h.do("POST", "/ping/api/0", "", hdrs); rr.Code != http.StatusOK {
		t.Errorf("/0: got %d", rr.Code)
	}
	snap, _ = h.engine.Snapshot(c.UUID)
	if snap.Status != store.StatusUp {
		t.Errorf("exit 0: got %q", snap.Status)
	}

	if rr := h.do("POST", "/ping/api/17", "", hdrs); rr.Code != http.StatusOK {
		t.Errorf("/17: got %d", rr.Code)
	}
	snap, _ = h.engine.Snapshot(c.UUID)
	if snap.Status != store.StatusDown {
		t.Errorf("exit 17: got %q", snap.Status)
	}

	if rr := h.do("POST", "/ping/api/fail", "", hdrs); rr.Code != http.StatusOK {
		t.Errorf("/fail: got %d", rr.Code)
	}
	// Already down — should stay down.
	snap, _ = h.engine.Snapshot(c.UUID)
	if snap.Status != store.StatusDown {
		t.Errorf("after /fail: got %q", snap.Status)
	}
}

func TestPingBodyCapturedAndCappedAdvertised(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys:
  - { name: ops, key: "k" }
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	body := "stdout from cron job"
	rr := h.do("POST", "/ping/api/log", body, map[string]string{"X-Ping-Key": "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("/log: got %d", rr.Code)
	}
	limit, _ := strconv.Atoi(rr.Header().Get("Ping-Body-Limit"))
	if limit != store.DefaultMaxBodyBytes {
		t.Errorf("Ping-Body-Limit: got %d", limit)
	}
	c := h.reg.CheckBySlug("api")
	pings, _ := h.store.RecentPings(c.UUID, 0)
	if len(pings) != 1 || !pings[0].HasBody || pings[0].BodyBytes != len(body) {
		t.Fatalf("body not captured correctly: %+v", pings)
	}
	le, err := h.store.FetchLog(c.UUID, pings[0].At)
	if err != nil {
		t.Fatal(err)
	}
	if string(le.Body) != body {
		t.Errorf("body content: got %q", string(le.Body))
	}
}

func TestPingNonGetPost(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys: [{name: ops, key: "k"}]
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	rr := h.do("DELETE", "/ping/api", "", map[string]string{"X-Ping-Key": "k"})
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: got %d, want 405", rr.Code)
	}
}

func TestPingResponseBody(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys: [{name: ops, key: "k"}]
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	rr := h.do("GET", "/ping/api", "", map[string]string{"X-Ping-Key": "k"})
	got, _ := io.ReadAll(rr.Body)
	if string(got) != "OK" {
		t.Errorf("body: got %q", string(got))
	}
}

func TestPingBadExitCode(t *testing.T) {
	h := newPingHarness(t, `
server: { uuid_salt: "s" }
ping_keys: [{name: ops, key: "k"}]
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`)
	// Exit code segment isn't a number and isn't a literal action.
	rr := h.do("POST", "/ping/api/banana", "", map[string]string{"X-Ping-Key": "k"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("/banana: got %d, want 400", rr.Code)
	}
}
