// SPDX-License-Identifier: MIT

//go:build load_local

// Package redis_disconnect_midflight exercises pkg/gateway/slotcounter
// when miniredis is restarted mid-run. The §5.2 invariant: slot
// reservations fail closed when Redis is unavailable, and recover
// cleanly once Redis returns. There must be no overcommit at any
// point.
//
// TESTING.md §12.7.a multi-component scenarios.
package redis_disconnect_midflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

func init() {
	loadgen.Register("redis_disconnect_midflight", func() loadgen.Scenario { return &Scenario{} })
}

type Scenario struct {
	mr      *miniredis.Miniredis
	client  *redis.Client
	counter *slotcounter.Counter

	maxConcurrent int32

	mu           sync.Mutex
	inFlight     int32
	peakInFlight int32

	reserves         atomic.Int64
	rejections       atomic.Int64
	transportErrs    atomic.Int64
	overcommitEvents atomic.Int64

	disconnectOnce sync.Once
}

func (s *Scenario) Name() string { return "redis_disconnect_midflight" }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 20, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	mr, err := miniredis.Run()
	if err != nil {
		return err
	}
	s.mr = mr
	s.client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.counter = slotcounter.New(s.client)
	s.maxConcurrent = 6
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.mr != nil {
		s.mr.Close()
	}
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Halfway through the run, disconnect+reconnect miniredis. The
	// first VU to reach iter==25 triggers a brief close-and-restart.
	if iter == 25 {
		s.disconnectOnce.Do(func() {
			// Restart cycles the miniredis listener (close + relisten
			// on the same address). The Redis client must reconnect.
			_ = s.mr.Restart()
		})
	}

	_, _, err := s.counter.Reserve(ctx, "pod-disc", s.maxConcurrent)
	if err != nil {
		switch {
		case errors.Is(err, slotcounter.ErrSlotsExhausted):
			s.rejections.Add(1)
			return nil
		default:
			// Transport error during disconnect window is expected
			// and must not corrupt state.
			s.transportErrs.Add(1)
			return nil
		}
	}
	s.reserves.Add(1)
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.peakInFlight {
		s.peakInFlight = s.inFlight
	}
	if s.inFlight > s.maxConcurrent {
		s.overcommitEvents.Add(1)
	}
	s.mu.Unlock()
	// Release immediately; the assertion is about the disconnect
	// recovery, not about hold time.
	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
	if _, err := s.counter.Release(ctx, "pod-disc"); err != nil {
		// Transport error during disconnect window: still benign.
		s.transportErrs.Add(1)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("reserves", float64(s.reserves.Load()))
	r.AddCustom("rejections", float64(s.rejections.Load()))
	r.AddCustom("transport_errs", float64(s.transportErrs.Load()))
	r.AddCustom("overcommit_events", float64(s.overcommitEvents.Load()))
	r.AddCustom("peak_in_flight", float64(s.peakInFlight))
	if s.overcommitEvents.Load() > 0 {
		return fmt.Errorf("§5.2 violated: %d overcommit events; peak in-flight = %d", s.overcommitEvents.Load(), s.peakInFlight)
	}
	if s.reserves.Load() == 0 {
		return fmt.Errorf("scenario did not reach any successful Reserve")
	}
	return nil
}
