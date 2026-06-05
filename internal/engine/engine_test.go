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

// newEngine wraps New with a t.Cleanup that stops the alert worker, so
// individual tests don't leak goroutines under -race.
func newEngine(t *testing.T, reg *config.Registry, st *store.Store, opts Options) *Engine {
	t.Helper()
	e, err := New(reg, st, opts)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
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

	e := newEngine(t, reg, st, Options{Bus: bus, Alerter: alerter, Now: now})

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
	e.WaitAlerts()
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
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
	e.WaitAlerts()
	if alerter.downCount() != 0 {
		t.Error("late should not alert")
	}

	// 16 minutes in: grace exhausted.
	setNow(base.Add(16 * time.Minute))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("past grace: %q", snap.Status)
	}
	e.WaitAlerts()
	if alerter.downCount() != 1 {
		t.Errorf("down alert count: got %d, want 1", alerter.downCount())
	}

	// Further ticks while down must not fire more alerts.
	setNow(base.Add(20 * time.Minute))
	e.Tick(now())
	setNow(base.Add(30 * time.Minute))
	e.Tick(now())
	e.WaitAlerts()
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	setNow(base.Add(20 * time.Minute))
	e.Tick(now())
	e.WaitAlerts()
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
	e.WaitAlerts()
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := e.Snapshot(c.UUID); snap.Status != store.StatusDown {
		t.Errorf("after fail: %q", snap.Status)
	}
	e.WaitAlerts()
	if alerter.downCount() != 1 {
		t.Errorf("want 1 down alert, got %d", alerter.downCount())
	}

	// Second fail while already down should NOT re-alert (no state change).
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}
	e.WaitAlerts()
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
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
	e := newEngine(t, reg, st, Options{Now: now})
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
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
	e.WaitAlerts()
	if alerter.downCount() != 1 {
		t.Errorf("want 1 timeout alert, got %d", alerter.downCount())
	}
}

// TestRunTimeoutFallsBackToPeriodPlusGrace covers C2: a check with no
// explicit `timeout:` still rolls a /start run to down once period+grace
// elapses, instead of leaving the run open forever.
func TestRunTimeoutFallsBackToPeriodPlusGrace(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m, grace: 2m }
`)
	st := openStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	now, setNow := fixedClock(base)
	alerter := &recordingAlerter{}
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingStart})

	// 11 minutes in: still within period+grace (12m). Run stays open.
	setNow(base.Add(11 * time.Minute))
	e.Tick(now())
	if snap, _ := e.Snapshot(c.UUID); !snap.Started {
		t.Error("run closed too early")
	}

	// 13 minutes in: past period+grace; the fallback timeout fires.
	setNow(base.Add(13 * time.Minute))
	e.Tick(now())
	snap, _ := e.Snapshot(c.UUID)
	if snap.Status != store.StatusDown {
		t.Errorf("after fallback timeout: %q", snap.Status)
	}
	if snap.Started {
		t.Error("Started should clear on fallback timeout")
	}
	e.WaitAlerts()
	if alerter.downCount() != 1 {
		t.Errorf("want 1 down alert, got %d", alerter.downCount())
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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
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
	e.WaitAlerts()
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
	e := newEngine(t, reg, st, Options{Now: now})
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
	e := newEngine(t, reg, st, Options{Now: now})

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
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
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
	e.WaitAlerts()
	if alerter.downCount() != 1 {
		t.Errorf("want 1 down alert, got %d", alerter.downCount())
	}
}

// TestSlugRenameStartsFreshHistory protects the design invariant from
// the design-decisions memory: a slug rename produces a new UUID, and
// the new check's history starts empty rather than reusing the prior
// slug's state. The prior slug's data stays addressable in the store
// (by its still-valid UUID), so a roll-back rename re-finds it.
func TestSlugRenameStartsFreshHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// First registry: original slug.
	originalYAML := `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
`
	reg1 := loadRegistry(t, originalYAML)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e1 := newEngine(t, reg1, st, Options{Now: now})

	origCheck := reg1.CheckBySlug("api")
	if err := e1.HandlePing(origCheck.UUID, &PingRequest{Kind: store.PingSuccess}); err != nil {
		t.Fatal(err)
	}
	origUUID := origCheck.UUID
	origPings, err := st.RecentPings(origUUID, 0)
	if err != nil || len(origPings) != 1 {
		t.Fatalf("setup pings: got %d, err %v", len(origPings), err)
	}

	// Second registry: same salt, renamed slug. UUIDv5(salt, slug) means
	// the renamed check derives a different UUID.
	renamedYAML := `
server: { uuid_salt: "s" }
checks:
  - { slug: api-v2, period: 1h }
