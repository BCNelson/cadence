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
	"strconv"
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
	oidc     *OIDCVerifier // nil when OIDC is not configured
	cronP    cron.Parser   // for computing next_ping on cron-based checks
}

func NewMgmtHandler(reg *config.Registry, eng *engine.Engine, st *store.Store, ov *OIDCVerifier) *MgmtHandler {
	return &MgmtHandler{
		registry: reg,
		engine:   eng,
		store:    st,
		oidc:     ov,
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
		{Pattern: "GET /api/v3/checks/{id}/pings/{ping_id}", Handler: h.getPing},
		{Pattern: "GET /api/v3/channels/", Handler: h.listChannels},
		{Pattern: "GET /api/v3/badges/", Handler: h.listBadges},
		{Pattern: "GET /api/v3/tags/", Handler: h.listTags},
		{Pattern: "GET /api/v3/tags/{tag}", Handler: h.getTag},
		// /api/v3/auth/config is public: the SPA reads it pre-login to
		// decide whether to drive OIDC or fall back to the API-key gate.
		{Pattern: "GET /api/v3/auth/config", Handler: h.authConfig},
		// Badge render handler. Public (README-embed use case). The path
		// segment after /badge/ encodes the target (check unique_key or
		// "tag/<name>") and the .ext extension picks the format.
		{Pattern: "GET /badge/", Handler: h.handleBadge},

		// Writes — all return 409.
		{Pattern: "POST /api/v3/checks/", Handler: h.writeRejected("create")},
		{Pattern: "POST /api/v3/checks/{id}", Handler: h.writeRejected("update")},
		{Pattern: "DELETE /api/v3/checks/{id}", Handler: h.writeRejected("delete")},
		{Pattern: "POST /api/v3/checks/{id}/pause", Handler: h.writeRejected("pause")},
		{Pattern: "POST /api/v3/checks/{id}/resume", Handler: h.writeRejected("resume")},
	}
}

func (h *MgmtHandler) listChecks(w http.ResponseWriter, r *http.Request) {
	kind := Authenticate(h.registry, h.oidc, r)
	if kind == KeyNone {
		writeAuthChallenge(w, h.oidc)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}

	// Repeated ?tag= filters narrow the result with AND semantics — matches
	// HC.io's behavior so existing clients work unchanged.
	wanted := r.URL.Query()["tag"]
	views := make([]checkView, 0, len(h.registry.Checks))
	for _, c := range h.registry.Checks {
		if !hasAllTags(c, wanted) {
			continue
		}
		views = append(views, h.buildView(c, kind))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Slug < views[j].Slug })

	writeJSON(w, http.StatusOK, map[string]any{"checks": views})
}

func (h *MgmtHandler) getCheck(w http.ResponseWriter, r *http.Request) {
	kind := Authenticate(h.registry, h.oidc, r)
	if kind == KeyNone {
		writeAuthChallenge(w, h.oidc)
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

// resolveCheck accepts a UUID, a unique_key (sha1-truncated form), or a
// slug. UUID and unique_key are the HC.io-compatible identifiers; slug is
// a cadence extension so the SPA can build user-friendly URLs like
// /checks/<slug>. All three forms are gated by the same authentication, so
// adding slug doesn't widen access — the request still needs a valid key.
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
	if c := h.registry.CheckBySlug(id); c != nil {
		return c, nil
	}
	return nil, errors.New("not found")
}

// writeRejected returns a 409 handler explaining that the operation is
// not supported because configuration is the source of truth.
func (h *MgmtHandler) writeRejected(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if Authenticate(h.registry, h.oidc, r) == KeyNone {
			writeAuthChallenge(w, h.oidc)
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
	if Authenticate(h.registry, h.oidc, r) == KeyNone {
		writeAuthChallenge(w, h.oidc)
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
// `id` is a cadence extension — the unix-nanosecond timestamp as a string,
// used as the stable URL identifier for the per-ping detail endpoint. It
// matches the storage key, so a successful lookup is a direct store hit
// rather than a linear scan.
type pingView struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Date       string `json:"date"`
	ExitStatus *int   `json:"exitstatus,omitempty"`
	BodySize   int    `json:"body_size,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	HasBody    bool   `json:"has_body,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UA         string `json:"ua,omitempty"`
}

// pingDetailView extends pingView with the captured body for the
// per-ping detail endpoint. Body is returned as a UTF-8 string; the
// content-type isn't tracked at capture time so the SPA renders it as
// preformatted text.
type pingDetailView struct {
	pingView
	Body string `json:"body,omitempty"`
}

// pingsForCheck handles GET /api/v3/checks/{id}/pings/.
func (h *MgmtHandler) pingsForCheck(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, h.oidc, r) == KeyNone {
		writeAuthChallenge(w, h.oidc)
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
		ID:         strconv.FormatInt(p.At.UnixNano(), 10),
		Type:       string(p.Kind),
		Date:       p.At.UTC().Format(time.RFC3339),
		BodySize:   p.BodyBytes,
		Truncated:  p.Truncated,
		HasBody:    p.HasBody,
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

// getPing handles GET /api/v3/checks/{id}/pings/{ping_id}. {ping_id} is
// the unix-nanosecond timestamp returned in the list response. Returns
// the full ping record plus its captured body (when one was stored).
func (h *MgmtHandler) getPing(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, h.oidc, r) == KeyNone {
		writeAuthChallenge(w, h.oidc)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}
	check, err := h.resolveCheck(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "check not found")
		return
	}
	pingID := r.PathValue("ping_id")
	nanos, err := strconv.ParseInt(pingID, 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "ping id must be a unix-nanosecond integer")
		return
	}
	// Linear scan is fine — retained pings per check are capped (default
	// 1000). Avoids exposing the LevelDB key layout through a direct
	// fetch helper that doesn't exist on store today.
	pings, err := h.store.RecentPings(check.UUID, 0)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "read pings")
		return
	}
	var match *store.Ping
	for i := range pings {
		if pings[i].At.UnixNano() == nanos {
			match = &pings[i]
			break
		}
	}
	if match == nil {
		writeAPIError(w, http.StatusNotFound, "ping not found")
		return
	}
	detail := pingDetailView{pingView: pingViewFromStore(match)}
	if match.HasBody {
		if log, err := h.store.FetchLog(check.UUID, match.At); err == nil {
			detail.Body = string(log.Body)
			// Trust the freshly-fetched log over the cached flag — the
			// body's truncation state is authoritative there.
			detail.Truncated = log.Truncated
		}
	}
	writeJSON(w, http.StatusOK, detail)
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
	if Authenticate(h.registry, h.oidc, r) != KeyReadWrite {
		writeAuthChallenge(w, h.oidc)
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

// authConfig handles GET /api/v3/auth/config. Public (no auth) — the SPA
// reads this once at boot to decide whether to render the API-key gate or
// drive an OIDC sign-in. All exposed fields are non-secret (the OIDC flow
// is auth-code + PKCE with a public client).
func (h *MgmtHandler) authConfig(w http.ResponseWriter, _ *http.Request) {
	cfg, ok := h.oidc.PublicConfig()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"oidc": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"oidc": map[string]string{
			"issuer":    cfg.Issuer,
			"client_id": cfg.ClientID,
			"audience":  cfg.Audience,
		},
	})
}

