// Package engine is cadence's evaluator and state machine. It owns the
// new/up/late/down lifecycle for every check, fires alerts on entry to
// down (plus recovery on return to up), and publishes transitions to an
// event bus so the SSE layer can fan them out.
//
// The engine consumes a config.Registry (definitions, read-only) and a
// store.Store (state, history, events). Definitions never come from the
// store — that's a load-bearing design rule from the spec.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Engine drives the monitoring lifecycle. Safe for concurrent use.
type Engine struct {
	reg     *config.Registry
	store   *store.Store
	bus     EventBus
	alerter Alerter
	now     func() time.Time

	mu     sync.Mutex
	states map[uuid.UUID]*store.CheckState
	crons  map[uuid.UUID]cron.Schedule
}

// Options configure New. Zero values fall back to nop implementations
// and a real clock; tests can stub each piece.
type Options struct {
	Bus     EventBus
	Alerter Alerter
	Now     func() time.Time
}

// New builds an Engine, pre-parsing every cron expression and seeding the
// in-memory state cache from the store. Each check that has no stored
// state starts in StatusNew (paused checks immediately become StatusPaused).
func New(reg *config.Registry, st *store.Store, opts Options) (*Engine, error) {
	bus := opts.Bus
	if bus == nil {
		bus = NopBus{}
	}
	alerter := opts.Alerter
	if alerter == nil {
		alerter = NopAlerter{}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	e := &Engine{
		reg:     reg,
		store:   st,
		bus:     bus,
		alerter: alerter,
		now:     nowFn,
		states:  make(map[uuid.UUID]*store.CheckState, len(reg.Checks)),
		crons:   make(map[uuid.UUID]cron.Schedule, len(reg.Checks)),
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, c := range reg.Checks {
		if c.Cron != "" {
			sched, err := parser.Parse(c.Cron)
			if err != nil {
				return nil, fmt.Errorf("engine: check %q cron %q: %w", c.Slug, c.Cron, err)
			}
			e.crons[c.UUID] = sched
		}
		st, err := e.store.GetState(c.UUID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			initial := initialState(c, nowFn())
			e.states[c.UUID] = &initial
		case err != nil:
			return nil, fmt.Errorf("engine: load state %s: %w", c.Slug, err)
		default:
			// If the check was paused/unpaused since last run, reconcile.
			st.Status = reconcileStatus(st.Status, c)
			cached := st
			e.states[c.UUID] = &cached
		}
	}
	return e, nil
}

// initialState picks the starting status for a check we've never seen.
// Disabled checks land in paused; everything else in new.
func initialState(c *config.ResolvedCheck, now time.Time) store.CheckState {
	status := store.StatusNew
	if !c.Enabled {
		status = store.StatusPaused
	}
	return store.CheckState{
		UUID:           c.UUID,
		Status:         status,
		LastTransition: now,
	}
}

// reconcileStatus flips status to/from paused when the enabled flag has
// flipped between runs. The other transitions are driven by the engine
// itself.
func reconcileStatus(cur store.Status, c *config.ResolvedCheck) store.Status {
	if !c.Enabled {
		return store.StatusPaused
	}
	if cur == store.StatusPaused {
		// Was paused, now enabled — reset to new and let the next ping
		// or tick set the live status.
		return store.StatusNew
	}
	return cur
}

// Run starts the periodic tick loop. Blocks until ctx is cancelled.
// tickInterval controls the resolution of late/down detection — values
// around 1s are reasonable.
func (e *Engine) Run(ctx context.Context, tickInterval time.Duration) error {
	if tickInterval <= 0 {
		tickInterval = time.Second
	}
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			e.Tick(now.UTC())
		}
	}
}

// snapshot returns a copy of the current state for a check (or zero if
// the check is unknown). For external consumers (the API).
func (e *Engine) Snapshot(u uuid.UUID) (store.CheckState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[u]
	if !ok {
		return store.CheckState{}, false
	}
	return *st, true
}

// SnapshotAll returns a copy of every check's current state, keyed by UUID.
// Used by the management API's list endpoint.
func (e *Engine) SnapshotAll() map[uuid.UUID]store.CheckState {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[uuid.UUID]store.CheckState, len(e.states))
	for u, st := range e.states {
		out[u] = *st
	}
	return out
}
