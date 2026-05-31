// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

// autoExtensionLimiter enforces the §8.6 line 712 auto-mode rate limit.
// It counts auto-mode extension requests per task tree per one-minute
// window and reports when a tree has exceeded its configured
// maxAutoExtensionsPerMinute, at which point ExtendLease pauses
// auto-approval and falls back to elicitation for the remainder of the
// window — the safety valve against a runaway agent draining the entire
// maxExtendableBudget without human visibility.
//
// It reuses the §11.1 ratelimit.Counter, the per-minute fixed-window
// primitive the request-rate middleware and the §7.2 messaging limiter
// already use, keyed per tenant and tree. The counter increments on every
// auto-mode request; the limit comparison rolls with the window, so once
// a tree trips the limit it stays in elicitation until the window
// advances. spec: §8.6 line 712.
type autoExtensionLimiter struct {
	counter ratelimit.Counter
}

// newAutoExtensionLimiter returns a limiter over counter, defaulting to an
// in-memory counter when none is supplied.
func newAutoExtensionLimiter(counter ratelimit.Counter) *autoExtensionLimiter {
	if counter == nil {
		counter = ratelimit.NewMemory()
	}
	return &autoExtensionLimiter{counter: counter}
}

// over records one auto-mode extension request against the tree's current
// one-minute window and reports whether the tree has now exceeded
// maxPerMin. A maxPerMin <= 0 means "no limit" — the §8.6 line 712 default
// — so the limiter never increments and never trips, leaving auto mode
// fully independent. spec: §8.6 line 712.
func (l *autoExtensionLimiter) over(ctx context.Context, tenantID, rootSessionID string, maxPerMin int, now time.Time) (bool, error) {
	if maxPerMin <= 0 {
		return false, nil
	}
	key := fmt.Sprintf("lease_ext_auto:%s:%s", tenantID, rootSessionID)
	n, err := l.counter.Incr(ctx, key, now)
	if err != nil {
		return false, err
	}
	return n > maxPerMin, nil
}
