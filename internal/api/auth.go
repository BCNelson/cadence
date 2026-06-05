package api

import (
	"net/http"

	"github.com/bcnelson/cadence/internal/config"
)

// KeyKind describes which credential authenticated a request, so callers
// can decide whether to include read-write-only fields in responses.
type KeyKind int

const (
	KeyNone KeyKind = iota
	KeyReadOnly
	KeyReadWrite
)

// Authenticate checks a request against the configured credentials.
//
// Precedence (first match wins):
//  1. `X-Api-Key` header (configured api_keys.{read_write,read_only}).
//  2. `?api_key=` URL query (browser EventSource fallback).
//  3. `Authorization: Bearer <token>` (OIDC, if configured).
//  4. `?access_token=<token>` URL query (EventSource fallback for OIDC).
//
// HC.io's docs also describe an `api_key` JSON body field; we don't honor
// that form (it's awkward for GET requests and unnecessary for v1).
func Authenticate(reg *config.Registry, ov *OIDCVerifier, r *http.Request) KeyKind {
	provided := r.Header.Get("X-Api-Key")
	if provided == "" {
		provided = r.URL.Query().Get("api_key")
	}
	if provided != "" {
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
	}
	if ov.Configured() {
		if tok := extractBearer(r); tok != "" {
			return ov.Verify(r.Context(), tok)
		}
	}
	return KeyNone
}

// RequireKey wraps next so a missing or unknown credential is rejected
// with 401 before the inner handler runs. Read-only is sufficient — use
// this for endpoints (like the SSE stream) that only expose read-side data.
func RequireKey(reg *config.Registry, ov *OIDCVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Authenticate(reg, ov, r) == KeyNone {
			writeAuthChallenge(w, ov)
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeAuthChallenge advertises the supported auth schemes on 401
// responses. Bearer is included when OIDC is configured so dev-tools and
// curl -i surface the right scheme; CadenceApiKey is always advertised
// because the API-key path is always supported.
func writeAuthChallenge(w http.ResponseWriter, ov *OIDCVerifier) {
	if ov.Configured() {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cadence", CadenceApiKey realm="cadence", charset="UTF-8"`)
		return
	}
	w.Header().Set("WWW-Authenticate", `CadenceApiKey realm="cadence", charset="UTF-8"`)
}
