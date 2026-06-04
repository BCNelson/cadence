package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileBasic(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "cfg.yaml", `
server:
  uuid_salt: "salt"
checks:
  - { slug: a, period: 1h }
`)
	got, err := loadFile(path, nil)
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("server not map: %T", got["server"])
	}
	salt, ok := server["uuid_salt"]
	if !ok {
		t.Fatal("uuid_salt missing")
	}
	// All scalars are tagged with their origin file's directory.
	ts, ok := salt.(taggedString)
	if !ok {
		t.Fatalf("salt not tagged: %T", salt)
	}
	if ts.value != "salt" || ts.dir != filepath.Dir(path) {
		t.Errorf("tag wrong: %+v", ts)
	}
}

func TestLoadFileRejectsNonMappingTop(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "cfg.yaml", "- a\n- b\n")
	_, err := loadFile(path, nil)
	if err == nil || !strings.Contains(err.Error(), "top-level must be a mapping") {
		t.Errorf("want top-level error, got %v", err)
	}
}

func TestLoadFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "cfg.yaml", "")
	got, err := loadFile(path, nil)
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty file should produce empty map, got %+v", got)
	}
}

func TestLoadFileMissing(t *testing.T) {
	_, err := loadFile(filepath.Join(t.TempDir(), "nope.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Errorf("want read error, got %v", err)
	}
}

func TestLoadDirSortsAndFiltersExtensions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "layers/20-second.yaml", `checks: [{ slug: b, period: 1h }]`)
	writeFile(t, dir, "layers/10-first.yaml", `checks: [{ slug: a, period: 1h }]`)
	writeFile(t, dir, "layers/notes.txt", `should be ignored`)
	writeFile(t, dir, "layers/also.yml", `checks: [{ slug: c, period: 1h }]`)

	got, err := loadDir(filepath.Join(dir, "layers"), nil)
	if err != nil {
		t.Fatalf("loadDir: %v", err)
	}
	checks, _ := got["checks"].([]any)
	if len(checks) != 3 {
		t.Fatalf("want 3 checks (txt ignored), got %d: %+v", len(checks), checks)
	}
	// 10-first.yaml runs before 20-second.yaml lexically; .yml's "also" sorts
	// after "20-second" by filename so it's last.
	gotSlugs := []string{
		slugOf(checks[0]),
		slugOf(checks[1]),
		slugOf(checks[2]),
	}
	want := []string{"a", "b", "c"}
	for i := range gotSlugs {
		if gotSlugs[i] != want[i] {
			t.Errorf("slug[%d]: got %q, want %q (full: %v)", i, gotSlugs[i], want[i], gotSlugs)
		}
	}
}

