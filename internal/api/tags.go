package api

import (
	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
)

// rollupStatus collapses a set of checks down to one combined status using
// worst-wins: down > late > new > up. Paused checks are excluded from the
// rollup so pausing a noisy member doesn't taint the tag — unless every
// member is paused, in which case the tag reports paused. Empty input
// returns "" so callers can distinguish "no members" from a real status.
func (h *MgmtHandler) rollupStatus(checks []*config.ResolvedCheck) store.Status {
	if len(checks) == 0 {
		return ""
	}
	// Higher rank = worse.
	rank := func(s store.Status) int {
		switch s {
		case store.StatusDown:
			return 4
		case store.StatusLate:
			return 3
		case store.StatusNew:
			return 2
		case store.StatusUp:
			return 1
		default:
			return 0
		}
	}
	worst := store.Status("")
	worstRank := -1
	sawActive := false
	for _, c := range checks {
		snap, _ := h.engine.Snapshot(c.UUID)
		if snap.Status == store.StatusPaused {
			continue
		}
		sawActive = true
		if r := rank(snap.Status); r > worstRank {
			worstRank = r
			worst = snap.Status
		}
	}
	if !sawActive {
		return store.StatusPaused
	}
	return worst
}

// hasAllTags returns true when every requested tag is present on the
// check. An empty `wanted` matches everything (so callers don't need a
// special branch for "no filter").
func hasAllTags(c *config.ResolvedCheck, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(c.Tags))
	for _, t := range c.Tags {
		have[t] = struct{}{}
	}
	for _, w := range wanted {
		if _, ok := have[w]; !ok {
			return false
		}
	}
	return true
}

// tagBuckets groups every registered check by tag. A check with N tags
// appears in N buckets. The map is keyed by raw tag string (no
// case-folding — tags are passed through verbatim from YAML).
func (h *MgmtHandler) tagBuckets() map[string][]*config.ResolvedCheck {
	out := make(map[string][]*config.ResolvedCheck)
	for _, c := range h.registry.Checks {
		for _, t := range c.Tags {
			out[t] = append(out[t], c)
		}
	}
	return out
}
