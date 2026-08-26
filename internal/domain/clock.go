package domain

import (
	"sync/atomic"
	"time"
)

// LogicalClock supplies monotonic logical time for leases, readings and
// idempotency ordering. It is injected so tests can advance it deterministically
// and recovery can be decided purely from persisted logical time.
type LogicalClock interface {
	// Now returns the current logical time, strictly non-decreasing.
	Now() int64
}

// WallClock is a monotonic logical clock derived from the wall clock. It never
// steps backwards: when the host clock is observed to run behind a previously
// returned value it advances by one so callers still observe strict monotonicity.
type WallClock struct {
	last atomic.Int64
}

// NewWallClock builds a WallClock seeded from the current wall-clock time.
func NewWallClock() *WallClock {
	c := &WallClock{}
	c.last.Store(time.Now().UnixNano())
	return c
}

// Now returns a strictly non-decreasing logical timestamp.
func (c *WallClock) Now() int64 {
	for {
		last := c.last.Load()
		now := time.Now().UnixNano()
		if now <= last {
			now = last + 1
		}
		if c.last.CompareAndSwap(last, now) {
			return now
		}
	}
}
