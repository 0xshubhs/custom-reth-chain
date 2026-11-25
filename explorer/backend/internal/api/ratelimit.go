package api

import (
	"context"
	"sync"
	"time"
)

// limiter is a per-key fixed-window counter: at most limit hits per window.
type limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	start time.Time
	count int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, now: time.Now, buckets: map[string]*bucket{}}
}

// allow records a hit for key. When over the limit it returns false and how
// long until the window resets (at least one second).
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	if b.count >= l.limit {
		wait := b.start.Add(l.window).Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait
	}
	b.count++
	return true, 0
}

// sweep drops expired windows.
func (l *limiter) sweep() {
	now := l.now()
	l.mu.Lock()
	for k, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, k)
		}
	}
	l.mu.Unlock()
}

func (l *limiter) run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}
