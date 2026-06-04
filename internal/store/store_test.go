package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newTestStore opens a Store in a temp dir with overridable retention.
// Used by every test to keep setup terse.
func newTestStore(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStateRoundTrip(t *testing.T) {
	s := newTestStore(t, Options{})
	u := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	if _, err := s.GetState(u); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty store: want ErrNotFound, got %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	want := CheckState{
		UUID:           u,
		Status:         StatusUp,
		LastPing:       now,
		LastTransition: now,
		Started:        true,
		RunStartedAt:   now.Add(-time.Minute),
	}
	if err := s.SetState(&want); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, err := s.GetState(u)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.Status != want.Status || !got.LastPing.Equal(want.LastPing) || got.Started != want.Started {
		t.Errorf("state mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestPingRingTrim(t *testing.T) {
	s := newTestStore(t, Options{MaxPings: 3})
	u := uuid.New()
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 5; i++ {
		if err := s.AppendPing(u, &Ping{At: base.Add(time.Duration(i) * time.Second), Kind: PingSuccess}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentPings(u, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("ring not trimmed: got %d, want 3", len(got))
	}
	// RecentPings is newest-first; the trimmed set should be i=2,3,4.
	if !got[0].At.Equal(base.Add(4 * time.Second)) {
		t.Errorf("newest ping wrong: got %v", got[0].At)
	}
	if !got[2].At.Equal(base.Add(2 * time.Second)) {
		t.Errorf("oldest retained ping wrong: got %v", got[2].At)
	}
}

func TestPingRingDropsAssociatedBody(t *testing.T) {
	s := newTestStore(t, Options{MaxPings: 2})
	u := uuid.New()
	base := time.Unix(1_700_000_000, 0).UTC()

	// Three pings, each with a body. The oldest should be evicted from
	// both the ping ring and the log namespace.
	firstAt := base
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		if _, err := s.StoreBody(u, at, []byte("payload")); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendPing(u, &Ping{At: at, Kind: PingSuccess, HasBody: true, BodyBytes: 7}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.FetchLog(u, firstAt); !errors.Is(err, ErrNotFound) {
		t.Errorf("evicted ping's body should be gone too, got err=%v", err)
	}
}

func TestEventsCapped(t *testing.T) {
	s := newTestStore(t, Options{MaxEvents: 2})
	u := uuid.New()
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 4; i++ {
		if err := s.AppendEvent(u, &Event{
			At:     base.Add(time.Duration(i) * time.Second),
			From:   StatusUp,
			To:     StatusDown,
			Reason: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentEvents(u, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("events not capped: got %d", len(got))
	}
}

func TestBodyTruncation(t *testing.T) {
	s := newTestStore(t, Options{MaxBodyBytes: 4})
	u := uuid.New()
	at := time.Now().UTC()
	truncated, err := s.StoreBody(u, at, []byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Error("body should report truncation")
	}
	le, err := s.FetchLog(u, at)
	if err != nil {
		t.Fatal(err)
	}
	if string(le.Body) != "0123" {
		t.Errorf("truncated body wrong: got %q", string(le.Body))
	}
}

func TestNamespacesAreIsolatedByUUID(t *testing.T) {
	s := newTestStore(t, Options{})
	a := uuid.New()
	b := uuid.New()
	base := time.Unix(1_700_000_000, 0).UTC()

	if err := s.AppendPing(a, &Ping{At: base, Kind: PingSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPing(b, &Ping{At: base.Add(time.Second), Kind: PingFail}); err != nil {
		t.Fatal(err)
	}

	pa, _ := s.RecentPings(a, 0)
	pb, _ := s.RecentPings(b, 0)
	if len(pa) != 1 || pa[0].Kind != PingSuccess {
		t.Errorf("a's pings leaked: %+v", pa)
	}
	if len(pb) != 1 || pb[0].Kind != PingFail {
		t.Errorf("b's pings leaked: %+v", pb)
	}
}

func TestReopenPreservesData(t *testing.T) {
	dir := t.TempDir()
	u := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)

	s1, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SetState(&CheckState{UUID: u, Status: StatusDown, LastTransition: now}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()

	got, err := s2.GetState(u)
	if err != nil {
		t.Fatalf("GetState after reopen: %v", err)
	}
	if got.Status != StatusDown {
		t.Errorf("state lost across reopen: got %q", got.Status)
	}
}

func TestRecentPingsLimit(t *testing.T) {
	s := newTestStore(t, Options{})
	u := uuid.New()
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 10; i++ {
		if err := s.AppendPing(u, &Ping{At: base.Add(time.Duration(i) * time.Second), Kind: PingSuccess}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentPings(u, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit not honored: got %d", len(got))
	}
	if !got[0].At.Equal(base.Add(9 * time.Second)) {
		t.Errorf("expected newest first, got %v", got[0].At)
	}
}

func TestMaxBodyBytesReflectsOptions(t *testing.T) {
	s := newTestStore(t, Options{MaxBodyBytes: 4096})
	if got := s.MaxBodyBytes(); got != 4096 {
		t.Errorf("explicit MaxBodyBytes: got %d, want 4096", got)
	}

	d := newTestStore(t, Options{})
	if got := d.MaxBodyBytes(); got != DefaultMaxBodyBytes {
		t.Errorf("default MaxBodyBytes: got %d, want %d", got, DefaultMaxBodyBytes)
	}
}

func TestDefaultsApplied(t *testing.T) {
	o := Options{}.withDefaults()
	if o.MaxPings != DefaultMaxPings || o.MaxEvents != DefaultMaxEvents || o.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("defaults wrong: %+v", o)
	}
	if !strings.HasPrefix(string(StatusUp), "up") {
		t.Error("status constants should be plain strings")
	}
}
