package engine

import (
	"context"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// Transition is what the engine publishes on every state change. The SSE
// fan-out subscribes here.
type Transition struct {
	CheckUUID uuid.UUID    `json:"check_uuid"`
	Slug      string       `json:"slug"`
	From      store.Status `json:"from"`
	To        store.Status `json:"to"`
	At        time.Time    `json:"at"`
	Reason    string       `json:"reason,omitempty"`
}

// EventBus receives every state transition. Implementations should never
// block — the engine holds its mutex while publishing. The pointer is
// for cheapness; implementations must not retain or mutate it.
type EventBus interface {
	Publish(t *Transition)
}

// NopBus discards every transition. Useful in tests and for the
// pre-wiring phase before the SSE layer is in place.
type NopBus struct{}

func (NopBus) Publish(*Transition) {}

// Alerter dispatches webhook notifications. Down fires once on entry to
// down; Recover fires when a down check returns to up. Implementations
// run synchronously from the engine's perspective — they should manage
// their own goroutines / queues if real I/O would slow ticks.
type Alerter interface {
	Down(ctx context.Context, check *config.ResolvedCheck, t *Transition) error
	Recover(ctx context.Context, check *config.ResolvedCheck, t *Transition) error
}

// NopAlerter does nothing. Pre-wiring placeholder.
type NopAlerter struct{}

func (NopAlerter) Down(context.Context, *config.ResolvedCheck, *Transition) error    { return nil }
func (NopAlerter) Recover(context.Context, *config.ResolvedCheck, *Transition) error { return nil }
