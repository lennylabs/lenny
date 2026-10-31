// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// spec: 12.4 (Redis HA and failure modes: the session slot set's Postgres
// fallback under a per-pod advisory lock), 5.2 (Slot assignment atomicity),
// 4.6.1 (Orphaned SandboxClaim detection).
// diagnosis: a failure means a Redis outage that began while admissions were
// in flight let three gateway replicas admit more sessions onto one pod than
// maxConcurrentSessions (F-12.4.38): either an admission the Redis set made
// before the outage was invisible to the §12.4 gate, or the gate released
// its per-pod advisory lock before recording its own admission. It also
// fails when a refused or failed reservation left a record that the gate
// counts, or when the reclaim of crashed creates' records did not return
// exactly their slots.
func TestRedisOutageMidAdmissionsHoldsTheBound_spec_12_4(t *testing.T) {
	const (
		racers   = 30
		maxSlots = 4
		replicas = 3
		rounds   = 4
	)
	c := newSlotChaos(t)
	ctx := context.Background()
	for round := 0; round < rounds; round++ {
		c.proxy.Heal()
		var counters [replicas]*slotcounter.Counter
		for i := range counters {
			counters[i] = c.counter(t)
		}
		pod := "pod-" + newChaosUUID(t)[:8]
		rng := rand.New(rand.NewSource(int64(round) + 1))
		delays := make([]time.Duration, racers)
		crash := make([]bool, racers)
		for i := range delays {
			delays[i] = time.Duration(rng.Intn(40)) * time.Millisecond
			crash[i] = rng.Intn(4) == 0
		}
		dropAfter := time.Duration(rng.Intn(25)) * time.Millisecond

		var admitted, crashed atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				time.Sleep(delays[i])
				id := newChaosUUID(t)
				_, _, err := counters[i%replicas].Reserve(ctx, pod, id, maxSlots)
				if err != nil {
					if !errors.Is(err, slotcounter.ErrSlotsExhausted) && !errors.Is(err, slotcounter.ErrFailClosed) {
						t.Errorf("Reserve: %v", err)
					}
					return
				}
				if n := admitted.Add(1); n > maxSlots {
					t.Errorf("round %d: admission %d on pod %s exceeds maxConcurrentSessions %d", round, n, pod, maxSlots)
				}
				if crash[i] {
					// The replica dies before persisting the row.
					crashed.Add(1)
					return
				}
				if err := c.store.Create(ctx, sessionstore.Session{
					ID: id, TenantID: "acme", State: session.StateRunning, RuntimeRef: "echo",
					PodAssignment: pod, ScrubPolicy: "best-effort-per-slot",
				}); err != nil {
					t.Errorf("persist row: %v", err)
				}
			}(i)
		}
		close(start)
		time.Sleep(dropAfter)
		c.proxy.Drop()
		wg.Wait()

		got := admitted.Load()
		if got == 0 {
			t.Fatalf("round %d: no admission at all; the gate failed closed instead of degrading", round)
		}
		// Every admission, bound or crashed, is counted by the gate; refusals
		// and failed reservations left nothing behind.
		probe := func() int {
			n := 0
			for i := 0; i < maxSlots+1; i++ {
				if _, ok, err := c.store.ReserveSlotUnderLock(ctx, pod, newChaosUUID(t), maxSlots); err != nil {
					t.Fatalf("probe: %v", err)
				} else if ok {
					n++
				}
			}
			return n
		}
		if free := probe(); free != maxSlots-int(got) {
			t.Errorf("round %d: the gate admitted %d probes after %d admissions, want %d", round, free, got, maxSlots-int(got))
		}
		// The probes' records are abandoned creates too: the reclaim with a
		// cutoff past every create returns the crashed creates' and the
		// probes' slots, and keeps the bound sessions' records.
		if _, err := c.store.ReclaimSlotReservations(ctx, time.Now().Add(time.Second)); err != nil {
			t.Fatalf("reclaim: %v", err)
		}
		bound := int(got - crashed.Load())
		if free := probe(); free != maxSlots-bound {
			t.Errorf("round %d: after the reclaim the gate admitted %d probes, want %d (%d bound sessions)", round, free, maxSlots-bound, bound)
		}
	}
	c.proxy.Heal()
}
