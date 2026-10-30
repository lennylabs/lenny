// SPDX-License-Identifier: MIT

package lenny

import (
	"math/rand"
	"time"
)

// RetryPolicy governs how the Client retries retryable errors. The
// zero value is not used directly; the Client fills unset fields
// from DefaultRetryPolicy.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the
	// first. A value of 1 disables retries. Values below 1 are
	// clamped to 1.
	MaxAttempts int

	// BaseDelay is the delay before the first retry. Each subsequent
	// retry doubles the delay up to MaxDelay.
	BaseDelay time.Duration

	// MaxDelay caps the per-attempt delay after exponential growth.
	MaxDelay time.Duration

	// Jitter, when true, multiplies each computed delay by a random
	// factor in [0.5, 1.0). Jitter spreads retries from many clients
	// so a recovering gateway is not hit by a synchronized burst.
	Jitter bool
}

// DefaultRetryPolicy is the retry policy a Client uses when the
// caller supplies none. It retries up to three times with
// exponential backoff and jitter.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts: 3,
	BaseDelay:   200 * time.Millisecond,
	MaxDelay:    5 * time.Second,
	Jitter:      true,
}

// normalized returns the policy with unset fields filled from
// DefaultRetryPolicy and MaxAttempts clamped to at least 1.
func (p RetryPolicy) normalized() RetryPolicy {
	out := p
	if out.MaxAttempts < 1 {
		out.MaxAttempts = DefaultRetryPolicy.MaxAttempts
	}
	if out.MaxAttempts < 1 {
		out.MaxAttempts = 1
	}
	if out.BaseDelay <= 0 {
		out.BaseDelay = DefaultRetryPolicy.BaseDelay
	}
	if out.MaxDelay <= 0 {
		out.MaxDelay = DefaultRetryPolicy.MaxDelay
	}
	return out
}

// delayForAttempt returns the wait before retry number attempt
// (attempt is 1-indexed: the wait before the first retry is
// attempt 1). The delay grows as BaseDelay * 2^(attempt-1), capped
// at MaxDelay, with optional jitter. rng supplies the jitter
// randomness; pass nil to use the package-global source.
func (p RetryPolicy) delayForAttempt(attempt int, rng *rand.Rand) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if delay > p.MaxDelay {
		delay = p.MaxDelay
	}
	if p.Jitter {
		factor := 0.5 + randFloat(rng)*0.5
		delay = time.Duration(float64(delay) * factor)
	}
	return delay
}

// randFloat returns a float in [0.0, 1.0) from rng, falling back to
// the package-global source when rng is nil.
func randFloat(rng *rand.Rand) float64 {
	if rng != nil {
		return rng.Float64()
	}
	return rand.Float64()
}
