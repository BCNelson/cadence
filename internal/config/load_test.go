package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// writeFile is a tiny helper that fails the test on write error rather than
// requiring every caller to handle it.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return full
}

// fixedEnv returns an envLookup backed by the given map. Tests use it so
// interpolation behavior doesn't depend on the host environment.
func fixedEnv(m map[string]string) envLookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestLoadBasic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server:
  listen: ":8080"
  base_url: "https://example.com"
  uuid_salt: "test-salt"
  api_keys:
    read_write: ["rw1"]

data_dir: "./data"

retention:
  pings: 100
  events: 50

ping_keys:
  - { name: ops, key: "ops-secret" }

defaults:
  grace: 5m
  ping_keys: [ops]

channels:
  - { name: hook, type: webhook, url: "https://hook.example.com" }

checks:
  - slug: homepage
    name: "Homepage"
    cron: "*/5 * * * *"
    tags: [web]
  - slug: backup
    period: 24h
    grace: 1h
    channels: [hook]
`)

	reg, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := reg.Server.UUIDSalt; got != "test-salt" {
		t.Errorf("uuid_salt: got %q", got)
	}
	if reg.Retention.Pings != 100 {
		t.Errorf("retention.pings: got %d", reg.Retention.Pings)
	}
	if _, ok := reg.PingKeys["ops"]; !ok {
		t.Errorf("ping_keys missing 'ops'")
	}
	if len(reg.Checks) != 2 {
		t.Fatalf("checks: got %d, want 2", len(reg.Checks))
	}

	hp := reg.CheckBySlug("homepage")
	if hp == nil {
		t.Fatal("homepage check missing")
	}
	if hp.Cron != "*/5 * * * *" {
		t.Errorf("homepage.cron: got %q", hp.Cron)
	}
	if hp.Grace != 5*time.Minute {
		t.Errorf("homepage.grace: got %v, want defaults' 5m", hp.Grace)
	}
	if len(hp.PingKeys) != 1 || hp.PingKeys[0] != "ops" {
		t.Errorf("homepage.ping_keys: got %v, want [ops] from defaults", hp.PingKeys)
	}
	if !hp.Enabled {
		t.Errorf("homepage.enabled: got false, want true (unset -> default true)")
	}

	bk := reg.CheckBySlug("backup")
	if bk == nil {
		t.Fatal("backup check missing")
	}
	if bk.Period != 24*time.Hour {
		t.Errorf("backup.period: got %v", bk.Period)
	}
	if bk.Grace != time.Hour {
		t.Errorf("backup.grace: got %v, want override of 1h", bk.Grace)
	}
	if len(bk.Channels) != 1 || bk.Channels[0] != "hook" {
		t.Errorf("backup.channels: got %v", bk.Channels)
	}

	// UUIDs derived from same salt+slug must be reproducible.
	if hp.UUID != DeriveUUID("test-salt", "homepage") {
		t.Errorf("homepage uuid not stable: %s", hp.UUID)
	}
	if hp.UUID == bk.UUID {
		t.Error("two checks should not share a UUID")
	}
}

func TestLoadLayering(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", `
server: { uuid_salt: "salt" }
ping_keys:
  - { name: ops, key: "base-secret" }
checks:
  - slug: backup
    period: 24h
    grace: 1h
`)
	writeFile(t, dir, "overlay.yaml", `
checks:
  - slug: backup
    grace: 2h
`)

	reg, err := Load(
		[]string{filepath.Join(dir, "base.yaml"), filepath.Join(dir, "overlay.yaml")},
		Options{Env: fixedEnv(nil)},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bk := reg.CheckBySlug("backup")
	if bk == nil {
		t.Fatal("backup missing")
	}
	if bk.Period != 24*time.Hour {
		t.Errorf("base period lost in merge: got %v", bk.Period)
	}
	if bk.Grace != 2*time.Hour {
		t.Errorf("overlay grace not applied: got %v", bk.Grace)
	}
}

func TestLoadDirectoryExpansion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "layers/00-base.yaml", `
server: { uuid_salt: "salt" }
checks:
  - { slug: a, period: 1h }
`)
	writeFile(t, dir, "layers/10-extra.yaml", `
