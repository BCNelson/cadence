package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStringEnvBasic(t *testing.T) {
	env := fixedEnv(map[string]string{"FOO": "bar"})
	got, err := resolveString(taggedString{value: "${env:FOO}", dir: ".", origin: "test"}, env)
	if err != nil {
		t.Fatalf("resolveString: %v", err)
	}
	if got != "bar" {
		t.Errorf("got %q", got)
	}
}

func TestResolveStringEnvDefaultUnset(t *testing.T) {
	env := fixedEnv(nil)
	got, err := resolveString(taggedString{value: "${env:MISSING:-fallback}", origin: "t"}, env)
	if err != nil {
		t.Fatalf("default unset: %v", err)
	}
	if got != "fallback" {
		t.Errorf("got %q", got)
	}
}

func TestResolveStringEnvDefaultPresent(t *testing.T) {
	env := fixedEnv(map[string]string{"PRESENT": "real"})
	got, err := resolveString(taggedString{value: "${env:PRESENT:-fallback}", origin: "t"}, env)
	if err != nil {
		t.Fatalf("default present: %v", err)
	}
	if got != "real" {
		t.Errorf("present should win: got %q", got)
	}
}

func TestResolveStringEnvEmptyTakesDefault(t *testing.T) {
	// expandEnv treats empty string the same as unset — the default applies.
	env := fixedEnv(map[string]string{"X": ""})
	got, err := resolveString(taggedString{value: "${env:X:-fallback}", origin: "t"}, env)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "fallback" {
		t.Errorf("empty should fall back: got %q", got)
	}
}

func TestResolveStringEnvUnsetErrors(t *testing.T) {
	env := fixedEnv(nil)
	_, err := resolveString(taggedString{value: "${env:NOPE}", origin: "/cfg/test.yaml"}, env)
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("want NOPE in error, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "/cfg/test.yaml") {
		t.Errorf("origin missing from error: %v", err)
	}
}

func TestResolveStringFileRelativeToTaggedDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "secrets/k.txt", "the-secret\n\n")

	got, err := resolveString(taggedString{
		value:  "${file:./secrets/k.txt}",
		dir:    dir,
		origin: filepath.Join(dir, "cfg.yaml"),
	}, fixedEnv(nil))
	if err != nil {
		t.Fatalf("resolveString: %v", err)
	}
	if got != "the-secret" {
		t.Errorf("got %q (expected trailing newlines trimmed)", got)
	}
}

func TestResolveStringFileAbsolute(t *testing.T) {
	dir := t.TempDir()
	abs := writeFile(t, dir, "k.txt", "abs-value")
	got, err := resolveString(taggedString{
		value:  "${file:" + abs + "}",
		dir:    "/totally/unrelated",
		origin: "cfg.yaml",
	}, fixedEnv(nil))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "abs-value" {
		t.Errorf("absolute path ignored dir? got %q", got)
	}
}

func TestResolveStringFileMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveString(taggedString{
		value:  "${file:./does-not-exist.txt}",
		dir:    dir,
		origin: "cfg.yaml",
	}, fixedEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "does-not-exist.txt") {
		t.Errorf("want missing-file error containing path, got %v", err)
	}
}

func TestResolveStringDollarEscape(t *testing.T) {
	got, err := resolveString(taggedString{value: "literal-$$-dollar"}, fixedEnv(nil))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "literal-$-dollar" {
		t.Errorf("$$ -> $: got %q", got)
	}
}

func TestResolveStringEnvThenFileOrdering(t *testing.T) {
	// Env substitution runs first, so a ${file:...} argument can itself
	// be an ${env:...} token — the operator points env at a secret path
	// and the daemon reads the contents.
	dir := t.TempDir()
	abs := writeFile(t, dir, "salt.txt", "the-real-salt\n")
	env := fixedEnv(map[string]string{"SALT_FILE": abs})
	got, err := resolveString(taggedString{
		value:  "${file:${env:SALT_FILE}}",
		dir:    dir,
		origin: "cfg.yaml",
	}, env)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "the-real-salt" {
		t.Errorf("two-phase: got %q", got)
	}
}

