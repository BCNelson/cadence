package api

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket. Buckets fill at refill tokens/sec
// up to capacity; Allow consumes one. Zero capacity disables the limiter
// — Allow always returns true so the daemon ships with an explicit
// opt-out path.
type rateLimiter struct {
	capacity float64
	refill   float64
	now      func() time.Time

	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(capacity, refillPerSec float64, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		capacity: capacity,
		refill:   refillPerSec,
		now:      now,
		buckets:  make(map[string]*tokenBucket),
	}
}

// Allow returns true and consumes one token, or false if the bucket for
// key is empty. A disabled limiter (capacity == 0) always allows.
func (l *rateLimiter) Allow(key string) bool {
	if l == nil || l.capacity <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		// Buckets start full so a freshly-seen key gets the full burst.
		b = &tokenBucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.refill
			if b.tokens > l.capacity {
				b.tokens = l.capacity
			}
			b.last = now
		}
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
