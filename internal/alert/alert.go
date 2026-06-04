// Package alert dispatches webhook notifications when checks transition
// into down (and back to up). It implements engine.Alerter.
//
// Payload shape (v1, resolves spec open-question #2):
//
//	{
//	  "event":     "down" | "recover",
//	  "check": {
//	    "slug":   "...",
//	    "name":   "...",
//	    "uuid":   "...",
//	    "tags":   ["..."],
//	    "status": "down" | "up"
//	  },
//	  "from":   "<previous status>",
//	  "to":     "<new status>",
//	  "at":     "<RFC3339 UTC>",
//	  "reason": "<engine reason string>"
//	}
//
// Per-channel templating is a future enhancement; v1 ships one canonical
// shape so channels can be wired up against a stable contract.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/google/uuid"
)

// Webhook fires HTTP webhooks for each channel attached to a check.
// Implements engine.Alerter.
type Webhook struct {
	channels map[string]config.Channel
	client   *http.Client
}

// Options configures the dispatcher. Zero values are sensible: a 10s
// HTTP timeout and the default http.Client transport.
type Options struct {
	Timeout time.Duration
	Client  *http.Client
}

// New builds a Webhook dispatcher from the resolved channel registry.
func New(channels map[string]config.Channel, opts Options) *Webhook {
	c := opts.Client
	if c == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		c = &http.Client{Timeout: timeout}
	}
	return &Webhook{channels: channels, client: c}
}

// Down fires a "down" notification to every channel on the check.
func (w *Webhook) Down(ctx context.Context, check *config.ResolvedCheck, t *engine.Transition) error {
	return w.dispatch(ctx, check, t, "down")
}

// Recover fires a "recover" notification to every channel on the check.
func (w *Webhook) Recover(ctx context.Context, check *config.ResolvedCheck, t *engine.Transition) error {
	return w.dispatch(ctx, check, t, "recover")
}

// dispatch builds the payload once and sends it to each channel
// concurrently. A failure on one channel does not block the others; the
// returned error joins all per-channel errors. The engine logs whatever
// comes back but does not retry — channels handle their own retry/queue
// semantics if they need them.
func (w *Webhook) dispatch(ctx context.Context, check *config.ResolvedCheck, t *engine.Transition, event string) error {
	payload, err := json.Marshal(buildPayload(event, check, t))
	if err != nil {
		return fmt.Errorf("alert: marshal payload: %w", err)
	}

	type result struct {
		channel string
		err     error
	}
	results := make(chan result, len(check.Channels))
	for _, name := range check.Channels {
		channel, ok := w.channels[name]
		if !ok {
			// Validation in config.resolve already catches unknown
			// channel references, so reaching here means an internal
			// inconsistency — log and skip rather than fail the whole
			// dispatch.
			slog.Error("alert: missing channel from registry", "channel", name, "slug", check.Slug)
			results <- result{channel: name, err: nil}
			continue
		}
		go func(n string, ch config.Channel) {
			results <- result{channel: n, err: w.fire(ctx, ch, payload)}
		}(name, channel)
	}

	var errs []error
	for range check.Channels {
		r := <-results
		if r.err != nil {
			errs = append(errs, fmt.Errorf("channel %q: %w", r.channel, r.err))
		}
	}
	return errors.Join(errs...)
}

// fire sends one HTTP request to one channel and reads the response just
// enough to detect non-2xx. Reading the body keeps connection reuse
// healthy under net/http.
func (w *Webhook) fire(ctx context.Context, ch config.Channel, payload []byte) error {
	method := ch.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, ch.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cadence/v1")
	for k, v := range ch.Headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// payload is the v1 alert payload shape. Documented at the package level.
type payload struct {
	Event  string       `json:"event"`
	Check  checkSummary `json:"check"`
	From   string       `json:"from"`
	To     string       `json:"to"`
	At     string       `json:"at"`
	Reason string       `json:"reason,omitempty"`
}

type checkSummary struct {
	Slug   string    `json:"slug"`
	Name   string    `json:"name,omitempty"`
	UUID   uuid.UUID `json:"uuid"`
	Tags   []string  `json:"tags,omitempty"`
	Status string    `json:"status"`
}

func buildPayload(event string, check *config.ResolvedCheck, t *engine.Transition) payload {
	return payload{
		Event: event,
		Check: checkSummary{
			Slug:   check.Slug,
			Name:   check.Name,
			UUID:   check.UUID,
			Tags:   check.Tags,
			Status: string(t.To),
		},
		From:   string(t.From),
		To:     string(t.To),
		At:     t.At.UTC().Format(time.RFC3339),
		Reason: t.Reason,
	}
}
