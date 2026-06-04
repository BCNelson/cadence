package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bcnelson/cadence/internal/config"
)

// authConfig is the smallest config that exercises both key kinds.
const authConfig = `
server:
  uuid_salt: "s"
  api_keys:
    read_write: ["rw-key"]
    read_only:  ["ro-key"]
checks:
  - { slug: a, period: 1h }
`

func loadAuthRegistry(t *testing.T) *config.Registry {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(authConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{cfgPath}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestAuthenticate(t *testing.T) {
	reg := loadAuthRegistry(t)
	cases := []struct {
		name   string
		header string
		query  string
		want   KeyKind
	}{
		{"no key", "", "", KeyNone},
		{"unknown header key", "nope", "", KeyNone},
		{"unknown query key", "", "nope", KeyNone},
		{"read-only via header", "ro-key", "", KeyReadOnly},
		{"read-write via header", "rw-key", "", KeyReadWrite},
		{"read-only via query", "", "ro-key", KeyReadOnly},
		{"read-write via query", "", "rw-key", KeyReadWrite},
		// Header wins when both are set (and disagree).
		{"header beats query", "rw-key", "ro-key", KeyReadWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/x"
			if tc.query != "" {
				url += "?api_key=" + tc.query
			}
			r := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			if tc.header != "" {
				r.Header.Set("X-Api-Key", tc.header)
			}
			if got := Authenticate(reg, r); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequireKey(t *testing.T) {
	reg := loadAuthRegistry(t)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mw := RequireKey(reg, inner)

	cases := []struct {
		name   string
		header string
		query  string
		want   int
	}{
		{"missing", "", "", http.StatusUnauthorized},
		{"wrong", "nope", "", http.StatusUnauthorized},
		{"header allowed", "ro-key", "", http.StatusOK},
		{"query allowed", "", "rw-key", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/x"
			if tc.query != "" {
				url += "?api_key=" + tc.query
			}
			r := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			if tc.header != "" {
				r.Header.Set("X-Api-Key", tc.header)
			}
			rr := httptest.NewRecorder()
			mw.ServeHTTP(rr, r)
			if rr.Code != tc.want {
				t.Errorf("got %d, want %d (body=%q)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}
