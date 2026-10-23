// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// interleavingPod is the pod the randomized outage sequences act on.
const interleavingPod = "pod-mix"

// interleaving is one randomized sequence of fallback admissions, releases
// that fail during an outage, terminal writes on the fake source, outage
// toggles, and recovery rebuilds, over two replicas' Counters on one Redis.
type interleaving struct {
	t        *testing.T
	env      *outageEnv
	replicas [2]*slotcounter.Counter
	rng      *rand.Rand
	up       bool
	sessions []string
	// leakedMarks lists every session whose leaked release failed, so its
	// leaked: member is owed by a later rebuild.
	leakedMarks []string
	next        int
}

// members returns the pod's set while Redis is up.
func (s *interleaving) members() map[string]bool {
	ms, err := s.replicas[0].Members(context.Background(), interleavingPod)
	if err != nil {
		s.t.Fatalf("read the set while Redis is up: %v", err)
	}
	out := map[string]bool{}
	for _, m := range ms {
		out[m] = true
	}
	return out
}

// step runs one random operation and returns the member it may remove,
// which the no-removal check excludes.
func (s *interleaving) step(ctx context.Context) (target string) {
	c := s.replicas[s.rng.Intn(2)]
	// An operation against an unreachable Redis waits out the client's dial
	// timeout, so outages are kept short: half of the steps taken during an
	// outage end it first.
	if !s.up && s.rng.Intn(2) == 0 {
		s.env.up()
		s.up = true
	}
	switch op := s.rng.Intn(10); {
	case op < 3:
		s.next++
		id := fmt.Sprintf("sess-%d", s.next)
		if _, _, err := c.Reserve(ctx, interleavingPod, id, 64); err == nil {
			s.sessions = append(s.sessions, id)
			if s.rng.Intn(2) == 0 {
				s.env.src.set(id, interleavingPod, false)
			}
		}
	case op < 5 && len(s.sessions) > 0:
		id := s.sessions[s.rng.Intn(len(s.sessions))]
		leaked := s.rng.Intn(2) == 0
		if _, _, _, err := c.Release(ctx, interleavingPod, id, leaked); err != nil && leaked {
			s.leakedMarks = append(s.leakedMarks, id)
		}
		return id
	case op < 6 && len(s.sessions) > 0:
		s.env.src.set(s.sessions[s.rng.Intn(len(s.sessions))], interleavingPod, true)
	case op < 8:
		if s.up {
			s.env.down()
		} else {
			s.env.up()
		}
		s.up = !s.up
	default:
		c.ProbeTickForTest(ctx)
	}
	return ""
}

// checkStep asserts the step's invariants: a rebuild never clears a
// replica's outage clock while that replica's ledger holds an entry, and no
// rebuild removes a member the set held before the step.
func (s *interleaving) checkStep(i int, before map[string]bool, since [2]time.Time, target string) {
	for r, c := range s.replicas {
		if !since[r].IsZero() && c.OutageSinceForTest().IsZero() && c.LedgerLenForTest() != 0 {
			s.t.Fatalf("step %d: replica %d cleared its outage clock with %d ledger entries", i, r, c.LedgerLenForTest())
		}
	}
	if before == nil || !s.up {
		return
	}
	after := s.members()
	for m := range before {
		if m != target && !after[m] {
			s.t.Fatalf("step %d: member %s was removed by a step that did not release it (target %q)", i, m, target)
		}
	}
}

// interleavingSeed returns LENNY_SLOT_INTERLEAVING_SEED, or a fresh seed.
func interleavingSeed(t *testing.T) int64 {
	if v := os.Getenv("LENNY_SLOT_INTERLEAVING_SEED"); v != "" {
		seed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("LENNY_SLOT_INTERLEAVING_SEED: %v", err)
		}
		return seed
	}
	return time.Now().UnixNano()
}

// TestRecoveryRebuildRandomizedInterleavings_spec_12_4 drives randomized
// sequences of fallback admissions, failing releases, terminal writes, and
// recovery rebuilds on two Counters. A rebuild never clears a replica's
// outage clock while its ledger holds an entry, no rebuild removes a member,
// and once Redis stays reachable and a last rebuild runs on each Counter,
// every recorded leaked mark is a member.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity), §6.2 (leaked slot semantics)
func TestRecoveryRebuildRandomizedInterleavings_spec_12_4(t *testing.T) {
	seed := interleavingSeed(t)
	t.Logf("seed %d", seed)
	ctx := context.Background()
	for round := 0; round < 6; round++ {
		env := newOutageEnv(t)
		env.seedPod(interleavingPod)
		s := &interleaving{
			t: t, env: env, rng: rand.New(rand.NewSource(seed + int64(round))), up: true,
			replicas: [2]*slotcounter.Counter{
				env.replica(slotcounter.WithRehydrationTimeout(50 * time.Millisecond)),
				env.replica(slotcounter.WithRehydrationTimeout(50 * time.Millisecond)),
			},
		}
		for i := 0; i < 60; i++ {
			var before map[string]bool
			if s.up {
				before = s.members()
			}
			since := [2]time.Time{s.replicas[0].OutageSinceForTest(), s.replicas[1].OutageSinceForTest()}
			target := s.step(ctx)
			s.checkStep(i, before, since, target)
		}
		if !s.up {
			env.up()
			s.up = true
		}
		for _, c := range s.replicas {
			c.ProbeTickForTest(ctx)
		}
		final := s.members()
		for _, id := range s.leakedMarks {
			if !final[slotcounter.LeakedMember(id)] {
				t.Fatalf("round %d (seed %d): the failed leaked release of %s left no leaked member after the last rebuilds: %v", round, seed, id, final)
			}
		}
	}
}