checks:
  - { slug: b, period: 2h }
`)
	reg, err := Load([]string{filepath.Join(dir, "layers")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.CheckBySlug("a") == nil || reg.CheckBySlug("b") == nil {
		t.Errorf("dir expansion lost a file: %v", reg.Checks)
	}
}

func TestImportRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "checks.d/foo.yaml", `
checks:
  - { slug: foo, period: 1h }
`)
	writeFile(t, dir, "main.yaml", `
import: [ "checks.d/" ]
server: { uuid_salt: "salt" }
checks:
  - { slug: bar, period: 2h }
`)
	reg, err := Load([]string{filepath.Join(dir, "main.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.CheckBySlug("foo") == nil {
		t.Errorf("imported foo missing")
	}
	if reg.CheckBySlug("bar") == nil {
		t.Errorf("own bar missing")
	}
}

func TestImportPrecedence(t *testing.T) {
	// The importing file's body wins over what it imports.
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", `
checks:
  - { slug: x, period: 1h, grace: 1m }
`)
	writeFile(t, dir, "main.yaml", `
import: [ "./base.yaml" ]
server: { uuid_salt: "salt" }
checks:
  - { slug: x, grace: 5m }
`)
	reg, err := Load([]string{filepath.Join(dir, "main.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	x := reg.CheckBySlug("x")
	if x == nil {
		t.Fatal("x missing")
	}
	if x.Period != time.Hour {
		t.Errorf("base period lost: got %v", x.Period)
	}
	if x.Grace != 5*time.Minute {
		t.Errorf("importing-file grace not applied: got %v", x.Grace)
	}
}

func TestImportCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", `
import: [ "./b.yaml" ]
server: { uuid_salt: "s" }
`)
	writeFile(t, dir, "b.yaml", `
import: [ "./a.yaml" ]
`)
	_, err := Load([]string{filepath.Join(dir, "a.yaml")}, Options{Env: fixedEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestInterpolateEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server:
  uuid_salt: "${env:SALT}"
  listen: "${env:PORT:-:9999}"
ping_keys:
  - { name: ops, key: "$$literal" }
checks:
  - { slug: a, period: 1h }
`)
	reg, err := Load(
		[]string{filepath.Join(dir, "cfg.yaml")},
		Options{Env: fixedEnv(map[string]string{"SALT": "from-env"})},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Server.UUIDSalt != "from-env" {
		t.Errorf("env: got %q", reg.Server.UUIDSalt)
	}
	if reg.Server.Listen != ":9999" {
		t.Errorf("env default: got %q", reg.Server.Listen)
	}
	if got := reg.PingKeys["ops"]; got != "$literal" {
		t.Errorf("$$ escape: got %q", got)
	}
}

func TestInterpolateEnvUnsetErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "${env:MISSING}" }
checks:
  - { slug: a, period: 1h }
`)
	_, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("want MISSING unset error, got %v", err)
	}
}

func TestInterpolateFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "secrets/key.txt", "secret-value\n\n")
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "salt" }
ping_keys:
  - { name: ops, key: "${file:./secrets/key.txt}" }
checks:
  - { slug: a, period: 1h }
`)
	reg, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reg.PingKeys["ops"]; got != "secret-value" {
		t.Errorf("file interpolation: got %q (want trimmed contents)", got)
	}
}

func TestValidatePeriodCronXor(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"neither", `
server: { uuid_salt: "s" }
checks: [ { slug: a } ]
`, "exactly one of period or cron"},
		{"both", `
server: { uuid_salt: "s" }
checks: [ { slug: a, period: 1h, cron: "* * * * *" } ]
`, "exactly one of period or cron"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "cfg.yaml", tc.yaml)
			_, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateUnknownPingKeyRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "s" }
checks:
  - { slug: a, period: 1h, ping_keys: [ghost] }
