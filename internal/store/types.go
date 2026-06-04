// Package store wraps LevelDB with cadence's runtime data model.
//
// LevelDB only holds runtime state — never check definitions. Definitions
// live in the YAML config and are loaded fresh on start/reload. Keys are
// namespaced by check UUID so a slug rename (which yields a new UUID via
// DeriveUUID) starts a fresh series rather than corrupting the prior one.
package store

import (
	"time"

	"github.com/google/uuid"
)

// Status mirrors the spec's state vocabulary. The HC.io v3 API exposes
// "late" as "grace" — that mapping is applied at the API boundary, not here.
type Status string

const (
	StatusNew    Status = "new"
	StatusUp     Status = "up"
	StatusLate   Status = "late"
	StatusDown   Status = "down"
	StatusPaused Status = "paused"
)

// CheckState is the latest snapshot for one check. Exactly one row per
// check UUID; overwritten on every transition or successful ping.
type CheckState struct {
	UUID           uuid.UUID `json:"uuid"`
	Status         Status    `json:"status"`
	LastPing       time.Time `json:"last_ping,omitempty"`
	LastTransition time.Time `json:"last_transition,omitempty"`

	// Started is true when a /start ping has opened a run that hasn't
	// closed yet. v3 reports this as a top-level boolean rather than
	// merging it into status.
	Started      bool      `json:"started,omitempty"`
	RunStartedAt time.Time `json:"run_started_at,omitempty"`
}

// PingKind enumerates the request shapes the Ping API handles. The set is
// closed and load-bearing for HC.io wire compatibility — don't extend it
// without revisiting the ping handlers.
type PingKind string

const (
	PingSuccess PingKind = "success"
	PingStart   PingKind = "start"
	PingFail    PingKind = "fail"
	PingLog     PingKind = "log"
	PingExit    PingKind = "exit"
)

// Ping is one inbound request, recorded for history. HasBody indicates a
// log entry was stored at the same timestamp; FetchLog(uuid, at) retrieves
// it. The body location is derived (not stored) so a Ping survives a JSON
// round-trip even when the underlying body key contains non-UTF8 bytes.
type Ping struct {
	At         time.Time `json:"at"`
	Kind       PingKind  `json:"kind"`
	ExitCode   int       `json:"exit_code,omitempty"`
	BodyBytes  int       `json:"body_bytes,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
	HasBody    bool      `json:"has_body,omitempty"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// Event records a state transition. Capped per check by retention.events.
type Event struct {
	At     time.Time `json:"at"`
	From   Status    `json:"from"`
	To     Status    `json:"to"`
	Reason string    `json:"reason,omitempty"`
}

// LogEntry is a captured ping body. Indexed by BodyKey on Ping.
type LogEntry struct {
	At        time.Time `json:"at"`
	Body      []byte    `json:"body"`
	Truncated bool      `json:"truncated,omitempty"`
}
