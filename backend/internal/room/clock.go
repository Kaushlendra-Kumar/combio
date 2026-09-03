package room

import (
	"sort"
	"sync"
	"time"
)

// Clock is the room's only access to time. Injecting it keeps timer behaviour
// testable: FakeClock advances on command, so "did the stale timer fire?" is a
// deterministic question instead of a race.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Stopper
}

// Stopper cancels a scheduled callback.
type Stopper interface{ Stop() }

// ---------- real ----------

type realClock struct{}

// RealClock is the production clock.
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) Stopper {
	return stdTimer{time.AfterFunc(d, f)}
}

type stdTimer struct{ t *time.Timer }

func (s stdTimer) Stop() { s.t.Stop() }

// ---------- fake ----------

// FakeClock is a manually advanced clock for tests. It is in the non-test build
// so the server package's tests can use it too.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	nextID  int
	pending map[int]*fakeTimer
}

type fakeTimer struct {
	id    int
	at    time.Time
	fn    func()
	clock *FakeClock
}

func (f *fakeTimer) Stop() {
	f.clock.mu.Lock()
	defer f.clock.mu.Unlock()
	delete(f.clock.pending, f.id)
}

func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Unix(1700000000, 0), pending: map[int]*fakeTimer{}}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) Stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	t := &fakeTimer{id: c.nextID, at: c.now.Add(d), fn: f, clock: c}
	c.pending[t.id] = t
	return t
}

// Advance moves time forward and fires everything now due, earliest first.
// Callbacks run outside the lock so they may schedule new timers.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := make([]*fakeTimer, 0, len(c.pending))
	for _, t := range c.pending {
		if !t.at.After(c.now) {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].at.Equal(due[j].at) {
			return due[i].id < due[j].id
		}
		return due[i].at.Before(due[j].at)
	})
	for _, t := range due {
		delete(c.pending, t.id)
	}
	c.mu.Unlock()

	for _, t := range due {
		t.fn()
	}
}

// Pending reports how many callbacks are scheduled.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}
