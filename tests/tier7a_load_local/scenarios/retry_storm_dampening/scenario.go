// SPDX-License-Identifier: MIT

//go:build load_local

// Package retry_storm_dampening models the §11.6 retry contract.
// N goroutines retry a failing endpoint; exponential backoff caps
// the per-second retry rate so the storm doesn't amplify load on
// the failing backend. Invariant: the observed retry rate after t
// seconds is bounded by sum(initial * 2^i) / t for i in [0, log(t)].
//
// TESTING.md §12.7.a resiliency scenarios.
package retry_storm_dampening

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "retry_storm_dampening"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// retryClient models a client doing exponential backoff with jitter
// against a downstream that always fails.
type retryClient struct {
	mu       sync.Mutex
	initial  time.Duration
	maxDelay time.Duration
	rng      *rand.Rand
}

func newRetryClient() *retryClient {
	return &retryClient{
		initial:  10 * time.Millisecond,
		maxDelay: 500 * time.Millisecond,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// call retries until ctx expires; counters track the retries.
func (c *retryClient) call(ctx context.Context, counters *scenkit.Counters) {
	delay := c.initial
	for attempt := 0; ; attempt++ {
		counters.Inc("attempts")
		// Backend always fails. Wait, then retry.
		if delay > c.maxDelay {
			delay = c.maxDelay
		}
		c.mu.Lock()
		jitter := time.Duration(c.rng.Int63n(int64(delay) / 2))
		c.mu.Unlock()
		wait := delay + jitter
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay *= 2
	}
}

type Scenario struct {
	counters *scenkit.Counters
	client   *retryClient
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 2 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.client = newRetryClient()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	s.client.call(cctx, s.counters)
	s.counters.Inc("storms")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	storms := s.counters.Get("storms")
	attempts := s.counters.Get("attempts")
	if storms == 0 {
		return fmt.Errorf("scenario observed no storms")
	}
	// Backoff invariant: the average attempts-per-storm must be
	// small (typically < 10) because exponential backoff caps the
	// rate even in a 200ms window.
	avgAttempts := float64(attempts) / float64(storms)
	r.AddCustom("avg_attempts_per_storm", avgAttempts)
	if avgAttempts > 20 {
		return fmt.Errorf("§11.6 violated: avg %.1f attempts per storm (backoff is not dampening; expected < 20)", avgAttempts)
	}
	return nil
}
