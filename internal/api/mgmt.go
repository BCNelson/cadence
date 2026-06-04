package api

import (
	"crypto/sha1" //nolint:gosec // unique_key is a non-secret display identifier, not a security primitive
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// MgmtHandler serves the v3 Management API read side. It's wire-compatible
// with the Healthchecks.io v3 list/get endpoints; every write endpoint
// returns 409 with a config-file pointer because YAML is the source of
// truth in cadence.
type MgmtHandler struct {
	registry *config.Registry
	engine   *engine.Engine
	store    *store.Store
	cronP    cron.Parser // for computing next_ping on cron-based checks
}

func NewMgmtHandler(reg *config.Registry, eng *engine.Engine, st *store.Store) *MgmtHandler {
	return &MgmtHandler{
		registry: reg,
		engine:   eng,
		store:    st,
		cronP:    cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// Routes returns the endpoints the daemon should mount. The 409-returning
// write endpoints are registered explicitly so a request hits a known
// handler rather than 404ing — the user gets a real signal that cadence
// is rejecting the operation, not that the URL is wrong.
func (h *MgmtHandler) Routes() []Route {
	return []Route{
		{Pattern: "GET /api/v3/checks/", Handler: h.listChecks},
		{Pattern: "GET /api/v3/checks/{id}", Handler: h.getCheck},

		// Writes — all return 409.
		{Pattern: "POST /api/v3/checks/", Handler: h.writeRejected("create")},
		{Pattern: "POST /api/v3/checks/{id}", Handler: h.writeRejected("update")},
		{Pattern: "DELETE /api/v3/checks/{id}", Handler: h.writeRejected("delete")},
		{Pattern: "POST /api/v3/checks/{id}/pause", Handler: h.writeRejected("pause")},
		{Pattern: "POST /api/v3/checks/{id}/resume", Handler: h.writeRejected("resume")},
	}
}

// keyKind describes which api_keys list authenticated a request, so we
// can decide whether to include the read-write-only fields in responses.
type keyKind int

const (
	keyNone keyKind = iota
	keyReadOnly
	keyReadWrite
)

func (h *MgmtHandler) authenticate(r *http.Request) keyKind {
	provided := r.Header.Get("X-Api-Key")
	if provided == "" {
		// HC.io's docs say keys can also live in a JSON `api_key` body
		// field. For v1 we only honor the header — the JSON form is
		// awkward for GET requests anyway.
		return keyNone
	}
	for _, k := range h.registry.Server.APIKeys.ReadWrite {
		if k == provided {
			return keyReadWrite
		}
	}
	for _, k := range h.registry.Server.APIKeys.ReadOnly {
		if k == provided {
			return keyReadOnly
		}
	}
	return keyNone
}

func (h *MgmtHandler) listChecks(w http.ResponseWriter, r *http.Request) {
	kind := h.authenticate(r)
	if kind == keyNone {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}

	views := make([]checkView, 0, len(h.registry.Checks))
	for _, c := range h.registry.Checks {
		views = append(views, h.buildView(c, kind))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Slug < views[j].Slug })

	writeJSON(w, http.StatusOK, map[string]any{"checks": views})
}

func (h *MgmtHandler) getCheck(w http.ResponseWriter, r *http.Request) {
	kind := h.authenticate(r)
	if kind == keyNone {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}

	id := r.PathValue("id")
	check, err := h.resolveCheck(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "check not found")
		return
	}
	writeJSON(w, http.StatusOK, h.buildView(check, kind))
}

// resolveCheck accepts either a UUID or a unique_key (sha1-truncated form).
// Slug lookups are intentionally not supported on this endpoint — HC.io
// uses UUID and unique_key, and using the slug here would let clients
// enumerate the namespace from public-facing URLs.
func (h *MgmtHandler) resolveCheck(id string) (*config.ResolvedCheck, error) {
	if u, err := uuid.Parse(id); err == nil {
		c := h.registry.CheckByUUID(u)
		if c == nil {
			return nil, errors.New("not found")
		}
		return c, nil
	}
	// unique_key form: scan and match. v1 has small check counts so this
	// is fine; if it ever grows we add a precomputed map.
	for _, c := range h.registry.Checks {
		if uniqueKey(c.UUID) == id {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

// writeRejected returns a 409 handler explaining that the operation is
// not supported because configuration is the source of truth.
func (h *MgmtHandler) writeRejected(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.authenticate(r) == keyNone {
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
			return
		}
		writeAPIError(w, http.StatusConflict,
			fmt.Sprintf("cadence does not support %s via API — checks are declared in the YAML config file (the source of truth)", op))
	}
}

// checkView is the JSON shape returned by the read endpoints. Fields are
// modeled after HC.io v3 so existing clients/dashboards can consume them.
type checkView struct {
	Name     string  `json:"name,omitempty"`
	Slug     string  `json:"slug"`
	Tags     string  `json:"tags"` // space-separated, HC.io convention
	Status   string  `json:"status"`
	Started  bool    `json:"started"`
	LastPing *string `json:"last_ping,omitempty"`
	NextPing *string `json:"next_ping,omitempty"`
	Grace    int64   `json:"grace"`
	Schedule string  `json:"schedule,omitempty"`
	Timezone string  `json:"timezone,omitempty"`
	Timeout  int64   `json:"timeout,omitempty"`
	NPings   int     `json:"n_pings"`

	// Read-write-only fields. Pointer types so omitempty drops them
	// cleanly on read-only responses.
	PingURL  *string `json:"ping_url,omitempty"`
	Channels *string `json:"channels,omitempty"`

	// Read-only-only field.
	UniqueKey string `json:"unique_key,omitempty"`
}

func (h *MgmtHandler) buildView(c *config.ResolvedCheck, kind keyKind) checkView {
	snap, _ := h.engine.Snapshot(c.UUID)
	v := checkView{
		Name:    c.Name,
		Slug:    c.Slug,
		Tags:    strings.Join(c.Tags, " "),
		Status:  apiStatus(snap.Status),
		Started: snap.Started,
		Grace:   int64(c.Grace.Seconds()),
	}
	if !snap.LastPing.IsZero() {
		s := snap.LastPing.UTC().Format(time.RFC3339)
		v.LastPing = &s
	}
	if next, ok := h.nextPing(c, snap.LastPing); ok {
		s := next.UTC().Format(time.RFC3339)
		v.NextPing = &s
	}
	if c.Cron != "" {
		v.Schedule = c.Cron
		v.Timezone = "UTC" // locked decision; see project-cadence-cron-timezone memory
	}
	if c.Period > 0 {
		v.Timeout = int64(c.Period.Seconds())
	}
	if pings, err := h.store.RecentPings(c.UUID, 0); err == nil {
		v.NPings = len(pings)
	}

	switch kind {
	case keyReadWrite:
		pu := h.pingURL(c)
		ch := strings.Join(c.Channels, ",")
		v.PingURL = &pu
		v.Channels = &ch
	case keyReadOnly:
		v.UniqueKey = uniqueKey(c.UUID)
	case keyNone:
		// authenticate() rejects keyNone before we get here
	}
	return v
}

// apiStatus maps cadence's internal status to the HC.io v3 wire vocabulary.
// The only translation is `late` -> `grace`.
func apiStatus(s store.Status) string {
	if s == store.StatusLate {
		return "grace"
	}
	return string(s)
}

// nextPing returns the next expected ping time given the check's schedule
// and the last ping. Returns false for checks that have never pinged
// (we have no anchor to compute next).
func (h *MgmtHandler) nextPing(c *config.ResolvedCheck, lastPing time.Time) (time.Time, bool) {
	if lastPing.IsZero() {
		return time.Time{}, false
	}
	if c.Cron != "" {
		sched, err := h.cronP.Parse(c.Cron)
		if err != nil {
			return time.Time{}, false
		}
		return sched.Next(lastPing), true
	}
	if c.Period > 0 {
		return lastPing.Add(c.Period), true
	}
	return time.Time{}, false
}

// pingURL builds the canonical ping URL for a check. Uses server.base_url
// if set, otherwise emits a path-only URL for clients to combine themselves.
func (h *MgmtHandler) pingURL(c *config.ResolvedCheck) string {
	path := "/ping/" + c.Slug
	base := h.registry.Server.BaseURL
	if base == "" {
		return path
	}
	if parsed, err := url.Parse(base); err == nil {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + path
		return parsed.String()
	}
	return strings.TrimRight(base, "/") + path
}

// uniqueKey is an opaque, stable, non-secret identifier for a check. We
// derive it from the UUID so it's deterministic and short; clients use it
// in dashboards/URLs where the UUID would be too long.
func uniqueKey(u uuid.UUID) string {
	sum := sha1.Sum(u[:]) //nolint:gosec // display-only identifier, not a security primitive
	return hex.EncodeToString(sum[:3])
}

// writeJSON serializes v as the response body with content-type set.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
