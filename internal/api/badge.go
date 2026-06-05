package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
)

// Badge endpoints. Public (no auth) to match HC.io's README-embed use
// case: badges are designed for public dashboards and shouldn't require
// callers to know an API key.
//
// Path shapes:
//   /badge/{stem}.{ext}        per-check, stem = uniqueKey(uuid)
//   /badge/{stem}-3.{ext}      per-check, 3-state variant
//   /badge/tag/{name}.{ext}    per-tag rollup
//   /badge/tag/{name}-3.{ext}  per-tag, 3-state variant
//   /badge/tag/*.{ext}         all-checks rollup (HC.io convention)
//
// Extensions: .svg, .json, .shields. The `.shields` form returns the
// shields.io endpoint JSON so README authors can point shields.io at a
// cadence instance and get a styled badge for free.

func (h *MgmtHandler) handleBadge(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/badge/")
	if rest == "" || rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 {
		http.NotFound(w, r)
		return
	}
	stem, ext := rest[:dot], rest[dot:]

	// Strip the "-3" suffix to pick the 3-state palette; without it the
	// badge collapses late→down so README readers see only the binary
	// up/down signal HC.io's 2-state badges convey.
	threeState := false
	if strings.HasSuffix(stem, "-3") {
		threeState = true
		stem = strings.TrimSuffix(stem, "-3")
	}

	status, ok := h.resolveBadgeTarget(stem)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Short cache so a dashboard polling once per minute doesn't hammer
	// the server, but a status flip is reflected within ~30s.
	w.Header().Set("Cache-Control", "max-age=30, must-revalidate")

	switch ext {
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		_, _ = w.Write([]byte(renderSVG(status, threeState)))
	case ".json":
		writeJSON(w, http.StatusOK, map[string]string{"status": badgeLabel(status, threeState)})
	case ".shields":
		label, color := badgeLabel(status, threeState), badgeColor(status, threeState)
		writeJSON(w, http.StatusOK, map[string]any{
			"schemaVersion": 1,
			"label":         "status",
			"message":       label,
			"color":         color,
		})
	default:
		http.NotFound(w, r)
	}
}

// resolveBadgeTarget maps a stem to a rolled-up status. Returns
// (status, true) on success; (..., false) when the stem doesn't name a
// known check or tag. An empty bucket returns paused via the tag path
// rather than 404 so a tag previously seen by a dashboard doesn't break
// after every member is paused.
func (h *MgmtHandler) resolveBadgeTarget(stem string) (store.Status, bool) {
	if t, ok := strings.CutPrefix(stem, "tag/"); ok {
		if t == "*" {
			all := make([]*config.ResolvedCheck, 0, len(h.registry.Checks))
			for _, c := range h.registry.Checks {
				all = append(all, c)
			}
			return h.rollupStatus(all), true
		}
		members := h.tagBuckets()[t]
		if len(members) == 0 {
			return "", false
		}
		return h.rollupStatus(members), true
	}
	// Per-check: stem is uniqueKey(uuid). Linear scan is fine — v1 has
	// small check counts and badges are short-cached.
	for _, c := range h.registry.Checks {
		if uniqueKey(c.UUID) == stem {
			snap, _ := h.engine.Snapshot(c.UUID)
			return snap.Status, true
		}
	}
	return "", false
}

// badgeLabel is the user-visible word on the badge. 2-state collapses
// late→down so the binary signal matches HC.io's stock badge.
func badgeLabel(s store.Status, threeState bool) string {
	if !threeState && s == store.StatusLate {
		s = store.StatusDown
	}
	switch s {
	case store.StatusUp:
		return "up"
	case store.StatusLate:
		return "late"
	case store.StatusDown:
		return "down"
	case store.StatusPaused:
		return "paused"
	case store.StatusNew:
		return "new"
	}
	return "unknown"
}

// badgeColor maps status to a shields.io color name.
func badgeColor(s store.Status, threeState bool) string {
	if !threeState && s == store.StatusLate {
		s = store.StatusDown
	}
	switch s {
	case store.StatusUp:
		return "brightgreen"
	case store.StatusLate:
		return "yellow"
	case store.StatusDown:
		return "red"
	case store.StatusPaused:
		return "lightgrey"
	case store.StatusNew:
		return "blue"
	}
	return "lightgrey"
}

// SVG hex colors keyed on the shields.io name we'd emit for .shields.
var svgHex = map[string]string{
	"brightgreen": "#4c1",
	"yellow":      "#dfb317",
	"red":         "#e05d44",
	"lightgrey":   "#9f9f9f",
	"blue":        "#007ec6",
}

// renderSVG emits a flat Shields-style two-segment badge. The widths are
// fixed (no font-metrics math) and chosen to fit the longest label
// ("paused"); shorter labels just get a bit of extra padding. This keeps
// the renderer dependency-free and deterministic.
func renderSVG(s store.Status, threeState bool) string {
	label := badgeLabel(s, threeState)
	color := svgHex[badgeColor(s, threeState)]
	// Left segment width is fixed for "status"; right segment is sized to
	// the longest label we emit so every badge is the same shape.
	const leftW = 48
	const rightW = 56
	total := leftW + rightW
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="20" role="img" aria-label="status: %[2]s">
<title>status: %[2]s</title>
<linearGradient id="g" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%[1]d" height="20" rx="3"/></clipPath>
<g clip-path="url(#r)">
<rect width="%[3]d" height="20" fill="#555"/>
<rect x="%[3]d" width="%[4]d" height="20" fill="%[5]s"/>
<rect width="%[1]d" height="20" fill="url(#g)"/>
</g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
<text x="%[6]d" y="15" fill="#010101" fill-opacity=".3">status</text>
<text x="%[6]d" y="14">status</text>
<text x="%[7]d" y="15" fill="#010101" fill-opacity=".3">%[2]s</text>
<text x="%[7]d" y="14">%[2]s</text>
</g>
</svg>`,
		total,          // 1
		label,          // 2
		leftW,          // 3
		rightW,         // 4
		color,          // 5
		leftW/2,        // 6
		leftW+rightW/2, // 7
	)
}
