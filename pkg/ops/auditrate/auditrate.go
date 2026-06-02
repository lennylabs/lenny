// SPDX-License-Identifier: MIT

// Package auditrate implements the §25.9 diagnostics-audit rate
// limiting: per-resource coalescing of repeated diagnostic calls into a
// single audit event with an incremented invocationCount, and a
// per-service-account cap on distinct diagnostic audit events.
//
// A coalescing window opens on the first diagnostic call for a
// {resourceType, resourceId}. Repeated calls within the 60s window fold
// into the open window (incrementing its invocationCount) rather than
// emitting a new audit event. When the window closes, the single
// accumulated audit event is emitted via the flush callback carrying the
// final invocationCount. This bounds the audit-event volume the §25.9
// diagnostic endpoints produce while preserving the call count.
package auditrate

import (
	"sync"
	"time"
)

// Decision is the §25.9 disposition of a diagnostic audit event.
type Decision int

const (
	// Emit opens a new coalescing window; its audit event is emitted when
	// the window closes.
	Emit Decision = iota
	// Coalesce folds the call into the resource's open window,
	// incrementing its invocationCount rather than opening a new one.
	Coalesce
	// Drop discards the call because the per-service-account rate limit
	// is exceeded; the caller increments lenny_audit_rate_limited_total.
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

// Call identifies one diagnostic invocation to rate-limit and coalesce.
// EventType is the §16.7 audit event type (diagnostics.session_diagnosed,
// diagnostics.pool_diagnosed, diagnostics.credential_pool_diagnosed,
// diagnostics.connectivity_checked); OperationID is the cross-cutting
// §25.9 line 3703 X-Lenny-Operation-ID correlation stamped on the
// emitted event.
type Call struct {
	ServiceAccount string
	ResourceType   string
	ResourceID     string
	EventType      string
	OperationID    string
}

// Event is one coalesced diagnostic audit event, emitted via the flush
// callback when its coalescing window closes. InvocationCount is the
// number of diagnostic calls folded into the window (1 when the window
// saw no repeats). FirstAt is the instant the window opened, the
// timestamp the §25.9 audit event records.
type Event struct {
	ServiceAccount  string
	ResourceType    string
	ResourceID      string
	EventType       string
	OperationID     string
	InvocationCount int
	FirstAt         time.Time
}

// coalesceWindow is the §25.9 per-resource coalescing window: repeated
// diagnostic calls for one resource within it share a single audit
// event.
const coalesceWindow = 60 * time.Second

// DefaultRatePerMinute is the §25.9 ops.audit.diagnosticsRatePerMinute
// default cap on distinct diagnostic audit events per service account
// per minute.
const DefaultRatePerMinute = 60

// Limiter applies the §25.9 diagnostics-audit rate limiting. It is safe
// for concurrent use.
type Limiter struct {
	ratePerMinute int

	mu      sync.Mutex
	windows map[string]*coalesceState // resource key -> open coalescing window
	emits   map[string][]time.Time    // service account -> recent window-open times
	flush   func(Event)
}

// coalesceState is one open §25.9 coalescing window accumulating the
// invocationCount for a {serviceAccount, resourceType, resourceId}.
type coalesceState struct {
	call    Call
	count   int
	opensAt time.Time
}

func (st *coalesceState) event() Event {
	return Event{
		ServiceAccount:  st.call.ServiceAccount,
		ResourceType:    st.call.ResourceType,
		ResourceID:      st.call.ResourceID,
		EventType:       st.call.EventType,
		OperationID:     st.call.OperationID,
		InvocationCount: st.count,
		FirstAt:         st.opensAt,
	}
}

// New returns a Limiter with the given per-service-account rate. A
// non-positive rate selects DefaultRatePerMinute.
func New(ratePerMinute int) *Limiter {
	if ratePerMinute <= 0 {
		ratePerMinute = DefaultRatePerMinute
	}
	return &Limiter{
		ratePerMinute: ratePerMinute,
		windows:       map[string]*coalesceState{},
		emits:         map[string][]time.Time{},
	}
}

// WithFlush sets the callback invoked once per coalescing window when it
// closes, carrying the window's final invocationCount. A nil flush
// accounts coalesced events without emitting them (the Decide-only use).
func (l *Limiter) WithFlush(flush func(Event)) *Limiter {
	l.flush = flush
	return l
}

// Record registers one diagnostic call. It first flushes any coalescing
// windows that have closed, then opens a new window (Emit), folds the
// call into the resource's open window (Coalesce), or drops it when the
// per-service-account rate cap is reached (Drop). A new distinct event
// counts against the rate; a coalesced call does not. The audit Event is
// emitted via the flush callback when its window closes, carrying the
// accumulated invocationCount.
func (l *Limiter) Record(call Call, now time.Time) Decision {
	l.mu.Lock()
	pending := l.flushExpiredLocked(now)
	key := call.ServiceAccount + "\x00" + call.ResourceType + "\x00" + call.ResourceID
	var d Decision
	if st, ok := l.windows[key]; ok {
		st.count++
		d = Coalesce
	} else {
		// A new distinct event: enforce the per-service-account rate.
		recent := pruneBefore(l.emits[call.ServiceAccount], now.Add(-time.Minute))
		if len(recent) >= l.ratePerMinute {
			l.emits[call.ServiceAccount] = recent
			d = Drop
		} else {
			l.windows[key] = &coalesceState{call: call, count: 1, opensAt: now}
			l.emits[call.ServiceAccount] = append(recent, now)
			d = Emit
		}
	}
	l.mu.Unlock()
	l.emitAll(pending)
	return d
}

// Decide returns the §25.9 disposition for a diagnostic audit event
// without event metadata, retained for callers and tests that only need
// the disposition. It is Record with an empty EventType / OperationID.
func (l *Limiter) Decide(serviceAccount, resourceType, resourceID string, now time.Time) Decision {
	return l.Record(Call{ServiceAccount: serviceAccount, ResourceType: resourceType, ResourceID: resourceID}, now)
}

// Sweep flushes (emits) every coalescing window whose 60s have elapsed
// and prunes stale rate state so a long-running limiter does not
// accumulate entries. The diagnostic service runs it periodically so a
// closed window emits even during an idle period with no further calls.
func (l *Limiter) Sweep(now time.Time) {
	l.mu.Lock()
	pending := l.flushExpiredLocked(now)
	for sa, ts := range l.emits {
		if kept := pruneBefore(ts, now.Add(-time.Minute)); len(kept) == 0 {
			delete(l.emits, sa)
		} else {
			l.emits[sa] = kept
		}
	}
	l.mu.Unlock()
	l.emitAll(pending)
}

// Flush drains every open coalescing window regardless of age, emitting
// each window's accumulated event. The diagnostic service calls it on
// shutdown so no in-flight coalesced event is lost.
func (l *Limiter) Flush() {
	l.mu.Lock()
	out := make([]Event, 0, len(l.windows))
	for key, st := range l.windows {
		out = append(out, st.event())
		delete(l.windows, key)
	}
	l.mu.Unlock()
	l.emitAll(out)
}

// flushExpiredLocked removes every window whose coalescing window has
// elapsed and returns their Events. The caller emits them outside the
// lock so a flush callback cannot deadlock on l.mu. Caller holds l.mu.
func (l *Limiter) flushExpiredLocked(now time.Time) []Event {
	var out []Event
	for key, st := range l.windows {
		if now.Sub(st.opensAt) >= coalesceWindow {
			out = append(out, st.event())
			delete(l.windows, key)
		}
	}
	return out
}

// emitAll hands each Event to the flush callback. A nil callback drops
// them (the Decide-only use).
func (l *Limiter) emitAll(evs []Event) {
	if l.flush == nil {
		return
	}
	for _, e := range evs {
		l.flush(e)
	}
}

// pruneBefore returns the timestamps in ts at or after cutoff. ts is
// assumed ascending, as Record appends in time order.
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}
