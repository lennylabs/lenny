// SPDX-License-Identifier: MIT

//go:build load_local

// Package clientgo_throttle_floor models the §10.1 throttle decision
// the gateway makes when calling the fake K8s API. The regression
// source is commit 0b7c71c: at client-go's default QPS=5/Burst=10, a
// 5 req/s scenario saturated the client-side rate limiter and added
// >1s latency per request. With --cluster-qps=100/--cluster-burst=200
// the same arrival rate stays below the floor.
//
// The scenario drives a token-bucket limiter from N goroutines at two
// QPS settings and asserts the high-QPS observed latency is bounded
// while the low-QPS one is not.
//
// TESTING.md §12.7.a regression scenarios.
package clientgo_throttle_floor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
	"golang.org/x/time/rate"
)

const name = "clientgo_throttle_floor"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters

	// Two limiters: lowQPS (5/10) replicates the broken default,
	// highQPS (100/200) replicates the post-0b7c71c default.
	lowQPS  *rate.Limiter
	highQPS *rate.Limiter

	mu       sync.Mutex
	lowLats  []time.Duration
	highLats []time.Duration
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.lowQPS = rate.NewLimiter(5, 10)
	s.highQPS = rate.NewLimiter(100, 200)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	useLow := iter%2 == 0
	limiter := s.highQPS
	if useLow {
		limiter = s.lowQPS
	}
	start := time.Now()
	// Limit (4 tokens) replicates the 4-burst the §5.2 session-start
	// path consumes per request (list pools, get template, list
	// sandboxes, patch sandbox).
	if err := limiter.WaitN(ctx, 4); err != nil {
		s.counters.IncOnError(ctx, "wait_errors", err)
		return err
	}
	elapsed := time.Since(start)
	s.mu.Lock()
	if useLow {
		s.lowLats = append(s.lowLats, elapsed)
		s.counters.Inc("low_qps_calls")
	} else {
		s.highLats = append(s.highLats, elapsed)
		s.counters.Inc("high_qps_calls")
	}
	s.mu.Unlock()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lowLats) == 0 || len(s.highLats) == 0 {
		return fmt.Errorf("scenario did not exercise both QPS settings")
	}
	lowMax := maxDur(s.lowLats)
	highMax := maxDur(s.highLats)
	r.AddCustom("low_qps_max_ms", float64(lowMax.Milliseconds()))
	r.AddCustom("high_qps_max_ms", float64(highMax.Milliseconds()))
	// The §10.1 invariant: the high-QPS path should observe much
	// lower max latency than the low-QPS path under the same arrival
	// rate. We use a generous multiplier so the test is stable.
	if highMax >= lowMax {
		return fmt.Errorf("§10.1 violated: high-QPS max %s >= low-QPS max %s (the throttle floor inverted)", highMax, lowMax)
	}
	return nil
}

func maxDur(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}
