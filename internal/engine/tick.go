package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
)

// Tick evaluates every check against `now` and applies any pending
// transitions: up -> late (period or cron slot elapsed), late -> down
// (grace exhausted), or run-timeout -> down. Pure side effects: alerts
// fire and the store is updated; pings remain unchanged.
//
// Exposed so tests can drive the clock deterministically without spinning
// up Run.
func (e *Engine) Tick(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, c := range e.reg.Checks {
		st, ok := e.states[c.UUID]
		if !ok {
			continue
		}
		e.tickOne(c, st, now)
	}
}

// tickOne advances a single check at `now`. Caller holds the engine lock.
func (e *Engine) tickOne(c *config.ResolvedCheck, st *store.CheckState, now time.Time) {
	if st.Status == store.StatusPaused {
		return
	}

	// Open-run timeout takes precedence over schedule checks: a run that's
	// blown its timeout fails the check immediately.
	if st.Started && c.Timeout > 0 && now.Sub(st.RunStartedAt) > c.Timeout {
		st.Started = false
		st.RunStartedAt = time.Time{}
		e.transitionLocked(c, st, store.StatusDown, now, "run timeout")
		return
	}

	if st.Status == store.StatusNew {
		// First-ping-never-happened state. Don't auto-down — wait for the
		// service to introduce itself. This matches HC.io.
		return
	}

	deadline, ok := nextDeadline(c, e.cronFor(c), st.LastPing)
	if !ok {
		return
	}

	switch st.Status {
	case store.StatusUp:
		if now.After(deadline.Add(c.Grace)) {
			e.transitionLocked(c, st, store.StatusDown, now, "grace exhausted")
			return
		}
		if now.After(deadline) {
			e.transitionLocked(c, st, store.StatusLate, now, "deadline missed")
		}
	case store.StatusLate:
		if now.After(deadline.Add(c.Grace)) {
			e.transitionLocked(c, st, store.StatusDown, now, "grace exhausted")
		}
	case store.StatusDown:
		// Stays down until a success ping arrives. No further transitions
		// on tick.
	}
}

// cronFor returns the parsed cron schedule for a check, or nil for
// period-based checks. Lookup-only — must be called under the engine lock.
func (e *Engine) cronFor(c *config.ResolvedCheck) cronSchedule {
	if c.Cron == "" {
		return nil
	}
	if s, ok := e.crons[c.UUID]; ok {
		return s
	}
	return nil
}

// cronSchedule is the subset of robfig/cron's Schedule that we need.
// Defined as a local interface so tests can stub it.
type cronSchedule interface {
	Next(time.Time) time.Time
}

// transitionLocked records a status change: updates state, persists,
// publishes to the bus, fires alerts. Caller holds the engine lock.
func (e *Engine) transitionLocked(c *config.ResolvedCheck, st *store.CheckState, to store.Status, at time.Time, reason string) {
	from := st.Status
	if from == to {
		// Still persist LastPing changes.
		if err := e.store.SetState(st); err != nil {
			slog.Error("engine: persist state", "err", err, "slug", c.Slug)
		}
		return
	}
	st.Status = to
	st.LastTransition = at

	ev := store.Event{At: at, From: from, To: to, Reason: reason}
	if err := e.store.AppendEvent(c.UUID, &ev); err != nil {
		slog.Error("engine: append event", "err", err, "slug", c.Slug)
	}
	if err := e.store.SetState(st); err != nil {
		slog.Error("engine: persist state", "err", err, "slug", c.Slug)
	}

	trans := &Transition{
		CheckUUID: c.UUID,
		Slug:      c.Slug,
		From:      from,
		To:        to,
		At:        at,
		Reason:    reason,
	}
	e.bus.Publish(trans)

	// Alerts. Fire once on entry to down; recovery on return to up from
	// down. The single-fire is automatic because we only call this on a
	// state CHANGE, not while down stays down.
	ctx := context.Background()
	switch {
	case to == store.StatusDown:
		if err := e.alerter.Down(ctx, c, trans); err != nil {
			slog.Error("engine: alert down", "err", err, "slug", c.Slug)
		}
	case to == store.StatusUp && from == store.StatusDown:
		if err := e.alerter.Recover(ctx, c, trans); err != nil {
			slog.Error("engine: alert recover", "err", err, "slug", c.Slug)
		}
	}
}

// nextDeadline returns the time by which the check should have pinged in
// order to stay up. For period checks: lastPing + period. For cron checks:
// the next scheduled slot after lastPing. Returns ok=false for checks in
// the new state with no lastPing.
func nextDeadline(c *config.ResolvedCheck, sched cronSchedule, lastPing time.Time) (time.Time, bool) {
	if lastPing.IsZero() {
		return time.Time{}, false
	}
	if sched != nil {
		return sched.Next(lastPing), true
	}
	if c.Period <= 0 {
		return time.Time{}, false
	}
	return lastPing.Add(c.Period), true
}
