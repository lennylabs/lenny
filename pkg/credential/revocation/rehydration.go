// SPDX-License-Identifier: MIT

package revocation

import (
	"context"
	"time"
)

// DefaultReconnectSpacing is the minimum interval between the starts of
// two rehydrations that Trigger requests start, which the gateway passes
// to RunRehydration unless an operator overrides it. It bounds the reads
// a flapping Redis connection causes at one per replica per spacing, and
// delays a request's rehydration by at most the spacing, well inside the
// 30-second periodic bound.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
const DefaultReconnectSpacing = 5 * time.Second

// Trigger requests an immediate rehydration from RunRehydration. It
// holds at most one pending request, so a burst of requests while a
// rehydration runs, or before the loop reads the first one, causes one
// more rehydration rather than one per request. The zero value is not
// usable; construct with NewTrigger. A nil *Trigger ignores Fire.
type Trigger struct {
	ch chan struct{}
}

// NewTrigger returns a Trigger with no pending request.
func NewTrigger() *Trigger {
	return &Trigger{ch: make(chan struct{}, 1)}
}

// Fire records a request for an immediate rehydration and returns at
// once. A request already pending absorbs this one. Fire never blocks, so
// the pub/sub subscribe loop calls it inline on each subscription
// confirmation.
func (t *Trigger) Fire() {
	if t == nil {
		return
	}
	select {
	case t.ch <- struct{}{}:
	default:
	}
}

// requests returns the channel RunRehydration reads. A nil Trigger yields
// a nil channel, which a select never chooses.
func (t *Trigger) requests() <-chan struct{} {
	if t == nil {
		return nil
	}
	return t.ch
}

// discard drops a pending request, if any. RunRehydration calls it just
// before a held request's rehydration starts, because that read already
// covers every request made before it began.
func (t *Trigger) discard() {
	if t == nil {
		return
	}
	select {
	case <-t.ch:
	default:
	}
}

// RunRehydration rehydrates the cache from src every interval and on each
// request trigger holds, until ctx is cancelled. A request does not move
// the periodic schedule: the next tick comes interval after the previous
// one.
//
// Two rehydrations that requests start begin at least spacing apart. A
// request that arrives sooner after the start of the previous requested
// rehydration is held until the spacing has passed, while the periodic
// ticks run as usual, and further requests made while it is held fold
// into it. A spacing of zero or less starts every request's rehydration
// at once.
//
// Each rehydration runs under a deadline of one interval, so a read that
// hangs on an unresponsive connection fails and the loop reaches the next
// tick or request rather than blocking until the operating system gives
// up on the connection. A failed rehydration leaves the set and the time
// of the last success unchanged, so the staleness gate keeps counting
// from the last success, and is passed to onError unless ctx is already
// cancelled. It blocks; the gateway runs it in a goroutine after the
// startup rehydration.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation".
func (c *Cache) RunRehydration(ctx context.Context, interval, spacing time.Duration, trigger *Trigger, tenants TenantLister, src Source, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	spacer := requestSpacer{min: spacing, now: c.clock, after: c.after}
	for {
		requests := trigger.requests()
		if spacer.holding() {
			requests = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-requests:
			if !spacer.admit() {
				continue
			}
		case <-spacer.held:
			trigger.discard()
			spacer.release()
		}
		if err := c.rehydrateWithin(ctx, interval, tenants, src); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
}

// requestSpacer enforces the minimum interval between the starts of two
// requested rehydrations. It is owned by one RunRehydration goroutine and
// needs no lock.
type requestSpacer struct {
	min   time.Duration
	now   func() time.Time
	after func(time.Duration) <-chan time.Time
	// last is the start of the previous requested rehydration; the zero
	// value means none has started.
	last time.Time
	// held delivers when a held request may start; nil while none is held.
	held <-chan time.Time
}

// holding reports whether a request is held until the spacing passes.
func (s *requestSpacer) holding() bool { return s.held != nil }

// admit reports whether a request may start a rehydration now and, if so,
// records the start. Otherwise it holds the request for the rest of the
// spacing. The wait never exceeds the spacing, so a clock that steps back
// does not hold a request longer.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation".
func (s *requestSpacer) admit() bool {
	now := s.now()
	if s.min > 0 && !s.last.IsZero() {
		if wait := min(s.min-now.Sub(s.last), s.min); wait > 0 {
			s.held = s.after(wait)
			return false
		}
	}
	s.last = now
	return true
}

// release ends the hold and records the start of the held request's
// rehydration.
func (s *requestSpacer) release() {
	s.held = nil
	s.last = s.now()
}

// rehydrateWithin runs one Rehydrate under a deadline of d.
func (c *Cache) rehydrateWithin(ctx context.Context, d time.Duration, tenants TenantLister, src Source) error {
	readCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return c.Rehydrate(readCtx, tenants, src)
}
