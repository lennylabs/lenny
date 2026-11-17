// SPDX-License-Identifier: MIT

//go:build load_local

// Package slot_leaked_counted_race exercises the §6.2 leaked-slot occupancy
// invariant on podclaim.SlotClaimer.ReleaseSlot under high goroutine
// concurrency against miniredis.
//
// §6.2 requires a leaked slot to remain counted in the pod's slot set until
// pod termination, so the gateway does not over-assign a new slot into the
// leaked slot's unreleased resources. The keyed release script enforces this:
// a leaked release removes the session's member and adds
// leaked:<sessionId>, whether or not it removed a member, so a leak reported
// after a clean release of the same session is still counted. This scenario
// races many clean, leaked, and clean-then-leaked releases, each keyed by a
// distinct session, against a fixed set of seeded members on one pod, and
// asserts that the set never holds fewer members than the seeded floor plus
// the committed leaks, that every leak leaves its leaked: member and no plain
// member, and that at the end the set holds exactly the seeded members and
// one leaked: member per leak.
//
// Regression source: the release that discarded the shutdown's
// exitedCleanly result freed a leaked slot and let the pod be over-assigned.
//
// TESTING.md §12.7.a regression scenarios.
package slot_leaked_counted_race

import (
	"context"
	"fmt"
	"strings"
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
// The invariant checked in Run and Assert: the pod's occupancy (the slot
// set's cardinality) never drops below the seeded floor plus the committed
// leaks, because a leaked release keeps its slot counted.
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

	// leakBudget bounds how many iterations leak (reserve-then-leaked-release),
	// so leaked slots accumulate to a bounded ceiling rather than exhausting the
	// counter over a long run. Each leaked release permanently adds one to
	// occupancy (§6.2), so the running floor is leakedFloor plus the leaks
	// committed so far.
	leakBudget int32
	// leaksCommitted counts leaked releases that ran (the reserve succeeded
	// and the release added the leaked: member), so the floor
	// assertion can raise the expected minimum occupancy as leaks accumulate.
	leaksCommitted atomic.Int32
	// leakClaims counts the leak budget taken; it may run ahead of
	// leaksCommitted while a leaked release is in flight.
	leakClaims atomic.Int32
	// floorBreaches counts any observation of occupancy below the running floor
	// (leakedFloor plus committed leaks). A breach means a leaked slot was
	// wrongly freed.
	floorBreaches atomic.Int64
	// memberBreaches counts releases whose set result was wrong: a leak
	// without its leaked: member, or a released session still a member.
	memberBreaches atomic.Int64
	// finalMembers and finalLeaked are the set's cardinality and its count of
	// leaked: members after the run.
	finalMembers int64
	finalLeaked  int64
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
		if _, _, err := s.counter.Reserve(ctx, s.podID, fmt.Sprintf("floor-%d", i), s.maxConcurrent); err != nil {
			return fmt.Errorf("seed leaked slot %d: %w", i, err)
		}
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.client != nil {
		if members, err := s.client.SMembers(context.WithoutCancel(ctx), s.setKey()).Result(); err == nil {
			s.finalMembers = int64(len(members))
			for _, m := range members {
				if strings.HasPrefix(m, "leaked:") {
					s.finalLeaked++
				}
			}
		}
		_ = s.client.Close()
	}
	if s.mr != nil {
		s.mr.Close()
	}
	return nil
}

// setKey is the pod's slot-set key.
func (s *Scenario) setKey() string { return "lenny:pod:{" + s.podID + "}:active_slots" }

// Run reserves a slot for a distinct session, then releases it through
// SlotClaimer.ReleaseSlot: leaked while the bounded leak budget remains
// (every other leak first releases cleanly and then reports the leak), and
// clean thereafter. It then checks the set result for the session and the
// running floor.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	ctx = context.WithoutCancel(ctx)
	sessionID := fmt.Sprintf("vu%d-it%d", vu, iter)
	if _, _, err := s.counter.Reserve(ctx, s.podID, sessionID, s.maxConcurrent); err != nil {
		s.counters.Inc("reserve_rejected")
		return nil
	}
	n, leaked := s.claimLeak()
	switch {
	case leaked && n%2 == 0:
		s.counters.Inc("clean_then_leaked_release")
		if _, err := s.claimer.ReleaseSlot(ctx, s.podID, sessionID, false, false); err != nil {
			return fmt.Errorf("ReleaseSlot(clean before leak): %w", err)
		}
	case leaked:
		s.counters.Inc("leaked_release")
	default:
		s.counters.Inc("clean_release")
	}
	if _, err := s.claimer.ReleaseSlot(ctx, s.podID, sessionID, false, leaked); err != nil {
		return fmt.Errorf("ReleaseSlot(leaked=%v): %w", leaked, err)
	}
	if leaked {
		// leaksCommitted rises only once the leak's leaked: member exists,
		// so no floor check counts a leak still in flight.
		s.leaksCommitted.Add(1)
	}
	s.checkSession(ctx, sessionID, leaked)
	card, err := s.client.SCard(ctx, s.setKey()).Result()
	if err == nil && card < int64(s.leakedFloor+s.leaksCommitted.Load()) {
		s.floorBreaches.Add(1)
	}
	return nil
}

// claimLeak takes one unit of the leak budget, returning the leak's ordinal
// and true, or false once the budget is spent.
func (s *Scenario) claimLeak() (int32, bool) {
	for {
		cur := s.leakClaims.Load()
		if cur >= s.leakBudget {
			return 0, false
		}
		if s.leakClaims.CompareAndSwap(cur, cur+1) {
			return cur + 1, true
		}
	}
}

// checkSession asserts the set result for one released session: no plain
// member remains, and a leak leaves its leaked: member.
func (s *Scenario) checkSession(ctx context.Context, sessionID string, leaked bool) {
	plain, err1 := s.client.SIsMember(ctx, s.setKey(), sessionID).Result()
	mark, err2 := s.client.SIsMember(ctx, s.setKey(), slotcounter.LeakedMember(sessionID)).Result()
	if err1 != nil || err2 != nil {
		return
	}
	if plain || mark != leaked {
		s.memberBreaches.Add(1)
	}
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("floor_breaches", float64(s.floorBreaches.Load()))
	if s.floorBreaches.Load() > 0 {
		return fmt.Errorf("§6.2 violated: occupancy dropped below the leaked-slot floor %d times; a leaked slot was decremented",
			s.floorBreaches.Load())
	}
	r.AddCustom("member_breaches", float64(s.memberBreaches.Load()))
	if s.memberBreaches.Load() > 0 {
		return fmt.Errorf("§6.2 violated: %d releases left a wrong set result for their session", s.memberBreaches.Load())
	}
	leaks := int64(s.leaksCommitted.Load())
	if s.finalLeaked != leaks || s.finalMembers != int64(s.leakedFloor)+leaks {
		return fmt.Errorf("final set holds %d members (%d leaked), want %d seeded plus %d leaked",
			s.finalMembers, s.finalLeaked, s.leakedFloor, leaks)
	}
	if s.counters.Get("leaked_release") == 0 || s.counters.Get("clean_release") == 0 ||
		s.counters.Get("clean_then_leaked_release") == 0 {
		return fmt.Errorf("scenario must exercise the leaked, clean, and clean-then-leaked release paths (leaked=%d clean=%d clean_then_leaked=%d)",
			s.counters.Get("leaked_release"), s.counters.Get("clean_release"), s.counters.Get("clean_then_leaked_release"))
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}
	return nil
}