`
	reg2 := loadRegistry(t, renamedYAML)
	e2 := newEngine(t, reg2, st, Options{Now: now})

	renamedCheck := reg2.CheckBySlug("api-v2")
	if renamedCheck == nil {
		t.Fatal("renamed check missing from registry")
	}
	if renamedCheck.UUID == origUUID {
		t.Fatal("rename did not change UUID — DeriveUUID is broken")
	}

	// Renamed check has no history.
	renamedPings, _ := st.RecentPings(renamedCheck.UUID, 0)
	if len(renamedPings) != 0 {
		t.Errorf("renamed check should start empty: got %d pings", len(renamedPings))
	}
	snap, ok := e2.Snapshot(renamedCheck.UUID)
	if !ok || snap.Status != store.StatusNew {
		t.Errorf("renamed check status: ok=%v status=%q", ok, snap.Status)
	}

	// Original UUID's history is still there — a rollback rename would
	// re-discover it intact.
	stillPings, _ := st.RecentPings(origUUID, 0)
	if len(stillPings) != 1 {
		t.Errorf("original history was destroyed: got %d pings (want 1)", len(stillPings))
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
	e1 := newEngine(t, reg, st1, Options{Now: now})
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
	e2 := newEngine(t, reg2, st2, Options{Now: now})
	c2 := reg2.CheckBySlug("api")
	snap, _ := e2.Snapshot(c2.UUID)
	if snap.Status != store.StatusUp {
		t.Errorf("status lost across restart: %q", snap.Status)
	}
}

func TestSnapshotAll(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 1h }
  - { slug: worker, period: 1h }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())
	e := newEngine(t, reg, st, Options{Now: now})

	api := reg.CheckBySlug("api")
	worker := reg.CheckBySlug("worker")
	if err := e.HandlePing(api.UUID, &PingRequest{Kind: store.PingSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := e.HandlePing(worker.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}

	all := e.SnapshotAll()
	if len(all) != 2 {
		t.Fatalf("SnapshotAll size: got %d, want 2", len(all))
	}
	if got := all[api.UUID].Status; got != store.StatusUp {
		t.Errorf("api status: %q", got)
	}
	if got := all[worker.UUID].Status; got != store.StatusDown {
		t.Errorf("worker status: %q", got)
	}

	// Mutating the returned map must not affect the engine.
	delete(all, api.UUID)
	again := e.SnapshotAll()
	if len(again) != 2 {
		t.Errorf("SnapshotAll returned a live reference: second call sees %d", len(again))
	}
}

func TestNopBusAndAlerter(t *testing.T) {
	// These exist as wiring placeholders; the only contract is "doesn't
	// panic, returns nil errors." Pin that explicitly so a future refactor
	// can't silently change it.
	var bus EventBus = NopBus{}
	bus.Publish(&Transition{})

	var a Alerter = NopAlerter{}
	if err := a.Down(context.Background(), nil, &Transition{}); err != nil {
		t.Errorf("NopAlerter.Down: %v", err)
	}
	if err := a.Recover(context.Background(), nil, &Transition{}); err != nil {
		t.Errorf("NopAlerter.Recover: %v", err)
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
	e := newEngine(t, reg, st, Options{Now: now})

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

// TestAlertDispatchDoesNotBlockEngineLock covers C1: a slow Alerter must
// not stall the engine. We dispatch a transition, then immediately observe
// that an unrelated Snapshot returns without waiting for the alert to
// complete.
func TestAlertDispatchDoesNotBlockEngineLock(t *testing.T) {
	reg := loadRegistry(t, `
server: { uuid_salt: "s" }
checks:
  - { slug: api, period: 10m }
`)
	st := openStore(t)
	now, _ := fixedClock(time.Unix(1_700_000_000, 0).UTC())

	release := make(chan struct{})
	alerter := &blockingAlerter{release: release}
	e := newEngine(t, reg, st, Options{Now: now, Alerter: alerter})
	c := reg.CheckBySlug("api")

	// Push the check into down; the alerter goroutine will start dispatching
	// and block on `release`.
	_ = e.HandlePing(c.UUID, &PingRequest{Kind: store.PingSuccess})
	if err := e.HandlePing(c.UUID, &PingRequest{Kind: store.PingFail}); err != nil {
		t.Fatal(err)
	}

	// Snapshot must return immediately even while the alerter is blocked.
	// If transitionLocked were still calling the alerter under e.mu, this
	// would deadlock until the test timeout.
	done := make(chan struct{})
	go func() {
		_, _ = e.Snapshot(c.UUID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Snapshot stalled while alert was in flight")
	}

	close(release)
	e.WaitAlerts()
	if got := alerter.calls.Load(); got != 1 {
		t.Errorf("alerter Down calls: got %d, want 1", got)
	}
}

// blockingAlerter holds Down until the caller closes release. Used to
// prove the engine doesn't pin its lock during dispatch.
type blockingAlerter struct {
	release chan struct{}
	calls   atomicInt
}

func (b *blockingAlerter) Down(ctx context.Context, _ *config.ResolvedCheck, _ *Transition) error {
	b.calls.Add(1)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}
func (b *blockingAlerter) Recover(context.Context, *config.ResolvedCheck, *Transition) error {
	return nil
}

// atomicInt is a tiny sync wrapper to avoid pulling sync/atomic into the
// test file twice; named for readability at call sites.
type atomicInt struct {
	mu sync.Mutex
	n  int
}

func (a *atomicInt) Add(d int) { a.mu.Lock(); a.n += d; a.mu.Unlock() }
func (a *atomicInt) Load() int { a.mu.Lock(); defer a.mu.Unlock(); return a.n }
