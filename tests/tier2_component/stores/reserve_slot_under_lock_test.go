//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.4 Redis-outage capacity gate
// (SessionStore.ReserveSlotUnderLock) against a real Postgres with the
// production migrations applied. It verifies the count-and-decide reads the
// same live-session source GetActiveSlotsByPod reads, admits below the
// per-pod bound, rejects at the bound, and serializes concurrent admissions
// under the per-pod advisory lock so two callers cannot both observe the
// same free slot.
package stores_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
)

// spec: §12.4 line 208 (Postgres fallback under a per-pod advisory lock);
// §5.2 line 541.
// diagnosis: ReserveSlotUnderLock counted terminal sessions, the wrong pod,
// or did not serialize under the per-pod advisory lock, so the Redis-outage
// gate admitted or rejected a slot incorrectly.
func TestReserveSlotUnderLock(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()

	t.Run("admits below the bound and counts only live sessions", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		// Two live slots and one terminal (not counted).
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateCompleted)

		count, admitted, err := store.ReserveSlotUnderLock(ctx, pod, 4)
		if err != nil {
			t.Fatalf("ReserveSlotUnderLock: %v", err)
		}
		if !admitted {
			t.Fatal("admitted = false, want true (2 live slots < bound 4)")
		}
		if count != 3 {
			t.Errorf("count = %d, want 3 (2 live + this admission)", count)
		}
	})

	t.Run("rejects at the bound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)

		count, admitted, err := store.ReserveSlotUnderLock(ctx, pod, 2)
		if err != nil {
			t.Fatalf("ReserveSlotUnderLock: %v", err)
		}
		if admitted {
			t.Error("admitted = true, want false (pod at its bound of 2)")
		}
		if count != 2 {
			t.Errorf("count = %d, want the observed count 2", count)
		}
	})

	// The per-pod advisory lock serializes the count-and-decide: with the
	// admission of a session row interleaved between gated calls, two
	// concurrent admissions on a maxConcurrent=2 pod that starts at one live
	// slot must not both succeed and overrun the bound. The test holds the
	// advisory lock during a long count by issuing concurrent calls and
	// persisting a session row when each admits; the lock ensures the second
	// caller observes the first caller's committed occupancy.
	t.Run("serializes concurrent admissions under the per-pod advisory lock", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)

		const racers = 8
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				_, ok, err := store.ReserveSlotUnderLock(ctx, pod, 2)
				if err != nil {
					return
				}
				if ok {
					// Persist the admitted slot's session row so the next
					// caller's count-under-lock observes it. The advisory lock
					// the gate holds for the count is released at commit, but
					// the row this writes is visible to a subsequent gated
					// count, which is what bounds the total admissions.
					mustCreateSlotSession(t, ctx, store, tenant, pod, session.StateRunning)
					atomic.AddInt32(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		// Starting at 1 live slot on a bound of 2, at most one admission can
		// raise occupancy to the bound; once the admitted row is committed the
		// remaining racers observe the pod at its bound and are rejected.
		if got := atomic.LoadInt32(&admitted); got > 1 {
			t.Errorf("admitted = %d, want at most 1; the per-pod advisory lock must serialize the count-and-decide so the bound is not overrun", got)
		}
	})
}

func mustCreateSlotSession(t *testing.T, ctx context.Context, store *pgstore.Store, tenant, pod string, state session.State) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            newUUID(t),
		TenantID:      tenant,
		State:         state,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Fatalf("create slot session on %s: %v", pod, err)
	}
}
