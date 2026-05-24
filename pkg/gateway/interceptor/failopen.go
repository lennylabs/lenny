// SPDX-License-Identifier: MIT

package interceptor

import (
	"context"
	"time"
)

// DefaultFailOpenMaxConsecutive is the §4.8 line 1030 default ceiling on
// fail-open interceptor errors within the rolling window before the
// gateway auto-escalates the interceptor to fail-closed.
const DefaultFailOpenMaxConsecutive = 10

// DefaultFailOpenWindow is the §4.8 line 1030 rolling window over which
// fail-open errors are counted toward the escalation ceiling.
const DefaultFailOpenWindow = 5 * time.Minute

// FailOpenEvent describes an auto-escalation transition for the §4.8
// `interceptor.fail_open_escalated` / `interceptor.fail_open_restored`
// audit events.
type FailOpenEvent struct {
	// TenantID scopes the audit row; taken from the request that
	// triggered the transition.
	TenantID string
	// InterceptorName is the escalated/restored interceptor's Name().
	InterceptorName string
	// Phase is the chain phase the interceptor runs on.
	Phase Phase
	// Consecutive is the fail-open error count in the window at the
	// escalation transition. Zero on restore.
	Consecutive int
	// WindowSeconds is the rolling-window width in seconds.
	WindowSeconds int
	// MaxConsecutive is the configured escalation ceiling.
	MaxConsecutive int
}

// FailOpenObserver receives the §4.8 line 1030 escalation transitions.
// The gateway implements it to write the two audit events to the
// per-tenant §11.7 chain. A nil observer disables event emission;
// escalation still takes effect.
type FailOpenObserver interface {
	// FailOpenEscalated fires when a fail-open interceptor crosses the
	// error ceiling and is auto-promoted to fail-closed.
	FailOpenEscalated(ctx context.Context, ev FailOpenEvent)
	// FailOpenRestored fires when an escalated interceptor recovers (a
	// subsequent error-free call) and reverts to fail-open.
	FailOpenRestored(ctx context.Context, ev FailOpenEvent)
}

// SetFailOpenEscalation configures the §4.8 line 1030 cumulative
// fail-open escalation. maxConsecutive ≤ 0 selects
// DefaultFailOpenMaxConsecutive; window ≤ 0 selects DefaultFailOpenWindow.
// observer may be nil. clock overrides time.Now for tests; pass nil in
// production.
func (c *Chain) SetFailOpenEscalation(maxConsecutive int, window time.Duration, observer FailOpenObserver, clock func() time.Time) {
	if maxConsecutive <= 0 {
		maxConsecutive = DefaultFailOpenMaxConsecutive
	}
	if window <= 0 {
		window = DefaultFailOpenWindow
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	c.failMu.Lock()
	defer c.failMu.Unlock()
	c.failEnabled = true
	c.failOpenMax = maxConsecutive
	c.failOpenWindow = window
	c.failObserver = observer
	c.failClock = clock
	if c.failStates == nil {
		c.failStates = map[string]*failOpenState{}
	}
}

// failOpenState is one interceptor's rolling error window plus its
// current escalation flag.
type failOpenState struct {
	errs      []time.Time
	escalated bool
}

func (c *Chain) failConfig() (int, time.Duration, func() time.Time) {
	maxN := c.failOpenMax
	if maxN <= 0 {
		maxN = DefaultFailOpenMaxConsecutive
	}
	window := c.failOpenWindow
	if window <= 0 {
		window = DefaultFailOpenWindow
	}
	clock := c.failClock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return maxN, window, clock
}

// recordFailOpenError records one fail-open interceptor error and
// reports whether the interceptor is now escalated to fail-closed (so
// the chain must reject rather than skip). It emits
// `interceptor.fail_open_escalated` on the crossing transition.
func (c *Chain) recordFailOpenError(ctx context.Context, ic Interceptor, req Request) bool {
	maxN, window, clock := c.failConfig()
	now := clock()
	c.failMu.Lock()
	if !c.failEnabled {
		// Escalation is unconfigured: the chain skips the fail-open error
		// without tracking, preserving the plain fail-open semantics.
		c.failMu.Unlock()
		return false
	}
	if c.failStates == nil {
		c.failStates = map[string]*failOpenState{}
	}
	st := c.failStates[ic.Name()]
	if st == nil {
		st = &failOpenState{}
		c.failStates[ic.Name()] = st
	}
	st.errs = pruneBefore(st.errs, now.Add(-window))
	st.errs = append(st.errs, now)
	var ev *FailOpenEvent
	if len(st.errs) > maxN {
		if !st.escalated {
			st.escalated = true
			ev = &FailOpenEvent{
				TenantID:        req.TenantID,
				InterceptorName: ic.Name(),
				Phase:           req.Phase,
				Consecutive:     len(st.errs),
				WindowSeconds:   int(window / time.Second),
				MaxConsecutive:  maxN,
			}
		}
	}
	escalated := st.escalated
	obs := c.failObserver
	c.failMu.Unlock()

	if ev != nil && obs != nil {
		obs.FailOpenEscalated(ctx, *ev)
	}
	return escalated
}

// recordFailOpenSuccess records an error-free call by a fail-open
// interceptor. When the interceptor was escalated, the success reverts
// it to fail-open and emits `interceptor.fail_open_restored`.
func (c *Chain) recordFailOpenSuccess(ctx context.Context, ic Interceptor, req Request) {
	c.failMu.Lock()
	if c.failStates == nil {
		c.failMu.Unlock()
		return
	}
	st := c.failStates[ic.Name()]
	if st == nil || !st.escalated {
		c.failMu.Unlock()
		return
	}
	_, window, _ := c.failConfig()
	st.escalated = false
	st.errs = nil
	ev := FailOpenEvent{
		TenantID:        req.TenantID,
		InterceptorName: ic.Name(),
		Phase:           req.Phase,
		WindowSeconds:   int(window / time.Second),
		MaxConsecutive:  c.failOpenMax,
	}
	obs := c.failObserver
	c.failMu.Unlock()

	if obs != nil {
		obs.FailOpenRestored(ctx, ev)
	}
}

// pruneBefore drops timestamps strictly before cutoff. The slice is
// time-ordered (appends use a monotonic clock), so the kept tail is
// contiguous.
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	if i == 0 {
		return ts
	}
	return append(ts[:0], ts[i:]...)
}
