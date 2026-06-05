package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
)

// tagsConfig has overlapping tags across three checks so AND/OR/rollup
// behavior is all observable in one harness.
const tagsConfig = `
server:
  uuid_salt: "s"
  base_url: "https://cadence.example.com"
  api_keys:
    read_write: ["rw-key"]
    read_only: ["ro-key"]
ping_keys:
  - { name: ops, key: "k" }
checks:
  - { slug: api,    period: 1h, grace: 5m, ping_keys: [ops], tags: [web, prod] }
  - { slug: backup, period: 1h, grace: 5m, ping_keys: [ops], tags: [nightly, prod] }
  - { slug: db,     period: 1h, grace: 5m, ping_keys: [ops], tags: [db, prod] }
  - { slug: dev,    period: 1h, grace: 5m, ping_keys: [ops], tags: [web] }
`

func TestListChecksFilterByTagAND(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"no filter", "", []string{"api", "backup", "db", "dev"}},
		{"single tag", "?tag=web", []string{"api", "dev"}},
		{"two tags AND", "?tag=web&tag=prod", []string{"api"}},
		{"unknown tag", "?tag=nope", []string{}},
		{"all-prod", "?tag=prod", []string{"api", "backup", "db"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := h.do("GET", "/api/v3/checks/"+tc.query, map[string]string{"X-Api-Key": "ro-key"})
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d", rr.Code)
			}
			var resp struct{ Checks []checkView }
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(resp.Checks))
			for _, c := range resp.Checks {
				got = append(got, c.Slug)
			}
			sort.Strings(got)
			sort.Strings(tc.want)
			if !sliceEq(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListTagsRollup(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)

	// Drive `api` to up, `backup` to down (so the `prod` tag rolls up to
	// down — worst-wins). `db` and `dev` remain `new`.
	apiC := h.reg.CheckBySlug("api")
	backupC := h.reg.CheckBySlug("backup")
	_ = h.engine.HandlePing(apiC.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	_ = h.engine.HandlePing(backupC.UUID, &engine.PingRequest{Kind: store.PingFail})

	rr := h.do("GET", "/api/v3/tags/", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct{ Tags []tagSummary }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byName := map[string]tagSummary{}
	for _, ts := range resp.Tags {
		byName[ts.Name] = ts
	}
	// `prod` includes api(up), backup(down), db(new) → down.
	if got := byName["prod"].Status; got != "down" {
		t.Errorf("prod status: got %q, want down", got)
	}
	if byName["prod"].NChecks != 3 {
		t.Errorf("prod n_checks: %d, want 3", byName["prod"].NChecks)
	}
	// `nightly` is just backup(down) → down.
	if got := byName["nightly"].Status; got != "down" {
		t.Errorf("nightly status: got %q, want down", got)
	}
	// `web` is api(up) + dev(new) → new (new outranks up in worst-wins).
	if got := byName["web"].Status; got != "new" {
		t.Errorf("web status: got %q, want new", got)
	}
	// Tag list is sorted by name.
	got := make([]string, len(resp.Tags))
	for i, ts := range resp.Tags {
		got[i] = ts.Name
	}
	want := []string{"db", "nightly", "prod", "web"}
	if !sliceEq(got, want) {
		t.Errorf("tag order: got %v, want %v", got, want)
	}
}

func TestListTagsPausedExcluded(t *testing.T) {
	// Two checks share a tag. One is paused (declared via enabled: false,
	// which the engine reports as paused). The rollup should ignore it.
	cfg := `
server: { uuid_salt: "s", api_keys: { read_only: ["k"] } }
ping_keys: [{ name: ops, key: "x" }]
checks:
  - { slug: live,  period: 1h, grace: 5m, ping_keys: [ops], tags: [team] }
  - { slug: muted, period: 1h, grace: 5m, ping_keys: [ops], tags: [team], enabled: false }
`
	h := newMgmtHarness(t, cfg)
	live := h.reg.CheckBySlug("live")
	_ = h.engine.HandlePing(live.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	rr := h.do("GET", "/api/v3/tags/team", map[string]string{"X-Api-Key": "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var d tagDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "up" {
		t.Errorf("status: got %q, want up (paused member must be excluded)", d.Status)
	}
}

func TestListTagsAllPausedReportsPaused(t *testing.T) {
	cfg := `
server: { uuid_salt: "s", api_keys: { read_only: ["k"] } }
ping_keys: [{ name: ops, key: "x" }]
checks:
  - { slug: a, period: 1h, grace: 5m, ping_keys: [ops], tags: [t], enabled: false }
  - { slug: b, period: 1h, grace: 5m, ping_keys: [ops], tags: [t], enabled: false }
`
	h := newMgmtHarness(t, cfg)
	rr := h.do("GET", "/api/v3/tags/t", map[string]string{"X-Api-Key": "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var d tagDetail
	_ = json.Unmarshal(rr.Body.Bytes(), &d)
	if d.Status != "paused" {
		t.Errorf("status: got %q, want paused", d.Status)
	}
}

func TestGetTagDetailHasFullViews(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	rr := h.do("GET", "/api/v3/tags/web", map[string]string{"X-Api-Key": "rw-key"})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
	}
	var d tagDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Name != "web" {
		t.Errorf("name: %q", d.Name)
	}
	if len(d.Checks) != 2 {
		t.Fatalf("checks: got %d, want 2", len(d.Checks))
	}
	// rw key surfaces ping_url; verify the detail uses the same builder.
	for _, c := range d.Checks {
		if c.PingURL == nil {
			t.Errorf("%s: ping_url missing (rw key should expose it)", c.Slug)
		}
	}
}

func TestGetTagNotFound(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	rr := h.do("GET", "/api/v3/tags/nonexistent", map[string]string{"X-Api-Key": "ro-key"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

func TestTagsEndpointsRequireAuth(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	for _, p := range []string{"/api/v3/tags/", "/api/v3/tags/web"} {
		rr := h.do("GET", p, nil)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without key: got %d, want 401", p, rr.Code)
		}
	}
}

func TestRollupStatusEmpty(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	if got := h.mgmtHandlerForTest().rollupStatus(nil); got != "" {
		t.Errorf("empty input: got %q, want \"\"", got)
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mgmtHandlerForTest returns the MgmtHandler bound to this harness, for
// unit tests that need to drive package-private helpers (e.g. rollupStatus).
func (h *mgmtHarness) mgmtHandlerForTest() *MgmtHandler {
	return NewMgmtHandler(h.reg, h.engine, h.store, nil)
}
