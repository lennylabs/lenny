// SPDX-License-Identifier: MIT

//go:build load_local

// Package gateway_load_shedding models the §10.1 load-shedding
// contract: when the gateway's request queue depth exceeds a
// configured ceiling, new requests return 503 immediately instead
// of waiting on a saturated worker pool. The invariant: the gateway
// never exhausts resources; it sheds with a clean error envelope.
//
// TESTING.md §12.7.a resiliency scenarios.
package gateway_load_shedding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "gateway_load_shedding"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// queueGate is a bounded queue-depth gate. accept returns ErrShed
// when the queue is at capacity. The work goroutine drains slowly,
// so a steady arrival rate above the drain rate triggers the shed
// path.
type queueGate struct {
	cap     int
	work    chan struct{}
	mu      sync.Mutex
	depth   int
}

var errShed = errors.New("503 gateway shed")

func newQueueGate(cap int) *queueGate {
	g := &queueGate{cap: cap, work: make(chan struct{}, cap)}
	go g.drain()
	return g
}

func (g *queueGate) accept() error {
	g.mu.Lock()
	if g.depth >= g.cap {
		g.mu.Unlock()
		return errShed
	}
	g.depth++
	g.mu.Unlock()
	g.work <- struct{}{}
	return nil
}

func (g *queueGate) drain() {
	for range g.work {
		time.Sleep(500 * time.Microsecond)
		g.mu.Lock()
		g.depth--
		g.mu.Unlock()
	}
}

type Scenario struct {
	counters *scenkit.Counters
	gate     *queueGate
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.gate = newQueueGate(8)
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	err := s.gate.accept()
	if errors.Is(err, errShed) {
		s.counters.Inc("shed_503")
		return nil
	}
	s.counters.Inc("accepted")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	accepted := s.counters.Get("accepted")
	shed := s.counters.Get("shed_503")
	if accepted == 0 {
		return fmt.Errorf("scenario never accepted a request")
	}
	// §10.1 invariant: under sustained pressure above the drain
	// rate, the gateway must shed rather than queue indefinitely.
	if shed == 0 {
		return fmt.Errorf("§10.1 violated: gateway did not shed under sustained load (accepted=%d, shed=0)", accepted)
	}
	return nil
}
