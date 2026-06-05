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
		{Pattern: "GET /api/v3/checks/{id}/flips/", Handler: h.flipsForCheck},
		{Pattern: "GET /api/v3/checks/{id}/pings/", Handler: h.pingsForCheck},
		{Pattern: "GET /api/v3/channels/", Handler: h.listChannels},
		{Pattern: "GET /api/v3/badges/", Handler: h.listBadges},

		// Writes — all return 409.
		{Pattern: "POST /api/v3/checks/", Handler: h.writeRejected("create")},
		{Pattern: "POST /api/v3/checks/{id}", Handler: h.writeRejected("update")},
		{Pattern: "DELETE /api/v3/checks/{id}", Handler: h.writeRejected("delete")},
		{Pattern: "POST /api/v3/checks/{id}/pause", Handler: h.writeRejected("pause")},
		{Pattern: "POST /api/v3/checks/{id}/resume", Handler: h.writeRejected("resume")},
	}
}

func (h *MgmtHandler) listChecks(w http.ResponseWriter, r *http.Request) {
	kind := Authenticate(h.registry, r)
	if kind == KeyNone {
		writeAuthChallenge(w)
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
	kind := Authenticate(h.registry, r)
	if kind == KeyNone {
		writeAuthChallenge(w)
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
		if Authenticate(h.registry, r) == KeyNone {
			writeAuthChallenge(w)
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
			return
		}
		writeAPIError(w, http.StatusConflict,
			fmt.Sprintf("cadence does not support %s via API — checks are declared in the YAML config file (the source of truth)", op))
	}
}

// checkView is the JSON shape returned by the read endpoints. Fields are
// modeled after HC.io v3 so existing clients/dashboards can consume them.
//
// `has_open_run` is cadence-specific (HC.io reports `status: "started"`
// instead of a separate boolean). The name is explicit about meaning
// "a /start ping opened a run that hasn't closed yet," rather than the
// ambiguous `started`.
type checkView struct {
	Name         string  `json:"name,omitempty"`
	Slug         string  `json:"slug"`
	Tags         string  `json:"tags"` // space-separated, HC.io convention
	Status       string  `json:"status"`
	HasOpenRun   bool    `json:"has_open_run"`
	LastPing     *string `json:"last_ping,omitempty"`
	NextPing     *string `json:"next_ping,omitempty"`
	LastDuration *int64  `json:"last_duration,omitempty"` // seconds of last completed run
	Grace        int64   `json:"grace"`
	Schedule     string  `json:"schedule,omitempty"`
	Timezone     string  `json:"timezone,omitempty"`
	Timeout      int64   `json:"timeout,omitempty"`
	NPings       int     `json:"n_pings"`
	BadgeURL     string  `json:"badge_url,omitempty"`

	// Read-write-only fields. Pointer types so omitempty drops them
	// cleanly on read-only responses.
	PingURL  *string `json:"ping_url,omitempty"`
	Channels *string `json:"channels,omitempty"`

	// Read-only-only field.
	UniqueKey string `json:"unique_key,omitempty"`
}

func (h *MgmtHandler) buildView(c *config.ResolvedCheck, kind KeyKind) checkView {
	snap, _ := h.engine.Snapshot(c.UUID)
	v := checkView{
		Name:       c.Name,
		Slug:       c.Slug,
		Tags:       strings.Join(c.Tags, " "),
		Status:     apiStatus(snap.Status),
		HasOpenRun: snap.Started,
		Grace:      int64(c.Grace.Seconds()),
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
		if d, ok := lastRunDuration(pings); ok {
			secs := int64(d.Seconds())
			v.LastDuration = &secs
		}
	}
	if bu := h.badgeURL(c); bu != "" {
		v.BadgeURL = bu
	}

	switch kind {
	case KeyReadWrite:
		pu := h.pingURL(c)
		ch := strings.Join(c.Channels, ",")
		v.PingURL = &pu
		v.Channels = &ch
	case KeyReadOnly:
		v.UniqueKey = uniqueKey(c.UUID)
	case KeyNone:
		// Authenticate() rejects KeyNone before we get here.
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

// flipView is one state-transition record on the wire. HC.io's flips API
// only models the binary up/down dimension, so cadence collapses its richer
// state machine to that view: any transition into `up` is up=1, anything
// else is up=0. Transitions between `late` and `down`/`up` are still
// emitted as up=0 since neither side is `up`.
type flipView struct {
	Timestamp string `json:"timestamp"`
	Up        int    `json:"up"`
}

// flipsForCheck handles GET /api/v3/checks/{id}/flips/.
func (h *MgmtHandler) flipsForCheck(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, r) == KeyNone {
		writeAuthChallenge(w)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}
	check, err := h.resolveCheck(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "check not found")
		return
	}
	events, err := h.store.RecentEvents(check.UUID, 0)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "read events")
		return
	}
	// HC.io returns a bare array (not wrapped); preserve that shape.
	out := make([]flipView, 0, len(events))
	for _, e := range events {
		fv, ok := flipFromEvent(e)
		if !ok {
			continue
		}
		out = append(out, fv)
	}
	writeJSON(w, http.StatusOK, out)
}

