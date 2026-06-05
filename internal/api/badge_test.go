package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
)

func TestBadgePerCheckSVG(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	uk := uniqueKey(c.UUID)
	rr := h.do("GET", "/badge/"+uk+".svg", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Errorf("content-type: %q", got)
	}
	if cc := rr.Header().Get("Cache-Control"); cc == "" {
		t.Error("Cache-Control header missing")
	}
	body := rr.Body.String()
	if !strings.Contains(body, ">up<") {
		t.Errorf("svg should include 'up' label: %s", body)
	}
	if !strings.Contains(body, "#4c1") {
		t.Errorf("svg should include up's green hex: %s", body)
	}
}

func TestBadgePerCheckJSON(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingFail})

	rr := h.do("GET", "/badge/"+uniqueKey(c.UUID)+".json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "down" {
		t.Errorf("status: got %q, want down", body["status"])
	}
}

func TestBadgeShieldsFormat(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})

	rr := h.do("GET", "/badge/"+uniqueKey(c.UUID)+".shields", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["schemaVersion"].(float64) != 1 {
		t.Errorf("schemaVersion: %v", body["schemaVersion"])
	}
	if body["label"] != "status" {
		t.Errorf("label: %v", body["label"])
	}
	if body["message"] != "up" {
		t.Errorf("message: %v", body["message"])
	}
	if body["color"] != "brightgreen" {
		t.Errorf("color: %v", body["color"])
	}
}

func TestBadge3StatePreservesLate(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	c := h.reg.CheckBySlug("api")
	// Pings, then tick to push into late (period 1h + 3m, within grace 5m).
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	// Engine's Now is fixed at 1_700_000_000; advance via Tick to push
	// the check from up into late (period 1h elapsed, still within grace 5m).
	base := time.Unix(1_700_000_000, 0).UTC()
	h.engine.Tick(base.Add(63 * time.Minute))

	// 2-state: late collapses to down.
	rr := h.do("GET", "/badge/"+uniqueKey(c.UUID)+".json", nil)
	var got map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["status"] != "down" {
		t.Errorf("2-state: got %q, want down", got["status"])
	}

	// 3-state: late shows as "late".
	rr = h.do("GET", "/badge/"+uniqueKey(c.UUID)+"-3.json", nil)
	got = nil
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["status"] != "late" {
		t.Errorf("3-state: got %q, want late", got["status"])
	}
}

func TestBadgePerTagRollup(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	// `prod` includes api(up), backup(down), db(new). Worst-wins → down.
	api := h.reg.CheckBySlug("api")
	backup := h.reg.CheckBySlug("backup")
	_ = h.engine.HandlePing(api.UUID, &engine.PingRequest{Kind: store.PingSuccess})
	_ = h.engine.HandlePing(backup.UUID, &engine.PingRequest{Kind: store.PingFail})

	rr := h.do("GET", "/badge/tag/prod.json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "down" {
		t.Errorf("prod rollup: got %q, want down", body["status"])
	}
}

func TestBadgeAllChecksStar(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	// One down anywhere → "*" is down.
	c := h.reg.CheckBySlug("api")
	_ = h.engine.HandlePing(c.UUID, &engine.PingRequest{Kind: store.PingFail})

	rr := h.do("GET", "/badge/tag/*.json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "down" {
		t.Errorf("star rollup: got %q, want down", body["status"])
	}
}

func TestBadgeUnknownTargets(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)
	cases := []string{
		"/badge/notarealkey.svg",
		"/badge/tag/nope.svg",
		"/badge/.svg",
		"/badge/foo.bogus",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rr := h.do("GET", p, nil)
			if rr.Code != http.StatusNotFound {
				t.Errorf("got %d, want 404", rr.Code)
			}
		})
	}
}

func TestListBadgesIncludesTagsMap(t *testing.T) {
	h := newMgmtHarness(t, tagsConfig)

	rr := h.do("GET", "/api/v3/badges/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d", rr.Code)
	}
	var resp struct {
		Badges map[string]badgeURLs `json:"badges"`
		Tags   map[string]badgeURLs `json:"tags"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.Tags["prod"]; !ok {
		t.Errorf("tags map missing 'prod': %v", resp.Tags)
	}
	if _, ok := resp.Tags["*"]; !ok {
		t.Errorf("tags map missing '*' all-checks rollup: %v", resp.Tags)
	}
	if !strings.Contains(resp.Tags["prod"].SVG, "/badge/tag/prod.svg") {
		t.Errorf("prod svg URL: %q", resp.Tags["prod"].SVG)
	}
}
