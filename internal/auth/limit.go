package auth

import (
	"sync"
	"time"
)

// limiter counts events by key, such as failed attempts to sign in, and
// allows at most max of them within window.
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
	calls  int // since forgetting old events of all keys
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, now: time.Now, events: map[string][]time.Time{}}
}

// take records an event of key, unless max events of key happened within
// the window, and reports whether it did. Checking and recording at once
// keeps concurrent requests from all getting through.
func (l *limiter) take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := l.recent(key)
	if len(events) >= l.max {
		return false
	}
	l.events[key] = append(events, l.now())
	return true
}

// give forgets the latest event of key, e.g. one that take recorded for an
// attempt that succeeded.
func (l *limiter) give(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if events := l.events[key]; len(events) > 1 {
		l.events[key] = events[:len(events)-1]
	} else {
		delete(l.events, key)
	}
}

// reset forgets the events of key.
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, key)
}

// recent returns the events of key within the window, forgetting older
// ones. Every now and then it forgets those of all keys, so that keys used
// once do not pile up.
func (l *limiter) recent(key string) []time.Time {
	since := l.now().Add(-l.window)
	if l.calls++; l.calls >= 1000 {
		l.calls = 0
		for k := range l.events {
			l.prune(k, since)
		}
	}
	return l.prune(key, since)
}

func (l *limiter) prune(key string, since time.Time) []time.Time {
	events := l.events[key]
	i := 0
	for i < len(events) && events[i].Before(since) {
		i++
	}
	events = events[i:]
	if len(events) == 0 {
		delete(l.events, key)
		return nil
	}
	l.events[key] = events
	return events
}