func TestResolveStringEnvFirstSurfacesEnvError(t *testing.T) {
	// ${file:${env:MISSING}} with MISSING unset must surface the env
	// "variable not set" error, not a file-not-found at the literal
	// path "${env:MISSING}".
	_, err := resolveString(taggedString{
		value:  "${file:${env:MISSING_PATH}}",
		dir:    ".",
		origin: "cfg.yaml",
	}, fixedEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "MISSING_PATH") {
		t.Errorf("want env error mentioning MISSING_PATH, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "file interpolation") {
		t.Errorf("env error should not be wrapped as file error: %v", err)
	}
}

func TestResolveStringUnresolvedTokenErrors(t *testing.T) {
	// An unknown scheme survives both phases and triggers the catch-all
	// error. (env and file are the only known ones.)
	_, err := resolveString(taggedString{value: "${weird:thing}", origin: "cfg.yaml"}, fixedEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Errorf("want unresolved error, got %v", err)
	}
}

func TestResolveStringUnterminatedToken(t *testing.T) {
	_, err := resolveString(taggedString{value: "${env:UNCLOSED", origin: "cfg.yaml"}, fixedEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("want unterminated error, got %v", err)
	}
}

func TestResolveStringNoTokens(t *testing.T) {
	got, err := resolveString(taggedString{value: "just a plain string"}, fixedEnv(nil))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "just a plain string" {
		t.Errorf("plain string changed: got %q", got)
	}
}

func TestInterpolateWalksTree(t *testing.T) {
	env := fixedEnv(map[string]string{"A": "alpha", "B": "beta"})
	in := map[string]any{
		"top": taggedString{value: "${env:A}"},
		"nested": map[string]any{
			"inner": taggedString{value: "${env:B}"},
		},
		"list":      []any{taggedString{value: "${env:A}"}, "untagged-plain"},
		"untouched": 42,
	}
	out, err := interpolate(in, env)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := out.(map[string]any)
	if m["top"] != "alpha" {
		t.Errorf("top: %v", m["top"])
	}
	if m["nested"].(map[string]any)["inner"] != "beta" {
		t.Errorf("nested: %v", m["nested"])
	}
	list := m["list"].([]any)
	if list[0] != "alpha" {
		t.Errorf("list[0]: %v", list[0])
	}
	if list[1] != "untagged-plain" {
		t.Errorf("untagged scalar should pass through: %v", list[1])
	}
	if m["untouched"] != 42 {
		t.Errorf("non-string scalar: %v", m["untouched"])
	}
}

func TestInterpolatePropagatesError(t *testing.T) {
	_, err := interpolate(map[string]any{
		"k": taggedString{value: "${env:MISSING}", origin: "cfg.yaml"},
	}, fixedEnv(nil))
	if err == nil {
		t.Error("want propagated env error")
	}
}

func TestExpandEnvEmptyName(t *testing.T) {
	_, err := expandEnv(":-fallback", fixedEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "empty variable name") {
		t.Errorf("want empty-name error, got %v", err)
	}
}

func TestExpandFileTrimming(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "trailing.txt", "value\r\n  \t\n")
	got, err := expandFile("./trailing.txt", dir)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "value" {
		t.Errorf("trailing whitespace not trimmed: %q", got)
	}
}

func TestProductionEnvLookupUsesOSLookupEnv(t *testing.T) {
	// expandEnv is wired to whatever envLookup callers pass. Verify that
	// passing os.LookupEnv works against the real process environment by
	// setting one var via t.Setenv.
	t.Setenv("CADENCE_TEST_VAR", "from-os")
	got, err := expandEnv("CADENCE_TEST_VAR", os.LookupEnv)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "from-os" {
		t.Errorf("got %q", got)
	}
}
