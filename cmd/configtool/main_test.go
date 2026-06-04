package main

import (
	"bytes"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

func TestRepeatableFlag(t *testing.T) {
	var f repeatableFlag
	if err := f.Set("a"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := f.Set("b"); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if len(f) != 2 || f[0] != "a" || f[1] != "b" {
		t.Errorf("ordered append broken: %v", f)
	}
	if got := f.String(); !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("String missing values: %q", got)
	}
}

func TestMaskedKeysMasksAndSorts(t *testing.T) {
	in := map[string]string{
		"prod":    "super-secret-prod",
		"dev":     "dev-secret-value",
		"staging": "stage-secret",
	}
	out := maskedKeys(in)

	if len(out) != 3 {
		t.Fatalf("len: got %d", len(out))
	}
	// Sorted by name.
	gotNames := []string{out[0]["name"], out[1]["name"], out[2]["name"]}
	wantNames := []string{"dev", "prod", "staging"}
	if !equalSlice(gotNames, wantNames) {
		t.Errorf("name order: got %v, want %v", gotNames, wantNames)
	}
	// No raw secret in the output anywhere.
	for _, row := range out {
		if row["key"] != "***" {
			t.Errorf("key not masked: %q", row["key"])
		}
	}
	flat, _ := yaml.Marshal(out)
	for _, raw := range in {
		if strings.Contains(string(flat), raw) {
			t.Errorf("raw secret %q leaked into masked output", raw)
		}
	}
}

func TestMaskedKeysEmpty(t *testing.T) {
	out := maskedKeys(map[string]string{})
	if len(out) != 0 {
		t.Errorf("empty input: got %d rows", len(out))
	}
}

func TestSortedChannelsSorts(t *testing.T) {
	in := map[string]config.Channel{
		"slack":     {Name: "slack", Type: "webhook", URL: "https://hooks.slack.com"},
		"opsgenie":  {Name: "opsgenie", Type: "webhook", URL: "https://api.opsgenie.com"},
		"pagerduty": {Name: "pagerduty", Type: "webhook", URL: "https://events.pagerduty.com"},
	}
	out := sortedChannels(in)
	gotNames := []string{out[0].Name, out[1].Name, out[2].Name}
	wantNames := []string{"opsgenie", "pagerduty", "slack"}
	if !equalSlice(gotNames, wantNames) {
		t.Errorf("channel order: got %v, want %v", gotNames, wantNames)
	}
}

func TestSortedChecksOptionalFieldsAndOrder(t *testing.T) {
	checks := map[string]*config.ResolvedCheck{
		"zeta": {
			Slug:    "zeta",
			UUID:    uuid.MustParse("11111111-2222-3333-4444-555555555555"),
			Enabled: true,
			Period:  1 * time.Hour,
		},
		"alpha": {
			Slug:       "alpha",
			Name:       "Alpha Check",
			UUID:       uuid.MustParse("22222222-3333-4444-5555-666666666666"),
			Enabled:    false,
			Cron:       "*/5 * * * *",
			Grace:      30 * time.Second,
			Timeout:    1 * time.Minute,
			PingKeys:   []string{"ops"},
			Channels:   []string{"hook"},
			Tags:       []string{"web"},
			PinnedUUID: true,
		},
	}
	out := sortedChecks(checks)

	if len(out) != 2 {
		t.Fatalf("len: %d", len(out))
	}
	// Order is lexical by slug.
	if out[0]["slug"] != "alpha" || out[1]["slug"] != "zeta" {
		t.Errorf("slug order: got %v / %v", out[0]["slug"], out[1]["slug"])
	}

	alpha := out[0]
	// Optional fields that ARE set should appear.
	for _, k := range []string{"name", "cron", "grace", "timeout", "ping_keys", "channels", "tags", "uuid_pinned"} {
		if _, ok := alpha[k]; !ok {
			t.Errorf("alpha missing optional field %q: %+v", k, alpha)
		}
	}
	// Period must NOT appear (cron is set instead).
	if _, ok := alpha["period"]; ok {
		t.Errorf("alpha has period but cron was set: %+v", alpha)
	}

	zeta := out[1]
	// zeta uses period; should have period, not cron.
	if _, ok := zeta["period"]; !ok {
		t.Errorf("zeta missing period: %+v", zeta)
	}
	if _, ok := zeta["cron"]; ok {
		t.Errorf("zeta has cron when period was set: %+v", zeta)
	}
	// zeta's empty optional fields should be absent.
	for _, k := range []string{"name", "grace", "timeout", "ping_keys", "channels", "tags", "uuid_pinned"} {
		if _, ok := zeta[k]; ok {
			t.Errorf("zeta has unexpected optional field %q: %+v", k, zeta)
		}
	}
}

func TestPrintResolvedMasksKeysAndRoundTrips(t *testing.T) {
	reg := &config.Registry{
		Server:   config.Server{UUIDSalt: "salt"},
		DataDir:  "./data",
		PingKeys: map[string]string{"ops": "ops-super-secret"},
		Channels: map[string]config.Channel{
			"hook": {Name: "hook", Type: "webhook", URL: "https://hook.example.com"},
		},
		Checks: map[string]*config.ResolvedCheck{
			"api": {
				Slug:    "api",
				UUID:    uuid.MustParse("11111111-2222-3333-4444-555555555555"),
				Enabled: true,
				Period:  1 * time.Hour,
			},
		},
	}

	// Capture stdout for printResolved.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	if err := printResolved(reg); err != nil {
		t.Fatalf("printResolved: %v", err)
	}
	_ = w.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	// Output must be parseable YAML.
	var got map[string]any
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatalf("yaml unmarshal: %v\n---\n%s", err, body)
	}
	// And it must NOT contain the raw secret.
	if bytes.Contains(body, []byte("ops-super-secret")) {
		t.Errorf("raw secret leaked into stdout: %s", body)
	}
	// And it MUST contain the mask.
	if !bytes.Contains(body, []byte("***")) {
		t.Errorf("mask missing: %s", body)
	}
	// And the slug should round-trip.
	if !bytes.Contains(body, []byte("api")) {
		t.Errorf("slug missing: %s", body)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	// Also check exact order — the helpers must be lexical.
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
