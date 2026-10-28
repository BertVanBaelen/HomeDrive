// Package ratelimit counts events per key in a sliding window, in memory.
// It guards sign-in against guessing; a restart resets it, which is fine
// for a household server behind Cloudflare's own limits.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
	sweep  time.Time
}

// New allows limit events per key within window.
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, now: time.Now, events: map[string][]time.Time{}}
}

// Allow records an event for key and reports whether it is within the
// limit. When it isn't, it also returns how long until the next is allowed.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.maybeSweep(now)
	ev := l.recent(key, now)
	if len(ev) >= l.limit {
		l.events[key] = ev
		return false, ev[0].Add(l.window).Sub(now)
	}
	l.events[key] = append(ev, now)
	return true, 0
}

// Blocked reports whether key is over the limit, without recording.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ev := l.recent(key, now)
	if len(ev) >= l.limit {
		return true, ev[0].Add(l.window).Sub(now)
	}
	return false, 0
}

// Add records an event for key without checking.
func (l *Limiter) Add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.events[key] = append(l.recent(key, now), now)
}

// Reset forgets key, for example after a successful sign-in.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.events, key)
	l.mu.Unlock()
}

func (l *Limiter) recent(key string, now time.Time) []time.Time {
	ev := l.events[key]
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(ev) && !ev[i].After(cutoff) {
		i++
	}
	return ev[i:]
}

// maybeSweep drops idle keys now and then so memory stays bounded.
func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.sweep) < l.window {
		return
	}
	l.sweep = now
	for k := range l.events {
		if len(l.recent(k, now)) == 0 {
			delete(l.events, k)
		}
	}
}
