package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/alert"
	"github.com/bcnelson/cadence/internal/api"
	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/sse"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// mockOAuthImage is a purpose-built test IdP: its issuer URL is derived
// from the Host header, so we don't need to know the mapped port at config
// time. Pinning the tag keeps CI deterministic.
const mockOAuthImage = "ghcr.io/navikt/mock-oauth2-server:2.1.10"

// startMockOIDC launches the IdP container and returns the issuer URL
// reachable from the host. Skips the test if Docker is unavailable so the
// suite stays runnable in environments without a daemon.
func startMockOIDC(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        mockOAuthImage,
		ExposedPorts: []string{"8080/tcp"},
		WaitingFor: wait.ForHTTP("/default/.well-known/openid-configuration").
			WithPort("8080/tcp").
			WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("docker unavailable, skipping OIDC e2e: %v", err)
	}
	t.Cleanup(func() {
		tctx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer c2()
		_ = c.Terminate(tctx)
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "8080/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s/default", host, port.Port())
}

// mintIDToken asks mock-oauth2-server for an id_token via the password
// grant. The token's `iss` claim matches the issuer URL, and `aud`
// defaults to the client_id we send — the same client_id cadence is
// configured to accept.
func mintIDToken(t *testing.T, issuer string) string {
	t.Helper()
	form := url.Values{
		"grant_type":    {"password"},
		"username":      {"e2e-user"},
		"password":      {"unused"},
		"client_id":     {"test-client"},
		"client_secret": {"test-secret"},
		"scope":         {"openid"},
	}
	resp, err := http.PostForm(issuer+"/token", form)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token endpoint %d: %s", resp.StatusCode, body)
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		t.Fatalf("decode token response: %v\n---\n%s", err, body)
	}
	if tr.IDToken == "" {
		t.Fatalf("no id_token in response: %s", body)
	}
	return tr.IDToken
}

// oidcE2EConfigTemplate is the minimum config that turns on OIDC and still
// passes registry resolution. Keeping `api_keys.read_write` populated lets
// us assert coexistence in the same harness.
const oidcE2EConfigTemplate = `
server:
  uuid_salt: "e2e-oidc-salt"
  api_keys:
    read_write: ["rw-key"]
  oidc:
    issuer: %q
    client_id: test-client
    tier: read_write
checks:
  - { slug: api, period: 10m }
`

func setupOIDCE2E(t *testing.T, issuer string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(oidcE2EConfigTemplate, issuer)), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{cfgPath}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "store"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	bus := sse.NewBus()
	alerter := alert.New(reg.Channels, alert.Options{})
	eng, err := engine.New(reg, st, engine.Options{Bus: bus, Alerter: alerter})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	ov := api.NewOIDCVerifier(reg.Server.OIDC)
	mux := http.NewServeMux()
	registerRoutes(mux, reg, eng, st, bus, ov)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestE2EOIDCAuth(t *testing.T) {
	issuer := startMockOIDC(t)
	idToken := mintIDToken(t, issuer)
	serverURL := setupOIDCE2E(t, issuer)

	t.Run("bearer header authenticates", func(t *testing.T) {
		res := mustDo(t, http.MethodGet, serverURL+"/api/v3/checks/",
			http.Header{"Authorization": []string{"Bearer " + idToken}}, "")
		if res.code != http.StatusOK {
			t.Fatalf("got %d, want 200 (body=%q)", res.code, res.body)
		}
	})

	t.Run("access_token query authenticates (SSE fallback)", func(t *testing.T) {
		res := mustDo(t, http.MethodGet,
			serverURL+"/api/v3/checks/?access_token="+url.QueryEscape(idToken), nil, "")
		if res.code != http.StatusOK {
			t.Fatalf("got %d, want 200 (body=%q)", res.code, res.body)
		}
	})

	t.Run("invalid bearer is 401 with multi-scheme challenge", func(t *testing.T) {
		res := mustDo(t, http.MethodGet, serverURL+"/api/v3/checks/",
			http.Header{"Authorization": []string{"Bearer not-a-real-token"}}, "")
		if res.code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401 (body=%q)", res.code, res.body)
		}
		ch := res.headers.Get("WWW-Authenticate")
		if !strings.Contains(ch, "Bearer") || !strings.Contains(ch, "CadenceApiKey") {
			t.Errorf("WWW-Authenticate should list both schemes, got %q", ch)
		}
	})

	t.Run("X-Api-Key still works alongside OIDC", func(t *testing.T) {
		// Critical: HC.io-compatible automation must keep working when OIDC
		// is added. If this fails, scripted callers break on upgrade.
		res := mustDo(t, http.MethodGet, serverURL+"/api/v3/checks/",
			http.Header{"X-Api-Key": []string{"rw-key"}}, "")
		if res.code != http.StatusOK {
			t.Fatalf("got %d, want 200 (body=%q)", res.code, res.body)
		}
	})

	t.Run("auth/config exposes OIDC discovery fields", func(t *testing.T) {
		res := mustDo(t, http.MethodGet, serverURL+"/api/v3/auth/config", nil, "")
		if res.code != http.StatusOK {
			t.Fatalf("got %d, want 200 (body=%q)", res.code, res.body)
		}
		var ac struct {
			OIDC *struct {
				Issuer   string `json:"issuer"`
				ClientID string `json:"client_id"`
				Audience string `json:"audience"`
			} `json:"oidc"`
		}
		if err := json.Unmarshal([]byte(res.body), &ac); err != nil {
			t.Fatalf("decode: %v\n---\n%s", err, res.body)
		}
		if ac.OIDC == nil {
			t.Fatalf("oidc block should be present, got nil: %s", res.body)
		}
		if ac.OIDC.Issuer != issuer {
			t.Errorf("issuer: got %q want %q", ac.OIDC.Issuer, issuer)
		}
		if ac.OIDC.ClientID != "test-client" {
			t.Errorf("client_id: got %q", ac.OIDC.ClientID)
		}
		if ac.OIDC.Audience != "test-client" {
			t.Errorf("audience defaults to client_id: got %q", ac.OIDC.Audience)
		}
	})

	t.Run("no credentials is 401", func(t *testing.T) {
		res := mustDo(t, http.MethodGet, serverURL+"/api/v3/checks/", nil, "")
		if res.code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", res.code)
		}
	})
}
