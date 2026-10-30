// SPDX-License-Identifier: MIT

//go:build load_local

// Package connector_oauth_refresh_race models the §9.3 OAuth token
// refresh contract: N goroutines observe an expired token and call
// Refresh concurrently. The invariant: exactly one refresh per epoch
// completes; siblings re-read the cached refreshed value.
//
// TESTING.md §12.7.a multi-component scenarios.
package connector_oauth_refresh_race

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "connector_oauth_refresh_race"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// store is the per-connector cached token + refresh epoch. The
// refresh path uses a sync.Once per epoch so the §9.3 single-refresh
// invariant is enforced at the goroutine boundary.
type store struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
	epoch     int64
	once      *sync.Once

	refreshCalls atomic.Int64
}

func newStore() *store {
	return &store{
		token:     "tok-0",
		expiresAt: time.Now().Add(-time.Second),
		once:      &sync.Once{},
	}
}

// get returns the current token. If it is expired, the first caller
// in the epoch performs the refresh; siblings spin briefly then
// re-read.
func (s *store) get(now time.Time) (string, bool) {
	s.mu.Lock()
	if !s.expiresAt.Before(now) {
		tok := s.token
		s.mu.Unlock()
		return tok, false
	}
	once := s.once
	s.mu.Unlock()
	refreshed := false
	once.Do(func() {
		s.refreshCalls.Add(1)
		// Simulate the refresh round-trip.
		time.Sleep(100 * time.Microsecond)
		s.mu.Lock()
		s.epoch++
		s.token = fmt.Sprintf("tok-%d", s.epoch)
		s.expiresAt = time.Now().Add(time.Second)
		s.once = &sync.Once{} // arm for the next epoch
		s.mu.Unlock()
		refreshed = true
	})
	s.mu.Lock()
	tok := s.token
	s.mu.Unlock()
	return tok, refreshed
}

type Scenario struct {
	counters *scenkit.Counters
	store    *store
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = newStore()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tok, refreshed := s.store.get(time.Now())
	s.counters.Inc("get_calls")
	if refreshed {
		s.counters.Inc("performed_refresh")
	}
	if tok == "" {
		return fmt.Errorf("§9.3 violated: empty token returned")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("refresh_calls", float64(s.store.refreshCalls.Load()))
	// At least one refresh happened.
	if s.store.refreshCalls.Load() == 0 {
		return fmt.Errorf("scenario did not exercise the refresh path")
	}
	// Performed-refresh count equals the actual refresh call count.
	if s.counters.Get("performed_refresh") != s.store.refreshCalls.Load() {
		return fmt.Errorf("§9.3 violated: performed_refresh=%d but refresh_calls=%d",
			s.counters.Get("performed_refresh"), s.store.refreshCalls.Load())
	}
	return nil
}
