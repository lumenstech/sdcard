// Package ratelimit provides a small per-key token-bucket limiter used to
// protect the ingest pipeline from a single malfunctioning sidecar that
// retries indefinitely or sends frames too quickly.
//
// The implementation favours simplicity over micro-optimisation: one map +
// one mutex. A pilot site with a few hundred devices fits comfortably.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-key token-bucket limiter.
type Limiter struct {
	rate     float64 // tokens per second
	burst    float64 // bucket capacity
	mu       sync.Mutex
	buckets  map[string]*bucket
	lastSwep time.Time
	maxIdle  time.Duration
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter that refills `ratePerSecond` tokens/sec up to `burst`.
// keys idle longer than maxIdle are garbage-collected on subsequent calls.
func New(ratePerSecond, burst float64, maxIdle time.Duration) *Limiter {
	if ratePerSecond <= 0 {
		ratePerSecond = 1
	}
	if burst <= 0 {
		burst = ratePerSecond
	}
	if maxIdle <= 0 {
		maxIdle = 30 * time.Minute
	}
	return &Limiter{
		rate:    ratePerSecond,
		burst:   burst,
		buckets: make(map[string]*bucket),
		maxIdle: maxIdle,
	}
}

// Allow returns true if the key may proceed and consumes one token.
// A nil receiver always allows; a zero-rate Limiter behaves as disabled.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		l.maybeSweep(now)
		return false
	}
	b.tokens--
	l.maybeSweep(now)
	return true
}

func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.lastSwep) < l.maxIdle/2 {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > l.maxIdle {
			delete(l.buckets, k)
		}
	}
	l.lastSwep = now
}
