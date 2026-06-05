package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/sse"
	"github.com/bcnelson/cadence/internal/store"
)

func TestRepeatableFlag(t *testing.T) {
	var f repeatableFlag
	if err := f.Set("first"); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := f.Set("second"); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	if len(f) != 2 || f[0] != "first" || f[1] != "second" {
		t.Errorf("ordered append broken: %v", f)
	}
	if got := f.String(); !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("String missing values: %q", got)
	}
}

func TestRunMissingConfig(t *testing.T) {
	err := run([]string{filepath.Join(t.TempDir(), "does-not-exist.yaml")})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("missing config: want wrapped config error, got %v", err)
	}
}

func TestRunInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(bad, []byte(":\n  - not valid yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{bad})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("bad yaml: want wrapped config error, got %v", err)
	}
}

func TestSpaFallback(t *testing.T) {
	root := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><html>spa</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('hi');")},
	}
	handler := spaFallback(root, http.FileServer(http.FS(root)))

	t.Run("serves existing asset", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/app.js", http.NoBody)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "console.log") {
			t.Errorf("body: %q", rr.Body.String())
		}
	})

	t.Run("unknown route falls back to index.html", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/checks/123", http.NoBody)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d", rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type: got %q", got)
		}
		if !strings.Contains(rr.Body.String(), "spa") {
			t.Errorf("did not serve index.html: %q", rr.Body.String())
		}
	})

	t.Run("root path serves index", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status: got %d", rr.Code)
		}
	})

	t.Run("missing index returns 404", func(t *testing.T) {
		empty := fstest.MapFS{}
		h := spaFallback(empty, http.FileServer(http.FS(empty)))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/no/such/page", http.NoBody)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("status: got %d, want 404", rr.Code)
		}
	})
}

func TestRegisterRoutes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
server:
  uuid_salt: "s"
  api_keys:
    read_write: ["rw-token"]
ping_keys:
  - { name: ops, key: "ops-secret" }
checks:
  - { slug: api, period: 1h, ping_keys: [ops] }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{cfgPath}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "store"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	bus := sse.NewBus()
	eng, err := engine.New(reg, st, engine.Options{Bus: bus, Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	mux := http.NewServeMux()
	registerRoutes(mux, reg, eng, st, bus, nil)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "ok" {
			t.Errorf("body: %q", body)
		}
	})

	t.Run("mgmt list with read-write key", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v3/checks/", http.NoBody)
		req.Header.Set("X-Api-Key", "rw-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"slug":"api"`) {
			t.Errorf("body missing slug: %q", body)
		}
	})

	t.Run("ping slug requires key and updates engine", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ping/api", http.NoBody)
		req.Header.Set("X-Ping-Key", "ops-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: %d", resp.StatusCode)
		}
		c := reg.CheckBySlug("api")
		if snap, _ := eng.Snapshot(c.UUID); snap.Status != store.StatusUp {
			t.Errorf("engine not updated after ping: %q", snap.Status)
		}
	})

	t.Run("sse events streams", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", http.NoBody)
		req.Header.Set("X-Api-Key", "rw-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
			t.Errorf("Content-Type: %q", got)
		}
		// Close by canceling the context — we just wanted to confirm the
		// handler installed and replied with SSE headers.
		cancel()
		_, _ = io.Copy(io.Discard, resp.Body)
	})

	t.Run("sse events rejects missing api key", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status: got %d, want 401", resp.StatusCode)
		}
	})

	t.Run("spa fallback served at unknown route", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/some/spa/route")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: %d", resp.StatusCode)
		}
		// The real embedded SPA is served — just verify it's HTML.
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type: %q", got)
		}
	})
}
