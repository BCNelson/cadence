package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// Options control storage retention. Zero values fall back to spec
// defaults (1000 pings, 500 events, 10KiB bodies) so callers can pass
// the empty struct and get sensible behavior.
type Options struct {
	MaxPings     int
	MaxEvents    int
	MaxBodyBytes int
}

// Defaults mirror the spec's retention section. Centralized so the engine
// and store agree without each one open-coding the numbers.
const (
	DefaultMaxPings     = 1000
	DefaultMaxEvents    = 500
	DefaultMaxBodyBytes = 10 * 1024
)

func (o Options) withDefaults() Options {
	if o.MaxPings <= 0 {
		o.MaxPings = DefaultMaxPings
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = DefaultMaxEvents
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return o
}

// Store is the LevelDB-backed runtime store. Safe for concurrent use
// (LevelDB serializes writes internally; reads can race freely).
type Store struct {
	db   *leveldb.DB
	opts Options
}

// Open initializes a Store at path, creating the LevelDB directory if it
// doesn't exist.
func Open(path string, opts Options) (*Store, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return &Store{db: db, opts: opts.withDefaults()}, nil
}

// Close releases the LevelDB handle. Subsequent calls return an error.
func (s *Store) Close() error { return s.db.Close() }

// ErrNotFound is returned when a lookup misses. Callers can errors.Is it.
var ErrNotFound = errors.New("store: not found")

// GetState loads the latest snapshot for a check. Returns (zero, ErrNotFound)
// if the check has never been touched.
func (s *Store) GetState(u uuid.UUID) (CheckState, error) {
	raw, err := s.db.Get(stateKey(u), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return CheckState{}, ErrNotFound
	}
	if err != nil {
		return CheckState{}, fmt.Errorf("store: get state %s: %w", u, err)
	}
	var st CheckState
	if err := json.Unmarshal(raw, &st); err != nil {
		return CheckState{}, fmt.Errorf("store: decode state %s: %w", u, err)
	}
	return st, nil
}

// SetState overwrites the state row for a check.
func (s *Store) SetState(st *CheckState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("store: encode state: %w", err)
	}
	if err := s.db.Put(stateKey(st.UUID), raw, nil); err != nil {
		return fmt.Errorf("store: put state: %w", err)
	}
	return nil
}

// AppendPing records p and trims the per-check ring to MaxPings. If the
// trim drops pings that have associated log bodies, those log entries are
// also deleted to keep the log namespace from leaking storage.
func (s *Store) AppendPing(u uuid.UUID, p *Ping) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("store: encode ping: %w", err)
	}
	key := tsKey(prefixPing, u, p.At)
	if err := s.db.Put(key, raw, nil); err != nil {
		return fmt.Errorf("store: put ping: %w", err)
	}
	return s.trimPings(u)
}

// trimPings deletes the oldest pings until the count is <= MaxPings. Each
// dropped ping with HasBody also drops the corresponding log entry at the
// derived (uuid, at) location.
func (s *Store) trimPings(u uuid.UUID) error {
	keys, err := s.scanKeys(prefixPing, u)
	if err != nil {
		return err
	}
	excess := len(keys) - s.opts.MaxPings
	if excess <= 0 {
		return nil
	}
	batch := new(leveldb.Batch)
	for i := 0; i < excess; i++ {
		raw, err := s.db.Get(keys[i], nil)
		if err == nil {
			var p Ping
			if json.Unmarshal(raw, &p) == nil && p.HasBody {
				batch.Delete(tsKey(prefixLog, u, p.At))
			}
		}
		batch.Delete(keys[i])
	}
	return s.db.Write(batch, nil)
}

