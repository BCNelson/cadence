package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/google/uuid"
)

// loadRegistry writes yaml into a temp file and loads it through the real
// config pipeline so the registry has its internal lookup tables populated
// the same way production does.
func loadRegistry(t *testing.T, yaml string) *config.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := config.Load([]string{path}, config.Options{Env: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return reg
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// recordingAlerter captures every alert so tests can assert on call sites.
type recordingAlerter struct {
	mu       sync.Mutex
	downs    []Transition
	recovers []Transition
}

func (r *recordingAlerter) Down(_ context.Context, _ *config.ResolvedCheck, t *Transition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downs = append(r.downs, *t)
	return nil
}
func (r *recordingAlerter) Recover(_ context.Context, _ *config.ResolvedCheck, t *Transition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recovers = append(r.recovers, *t)
	return nil
}
func (r *recordingAlerter) downCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.downs)
}
func (r *recordingAlerter) recoverCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.recovers)
}

// recordingBus captures published transitions.
type recordingBus struct {
	mu sync.Mutex
	t  []Transition
}

func (r *recordingBus) Publish(t *Transition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.t = append(r.t, *t)
}

// fixedClock returns a clock that always reports the same time. The
// returned setter advances it.
func fixedClock(start time.Time) (get func() time.Time, set func(time.Time)) {
	var mu sync.Mutex
	now := start
	get = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	set = func(t time.Time) {
		mu.Lock()
		defer mu.Unlock()
		now = t
	}
	return get, set
}

