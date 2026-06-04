package config

// keyedListPaths declares which top-level lists deep-merge by a key field
// rather than replace wholesale. Per the spec: checks by `slug`,
// channels and ping_keys by `name`. Lists at any other path replace.
var keyedListPaths = map[string]string{
	"checks":    "slug",
	"channels":  "name",
	"ping_keys": "name",
}

// mergeMaps deep-merges b on top of a (b wins) and returns the result.
// Operates on the tagged tree from loadFile, so strings here are
// taggedString and merging two of them keeps b's tag (so ${file:...}
// resolution traces back to b's source).
func mergeMaps(a, b map[string]any) map[string]any {
	out := mergeAt("", a, b)
	if out == nil {
		return map[string]any{}
	}
	return out.(map[string]any)
}

// mergeAt is the recursive merge entry, path-aware so we can look up
// keyed-list semantics at specific positions in the tree.
func mergeAt(path string, a, b any) any {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	if aIsMap && bIsMap {
		return mergeMapsAt(path, am, bm)
	}

	al, aIsList := a.([]any)
	bl, bIsList := b.([]any)
	if aIsList && bIsList {
		if key, ok := keyedListPaths[path]; ok {
			return mergeKeyedList(path, key, al, bl)
		}
		return bl // non-keyed list: replace wholesale
	}

	return b // scalar or type-mismatched: b wins
}

func mergeMapsAt(path string, a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, bv := range b {
		if av, exists := out[k]; exists {
			out[k] = mergeAt(joinPath(path, k), av, bv)
		} else {
			out[k] = bv
		}
	}
	return out
}

// mergeKeyedList merges two lists of mappings keyed by `key`. Items in b
// whose key matches an item in a deep-merge; items whose key doesn't match
// append. Order: a's items first (in original order), then b's new items
// in the order they appear. A duplicate key WITHIN either side is an
// error — that detection happens in validation, not here.
func mergeKeyedList(path, key string, a, b []any) []any {
	indexA := make(map[string]int, len(a))
	out := make([]any, 0, len(a)+len(b))

	for _, item := range a {
		m, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		idx := len(out)
		out = append(out, m)
		if k, ok := stringValue(m[key]); ok {
			indexA[k] = idx
		}
	}

	for _, item := range b {
		m, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		k, hasKey := stringValue(m[key])
		if !hasKey {
			out = append(out, m)
			continue
		}
		if idx, present := indexA[k]; present {
			existing := out[idx].(map[string]any)
			out[idx] = mergeMapsAt(path, existing, m)
			continue
		}
		out = append(out, m)
	}
	return out
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// stringValue extracts the underlying string from either a plain string or
// a taggedString. Returns ok=false for any other type.
func stringValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case taggedString:
		return x.value, true
	}
	return "", false
}
