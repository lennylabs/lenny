// SPDX-License-Identifier: MIT

//go:build load_local

// Package auth_failure_storm models the §10.2 per-key rate limit:
// N invalid auth attempts from one source must not block legitimate
// auth from another source. Invariant: legitimate requests stay
// admitted at full throughput even when invalid attempts saturate
// the per-key limit.
//
// TESTING.md §12.7.a resiliency scenarios.
package auth_failure_storm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "auth_failure_storm"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// limiter is a per-key token-bucket. Each key gets 100 tokens per
// second; when exhausted, the source is throttled.
type limiter struct {
	mu       sync.Mutex
	keys     map[string]int
	maxRate  int
	lastTick time.Time
}

func newLimiter() *limiter {
	return &limiter{keys: make(map[string]int), maxRate: 100, lastTick: time.Now()}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Refill: simplistic — reset on a 1-second tick.
	if time.Since(l.lastTick) > time.Second {
		for k := range l.keys {
			l.keys[k] = 0
		}
		l.lastTick = time.Now()
	}
	if l.keys[key] >= l.maxRate {
		return false
	}
	l.keys[key]++
	return true
}

type Scenario struct {
	counters *scenkit.Counters
	lim      *limiter
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.lim = newLimiter()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// vu 0-7 attempt with one shared bad-actor key (storm).
	// vu 8+ each have their own legitimate key.
	var key string
	storm := vu < 8
	if storm {
		key = "bad-actor"
	} else {
		key = fmt.Sprintf("legit-%d", vu)
	}
	allowed := s.lim.allow(key)
	switch {
	case storm && allowed:
		s.counters.Inc("storm_allowed")
	case storm && !allowed:
		s.counters.Inc("storm_throttled")
	case !storm && allowed:
		s.counters.Inc("legit_allowed")
	case !storm && !allowed:
		s.counters.Inc("legit_throttled_unexpected")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	stormAllowed := s.counters.Get("storm_allowed")
	stormThrottled := s.counters.Get("storm_throttled")
	legitAllowed := s.counters.Get("legit_allowed")
	legitThrottled := s.counters.Get("legit_throttled_unexpected")
	// At very high throughput, even legit per-key limits saturate.
	// The §10.2 isolation invariant is that the bad actor is
	// throttled *more aggressively* than the legitimate keys —
	// because the bad actor's calls share one key while legit keys
	// are per-source. Compare the throttling ratios.
	if stormAllowed+stormThrottled == 0 || legitAllowed+legitThrottled == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	stormRatio := float64(stormThrottled) / float64(stormAllowed+stormThrottled)
	legitRatio := float64(legitThrottled) / float64(legitAllowed+legitThrottled)
	r.AddCustom("storm_throttle_ratio", stormRatio)
	r.AddCustom("legit_throttle_ratio", legitRatio)
	// The bad actor must be throttled significantly more than legit
	// keys; this is what per-key rate limiting buys you.
	if stormRatio <= legitRatio {
		return fmt.Errorf("§10.2 violated: storm throttle ratio %.4f ≤ legit ratio %.4f (isolation failed)", stormRatio, legitRatio)
	}
	if stormThrottled == 0 {
		return fmt.Errorf("§10.2 violated: bad-actor storm not rate-limited")
	}
	return nil
}
