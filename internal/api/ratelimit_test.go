package api

import (
	"testing"
	"time"
)

func TestRateLimiterDisabledAllowsAll(t *testing.T) {
	l := newRateLimiter(0, 0, nil)
	for i := 0; i < 1_000; i++ {
		if !l.Allow("x") {
			t.Fatalf("disabled limiter blocked at iteration %d", i)
		}
	}
}

func TestRateLimiterEnforcesBurstAndRefill(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	clock := &now
	l := newRateLimiter(3, 1, func() time.Time { return *clock })

	// Burst: first three allowed, fourth blocked.
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Errorf("Allow %d/3: blocked", i)
		}
	}
	if l.Allow("k") {
		t.Error("burst+1 should be blocked")
	}

	// One second later: one refill, so exactly one call allowed.
	*clock = clock.Add(time.Second)
	if !l.Allow("k") {
		t.Error("after 1s refill: want allow")
	}
	if l.Allow("k") {
		t.Error("after 1s refill: second call should block")
	}
}

func TestRateLimiterPerKeyIsolation(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	clock := &now
	l := newRateLimiter(1, 0, func() time.Time { return *clock })

	if !l.Allow("a") {
		t.Error("a's burst should allow first")
	}
	if l.Allow("a") {
		t.Error("a's burst should be exhausted")
	}
	if !l.Allow("b") {
		t.Error("b should not share a's bucket")
	}
}
