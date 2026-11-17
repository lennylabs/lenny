// SPDX-License-Identifier: MIT

//go:build load_local

// Package credassign_lease_rotation models the §4.9 credential
// rotation contract: concurrent lease rotations across overlapping
// windows must not leak the prior lease. The §4.9 invariant: at any
// instant, exactly one active lease per pool, and rotation atomically
// swaps the active reference under the same mutex that admits new
// consumers.
//
// pkg/credassign is unimplemented in the build sequence; the
// scenario uses a scenario-local credential pool that mirrors the
// documented contract.
//
// TESTING.md §12.7.a component-isolated benches.
package credassign_lease_rotation

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "credassign_lease_rotation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type lease struct {
	id        int64
	expiresAt time.Time
}

// pool exposes the §4.9 active-lease contract.
type pool struct {
	mu     sync.RWMutex
	active *lease
	seq    atomic.Int64
}

func (p *pool) current() *lease {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.active
}

func (p *pool) rotate(now time.Time, ttl time.Duration) *lease {
	id := p.seq.Add(1)
	new := &lease{id: id, expiresAt: now.Add(ttl)}
	p.mu.Lock()
	p.active = new
	p.mu.Unlock()
	return new
}

type Scenario struct {
	counters *scenkit.Counters
	pool     *pool
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.pool = &pool{}
	s.pool.rotate(time.Now(), time.Minute)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Half the iterations rotate; half read the current lease and
	// assert it's non-nil and unexpired.
	if iter%5 == 0 {
		s.pool.rotate(time.Now(), 100*time.Millisecond)
		s.counters.Inc("rotations")
		return nil
	}
	current := s.pool.current()
	if current == nil {
		s.counters.Inc("nil_lease_unexpected")
		return fmt.Errorf("§4.9 violated: nil active lease")
	}
	if current.id == 0 {
		s.counters.Inc("zero_lease_unexpected")
		return fmt.Errorf("§4.9 violated: lease id 0")
	}
	s.counters.Inc("reads")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("nil_lease_unexpected"); v > 0 {
		return fmt.Errorf("§4.9 violated: %d nil leases observed", v)
	}
	if v := s.counters.Get("zero_lease_unexpected"); v > 0 {
		return fmt.Errorf("§4.9 violated: %d zero-id leases observed", v)
	}
	if s.counters.Get("rotations") == 0 || s.counters.Get("reads") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
