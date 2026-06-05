package api

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCVerifier validates Bearer tokens against the configured IdP. It's
// lazily initialized — first verify call after process start triggers
// discovery, and a transient IdP outage isn't memoized as a permanent
// failure (a later request retries). A nil receiver means OIDC is disabled.
type OIDCVerifier struct {
	cfg      config.OIDC
	tier     KeyKind
	audience string

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier returns a verifier for the given config, or nil when
// OIDC is not configured (Issuer empty). The IdP is not contacted here.
func NewOIDCVerifier(cfg config.OIDC) *OIDCVerifier {
	if cfg.Issuer == "" {
		return nil
	}
	tier := KeyReadWrite
	if strings.EqualFold(cfg.Tier, "read_only") {
		tier = KeyReadOnly
	}
	aud := cfg.Audience
	if aud == "" {
		aud = cfg.ClientID
	}
	return &OIDCVerifier{cfg: cfg, tier: tier, audience: aud}
}

// Configured reports whether the verifier is wired up. Safe on a nil receiver.
func (v *OIDCVerifier) Configured() bool { return v != nil }

// PublicConfig returns the fields the SPA needs for auth-code + PKCE.
// All fields are non-secret in this flow (no client secret exists for a
// public client).
func (v *OIDCVerifier) PublicConfig() (config.OIDC, bool) {
	if v == nil {
		return config.OIDC{}, false
	}
	return config.OIDC{
		Issuer:   v.cfg.Issuer,
		ClientID: v.cfg.ClientID,
		Audience: v.audience,
	}, true
}

// Verify checks raw against the configured issuer + audience and returns
// the configured tier on success. Returns KeyNone on any failure (unknown
// signer, bad audience, expired token, IdP unreachable on first contact).
func (v *OIDCVerifier) Verify(ctx context.Context, raw string) KeyKind {
	if v == nil || raw == "" {
		return KeyNone
	}
	ver, err := v.ensureVerifier(ctx)
	if err != nil {
		return KeyNone
	}
	if _, err := ver.Verify(ctx, raw); err != nil {
		return KeyNone
	}
	return v.tier
}

func (v *OIDCVerifier) ensureVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	provider, err := oidc.NewProvider(ctx, v.cfg.Issuer)
	if err != nil {
		return nil, err
	}
	v.verifier = provider.Verifier(&oidc.Config{ClientID: v.audience})
	return v.verifier, nil
}

// extractBearer pulls the token out of `Authorization: Bearer ...` or, as a
// fallback for clients that can't set headers (notably browser EventSource),
// `?access_token=`.
func extractBearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			return strings.TrimSpace(h[len(prefix):])
		}
	}
	return r.URL.Query().Get("access_token")
}
