package config

import (
	"reflect"
	"testing"
)

func TestMergeAtScalarBWins(t *testing.T) {
	if got := mergeAt("foo", "old", "new"); got != "new" {
		t.Errorf("scalar: got %v", got)
	}
}

func TestMergeAtNilHandling(t *testing.T) {
	if got := mergeAt("foo", nil, "b"); got != "b" {
		t.Errorf("a==nil: got %v", got)
	}
	if got := mergeAt("foo", "a", nil); got != "a" {
		t.Errorf("b==nil: got %v", got)
	}
}

func TestMergeMapsDeep(t *testing.T) {
	a := map[string]any{
		"server": map[string]any{
			"listen":    ":8080",
			"uuid_salt": "salt",
		},
	}
	b := map[string]any{
		"server": map[string]any{
			"uuid_salt": "overridden",
			"base_url":  "https://x",
		},
	}
	out := mergeMaps(a, b)
	srv, ok := out["server"].(map[string]any)
	if !ok {
		t.Fatalf("server lost: %+v", out)
	}
	if srv["listen"] != ":8080" {
		t.Errorf("listen lost: %v", srv["listen"])
	}
	if srv["uuid_salt"] != "overridden" {
		t.Errorf("uuid_salt not overridden: %v", srv["uuid_salt"])
	}
	if srv["base_url"] != "https://x" {
		t.Errorf("base_url not added: %v", srv["base_url"])
	}
}

func TestMergeNonKeyedListReplaces(t *testing.T) {
	a := map[string]any{
		"defaults": map[string]any{
			"channels": []any{"old1", "old2"},
		},
	}
	b := map[string]any{
		"defaults": map[string]any{
			"channels": []any{"new1"},
		},
	}
	out := mergeMaps(a, b)
	got := out["defaults"].(map[string]any)["channels"].([]any)
	if len(got) != 1 || got[0] != "new1" {
		t.Errorf("non-keyed list should replace wholesale: got %v", got)
	}
}

func TestMergeKeyedListChecks(t *testing.T) {
	a := []any{
		map[string]any{"slug": "x", "period": "1h", "grace": "1m"},
		map[string]any{"slug": "y", "period": "2h"},
	}
	b := []any{
		map[string]any{"slug": "x", "grace": "5m"},  // matches, deep-merges
		map[string]any{"slug": "z", "period": "3h"}, // new, appends
	}
	got := mergeKeyedList("checks", "slug", a, b)
	if len(got) != 3 {
		t.Fatalf("want 3 items, got %d: %+v", len(got), got)
	}
	x := got[0].(map[string]any)
	if x["period"] != "1h" || x["grace"] != "5m" {
		t.Errorf("x merge wrong: %+v", x)
	}
	y := got[1].(map[string]any)
	if y["period"] != "2h" {
		t.Errorf("y survived but mutated: %+v", y)
	}
	z := got[2].(map[string]any)
	if z["slug"] != "z" {
		t.Errorf("z not appended: %+v", z)
	}
}

func TestMergeKeyedListChannelsByName(t *testing.T) {
	a := []any{
		map[string]any{"name": "hook", "url": "https://old.example"},
	}
	b := []any{
		map[string]any{"name": "hook", "url": "https://new.example"},
		map[string]any{"name": "other", "url": "https://other.example"},
	}
	out := mergeAt("channels", a, b).([]any)
	if len(out) != 2 {
		t.Fatalf("len: %d", len(out))
	}
	hook := out[0].(map[string]any)
	if hook["url"] != "https://new.example" {
		t.Errorf("hook url should be overridden: %v", hook["url"])
	}
}

func TestMergeKeyedListPingKeysByName(t *testing.T) {
	a := []any{map[string]any{"name": "ops", "key": "old"}}
	b := []any{map[string]any{"name": "ops", "key": "new"}}
	out := mergeAt("ping_keys", a, b).([]any)
	if len(out) != 1 {
		t.Fatalf("dedupe by name failed: %+v", out)
	}
	if out[0].(map[string]any)["key"] != "new" {
		t.Errorf("key not overridden: %+v", out[0])
	}
}

func TestMergeKeyedListPreservesTaggedStringKeys(t *testing.T) {
	// In real usage the keys come from loadFile tagged with origin info;
	// mergeKeyedList must still pair items by their underlying string value.
	a := []any{map[string]any{"slug": taggedString{value: "x", dir: "/a"}}}
	b := []any{map[string]any{"slug": taggedString{value: "x", dir: "/b"}, "grace": "5m"}}
	out := mergeKeyedList("checks", "slug", a, b)
	if len(out) != 1 {
		t.Fatalf("tagged keys not paired: %+v", out)
	}
	if out[0].(map[string]any)["grace"] != "5m" {
		t.Errorf("merge lost overlay field: %+v", out[0])
	}
}

func TestMergeKeyedListNonMapItemAppends(t *testing.T) {
	a := []any{"plain-string", map[string]any{"slug": "x"}}
	b := []any{42, map[string]any{"slug": "x", "p": 1}}
	out := mergeKeyedList("checks", "slug", a, b)
	// "plain-string" + map{x} + 42 + (x merged into the existing slot) = 3
	if len(out) != 3 {
		t.Errorf("non-map handling unexpected: got %d: %+v", len(out), out)
	}
}

func TestMergeAtTypeMismatch(t *testing.T) {
	// map onto list — type mismatch, b wins.
	a := []any{"a", "b"}
	b := map[string]any{"k": "v"}
	got := mergeAt("foo", a, b)
	if !reflect.DeepEqual(got, b) {
		t.Errorf("type mismatch: got %v, want %v", got, b)
	}
}

func TestMergeMapsHandlesNilInputs(t *testing.T) {
	// mergeMaps(nil, nil) must not panic.
	got := mergeMaps(nil, nil)
	if got == nil || len(got) != 0 {
		t.Errorf("nil/nil: got %+v", got)
	}
	a := map[string]any{"x": 1}
	if got := mergeMaps(nil, a); !reflect.DeepEqual(got, a) {
		t.Errorf("nil/a: got %+v", got)
	}
	if got := mergeMaps(a, nil); !reflect.DeepEqual(got, a) {
		t.Errorf("a/nil: got %+v", got)
	}
}

func TestJoinPath(t *testing.T) {
	cases := map[[2]string]string{
		{"", "checks"}:     "checks",
		{"checks", "slug"}: "checks.slug",
		{"a.b", "c"}:       "a.b.c",
	}
	for in, want := range cases {
		if got := joinPath(in[0], in[1]); got != want {
			t.Errorf("joinPath(%q,%q): got %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestStringValue(t *testing.T) {
	if v, ok := stringValue("plain"); !ok || v != "plain" {
		t.Errorf("plain string: %q / %v", v, ok)
	}
	if v, ok := stringValue(taggedString{value: "tagged"}); !ok || v != "tagged" {
		t.Errorf("tagged string: %q / %v", v, ok)
	}
	if _, ok := stringValue(42); ok {
		t.Error("int should not be a stringValue")
	}
	if _, ok := stringValue(nil); ok {
		t.Error("nil should not be a stringValue")
	}
}