// RecentPings returns up to n pings, newest first. n <= 0 returns all.
func (s *Store) RecentPings(u uuid.UUID, n int) ([]Ping, error) {
	keys, err := s.scanKeys(prefixPing, u)
	if err != nil {
		return nil, err
	}
	if n > 0 && n < len(keys) {
		keys = keys[len(keys)-n:]
	}
	out := make([]Ping, 0, len(keys))
	for i := len(keys) - 1; i >= 0; i-- {
		raw, err := s.db.Get(keys[i], nil)
		if err != nil {
			return nil, fmt.Errorf("store: read ping: %w", err)
		}
		var p Ping
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("store: decode ping: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// AppendEvent records e and trims the per-check ring to MaxEvents.
func (s *Store) AppendEvent(u uuid.UUID, e *Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: encode event: %w", err)
	}
	if err := s.db.Put(tsKey(prefixEvent, u, e.At), raw, nil); err != nil {
		return fmt.Errorf("store: put event: %w", err)
	}
	return s.trimEvents(u)
}

func (s *Store) trimEvents(u uuid.UUID) error {
	keys, err := s.scanKeys(prefixEvent, u)
	if err != nil {
		return err
	}
	excess := len(keys) - s.opts.MaxEvents
	if excess <= 0 {
		return nil
	}
	batch := new(leveldb.Batch)
	for i := 0; i < excess; i++ {
		batch.Delete(keys[i])
	}
	return s.db.Write(batch, nil)
}

// RecentEvents returns up to n events, newest first.
func (s *Store) RecentEvents(u uuid.UUID, n int) ([]Event, error) {
	keys, err := s.scanKeys(prefixEvent, u)
	if err != nil {
		return nil, err
	}
	if n > 0 && n < len(keys) {
		keys = keys[len(keys)-n:]
	}
	out := make([]Event, 0, len(keys))
	for i := len(keys) - 1; i >= 0; i-- {
		raw, err := s.db.Get(keys[i], nil)
		if err != nil {
			return nil, fmt.Errorf("store: read event: %w", err)
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("store: decode event: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// StoreBody persists a request body at the (uuid, at) location, returning
// the truncation flag. Bodies larger than MaxBodyBytes are clipped.
// Callers should set Ping.HasBody = true and use the same (uuid, at) to
// fetch.
func (s *Store) StoreBody(u uuid.UUID, at time.Time, body []byte) (truncated bool, err error) {
	if len(body) > s.opts.MaxBodyBytes {
		body = body[:s.opts.MaxBodyBytes]
		truncated = true
	}
	entry := LogEntry{At: at, Body: body, Truncated: truncated}
	raw, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("store: encode log: %w", err)
	}
	if err := s.db.Put(tsKey(prefixLog, u, at), raw, nil); err != nil {
		return false, fmt.Errorf("store: put log: %w", err)
	}
	return truncated, nil
}

// FetchLog reads a captured body for a specific ping. Returns ErrNotFound
// if no body was stored (e.g. the ping didn't carry one, or the ring evicted it).
func (s *Store) FetchLog(u uuid.UUID, at time.Time) (LogEntry, error) {
	raw, err := s.db.Get(tsKey(prefixLog, u, at), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return LogEntry{}, ErrNotFound
	}
	if err != nil {
		return LogEntry{}, fmt.Errorf("store: get log: %w", err)
	}
	var le LogEntry
	if err := json.Unmarshal(raw, &le); err != nil {
		return LogEntry{}, fmt.Errorf("store: decode log: %w", err)
	}
	return le, nil
}

// MaxBodyBytes exposes the configured body cap so the Ping API can
// advertise it in the X-Cadence-Body-Limit response header.
func (s *Store) MaxBodyBytes() int { return s.opts.MaxBodyBytes }

// scanKeys returns every key under (prefix, u) in ascending order. Used
// by retention trimming and by RecentX readers.
func (s *Store) scanKeys(prefix string, u uuid.UUID) ([][]byte, error) {
	start, limit := keyRange(prefix, u)
	it := s.db.NewIterator(&util.Range{Start: start, Limit: limit}, nil)
	defer it.Release()
	return collectKeys(it)
}

func collectKeys(it iterator.Iterator) ([][]byte, error) {
	var out [][]byte
	for it.Next() {
		k := it.Key()
		// Iterator buffer is reused — copy before storing.
		dup := make([]byte, len(k))
		copy(dup, k)
		out = append(out, dup)
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("store: iterate: %w", err)
	}
	return out, nil
}