// listBadges handles GET /api/v3/badges/. Public (no auth) to match HC.io's
// README-embed use case: badges are designed for public dashboards. The
// response carries two maps: per-check (keyed by slug) and per-tag (keyed
// by tag name, with "*" as the all-checks rollup). Both point at the
// /badge/ render handler.
func (h *MgmtHandler) listBadges(w http.ResponseWriter, _ *http.Request) {
	bySlug := make(map[string]badgeURLs, len(h.registry.Checks))
	for _, c := range h.registry.Checks {
		bySlug[c.Slug] = h.badgeURLsFor(c)
	}
	byTag := make(map[string]badgeURLs)
	for tag := range h.tagBuckets() {
		byTag[tag] = h.tagBadgeURLs(tag)
	}
	byTag["*"] = h.tagBadgeURLs("*")
	writeJSON(w, http.StatusOK, map[string]any{
		"badges": bySlug,
		"tags":   byTag,
	})
}

// tagBadgeURLs is the per-tag analogue of badgeURLsFor. Stem is
// "tag/<name>"; HC.io uses a project-scoped path which cadence doesn't
// have, so we namespace tags under /badge/tag/ to keep per-check stems
// (unique-key hex) and per-tag stems unambiguous.
func (h *MgmtHandler) tagBadgeURLs(tag string) badgeURLs {
	stem := "tag/" + tag
	return badgeURLs{
		SVG:     h.badgeLink(stem, ".svg"),
		SVG3:    h.badgeLink(stem+"-3", ".svg"),
		JSON:    h.badgeLink(stem, ".json"),
		JSON3:   h.badgeLink(stem+"-3", ".json"),
		Shields: h.badgeLink(stem, ".shields"),
	}
}

// tagSummary is one entry of the /api/v3/tags/ index. Members are listed
// as bare slugs to keep the index payload small; clients wanting full
// check views per tag use /api/v3/tags/{tag} or /api/v3/checks/?tag=NAME.
type tagSummary struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	NChecks int      `json:"n_checks"`
	Checks  []string `json:"checks"`
}

// listTags handles GET /api/v3/tags/. Returns every tag present on any
// check, with its rolled-up status and member slugs. Sorted by tag name.
func (h *MgmtHandler) listTags(w http.ResponseWriter, r *http.Request) {
	if Authenticate(h.registry, h.oidc, r) == KeyNone {
		writeAuthChallenge(w, h.oidc)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}
	buckets := h.tagBuckets()
	out := make([]tagSummary, 0, len(buckets))
	for name, checks := range buckets {
		slugs := make([]string, 0, len(checks))
		for _, c := range checks {
			slugs = append(slugs, c.Slug)
		}
		sort.Strings(slugs)
		out = append(out, tagSummary{
			Name:    name,
			Status:  apiStatus(h.rollupStatus(checks)),
			NChecks: len(checks),
			Checks:  slugs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}

// tagDetail is the /api/v3/tags/{tag} response shape. Embeds full check
// views (same shape as /api/v3/checks/) so a single fetch is enough to
// render a tag-scoped dashboard.
type tagDetail struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Checks []checkView `json:"checks"`
}

// getTag handles GET /api/v3/tags/{tag}. 404 when the tag has no members.
func (h *MgmtHandler) getTag(w http.ResponseWriter, r *http.Request) {
	kind := Authenticate(h.registry, h.oidc, r)
	if kind == KeyNone {
		writeAuthChallenge(w, h.oidc)
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid X-Api-Key")
		return
	}
	name := r.PathValue("tag")
	members := h.tagBuckets()[name]
	if len(members) == 0 {
		writeAPIError(w, http.StatusNotFound, "tag not found")
		return
	}
	views := make([]checkView, 0, len(members))
	for _, c := range members {
		views = append(views, h.buildView(c, kind))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Slug < views[j].Slug })
	writeJSON(w, http.StatusOK, tagDetail{
		Name:   name,
		Status: apiStatus(h.rollupStatus(members)),
		Checks: views,
	})
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
