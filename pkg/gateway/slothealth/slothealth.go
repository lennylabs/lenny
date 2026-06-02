// SPDX-License-Identifier: MIT

// Package slothealth tracks concurrent-workspace (§5.2) slot failures and
// leaks per pod over a rolling window so the gateway can apply the §5.2
// whole-pod replacement trigger: when ceil(maxConcurrent/2) or more slots
// on the same pod fail or leak within a 5-minute window, the pod is
// unhealthy and §6.2 drains it as a whole.
//
// spec: §5.2 "Concurrent-workspace slot retry policy" (whole-pod
// replacement trigger); §6.2 line 165 (slot_active → draining on the
// unhealthy-slot threshold), §6.2 line 179 (failed_slots + leaked_slots
// >= ceil(maxConcurrent/2) within the rolling window).
package slothealth

import (
	"sync"
	"time"
)

// DefaultWindow is the §5.2 / §6.2 line 179 rolling window over which slot
// failures and leaks are counted toward the whole-pod replacement trigger.
const DefaultWindow = 5 * time.Minute

// event is one slot fail-or-leak occurrence at a point in time. leaked
// distinguishes a §6.2 leaked slot (cleanup timeout exceeded) from an
// outright failure; both count toward the unhealthy threshold (§5.2 "Both
// categories contribute to the threshold").
type event struct {
	at     time.Time
	leaked bool
}

// Tracker accumulates per-pod slot fail/leak events in a rolling window
// and reports when a pod crosses the §5.2 ceil(maxConcurrent/2) unhealthy
// threshold. It is safe for concurrent use by multiple gateway request
// goroutines.
//
// Events are assumed to be recorded in non-decreasing time order (the
// production clock is wall time; tests inject a monotonic clock), which
// lets the rolling-window prune drop a contiguous expired prefix.
type Tracker struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	events map[string][]event
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
	t := &Tracker{window: DefaultWindow, now: time.Now, events: map[string][]event{}}
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
// failure). spec: §5.2 (failed slots contribute to the threshold).
func (t *Tracker) RecordFailure(pod string) { t.record(pod, false) }

// RecordLeak records that a slot on pod transitioned to leaked (the §6.2
// cleanup timeout was exceeded so the slot is not reclaimed until pod
// termination). A leaked slot counts toward the unhealthy threshold
// exactly like a failure because it consumes active_slots capacity
// without being reclaimable. spec: §6.2 line 179.
func (t *Tracker) RecordLeak(pod string) { t.record(pod, true) }

func (t *Tracker) record(pod string, leaked bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.events[pod] = append(t.pruneLocked(pod, now), event{at: now, leaked: leaked})
}

// Unhealthy reports whether pod has accumulated failed-or-leaked slots
// reaching ceil(maxConcurrent/2) within the rolling window. Expired
// events are pruned as a side effect. A pod with no recorded events is
// never unhealthy.
//
// spec: §5.2 "When ceil(maxConcurrent / 2) or more slots on the same pod
// fail or leak within a rolling 5-minute window".
func (t *Tracker) Unhealthy(pod string, maxConcurrent int32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	evs := t.pruneLocked(pod, t.now())
	if len(evs) == 0 {
		delete(t.events, pod)
		return false
	}
	t.events[pod] = evs
	return len(evs) >= UnhealthyThreshold(maxConcurrent)
}

// Counts returns the in-window failed and leaked slot counts for pod.
// Expired events are pruned as a side effect.
func (t *Tracker) Counts(pod string) (failed, leaked int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	evs := t.pruneLocked(pod, t.now())
	if len(evs) == 0 {
		delete(t.events, pod)
		return 0, 0
	}
	t.events[pod] = evs
	for _, e := range evs {
		if e.leaked {
			leaked++
		} else {
			failed++
		}
	}
	return failed, leaked
}

// Forget drops all recorded events for pod. The gateway calls it once a
// pod has been drained for replacement so a later pod reusing the name
// (or a pod that recovered) starts from a clean slate.
func (t *Tracker) Forget(pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.events, pod)
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

// UnhealthyThreshold is the §5.2 ceil(maxConcurrent/2) count of
// failed-or-leaked slots within the window that marks a pod unhealthy. A
// maxConcurrent below 1 is clamped to 1 (a single failure trips it).
func UnhealthyThreshold(maxConcurrent int32) int {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return int((maxConcurrent + 1) / 2)
}
