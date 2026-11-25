package main

import (
	"math"
	"sync"
	"time"
)

// ipLimiter is a token bucket per client IP. Each JSON-RPC call costs one
// token, so a batch of 25 costs 25: batching must not be a way around it.
type ipLimiter struct {
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(rate float64, burst int) *ipLimiter {
	return &ipLimiter{
		rate:    rate,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// allow takes n tokens from ip's bucket. When it cannot, it returns false and
// how long until n tokens will be there.
func (l *ipLimiter) allow(ip string, n int) (bool, time.Duration) {
	cost := float64(n)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(now)

	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if cost > l.burst {
		// Can never be satisfied; treat as "wait for a full bucket".
		cost = l.burst
	}
	if b.tokens >= cost {
		b.tokens -= cost
		return true, 0
	}
	wait := time.Duration((cost - b.tokens) / l.rate * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

// gc drops buckets that have refilled completely, at most once a minute, so
// the map cannot grow without bound under a spread of source addresses.
func (l *ipLimiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for ip, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, ip)
		}
	}
}
