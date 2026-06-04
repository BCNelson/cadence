// Package api wires the HTTP surfaces: the inbound Ping API (services
// pinging cadence) and the Management API v3 read side (dashboards and
// external clients listing/inspecting checks). Both are HC.io
// wire-compatible by design.
package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// PingHandler serves the /ping/ family of endpoints. It's wire-compatible
// with Healthchecks.io's ping API and additionally enforces cadence's
// ping_keys allow-list on slug-form requests.
type PingHandler struct {
	registry *config.Registry
	engine   *engine.Engine
	store    *store.Store
}

// NewPingHandler builds a handler that the daemon mounts under /ping/.
func NewPingHandler(reg *config.Registry, eng *engine.Engine, st *store.Store) *PingHandler {
	return &PingHandler{registry: reg, engine: eng, store: st}
}

// Routes returns the (pattern, handler) pairs the daemon should register.
// Using a slice rather than calling http.HandleFunc directly so the test
// suite (and the main wiring) can install them against any mux.
func (h *PingHandler) Routes() []Route {
	// In Go 1.22+ ServeMux, more specific patterns win, so the literal
	// /start, /fail, /log routes take precedence over /{action} which
	// catches the numeric exit-code suffix.
	return []Route{
		{Pattern: "/ping/{id}", Handler: h.handle(actionAuto)},
		{Pattern: "/ping/{id}/start", Handler: h.handle(actionStart)},
		{Pattern: "/ping/{id}/fail", Handler: h.handle(actionFail)},
		{Pattern: "/ping/{id}/log", Handler: h.handle(actionLog)},
		{Pattern: "/ping/{id}/{action}", Handler: h.handle(actionExit)},
	}
}

// Route is a (pattern, handler) pair the daemon mounts on its mux. The
// pattern is the Go 1.22+ ServeMux form.
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// action enumerates the URL-form action a request maps to before exit-code
// parsing. actionAuto is the bare /ping/{id} — success. actionExit is
// the catch-all numeric suffix.
type action int

const (
	actionAuto action = iota
	actionStart
	actionFail
	actionLog
	actionExit
)

func (h *PingHandler) handle(act action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		id := r.PathValue("id")
		check, err := h.authorize(id, r)
		if err != nil {
			// Per the spec: wrong key / unknown check both return 404 to
			// keep the namespace non-enumerable. No body, no key hints.
			http.NotFound(w, r)
			return
		}

		req, ok := h.buildRequest(act, r)
		if !ok {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Advertise the body cap so clients know how many bytes will be
		// captured per request. Set before WriteHeader so it lands on
		// the response.
		w.Header().Set("Ping-Body-Limit", strconv.Itoa(h.store.MaxBodyBytes()))

		if err := h.engine.HandlePing(check.UUID, req); err != nil {
			if errors.Is(err, engine.ErrUnknownCheck) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// authorize resolves the id (slug or UUID) to a check and enforces the
// auth rules. Both failure modes return error so the caller can blanket
// 404 without distinguishing.
//
// Rules (from the spec):
//   - Slug form: the check's ping_keys allow-list is enforced. Header
//     `X-Ping-Key` is checked first, then `?ping_key=` query.
//     A wrong/unknown key 404s.
//   - UUID form: only authorized for checks that are explicitly open
//     (ping_keys: []) OR pin a uuid: secret in config. An open check
//     accepts any UUID-form ping; a pinned check requires the URL UUID
//     to match the configured one exactly.
func (h *PingHandler) authorize(id string, r *http.Request) (*config.ResolvedCheck, error) {
	if u, err := uuid.Parse(id); err == nil {
		check := h.registry.CheckByUUID(u)
		if check == nil {
			return nil, errUnauthorized
		}
		if check.PinnedUUID {
			// Pinned uuid: the URL matches the pinned secret. Allowed
			// regardless of ping_keys (the pinned uuid itself is the auth).
			return check, nil
		}
		// Derived uuid: only valid for an explicitly open check. Otherwise
		// the uuid is just a stable identifier, not a credential.
		if len(check.PingKeys) == 0 {
			return check, nil
		}
		return nil, errUnauthorized
	}

	// Slug form.
	check := h.registry.CheckBySlug(id)
	if check == nil {
		return nil, errUnauthorized
	}
	if len(check.PingKeys) == 0 {
		// Open check. The spec says open checks are UUID-only — the slug
		// form is rejected so the slug can't accidentally become a secret.
		return nil, errUnauthorized
	}

	provided := r.Header.Get("X-Ping-Key")
	if provided == "" {
		provided = r.URL.Query().Get("ping_key")
	}
	if provided == "" {
		return nil, errUnauthorized
	}
	for _, name := range check.PingKeys {
		if secret, ok := h.registry.PingKeys[name]; ok && secret == provided {
			return check, nil
		}
	}
	return nil, errUnauthorized
}

var errUnauthorized = errors.New("api: unauthorized")

// buildRequest translates the parsed (action, URL) into a PingRequest. The
// only branch that can fail is actionExit, where the URL segment must be
// a non-negative integer.
func (h *PingHandler) buildRequest(act action, r *http.Request) (*engine.PingRequest, bool) {
	req := &engine.PingRequest{
		RemoteAddr: clientIP(r),
		UserAgent:  r.UserAgent(),
	}

	switch act {
	case actionAuto:
		req.Kind = store.PingSuccess
	case actionStart:
		req.Kind = store.PingStart
	case actionFail:
		req.Kind = store.PingFail
	case actionLog:
		req.Kind = store.PingLog
	case actionExit:
		exit := r.PathValue("action")
		code, err := strconv.Atoi(exit)
		if err != nil || code < 0 {
			return nil, false
		}
		req.Kind = store.PingExit
		req.ExitCode = code
	}

	// Capture the body up to one byte past the cap. The store truncates
	// to MaxBodyBytes and records the Truncated flag, so reading slightly
	// over the cap is what lets it set the flag correctly. The buffer
	// stays small either way.
	if r.Body != nil {
		limit := int64(h.store.MaxBodyBytes()) + 1
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
		if err == nil && len(body) > 0 {
			req.Body = body
		}
	}
	return req, true
}

// clientIP extracts a best-effort source identifier for the dashboard.
// Behind a reverse proxy this is the proxy IP; the user should configure
// trusted proxies if they care about the original client. v1 just stores
// whatever RemoteAddr arrives.
func clientIP(r *http.Request) string {
	// X-Forwarded-For is the conventional proxy header; honor it if
	// present. We take the leftmost entry which is the original client.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	addr := r.RemoteAddr
	if colon := strings.LastIndexByte(addr, ':'); colon >= 0 {
		return addr[:colon]
	}
	return addr
}