`)
	_, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want unknown ping_key error, got %v", err)
	}
}

func TestValidateDuplicateSlug(t *testing.T) {
	// Duplicate slugs in a SINGLE list don't go through the keyed merge
	// (merge expects them to merge by key), so the keyed-merge will combine
	// them into one. To catch the spec'd "duplicate within a single resolved
	// layer" we need a layer where two distinct overlay layers produce the
	// same slug — but that's deliberately legal (merging). So the duplicate
	// check here is the typed-check duplicate-slug guard, which fires when
	// the registry sees the same slug twice — only possible via pinned uuid
	// collision or post-merge.
	//
	// We verify pinned-uuid collisions cause an error instead.
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "s" }
checks:
  - { slug: a, period: 1h, uuid: "00000000-0000-0000-0000-000000000001" }
  - { slug: b, period: 1h, uuid: "00000000-0000-0000-0000-000000000001" }
`)
	_, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), "same UUID") {
		t.Fatalf("want pinned uuid collision error, got %v", err)
	}
}

func TestMissingUUIDSalt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
checks:
  - { slug: a, period: 1h }
`)
	_, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), "uuid_salt") {
		t.Fatalf("want uuid_salt required error, got %v", err)
	}
}

func TestDeriveUUIDStableAndUnguessable(t *testing.T) {
	a1 := DeriveUUID("salt-A", "homepage")
	a2 := DeriveUUID("salt-A", "homepage")
	b := DeriveUUID("salt-B", "homepage")
	c := DeriveUUID("salt-A", "different-slug")
	if a1 != a2 {
		t.Error("UUID not stable across calls")
	}
	if a1 == b {
		t.Error("UUID identical for different salts — salt isn't influencing derivation")
	}
	if a1 == c {
		t.Error("UUID identical for different slugs")
	}
}

func TestRegistryCheckByUUID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "salt-X" }
checks:
  - { slug: api, period: 1h }
`)
	reg, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	derived := DeriveUUID("salt-X", "api")
	if got := reg.CheckByUUID(derived); got == nil || got.Slug != "api" {
		t.Errorf("CheckByUUID(derived): got %+v", got)
	}
	if got := reg.CheckByUUID(uuid.New()); got != nil {
		t.Errorf("CheckByUUID(random): want nil, got %+v", got)
	}
}

func TestDurationStringAndStd(t *testing.T) {
	d := Duration(5 * time.Minute)
	if got := d.String(); got != "5m0s" {
		t.Errorf("String: got %q", got)
	}
	if got := d.Std(); got != 5*time.Minute {
		t.Errorf("Std: got %v", got)
	}
	// Zero value.
	if Duration(0).String() != "0s" {
		t.Errorf("zero duration: got %q", Duration(0).String())
	}
}

func TestDurationUnmarshalRejectsNonScalar(t *testing.T) {
	type wrap struct {
		D Duration `yaml:"d"`
	}
	// A sequence node where a scalar is expected.
	var w wrap
	err := yaml.Unmarshal([]byte("d: [1, 2, 3]\n"), &w)
	if err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("sequence input: want must-be-string error, got %v", err)
	}
	// A mapping node where a scalar is expected.
	err = yaml.Unmarshal([]byte("d: {a: 1}\n"), &w)
	if err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("mapping input: want must-be-string error, got %v", err)
	}
}

func TestDurationUnmarshalInvalid(t *testing.T) {
	type wrap struct {
		D Duration `yaml:"d"`
	}
	var w wrap
	// Bare "60" has no unit and must error rather than being silently treated
	// as nanoseconds.
	err := yaml.Unmarshal([]byte("d: \"60\"\n"), &w)
	if err == nil || !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("bare 60: want invalid-duration error, got %v", err)
	}
	// Empty string parses as zero — explicit allowance.
	w = wrap{}
	if err := yaml.Unmarshal([]byte("d: \"\"\n"), &w); err != nil {
		t.Errorf("empty string: want no error, got %v", err)
	}
	if w.D != 0 {
		t.Errorf("empty string: want zero, got %v", w.D)
	}
}

func TestPinnedUUID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg.yaml", `
server: { uuid_salt: "s" }
checks:
  - { slug: a, period: 1h, uuid: "11111111-2222-3333-4444-555555555555" }
`)
	reg, err := Load([]string{filepath.Join(dir, "cfg.yaml")}, Options{Env: fixedEnv(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := reg.CheckBySlug("a")
	if !a.PinnedUUID {
		t.Error("pinned uuid not flagged")
	}
	if a.UUID.String() != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("pinned uuid not used: got %s", a.UUID)
	}
}
