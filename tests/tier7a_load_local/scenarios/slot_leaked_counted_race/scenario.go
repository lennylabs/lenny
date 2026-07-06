// SPDX-License-Identifier: MIT

//go:build load_local

// Package slot_leaked_counted_race exercises the §6.2 leaked-slot occupancy
// invariant on podclaim.SlotClaimer.ReleaseSlot under high goroutine
// concurrency against miniredis.
//
// §6.2 requires a leaked slot to remain counted in the pod's Redis slot-counter
// occupancy until pod termination, so the gateway does not over-assign a new
// slot into the leaked slot's unreleased resources. SlotClaimer.ReleaseSlot
// enforces this by skipping the counter decrement when leaked is true. This
// scenario races many clean releases against a fixed set of leaked slots on one
// pod and asserts the pod's occupancy never drops below the leaked-slot floor:
// a clean release decrements, a leaked release does not, and no interleaving
// frees a leaked slot's occupancy.
//
// Regression source: proposal 0035 CODE-C. The pre-fix ReleaseSlot decremented
// the Redis counter unconditionally (it discarded the ShutdownSlot
// exitedCleanly result), so a leaked slot was decremented and the pod could be
// over-assigned. This scenario re-creates the concurrent release surface and
// asserts occupancy never falls below the leaked floor.
//
// TESTING.md §12.7.a regression scenarios.
package slot_leaked_counted_race

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "slot_leaked_counted_race"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario reserves a clean slot, then releases it either cleanly or leaked,
// against a pod pre-seeded with a fixed number of permanently-leaked slots.
// The invariant checked in Run and Assert: the pod's occupancy (the Redis
// counter) never drops below the leaked-slot floor, because a leaked release
// never decrements.
type Scenario struct {
	counters *scenkit.Counters

	mr      *miniredis.Miniredis
	client  *redis.Client
	counter *slotcounter.Counter
	claimer *podclaim.SlotClaimer

	// podID is the pod every iteration reserves and releases against.
	podID string
	// leakedFloor is the fixed count of permanently-leaked slots seeded on the
	// pod at Setup. Occupancy must never fall below this value.
	leakedFloor int32
	// maxConcurrent bounds the Redis counter; large enough that clean reserves
	// under load are not the bottleneck (the test targets the release gate, not
	// exhaustion).
	maxConcurrent int32

	// mu serializes the probe-reserve/probe-release pair in Run so a concurrent
	// iteration does not observe the transient +1 the probe adds. The invariant
	// under test is the leaked floor, which the probe never mutates.
	mu sync.Mutex
	// leakBudget bounds how many iterations leak (reserve-then-leaked-release),
	// so leaked slots accumulate to a bounded ceiling rather than exhausting the
	// counter over a long run. Each leaked release permanently adds one to
	// occupancy (§6.2), so the running floor is leakedFloor plus the leaks
	// committed so far.
	leakBudget int32
	// leaksCommitted counts leaked releases that actually ran (the reserve
	// succeeded and the leaked release skipped the decrement), so the floor
	// assertion can raise the expected minimum occupancy as leaks accumulate.
	leaksCommitted atomic.Int32
	// floorBreaches counts any observation of occupancy below the running floor
	// (leakedFloor plus committed leaks). A breach means a leaked slot was
	// wrongly decremented.
	floorBreaches atomic.Int64
}

func (s *Scenario) Name() string { return name }

// RampProfiles enumerates ascending VU counts for capacity discovery under
// LENNY_TIER7A_CAPACITY=1.
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 2 * time.Second},
	}
}

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	mr, err := miniredis.Run()
	if err != nil {
		return fmt.Errorf("miniredis start: %w", err)
	}
	s.mr = mr
	s.client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.counter = slotcounter.New(s.client)
	s.podID = "pod-leak"
	s.leakedFloor = 2
	s.leakBudget = 64
	// A high cap keeps the reserve path from being the bottleneck: the test
	// targets the release-gate decrement, so clean reserves must almost always
	// succeed rather than exhaust. Occupancy is bounded by leakedFloor plus the
	// bounded leak budget plus transient in-flight clean/probe slots, so a cap
	// well above that leaves the release gate the exercised path.
	s.maxConcurrent = 100_000
	// A nil Client is safe: every release in this scenario keeps occupancy
	// above zero (the leaked floor plus in-flight clean slots), so ReleaseSlot
	// never reaches the occupancy-zero disposition that touches the k8s client.
	// The scenario targets the counter-decrement gate, not the claim
	// disposition.
	s.claimer = &podclaim.SlotClaimer{Counter: s.counter}

	// Seed the permanently-leaked floor: reserve leakedFloor slots that are
	// never released. These hold occupancy for the whole run.
	for i := int32(0); i < s.leakedFloor; i++ {
		if _, _, err := s.counter.Reserve(ctx, s.podID, s.maxConcurrent); err != nil {
			return fmt.Errorf("seed leaked slot %d: %w", i, err)
		}
	}
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

// Run reserves a clean slot, then releases it via SlotClaimer.ReleaseSlot,
// leaked while the bounded leak budget remains and clean thereafter. A leaked
// release must not decrement (its slot stays counted, §6.2); a clean release
// must decrement (its slot is reclaimed). The running floor is the seeded
// leakedFloor plus every leak committed so far; occupancy must never read below
// it, which fails only if a leaked slot was wrongly decremented.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	sessionID := fmt.Sprintf("sess-%d-%d", vu, iter)
	if _, _, err := s.counter.Reserve(ctx, s.podID, s.maxConcurrent); err != nil {
		// Exhaustion is not expected at this bound; count and skip rather than
		// fail the whole run on a transient miniredis contention edge.
		s.counters.Inc("reserve_rejected")
		return nil
	}

	// Leak until the budget is spent, then run clean cycles. leaksCommitted is
	// the source of truth for how many leaked slots are permanently held, so
	// the floor check below raises the expected minimum accordingly.
	leaked := s.leaksCommitted.Load() < s.leakBudget
	if leaked {
		s.leaksCommitted.Add(1)
		s.counters.Inc("leaked_release")
	} else {
		s.counters.Inc("clean_release")
	}
	if _, err := s.claimer.ReleaseSlot(ctx, s.podID, sessionID, false, leaked); err != nil {
		return fmt.Errorf("ReleaseSlot(leaked=%v): %w", leaked, err)
	}

	// After the release, occupancy must never read below the running floor
	// (leakedFloor plus committed leaks). A probe reserve returns the
	// post-reserve count; occupancy before the probe is that minus one. Release
	// the probe immediately so it does not accumulate. The probe pair is
	// serialized so a concurrent iteration does not see the transient +1.
	s.mu.Lock()
	postProbe, _, err := s.counter.Reserve(ctx, s.podID, s.maxConcurrent)
	if err == nil {
		occupancy := postProbe - 1
		floor := s.leakedFloor + s.leaksCommitted.Load()
		if occupancy < floor {
			s.floorBreaches.Add(1)
		}
		_, _ = s.counter.Release(ctx, s.podID)
	}
	s.mu.Unlock()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("floor_breaches", float64(s.floorBreaches.Load()))
	if s.floorBreaches.Load() > 0 {
		return fmt.Errorf("§6.2 violated: occupancy dropped below the leaked-slot floor %d times; a leaked slot was decremented",
			s.floorBreaches.Load())
	}
	if s.counters.Get("leaked_release") == 0 || s.counters.Get("clean_release") == 0 {
		return fmt.Errorf("scenario must exercise both leaked and clean release paths (leaked=%d clean=%d)",
			s.counters.Get("leaked_release"), s.counters.Get("clean_release"))
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}
	return nil
}
