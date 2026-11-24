// SPDX-License-Identifier: MIT

package revocation

import (
	"context"
	"time"
)

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
// the pub/sub subscribe loop calls it inline on a reconnect.
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

// RunRehydration rehydrates the cache from src every interval and at once
// on each request trigger holds, until ctx is cancelled. A request does
// not move the periodic schedule: the next tick comes interval after the
// previous one. A failed rehydration leaves the set and the time of the
// last success unchanged, so the staleness gate keeps counting from the
// last success, and is passed to onError unless ctx is already cancelled.
// It blocks; the gateway runs it in a goroutine after the startup
// rehydration.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation".
func (c *Cache) RunRehydration(ctx context.Context, interval time.Duration, trigger *Trigger, tenants TenantLister, src Source, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-trigger.requests():
		}
		if err := c.Rehydrate(ctx, tenants, src); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
}
