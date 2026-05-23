// SPDX-License-Identifier: MIT

//go:build load_local

// Package auth_failure_storm asserts the §11.1 per-user request-rate
// isolation invariant: a single shared bad-actor key that exhausts
// its per-minute budget must not consume the global budget headroom
// that legitimate per-user keys depend on. The scenario drives the
// real pkg/gateway/ratelimit.Memory counter, increments the
// bad-actor scope from VUs 0..7 and per-VU legitimate scopes from
// VUs 8+, and compares throttling ratios.
//
// TESTING.md §12.7.a resiliency scenarios.
package auth_failure_storm

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "auth_failure_storm"

// perKeyLimit is the §11.1 per-user requests-per-minute cap the
// scenario enforces against ratelimit.Memory's running count.
const perKeyLimit = 100

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	counter  *ratelimit.Memory
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
	s.counter = ratelimit.NewMemory()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// VUs 0-7 share one bad-actor key (storm). VUs 8+ each have
	// their own legitimate key. The §11.1 per-user scope keeps the
	// two from interfering: bad-actor's count climbs through one
	// key and saturates; legit keys climb independently.
	var key string
	storm := vu < 8
	if storm {
		key = "u:acme:bad-actor"
	} else {
		key = fmt.Sprintf("u:acme:legit-%d", vu)
	}
	count, err := s.counter.Incr(ctx, key, time.Now())
	if err != nil {
		s.counters.Inc("counter_error")
		return nil
	}
	allowed := count <= perKeyLimit
	switch {
	case storm && allowed:
		s.counters.Inc("storm_allowed")
	case storm && !allowed:
		s.counters.Inc("storm_throttled")
	case !storm && allowed:
		s.counters.Inc("legit_allowed")
	case !storm && !allowed:
		s.counters.Inc("legit_throttled")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	stormAllowed := s.counters.Get("storm_allowed")
	stormThrottled := s.counters.Get("storm_throttled")
	legitAllowed := s.counters.Get("legit_allowed")
	legitThrottled := s.counters.Get("legit_throttled")
	if stormAllowed+stormThrottled == 0 || legitAllowed+legitThrottled == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	stormRatio := float64(stormThrottled) / float64(stormAllowed+stormThrottled)
	legitRatio := float64(legitThrottled) / float64(legitAllowed+legitThrottled)
	r.AddCustom("storm_throttle_ratio", stormRatio)
	r.AddCustom("legit_throttle_ratio", legitRatio)
	// The §11.1 isolation invariant: the bad-actor key, shared by 8
	// VUs, saturates its per-user cap and gets throttled at a
	// significantly higher rate than per-VU legitimate keys.
	if stormRatio <= legitRatio {
		return fmt.Errorf("§11.1 violated: storm throttle ratio %.4f ≤ legit ratio %.4f (per-user isolation failed)", stormRatio, legitRatio)
	}
	if stormThrottled == 0 {
		return fmt.Errorf("§11.1 violated: bad-actor storm not rate-limited")
	}
	return nil
}
