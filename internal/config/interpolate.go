package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envLookup returns (value, present). os.LookupEnv is the production
// implementation; tests substitute a map-backed version.
type envLookup func(name string) (string, bool)

// interpolate runs the two-phase substitution pass over the merged tree.
// Phase 1 expands every ${file:...} relative to the directory of the
// taggedString it appears in; Phase 2 expands every ${env:...} (with
// optional :-default fallback). After both phases any surviving ${...}
// is an error.
//
// Walks once, calling resolveString on each taggedString. Non-string
// scalars and structural nodes pass through unchanged.
func interpolate(v any, env envLookup) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			out, err := interpolate(child, env)
			if err != nil {
				return nil, err
			}
			x[k] = out
		}
		return x, nil
	case []any:
		for i, child := range x {
			out, err := interpolate(child, env)
			if err != nil {
				return nil, err
			}
			x[i] = out
		}
		return x, nil
	case taggedString:
		s, err := resolveString(x, env)
		if err != nil {
			return nil, err
		}
		return s, nil
	case string:
		// Untagged strings can only appear in fields that don't go through
		// loadFile (e.g. constructed by tests). Pass through as-is.
		return x, nil
	}
	return v, nil
}

// resolveString expands all interpolation tokens in s, in the order:
// ${file:...} first, then ${env:...}, then $$ -> $. Errors carry s.origin
// for user-facing messages.
func resolveString(s taggedString, env envLookup) (string, error) {
	out, err := substituteTokens(s.value, s.dir, s.origin, "file", expandFile)
	if err != nil {
		return "", err
	}
	out, err = substituteTokens(out, s.dir, s.origin, "env", func(arg, _ string) (string, error) {
		return expandEnv(arg, env)
	})
	if err != nil {
		return "", err
	}
	if idx := strings.Index(out, "${"); idx >= 0 {
		end := strings.Index(out[idx:], "}")
		token := out[idx:]
		if end >= 0 {
			token = out[idx : idx+end+1]
		}
		return "", fmt.Errorf("%s: unresolved interpolation %s", s.origin, token)
	}
	return strings.ReplaceAll(out, "$$", "$"), nil
}

// substituteTokens scans s for `${scheme:ARG}` tokens and replaces each by
// calling expand(arg, dir). Other schemes pass through untouched so a later
// phase can handle them. `$$` is left intact for the final unescape pass.
func substituteTokens(s, dir, origin, scheme string, expand func(arg, dir string) (string, error)) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	prefix := scheme + ":"

	for i := 0; i < len(s); {
		c := s[i]
		if c == '$' && i+1 < len(s) {
			next := s[i+1]
			if next == '$' {
				b.WriteByte('$')
				b.WriteByte('$')
				i += 2
				continue
			}
			if next == '{' {
				end := strings.IndexByte(s[i+2:], '}')
				if end < 0 {
					return "", fmt.Errorf("%s: unterminated interpolation in %q", origin, s)
				}
				body := s[i+2 : i+2+end]
				if strings.HasPrefix(body, prefix) {
					arg := body[len(prefix):]
					replacement, err := expand(arg, dir)
					if err != nil {
						return "", fmt.Errorf("%s: %w", origin, err)
					}
					b.WriteString(replacement)
				} else {
					b.WriteString(s[i : i+2+end+1])
				}
				i += 2 + end + 1
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

// expandFile reads the file at path (resolved relative to dir if not
// absolute) and returns its contents with trailing whitespace and newlines
// trimmed.
func expandFile(path, dir string) (string, error) {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, path)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("file interpolation %q: %w", path, err)
	}
	return strings.TrimRight(string(raw), " \t\r\n"), nil
}

// expandEnv resolves ${env:VAR} or ${env:VAR:-default}. An unset VAR with
// no default is an error.
func expandEnv(arg string, env envLookup) (string, error) {
	name := arg
	var defaultValue string
	hasDefault := false
	if idx := strings.Index(arg, ":-"); idx >= 0 {
		name = arg[:idx]
		defaultValue = arg[idx+2:]
		hasDefault = true
	}
	if name == "" {
		return "", fmt.Errorf("env interpolation: empty variable name in %q", arg)
	}
	val, present := env(name)
	if present && val != "" {
		return val, nil
	}
	if hasDefault {
		return defaultValue, nil
	}
	return "", fmt.Errorf("env interpolation: variable %s is not set", name)
}
