package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// PingRequest is the engine's view of an inbound ping. The Ping API
// translates HTTP into this and hands it off.
type PingRequest struct {
	Kind       store.PingKind
	ExitCode   int
	Body       []byte
	RemoteAddr string
	UserAgent  string
}

// ErrUnknownCheck is returned when HandlePing is called with a UUID the
// engine doesn't know about. Callers map this to a 404 at the HTTP layer.
var ErrUnknownCheck = errors.New("engine: unknown check uuid")

// HandlePing applies a ping to a check and persists the resulting state +
// history. Mutates the check's state under the engine lock.
func (e *Engine) HandlePing(u uuid.UUID, req *PingRequest) error {
	check := e.reg.CheckByUUID(u)
	if check == nil {
		return ErrUnknownCheck
	}

	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	cur, ok := e.states[u]
	if !ok {
		// Shouldn't happen — New seeds states for every registered check.
		// Guard anyway so a missing-state bug doesn't deadlock the engine.
		seeded := initialState(check, now)
		cur = &seeded
		e.states[u] = cur
	}

	// Paused checks ignore pings entirely. A paused check can't pingactivate
	// itself — it has to be unpaused in config.
	if cur.Status == store.StatusPaused {
		return nil
	}

	// Enforce monotonic ping timestamps per check. Coarse clocks (and
	// fixed test clocks) can repeat — without this bump, two pings that
	// land in the same nanosecond would share a store key and the second
	// would silently overwrite the first.
	if !cur.LastPing.IsZero() && !now.After(cur.LastPing) {
		now = cur.LastPing.Add(time.Nanosecond)
	}
	cur.LastPing = now

	// Persist a Ping record. HasBody is set lazily after the body is
	// stored, since the store does the size cap.
	pingRec := store.Ping{
		At:         now,
		Kind:       req.Kind,
		ExitCode:   req.ExitCode,
		RemoteAddr: req.RemoteAddr,
		UserAgent:  req.UserAgent,
		BodyBytes:  len(req.Body),
	}
	if len(req.Body) > 0 {
		truncated, err := e.store.StoreBody(u, now, req.Body)
		if err != nil {
			return fmt.Errorf("engine: store body: %w", err)
		}
		pingRec.HasBody = true
		pingRec.Truncated = truncated
	}
	if err := e.store.AppendPing(u, &pingRec); err != nil {
		return fmt.Errorf("engine: append ping: %w", err)
	}

	// Apply state transitions based on the ping kind. transitionLocked
	// updates in-memory state but does not persist — we always SetState
	// below so LastPing and any Started toggle land on disk in one call.
	switch req.Kind {
	case store.PingStart:
		cur.Started = true
		cur.RunStartedAt = now
		// /start does not change status — a running check stays in its
		// current health bucket until success/fail closes the run.
	case store.PingSuccess:
		cur.Started = false
		cur.RunStartedAt = time.Time{}
		e.transitionLocked(check, cur, store.StatusUp, now, "success ping")
	case store.PingFail:
		cur.Started = false
		cur.RunStartedAt = time.Time{}
		e.transitionLocked(check, cur, store.StatusDown, now, "fail ping")
	case store.PingExit:
		cur.Started = false
		cur.RunStartedAt = time.Time{}
		if req.ExitCode == 0 {
			e.transitionLocked(check, cur, store.StatusUp, now, "exit 0")
		} else {
			e.transitionLocked(check, cur, store.StatusDown, now,
				fmt.Sprintf("exit %d", req.ExitCode))
		}
	case store.PingLog:
		// /log records the body and ping but does not touch status — by
		// design, log pings are observational only.
	}

	if err := e.store.SetState(cur); err != nil {
		return fmt.Errorf("engine: persist state: %w", err)
	}
	return nil
}
