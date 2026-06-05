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
	"log/slog"
	"sync"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const (
	// alertQueueDepth caps how many alerts can sit waiting for the
	// dispatcher before new ones are dropped. State-transition events
	// only fire on actual changes, so this only fills under cascades
	// (mass startup, reload). 256 covers reasonable deployments while
	// keeping memory bounded.
	alertQueueDepth = 256

	// defaultAlertTimeout bounds a single alerter Down/Recover call.
	// Slow webhooks shouldn't pin the dispatcher forever.
	defaultAlertTimeout = 30 * time.Second
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

	// Async alerter dispatch. transitionLocked enqueues into alertCh
	// under e.mu; a worker goroutine drains it without holding the lock,
	// so a slow webhook can't stall ticks or pings.
	alertCh         chan alertJob
	alertWG         sync.WaitGroup // tracks enqueued-plus-in-flight
	alertWorkerDone chan struct{}
	alertTimeout    time.Duration
}

// Options configure New. Zero values fall back to nop implementations
// and a real clock; tests can stub each piece.
type Options struct {
	Bus     EventBus
	Alerter Alerter
	Now     func() time.Time

	// AlertTimeout bounds a single Alerter.Down/Recover call. Zero
	// falls back to defaultAlertTimeout.
	AlertTimeout time.Duration
}

// New builds an Engine, pre-parsing every cron expression and seeding the
// in-memory state cache from the store. Each check that has no stored
// state starts in StatusNew (paused checks immediately become StatusPaused).
// The returned engine has an alert-dispatch goroutine running; Close stops it.
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
	alertTimeout := opts.AlertTimeout
	if alertTimeout <= 0 {
		alertTimeout = defaultAlertTimeout
	}

	e := &Engine{
		reg:             reg,
		store:           st,
		bus:             bus,
		alerter:         alerter,
		now:             nowFn,
		states:          make(map[uuid.UUID]*store.CheckState, len(reg.Checks)),
		crons:           make(map[uuid.UUID]cron.Schedule, len(reg.Checks)),
		alertCh:         make(chan alertJob, alertQueueDepth),
		alertWorkerDone: make(chan struct{}),
		alertTimeout:    alertTimeout,
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

	go e.runAlertWorker()
	return e, nil
}

// Close stops the alert dispatcher and waits for it to drain. Safe to call
// at most once; subsequent calls panic on send to a closed channel.
func (e *Engine) Close() error {
	close(e.alertCh)
	<-e.alertWorkerDone
	return nil
}

// WaitAlerts blocks until every alert queued so far has been dispatched.
// Intended for tests; production code shouldn't need to synchronize on
// alert delivery.
func (e *Engine) WaitAlerts() {
	e.alertWG.Wait()
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

// alertJob is one queued dispatch. The worker calls the appropriate
// Alerter method with a bounded ctx.
type alertJob struct {
	kind  string // "down" | "recover"
	check *config.ResolvedCheck
	trans *Transition
}

// runAlertWorker drains the alert queue until alertCh is closed. Each
// call is bounded by alertTimeout so a single slow webhook can't pin
// the worker indefinitely.
func (e *Engine) runAlertWorker() {
	defer close(e.alertWorkerDone)
	for job := range e.alertCh {
		ctx, cancel := context.WithTimeout(context.Background(), e.alertTimeout)
		var err error
		switch job.kind {
		case "down":
			err = e.alerter.Down(ctx, job.check, job.trans)
		case "recover":
			err = e.alerter.Recover(ctx, job.check, job.trans)
		}
		cancel()
		if err != nil {
			slog.Error("engine: alert dispatch", "kind", job.kind, "slug", job.check.Slug, "err", err)
		}
		e.alertWG.Done()
	}
}

// enqueueAlert pushes a transition onto the worker queue. Non-blocking:
// if the queue is full, the alert is dropped with a warning so the caller
// (which holds the engine lock) never stalls.
func (e *Engine) enqueueAlert(kind string, check *config.ResolvedCheck, trans *Transition) {
	job := alertJob{kind: kind, check: check, trans: trans}
	e.alertWG.Add(1)
	select {
	case e.alertCh <- job:
	default:
		slog.Warn("engine: alert queue full, dropping", "kind", kind, "slug", check.Slug)
		e.alertWG.Done()
	}
}
