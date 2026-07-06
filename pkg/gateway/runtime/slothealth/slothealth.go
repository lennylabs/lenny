// SPDX-License-Identifier: MIT

// Package slothealth tracks concurrent-workspace (§5.2) slot failures and
// leaks per pod so the gateway can apply the §5.2 whole-pod replacement
// trigger. Because a failure is transient while a leak persists until pod
// termination, the two are counted with different lifetimes: a slot failure
// is counted within a rolling 5-minute window and ages out, while a leaked
// slot is counted persistently until the pod terminates. When the
// rolling-window failures plus the persistent leaks reach
// ceil(maxConcurrent/2) on the same pod, the pod is unhealthy and §6.2 drains
// it as a whole.
//
// spec: §5.2 "Concurrent-workspace slot retry policy" (whole-pod
// replacement trigger); §6.2 (claimed → draining on the
// unhealthy-slot threshold), §6.2 "`leaked` slot semantics"
// (rolling-window failed_slots plus persistent leaked_slots
// >= ceil(maxConcurrent/2)).
package slothealth

import (
	"sync"
	"time"
)

// DefaultWindow is the §5.2 rolling window over which slot failures are
// counted toward the whole-pod replacement trigger. Leaks are not windowed:
// a leaked slot persists until pod termination, so it is counted
// persistently rather than aged out (§6.2 "`leaked` slot semantics").
const DefaultWindow = 5 * time.Minute

// event is one slot failure occurrence at a point in time. A failure is
// transient, so it is counted within the rolling 5-minute window and ages
// out. Leaks are not events: a §6.2 leaked slot (cleanup timeout exceeded)
// persists until pod termination and is counted persistently instead (see
// Tracker.leaked). spec: §6.2 "`leaked` slot semantics" (failed slots
// counted within a rolling 5-minute window, leaked slots counted
// persistently).
type event struct {
	at time.Time
}

// Tracker accumulates per-pod slot failures in a rolling window and
// persistent per-pod leak counts, and reports when a pod crosses the §5.2
// ceil(maxConcurrent/2) unhealthy threshold. It is safe for concurrent use
// by multiple gateway request goroutines.
//
// A failed slot is transient and is counted within the rolling window; a
// leaked slot persists until pod termination and is counted persistently, so
// a pod that accumulates permanent leaks slowly (more than one window apart)
// still reaches the threshold rather than aging each leak out. spec: §6.2
// "`leaked` slot semantics", §5.2 whole-pod replacement trigger.
//
// Events are assumed to be recorded in non-decreasing time order (the
// production clock is wall time; tests inject a monotonic clock), which
// lets the rolling-window prune drop a contiguous expired prefix.
type Tracker struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	events map[string][]event
	// leaked is the per-pod count of currently-leaked slots. A leaked slot
	// stays counted until it is reclaimed at pod termination (Forget), so
	// this is a persistent count rather than a rolling-window series.
	// spec: §6.2 "`leaked` slot semantics" (a leaked slot is counted
	// persistently for as long as the slot remains leaked).
	leaked map[string]int
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithWindow overrides the default 5-minute rolling window.
func WithWindow(d time.Duration) Option {
	return func(t *Tracker) { t.window = d }
}

// WithClock injects a clock so tests can advance time deterministically.
func WithClock(now func() time.Time) Option {
	return func(t *Tracker) { t.now = now }
}