func TestLoadFileImportPrecedence(t *testing.T) {
	// The importing file's body wins over what it imports.
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", `
checks:
  - { slug: x, period: 1h, grace: 1m }
`)
	mainPath := writeFile(t, dir, "main.yaml", `
import: [ "./base.yaml" ]
checks:
  - { slug: x, grace: 5m }
`)
	got, err := loadFile(mainPath, nil)
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if _, ok := got["import"]; ok {
		t.Error("import key not stripped from result")
	}
	checks, _ := got["checks"].([]any)
	if len(checks) != 1 {
		t.Fatalf("checks merged into %d items: %+v", len(checks), checks)
	}
	row := checks[0].(map[string]any)
	if v, _ := stringValue(row["grace"]); v != "5m" {
		t.Errorf("importing file should win: got grace=%q", v)
	}
	if v, _ := stringValue(row["period"]); v != "1h" {
		t.Errorf("imported period should survive: got %q", v)
	}
}

func TestLoadFileImportCycleAcrossTwoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", `import: [ "./b.yaml" ]`)
	writeFile(t, dir, "b.yaml", `import: [ "./a.yaml" ]`)
	_, err := loadFile(filepath.Join(dir, "a.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("want cycle error, got %v", err)
	}
}

func TestExtractImportsAcceptsStringOrList(t *testing.T) {
	// Single string form.
	top := map[string]any{"import": "./one.yaml"}
	patterns, present, err := extractImports(top, "main.yaml")
	if err != nil || !present || len(patterns) != 1 || patterns[0] != "./one.yaml" {
		t.Errorf("string form: %v / %v / %v", patterns, present, err)
	}
	// List form.
	top = map[string]any{"import": []any{"./a.yaml", "./b.yaml"}}
	patterns, present, err = extractImports(top, "main.yaml")
	if err != nil || !present || len(patterns) != 2 {
		t.Errorf("list form: %v / %v / %v", patterns, present, err)
	}
	// Missing key.
	top = map[string]any{}
	_, present, err = extractImports(top, "main.yaml")
	if err != nil || present {
		t.Errorf("missing key: present=%v err=%v", present, err)
	}
	// Wrong type.
	top = map[string]any{"import": 42}
	_, _, err = extractImports(top, "main.yaml")
	if err == nil || !strings.Contains(err.Error(), "string or list") {
		t.Errorf("wrong type: want descriptive error, got %v", err)
	}
	// List with non-string element.
	top = map[string]any{"import": []any{"./ok.yaml", 42}}
	_, _, err = extractImports(top, "main.yaml")
	if err == nil || !strings.Contains(err.Error(), "import[1]") {
		t.Errorf("non-string element: want indexed error, got %v", err)
	}
}

func TestResolveImportPatternFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.yaml", `{}`)
	out, err := resolveImportPattern("./one.yaml", dir)
	if err != nil {
		t.Fatalf("resolveImportPattern: %v", err)
	}
	if len(out) != 1 || filepath.Base(out[0]) != "one.yaml" {
		t.Errorf("file: %v", out)
	}
}

func TestResolveImportPatternDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "checks.d")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := resolveImportPattern("./checks.d", dir)
	if err != nil {
		t.Fatalf("resolveImportPattern: %v", err)
	}
	if len(out) != 1 || out[0] != sub {
		t.Errorf("dir: %v", out)
	}
}

func TestResolveImportPatternGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "checks.d/a.yaml", `{}`)
	writeFile(t, dir, "checks.d/b.yaml", `{}`)
	out, err := resolveImportPattern("./checks.d/*.yaml", dir)
	if err != nil {
		t.Fatalf("resolveImportPattern: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("glob: %v", out)
	}
}

func TestResolveImportPatternGlobEmpty(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveImportPattern("./missing/*.yaml", dir)
	if err == nil || !strings.Contains(err.Error(), "no matches") {
		t.Errorf("empty glob: want no-matches error, got %v", err)
	}
}

func TestResolveImportPatternAbsolute(t *testing.T) {
	dir := t.TempDir()
	abs := writeFile(t, dir, "one.yaml", `{}`)
	out, err := resolveImportPattern(abs, "/totally/different/baseDir")
	if err != nil {
		t.Fatalf("resolveImportPattern: %v", err)
	}
	if len(out) != 1 || out[0] != abs {
		t.Errorf("absolute path should ignore baseDir: %v", out)
	}
}

func TestContainsGlob(t *testing.T) {
	cases := map[string]bool{
		"plain.yaml":     false,
		"a*.yaml":        true,
		"a?.yaml":        true,
		"a[bc].yaml":     true,
		"some/dir/x.yml": false,
	}
	for in, want := range cases {
		if got := containsGlob(in); got != want {
			t.Errorf("containsGlob(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestTagStringsPreservesOriginThroughTree(t *testing.T) {
	in := map[string]any{
		"top": "scalar",
		"nested": map[string]any{
			"inner": "deeper",
		},
		"list":  []any{"a", "b"},
		"int":   42,
		"mixed": []any{"x", 1, "y"},
	}
	tagged := tagStrings(in, "/some/dir", "/some/dir/cfg.yaml").(map[string]any)

	if ts, ok := tagged["top"].(taggedString); !ok || ts.dir != "/some/dir" {
		t.Errorf("top scalar tag: %+v", tagged["top"])
	}
	inner := tagged["nested"].(map[string]any)["inner"]
	if ts, ok := inner.(taggedString); !ok || ts.value != "deeper" {
		t.Errorf("nested scalar: %+v", inner)
	}
	if _, ok := tagged["int"].(taggedString); ok {
		t.Error("non-string values must not be tagged")
	}
	list := tagged["list"].([]any)
	if ts, ok := list[0].(taggedString); !ok || ts.value != "a" {
		t.Errorf("list scalar: %+v", list[0])
	}
	mixed := tagged["mixed"].([]any)
	if _, ok := mixed[1].(taggedString); ok {
		t.Error("non-string in mixed list must not be tagged")
	}
}

func slugOf(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	v, _ := stringValue(m["slug"])
	return v
}
