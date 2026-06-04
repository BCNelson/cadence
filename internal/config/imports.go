package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// taggedString is a string carrying the directory of the file that declared
// it. ${file:...} resolution after merging needs to know which file's
// directory a relative path is relative to; carrying it on the value
// survives deep-merges that interleave content from many files.
type taggedString struct {
	value  string
	dir    string // directory of the file that declared this value
	origin string // source file for error messages
}

// loadLayer reads one configuration "root" (file or directory) and returns
// the merged, import-resolved tree for that layer. The returned map has all
// scalar strings replaced with taggedString.
//
// Imports inside a file are resolved first (recursively), merged left-to-right,
// then the importing file's own body is merged on top (it wins over its
// imports). The import: key is consumed and removed.
//
// importStack carries absolute paths of files currently being loaded; if path
// re-appears, that's a cycle.
func loadLayer(path string, importStack []string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if info.IsDir() {
		return loadDir(abs, importStack)
	}
	return loadFile(abs, importStack)
}

// loadDir loads every *.yaml file directly inside dir, sorted lexically, and
// merges them left-to-right.
func loadDir(dir string, importStack []string) (map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) == ".yaml" || filepath.Ext(name) == ".yml" {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)

	acc := map[string]any{}
	for _, f := range files {
		layer, err := loadFile(f, importStack)
		if err != nil {
			return nil, err
		}
		acc = mergeMaps(acc, layer)
	}
	return acc, nil
}

// loadFile parses one YAML file, recursively resolves its imports, and
// returns the merged tree with the import: key stripped.
func loadFile(path string, importStack []string) (map[string]any, error) {
	for _, s := range importStack {
		if s == path {
			cycle := append([]string{}, importStack...) //nolint:gocritic // copy for error message
			cycle = append(cycle, path)
			return nil, fmt.Errorf("import cycle: %v", cycle)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}

	top, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: top-level must be a mapping, got %T", path, doc)
	}

	dir := filepath.Dir(path)
	imports, hasImports, err := extractImports(top, path)
	if err != nil {
		return nil, err
	}
	delete(top, "import")

	tagged := tagStrings(top, dir, path).(map[string]any)

	if !hasImports || len(imports) == 0 {
		return tagged, nil
	}

	nextStack := append(importStack, path) //nolint:gocritic // intentional grow

	acc := map[string]any{}
	for _, imp := range imports {
		resolved, err := resolveImportPattern(imp, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: import %q: %w", path, imp, err)
		}
		for _, target := range resolved {
			sub, err := loadLayer(target, nextStack)
			if err != nil {
				return nil, err
			}
			acc = mergeMaps(acc, sub)
		}
	}

	return mergeMaps(acc, tagged), nil
}

// extractImports reads and validates the import: directive, returning the
// list of patterns. Accepts a single string or a list of strings.
func extractImports(top map[string]any, path string) (patterns []string, present bool, err error) {
	v, ok := top["import"]
	if !ok {
		return nil, false, nil
	}
	switch t := v.(type) {
	case string:
		return []string{t}, true, nil
	case []any:
		out := make([]string, 0, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("%s: import[%d] must be a string, got %T", path, i, item)
			}
			out = append(out, s)
		}
		return out, true, nil
	default:
		return nil, true, fmt.Errorf("%s: import must be string or list of strings, got %T", path, v)
	}
}

// resolveImportPattern expands a single import: entry into one or more
// concrete file paths. The pattern is resolved relative to baseDir. Glob
// patterns and directories are both supported.
func resolveImportPattern(pattern, baseDir string) ([]string, error) {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(baseDir, pattern)
	}

	// Directory shorthand: trailing slash or actual directory.
	info, statErr := os.Stat(pattern)
	if statErr == nil && info.IsDir() {
		return []string{pattern}, nil
	}

	if containsGlob(pattern) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no matches for glob %q", pattern)
		}
		sort.Strings(matches)
		return matches, nil
	}

	if statErr != nil {
		return nil, statErr
	}
	return []string{pattern}, nil
}

func containsGlob(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// tagStrings walks the YAML tree, replacing every scalar string with a
// taggedString that remembers the file's directory. This preserves origin
// information across subsequent deep-merges so ${file:...} can resolve
// relative paths correctly even after layers have been combined.
func tagStrings(v any, dir, origin string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			x[k] = tagStrings(child, dir, origin)
		}
		return x
	case []any:
		for i, child := range x {
			x[i] = tagStrings(child, dir, origin)
		}
		return x
	case string:
		return taggedString{value: x, dir: dir, origin: origin}
	}
	return v
}