func TestFirstPingMovesNewToUp(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`)
	st := openStore(t)
	now, setNow := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	alerter := &recordingAlerter{}
	bus := &recordingBus{}

	e, err := New(reg, st, Options{Bus: bus, Alerter: alerter, Now: now})
	if err != nil {
		t.Fatal(err)
	}

	c := reg.CheckBySlug("api")
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusNew {
		t.Fatalf("initial status: got %q", snap.Status)
	}

	setNow(now().Add(time.Second))
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess}); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.Snapshot(c.UUID)
	if snap.Status != store.StatusUp {
		t.Errorf("after success ping: status %q", snap.Status)
	}
	if alerter.downCount() != 0 || alerter.recoverCount() != 0 {
		t.Errorf("first-success should not alert: %+v / %+v", alerter.downs, alerter.recovers)
	}
}

func TestPeriodElapsedLateThenDown(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m, grace: 5m }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e, err := New(reg, st, Options{Now: now, Alerter: alerter})
	if err != nil {
		t.Fatal(err)
	}
	c := reg.CheckBySlug("api")

	// Start in up by sending a success.
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess}); err != nil {
		t.Fatal(err)
	}

	// Half-period in: still up, no transitions.
	setNow(base.Add(5 * time.Minute))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("mid-period: %q", snap.Status)
	}

	// 11 minutes in: deadline (10m) passed but grace (5m more) not yet.
	setNow(base.Add(11 * time.Minute))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusLate {
		t.Errorf("past deadline: %q", snap.Status)
	}
	if alerter.downCount() != 0 {
		t.Error("late should not alert")
	}

	// 16 minutes in: grace exhausted.
	setNow(base.Add(16 * time.Minute))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("past grace: %q", snap.Status)
	}
	if alerter.downCount() != 1 {
		t.Errorf("down alert count: got %d, want 1", alerter.downCount())
	}

	// Further ticks while down must not fire more alerts.
	setNow(base.Add(20 * time.Minute))
	e.Tick(now())
	setNow(base.Add(30 * time.Minute))
	e.Tick(now())
	if alerter.downCount() != 1 {
		t.Errorf("repeat ticks alerted again: got %d, want 1", alerter.downCount())
	}
}

func TestRecoveryAlertOnSuccessAfterDown(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m, grace: 5m }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	setNow(base.Add(20 * time.Minute))
	e.Tick(now())
	if alerter.downCount() != 1 {
		t.Fatalf("setup: want down, got %d alerts", alerter.downCount())
	}

	setNow(base.Add(25 * time.Minute))
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("recovery status: %q", snap.Status)
	}
	if alerter.recoverCount() != 1 {
		t.Errorf("want 1 recovery alert, got %d", alerter.recoverCount())
	}
}

func TestFailPingImmediateDownAndOneAlert(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("after fail: %q", snap.Status)
	}
	if alerter.downCount() != 1 {
		t.Errorf("want 1 down alert, got %d", alerter.downCount())
	}

	// Second fail while already down should NOT re-alert (no state change).
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}
	if alerter.downCount() != 1 {
		t.Errorf("repeat fail alerted again: got %d", alerter.downCount())
	}
}

func TestExitCodeRouting(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingExit, ExitCode: 0})
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("exit 0: %q", snap.Status)
	}
	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingExit, ExitCode: 17})
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("exit 17: %q", snap.Status)
	}
}

func TestStartOpensRunAndSuccessCloses(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h, timeout: 30s }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	e, _ := New(reg, st, Options{Now: now})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingStart})
	snap, _ := e.Snapshot(c.UUID)
	if !snap.Started {
		t.Error("after /start: Started should be true")
	}
	// Status stays in new until success.
	if snap.Status != store.StatusNew {
		t.Errorf("/start changed status: %q", snap.Status)
	}

	setNow(base.Add(5 * time.Second))
	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	snap, _ = e.Snapshot(c.UUID)
	if snap.Started {
		t.Error("after success: Started should be false")
	}
	if snap.Status != store.StatusUp {
		t.Errorf("after success: %q", snap.Status)
	}
}

func TestRunTimeoutMakesDown(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h, timeout: 30s }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingStart})
	setNow(base.Add(45 * time.Second))
	e.Tick(now())
	snap, _ := e.Snapshot(c.UUID)
	if snap.Status != store.StatusDown {
		t.Errorf("after run timeout: %q", snap.Status)
	}
	if snap.Started {
		t.Error("Started should clear on timeout")
	}
	if alerter.downCount() != 1 {
		t.Errorf("want 1 timeout alert, got %d", alerter.downCount())
	}
}

func TestPausedIgnoresEverything(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1m, grace: 1m, enabled: false }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusPaused {
		t.Fatalf("initial: %q", snap.Status)
	}
	// Ping should not move it out of paused.
	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusPaused {
		t.Errorf("after ping: %q", snap.Status)
	}
	// Tick way past deadlines — still paused.
	setNow(base.Add(time.Hour))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusPaused {
		t.Errorf("after tick: %q", snap.Status)
	}
	if alerter.downCount() != 0 {
		t.Errorf("paused alerted: %d", alerter.downCount())
	}
}

func TestLogPingDoesNotChangeStatus(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e, _ := New(reg, st, Options{Now: now})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingLog, Body: []byte("hello")})
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("after /log: %q", snap.Status)
	}
	// Body should be recorded.
	pings, _ := st.RecentPings(c.UUID, 0)
	if len(pings) != 2 {
		t.Fatalf("pings: %d", len(pings))
	}
	logPing := pings[0]
	if !logPing.HasBody || logPing.BodyBytes != 5 {
		t.Errorf("/log body not captured: %+v", logPing)
	}
}

func TestUnknownCheckRejected(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e, _ := New(reg, st, Options{Now: now})

	err := e.HandlePing(uuid.New(), &PingRequest{Kind: store.PingSuccess})
	if !errors.Is(err, ErrUnknownCheck) {
		t.Errorf("want ErrUnknownCheck, got %v", err)
	}
}

func TestCronMissedSlotGoesLateAndDown(t *testing.T) {
	// Every minute, at second 0. The schedule.Next from t returns the
	// next whole-minute boundary > t.
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, cron: "* * * * *", grace: 30s }
`)
	st := openStore(t)
	// Base at minute boundary so cron math is easy to follow.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e, _ := New(reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	// schedule.Next(base) = 12:01:00 — the next expected ping.

	// 20s later: still in the same slot, up.
	setNow(base.Add(20 * time.Second))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusUp {
		t.Errorf("inside slot: %q", snap.Status)
	}

	// 12:01:15 — past the expected ping by 15s, under grace (30s).
	setNow(base.Add(75 * time.Second))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusLate {
		t.Errorf("past slot, within grace: %q", snap.Status)
	}

	// 12:01:45 — past grace.
	setNow(base.Add(105 * time.Second))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("past grace: %q", snap.Status)
	}
	if alerter.downCount() != 1 {
		t.Errorf("want 1 down alert, got %d", alerter.downCount())
	}
}

func TestStatePersistedAcrossEngineRestart(t *testing.T) {
	yaml := `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`
	reg := loadRegistry(t, yaml)
	dir := t.TempDir()
	st1, err := store.Open(dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e1, _ := New(reg, st1, Options{Now: now})
	c := reg.CheckBySlug("api")
	_ = e1.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	_ = st1.Close()

	// Reload from a fresh registry pointing at the same store.
	reg2 := loadRegistry(t, yaml)
	st2, err := store.Open(dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()
	e2, _ := New(reg2, st2, Options{Now: now})
	c2 := reg2.CheckBySlug("api")
	snap, _ := e2.Snapshot(c2.UUID)
	if snap.Status != store.StatusUp {
		t.Errorf("status lost across restart: %q", snap.Status)
	}
}

func TestRunBlocksUntilContextCancel(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e, _ := New(reg, st, Options{Now: now})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, 10*time.Millisecond) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