// flipFromEvent collapses cadence's richer state machine onto HC.io's
// binary up/down flip model. Returns ok=false for transitions that are
// purely internal noise on the wire (e.g. new -> up on first ping, or any
// transition where both endpoints are the same on the up/down axis).
func flipFromEvent(e store.Event) (flipView, bool) {
	fromUp := isUpForFlip(e.From)
	toUp := isUpForFlip(e.To)
	if fromUp == toUp {
		// No movement on the up/down axis (e.g. up -> late, late -> down
		// where the binary view is identical on both sides for some pairs).
		// Up<->down boundary crossings are the only ones HC.io clients care
		// about; skip the rest to avoid surfacing internal-only steps.
		return flipView{}, false
	}
	up := 0
	if toUp {
		up = 1
	}
	return flipView{Timestamp: e.At.UTC().Format(time.RFC3339), Up: up}, true
}

// isUpForFlip maps cadence's state vocabulary onto HC.io's binary view.
// `up` is up; `late` (a.k.a. `grace`) counts as down because the check is
// outside its expected schedule; everything else is down.
func isUpForFlip(s store.Status) bool {
	return s == store.StatusUp
}

// pingView is one row of the ping history endpoint, named to match HC.io.
type pingView struct {
	Type       string `json:"type"`
	Date       string `json:"date"`
	ExitStatus *int   `json:"exitstatus,omitempty"`
	BodySize   int    `json:"body_size,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UA         string `json:"ua,omitempty"`
}

// pingsForCheck handles GET /api/v3/checks/{id}/pings/.
func (h *MgmtHandler) pingsForCheck(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, r) == KeyNone {
		writeAuthChallenge(w)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}
	check, err := h.resolveCheck(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "check not found")
		return
	}
	pings, err := h.store.RecentPings(check.UUID, 0)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "read pings")
		return
	}
	out := make([]pingView, 0, len(pings))
	for i := range pings {
		out = append(out, pingViewFromStore(&pings[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pings": out})
}

// pingViewFromStore maps cadence's internal Ping to the HC.io-shaped wire
// row. `PingExit` serializes as "exitstatus" so clients see the same kind
// string HC.io emits for numeric-exit-code pings.
func pingViewFromStore(p *store.Ping) pingView {
	v := pingView{
		Type:       string(p.Kind),
		Date:       p.At.UTC().Format(time.RFC3339),
		BodySize:   p.BodyBytes,
		RemoteAddr: p.RemoteAddr,
		UA:         p.UserAgent,
	}
	if p.Kind == store.PingExit {
		v.Type = "exitstatus"
		code := p.ExitCode
		v.ExitStatus = &code
	}
	return v
}

// channelView is the JSON shape returned by /api/v3/channels/. Transport
// details (url, method, headers) are deliberately omitted because they
// frequently embed secrets — HC.io's own /channels/ response also omits them.
type channelView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// listChannels handles GET /api/v3/channels/. Requires a read-write key:
// channel definitions carry webhook URLs and similar; read-only viewers
// don't need to enumerate them.
func (h *MgmtHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, r) != KeyReadWrite {
		writeAuthChallenge(w)
		writeAPIError(w, http.StatusUnauthorized, "channels require a read-write X-Api-Key")
		return
	}
	out := make([]channelView, 0, len(h.registry.Channels))
	for name, c := range h.registry.Channels {
		out = append(out, channelView{ID: name, Name: name, Kind: c.Type})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// badgeURLs is the per-check bundle of badge endpoint URLs.
type badgeURLs struct {
	SVG     string `json:"svg"`
	SVG3    string `json:"svg3"`
	JSON    string `json:"json"`
	JSON3   string `json:"json3"`
	Shields string `json:"shields"`
}

// listBadges handles GET /api/v3/badges/. Public (no auth) to match HC.io's
// README-embed use case: badges are designed for public dashboards. The
// badge URLs themselves dereference to the badge-render handler, which is
// deferred to a follow-up — this endpoint emits the URLs so HC.io clients
// reading the index work today.
func (h *MgmtHandler) listBadges(w http.ResponseWriter, r *http.Request) {
	out := make(map[string]badgeURLs, len(h.registry.Checks))
	for _, c := range h.registry.Checks {
		out[c.Slug] = h.badgeURLsFor(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"badges": out})
}

// badgeURLsFor builds the five HC.io badge URLs for a check. Returns
// empty URLs when server.base_url is unset (no way to construct an
// absolute link); the keys are still present so the response shape is
// stable for clients.
func (h *MgmtHandler) badgeURLsFor(c *config.ResolvedCheck) badgeURLs {
	uk := uniqueKey(c.UUID)
	return badgeURLs{
		SVG:     h.badgeLink(uk, ".svg"),
		SVG3:    h.badgeLink(uk+"-3", ".svg"),
		JSON:    h.badgeLink(uk, ".json"),
		JSON3:   h.badgeLink(uk+"-3", ".json"),
		Shields: h.badgeLink(uk, ".shields"),
	}
}

func (h *MgmtHandler) badgeLink(stem, ext string) string {
	base := h.registry.Server.BaseURL
	path := "/badge/" + stem + ext
	if base == "" {
		return path
	}
	if parsed, err := url.Parse(base); err == nil {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + path
		return parsed.String()
	}
	return strings.TrimRight(base, "/") + path
}

// badgeURL is the canonical SVG badge URL for a check, used to populate
// the per-check `badge_url` field on checkView. Returns empty when
// server.base_url is unset — the field is then omitted entirely.
func (h *MgmtHandler) badgeURL(c *config.ResolvedCheck) string {
	if h.registry.Server.BaseURL == "" {
		return ""
	}
	return h.badgeLink(uniqueKey(c.UUID), ".svg")
}

// lastRunDuration finds the duration of the most recent completed run in
// the ping history. A "run" is a /start ping followed by a closing ping
// (success, fail, or numeric exit) with no other closing ping in between.
// Returns (0, false) when there is no closed run in the retained history.
//
// pings is newest-first (as returned by store.RecentPings).
func lastRunDuration(pings []store.Ping) (time.Duration, bool) {
	for i, end := range pings {
		if !isClosingPing(end.Kind) {
			continue
		}
		for j := i + 1; j < len(pings); j++ {
			if pings[j].Kind == store.PingStart {
				return end.At.Sub(pings[j].At), true
			}
			if isClosingPing(pings[j].Kind) {
				// Older closing ping with no start in between means the
				// start that opened *this* run rolled off the ring or
				// never existed. Stop scanning.
				break
			}
		}
		// Only consider the most recent closing ping; older ones might
		// pair with rolled-off starts and we'd over-report duration.
		return 0, false
	}
	return 0, false
}

func isClosingPing(k store.PingKind) bool {
	return k == store.PingSuccess || k == store.PingFail || k == store.PingExit
}