// New builds a Tracker. Without options it uses the §5.2 5-minute window
// and the wall clock.
func New(opts ...Option) *Tracker {
	t := &Tracker{window: DefaultWindow, now: time.Now, events: map[string][]event{}, leaked: map[string]int{}}
	for _, o := range opts {
		o(t)
	}
	if t.window <= 0 {
		t.window = DefaultWindow
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t
}

// RecordFailure records that a slot on pod transitioned to failed
// (runtime error, OOM, unhandled exception, or a non-retryable bind
// failure). A failure is transient, so it is counted within the rolling
// 5-minute window and ages out. spec: §5.2 (failed slots contribute to the
// threshold, counted within a rolling 5-minute window).
func (t *Tracker) RecordFailure(pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.events[pod] = append(t.pruneLocked(pod, now), event{at: now})
}

// RecordLeak records that a slot on pod transitioned to leaked (the §6.2
// cleanup timeout was exceeded so the slot is not reclaimed until pod
// termination). A leaked slot counts toward the unhealthy threshold exactly
// like a failure because it consumes slot capacity without being
// reclaimable, but it is counted persistently rather than within the
// rolling window: a leaked slot persists until pod termination, so a pod
// that accumulates permanent leaks slowly (more than one window apart) must
// still reach the threshold rather than aging each leak out. The count is
// released at pod termination via Forget. spec: §6.2 "`leaked` slot
// semantics" (leaked slots counted persistently); §5.2 whole-pod
// replacement trigger.
func (t *Tracker) RecordLeak(pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.leaked[pod]++
}

// Unhealthy reports whether pod has accumulated in-window failed slots plus
// persistently-leaked slots reaching ceil(maxConcurrent/2). Expired failure
// events are pruned as a side effect; the persistent leak count is not
// pruned. A pod with no recorded failures and no leaks is never unhealthy.
//
// spec: §6.2 "`leaked` slot semantics" (the pod transitions to draining
// when the rolling-window failed_slots plus the persistent leaked_slots
// reaches ceil(maxConcurrentSessions/2)); §5.2 whole-pod replacement
// trigger.
func (t *Tracker) Unhealthy(pod string, maxConcurrent int32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.countsLocked(pod) >= UnhealthyThreshold(maxConcurrent)
}

// Counts returns the in-window failed slot count and the persistent leaked
// slot count for pod. Expired failure events are pruned as a side effect.
func (t *Tracker) Counts(pod string) (failed, leaked int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	evs := t.pruneLocked(pod, t.now())
	if len(evs) == 0 {
		delete(t.events, pod)
	} else {
		t.events[pod] = evs
	}
	return len(evs), t.leaked[pod]
}

// countsLocked returns the combined failed-plus-leaked slot count for pod:
// the pruned rolling-window failure count summed with the persistent leaked
// count. The caller holds t.mu. spec: §6.2 "`leaked` slot semantics"
// (rolling-window failed_slots plus persistent leaked_slots).
func (t *Tracker) countsLocked(pod string) int {
	evs := t.pruneLocked(pod, t.now())
	if len(evs) == 0 {
		delete(t.events, pod)
	} else {
		t.events[pod] = evs
	}
	return len(evs) + t.leaked[pod]
}

// Forget drops all recorded failures and the persistent leak count for pod.
// The gateway calls it once a pod has been drained for replacement so a
// later pod reusing the name (or a pod that recovered) starts from a clean
// slate. The pod's leaked slots are reclaimed with the terminated pod, so
// the persistent leak count is released here. spec: §6.2 "`leaked` slot
// semantics" (a leaked slot is counted persistently for as long as the slot
// remains leaked, released at pod termination).
func (t *Tracker) Forget(pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.events, pod)
	delete(t.leaked, pod)
}

// pruneLocked returns pod's events with those at or before the window
// cutoff removed. It relies on events being time-ordered so it can drop a
// contiguous expired prefix. The caller holds t.mu.
func (t *Tracker) pruneLocked(pod string, now time.Time) []event {
	evs := t.events[pod]
	if len(evs) == 0 {
		return evs
	}
	cutoff := now.Add(-t.window)
	i := 0
	for i < len(evs) && !evs[i].at.After(cutoff) {
		i++
	}
	if i == 0 {
		return evs
	}
	rest := evs[i:]
	if len(rest) == 0 {
		return nil
	}
	// Copy so the retained slice does not pin the dropped prefix's backing
	// array, and so repeated appends do not alias an earlier snapshot.
	out := make([]event, len(rest))
	copy(out, rest)
	return out
}

// UnhealthyThreshold is the §5.2 ceil(maxConcurrent/2) count that marks a
// pod unhealthy: the rolling-window failed slots plus the persistent leaked
// slots reaching this bound trips the whole-pod replacement trigger. A
// maxConcurrent below 1 is clamped to 1 (a single failure trips it).
func UnhealthyThreshold(maxConcurrent int32) int {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return int((maxConcurrent + 1) / 2)
}
