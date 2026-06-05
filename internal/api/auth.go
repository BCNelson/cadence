package api

import (
	"net/http"

	"github.com/bcnelson/cadence/internal/config"
)

// KeyKind describes which api_keys list authenticated a request, so callers
// can decide whether to include read-write-only fields in responses.
type KeyKind int

const (
	KeyNone KeyKind = iota
	KeyReadOnly
	KeyReadWrite
)

// Authenticate checks a request's API key against the configured allow-lists.
// The key may arrive via the `X-Api-Key` header or — for clients that can't
// set headers, notably browser EventSource — the `api_key` URL query
// parameter. The header wins if both are present.
//
// HC.io's docs also describe an `api_key` JSON body field; we don't honor
// that form (it's awkward for GET requests and unnecessary for v1).
func Authenticate(reg *config.Registry, r *http.Request) KeyKind {
	provided := r.Header.Get("X-Api-Key")
	if provided == "" {
		provided = r.URL.Query().Get("api_key")
	}
	if provided == "" {
		return KeyNone
	}
	for _, k := range reg.Server.APIKeys.ReadWrite {
		if k == provided {
			return KeyReadWrite
		}
	}
	for _, k := range reg.Server.APIKeys.ReadOnly {
		if k == provided {
			return KeyReadOnly
		}
	}
	return KeyNone
}

// RequireKey wraps next so a missing or unknown key is rejected with 401
// before the inner handler runs. Read-only is sufficient — use this for
// endpoints (like the SSE stream) that only expose read-side data.
func RequireKey(reg *config.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Authenticate(reg, r) == KeyNone {
			writeAuthChallenge(w)
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid api key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeAuthChallenge advertises the auth scheme on 401 responses so
// standards-compliant tools (curl -i, dev-tools) can surface the
// expected credential format instead of falling back to Basic. The
// scheme name `CadenceApiKey` is non-standard but identifies the
// X-Api-Key / ?api_key= pair without misrepresenting Bearer semantics.
func writeAuthChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `CadenceApiKey realm="cadence", charset="UTF-8"`)
}
