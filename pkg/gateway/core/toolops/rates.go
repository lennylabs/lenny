// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/ratewindow"
)

// RateCounter serves seam.RateCounter over the gateway's request-rate
// counter, or over an in-memory counter of its own when none is wired.
type RateCounter struct {
	counter ratelimit.Counter
}

var _ seam.RateCounter = RateCounter{}

// NewRateCounter returns the rate counter over counter. A nil counter
// counts in memory, as the platform tools seam's limiters did with none
// wired.
func NewRateCounter(counter ratelimit.Counter) RateCounter {
	if counter == nil {
		counter = ratewindow.New()
	}
	return RateCounter{counter: counter}
}

// IncrementRate counts one request under key in the counter's minute
// window and returns the count so far.
// spec: GWSEAM-IncrementRate
func (r RateCounter) IncrementRate(ctx context.Context, key string, now time.Time) (int, error) {
	return r.counter.Incr(ctx, key, now)
}
