// SPDX-License-Identifier: MIT

// Package auditrate implements the §25.6 diagnostics-audit rate
// limiting: per-resource coalescing of repeated diagnostic calls and a
// per-service-account cap on distinct diagnostic audit events.
package auditrate

import (
	"sync"
	"time"
)

// Decision is the §25.6 disposition of a diagnostic audit event.
type Decision int

const (
	// Emit records a new diagnostic audit event.
	Emit Decision = iota
	// Coalesce folds the call into the resource's active event,
	// incrementing its invocationCount rather than emitting a new one.
	Coalesce
	// Drop discards the event because the per-service-account rate
	// limit is exceeded; the caller increments
	// lenny_audit_rate_limited_total.
	Drop
)

func (d Decision) String() string {
	switch d {
	case Emit:
		return "emit"
	case Coalesce:
		return "coalesce"
	case Drop:
		return "drop"
	default:
		return "unknown"
	}
}

// coalesceWindow is the §25.6 per-resource coalescing window: repeated
// diagnostic calls for one resource within it share a single audit
// event.
const coalesceWindow = 60 * time.Second

// DefaultRatePerMinute is the §25.6 default cap on distinct diagnostic
// audit events per service account per minute.
const DefaultRatePerMinute = 60

// Limiter applies the §25.6 diagnostics-audit rate limiting. It is
// safe for concurrent use.
type Limiter struct {
	ratePerMinute int

	mu      sync.Mutex
	windows map[string]time.Time   // resource key -> coalescing window start
	emits   map[string][]time.Time // service account -> recent Emit times
}

// New returns a Limiter with the given per-service-account rate. A
// non-positive rate selects DefaultRatePerMinute.
func New(ratePerMinute int) *Limiter {
	if ratePerMinute <= 0 {
		ratePerMinute = DefaultRatePerMinute
	}
	return &Limiter{
		ratePerMinute: ratePerMinute,
		windows:       map[string]time.Time{},
		emits:         map[string][]time.Time{},
	}
}

// Decide returns the §25.6 disposition for a diagnostic audit event
// for resource {resourceType, resourceID} requested by serviceAccount
// at now. A repeated call for the same resource within the 60-second
// window coalesces; otherwise a new event emits unless the service
// account has reached its per-minute cap, in which case it is dropped.
func (l *Limiter) Decide(serviceAccount, resourceType, resourceID string, now time.Time) Decision {
	key := serviceAccount + "\x00" + resourceType + "\x00" + resourceID
	l.mu.Lock()
	defer l.mu.Unlock()

	if start, ok := l.windows[key]; ok && now.Sub(start) < coalesceWindow {
		return Coalesce
	}

	// A new distinct event: enforce the per-service-account rate.
	recent := pruneBefore(l.emits[serviceAccount], now.Add(-time.Minute))
	if len(recent) >= l.ratePerMinute {
		l.emits[serviceAccount] = recent
		return Drop
	}
	l.windows[key] = now
	l.emits[serviceAccount] = append(recent, now)
	return Emit
}

// Sweep drops state older than the coalescing and rate windows so a
// long-running limiter does not accumulate stale entries. The
// diagnostic service calls it periodically.
func (l *Limiter) Sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, start := range l.windows {
		if now.Sub(start) >= coalesceWindow {
			delete(l.windows, key)
		}
	}
	for sa, ts := range l.emits {
		if kept := pruneBefore(ts, now.Add(-time.Minute)); len(kept) == 0 {
			delete(l.emits, sa)
		} else {
			l.emits[sa] = kept
		}
	}
}

// pruneBefore returns the timestamps in ts at or after cutoff. ts is
// assumed ascending, as Decide appends in time order.
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}
