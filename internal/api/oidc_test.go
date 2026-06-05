package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcnelson/cadence/internal/config"
)

func TestNewOIDCVerifier(t *testing.T) {
	cases := []struct {
		name     string
		cfg      config.OIDC
		wantOK   bool
		wantTier KeyKind
		wantAud  string
	}{
		{"empty issuer disables", config.OIDC{}, false, 0, ""},
		{"defaults tier=rw, audience=client_id",
			config.OIDC{Issuer: "https://idp.example/", ClientID: "cli"},
			true, KeyReadWrite, "cli"},
		{"explicit read_only tier",
			config.OIDC{Issuer: "https://idp.example/", ClientID: "cli", Tier: "read_only"},
			true, KeyReadOnly, "cli"},
		{"audience overrides client_id",
			config.OIDC{Issuer: "https://idp.example/", ClientID: "cli", Audience: "api"},
			true, KeyReadWrite, "api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewOIDCVerifier(tc.cfg)
			if got := v.Configured(); got != tc.wantOK {
				t.Fatalf("Configured: got %v, want %v", got, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if v.tier != tc.wantTier {
				t.Errorf("tier: got %v, want %v", v.tier, tc.wantTier)
			}
			if v.audience != tc.wantAud {
				t.Errorf("audience: got %q, want %q", v.audience, tc.wantAud)
			}
			pub, ok := v.PublicConfig()
			if !ok {
				t.Fatal("PublicConfig should be ok when configured")
			}
			if pub.Issuer != tc.cfg.Issuer || pub.ClientID != tc.cfg.ClientID || pub.Audience != tc.wantAud {
				t.Errorf("PublicConfig: %+v", pub)
			}
		})
	}
}

func TestExtractBearer(t *testing.T) {
	cases := []struct {
		name   string
		header string
		query  string
		want   string
	}{
		{"none", "", "", ""},
		{"bearer header", "Bearer abc.def.ghi", "", "abc.def.ghi"},
		{"case-insensitive prefix", "bearer abc.def", "", "abc.def"},
		{"leading/trailing whitespace trimmed", "Bearer   abc  ", "", "abc"},
		{"non-bearer scheme ignored", "Basic dXNlcjpwYXNz", "", ""},
		{"access_token query fallback", "", "tok-from-query", "tok-from-query"},
		{"header beats query", "Bearer header-tok", "query-tok", "header-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := "/x"
			if tc.query != "" {
				u += "?access_token=" + tc.query
			}
			r := httptest.NewRequest(http.MethodGet, u, http.NoBody)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if got := extractBearer(r); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthChallenge(t *testing.T) {
	cases := []struct {
		name string
		ov   *OIDCVerifier
		want []string // substrings the challenge must include
	}{
		{"oidc off: API-key only", nil, []string{"CadenceApiKey"}},
		{"oidc on: bearer + API-key",
			&OIDCVerifier{cfg: config.OIDC{Issuer: "https://idp/", ClientID: "c"}},
			[]string{"Bearer", "CadenceApiKey"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeAuthChallenge(rr, tc.ov)
			got := rr.Header().Get("WWW-Authenticate")
			for _, sub := range tc.want {
				if !strings.Contains(got, sub) {
					t.Errorf("WWW-Authenticate %q missing %q", got, sub)
				}
			}
		})
	}
}

// API-key precedence over Bearer is load-bearing: HC.io-compatible automation
// must keep working unchanged when OIDC is enabled. If precedence flipped,
// this test would hit the (intentionally unreachable) bogus issuer and either
// fail or hang — which is the failure signal we want.
func TestAuthenticateAPIKeyBeatsBearer(t *testing.T) {
	reg := loadAuthRegistry(t)
	ov := &OIDCVerifier{
		cfg:      config.OIDC{Issuer: "http://127.0.0.1:1/never-contacted", ClientID: "x"},
		audience: "x",
		tier:     KeyReadWrite,
	}
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	r.Header.Set("X-Api-Key", "ro-key")
	r.Header.Set("Authorization", "Bearer bearer-token-never-verified")
	if got := Authenticate(reg, ov, r); got != KeyReadOnly {
		t.Errorf("got %v, want KeyReadOnly", got)
	}
}
