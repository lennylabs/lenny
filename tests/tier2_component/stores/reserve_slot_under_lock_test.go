//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.4 Redis-outage capacity gate
// (SessionStore.ReserveSlotUnderLock) and the slot reservation record it
// counts (migration 0197) against a real Postgres with the production
// migrations applied. It verifies the count reads the same live-session
// source GetActiveSlotsByPod reads plus the pod's reservation records,
// admits below the per-pod bound, rejects at the bound, records each
// admission before the advisory lock is released so two callers cannot both
// observe the same free slot (F-12.4.38), and that the reclaim deletes only
// abandoned records.
package stores_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: §12.4;
// §5.2.
// diagnosis: ReserveSlotUnderLock counted terminal sessions, the wrong pod,
// or released its per-pod advisory lock before recording the admission, so
// the Redis-outage gate admitted or rejected a slot incorrectly. A failure
// of a serialization case under load is the F-12.4.38 overrun.
func TestReserveSlotUnderLock(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()

	t.Run("admits below the bound and counts only live sessions", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		// Two live slots and one terminal (not counted).
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateCompleted)

		count, admitted, err := store.ReserveSlotUnderLock(ctx, pod, newUUID(t), 4)
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
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateRunning)
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateRunning)

		probe := newUUID(t)
		count, admitted, err := store.ReserveSlotUnderLock(ctx, pod, probe, 2)
		if err != nil {
			t.Fatalf("ReserveSlotUnderLock: %v", err)
		}
		if admitted {
			t.Error("admitted = true, want false (pod at its bound of 2)")
		}
		if count != 2 {
			t.Errorf("count = %d, want the observed count 2", count)
		}
		if n := countReservations(t, ctx, pg, pod); n != 0 {
			t.Errorf("a refused admission left %d reservation records, want 0", n)
		}
	})

	// The per-pod advisory lock serializes the count-and-decide and the
	// record of the admission: with a session row persisted only after each
	// admission, as the gateway's create path does, concurrent admissions on
	// a maxConcurrent=2 pod that starts at one live slot must not overrun
	// the bound however the binds interleave with the counts.
	t.Run("serializes concurrent admissions under the per-pod advisory lock", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		mustCreateSlotSession(t, ctx, store, tenant, pod, newUUID(t), session.StateRunning)

		const racers = 8
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				id := newUUID(t)
				_, ok, err := store.ReserveSlotUnderLock(ctx, pod, id, 2)
				if err != nil {
					t.Errorf("ReserveSlotUnderLock: %v", err)
					return
				}
				if ok {
					// Persist the admitted session's row naming the pod, as
					// the create path does after the claim returns.
					mustCreateSlotSession(t, ctx, store, tenant, pod, id, session.StateRunning)
					atomic.AddInt32(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != 1 {
			t.Errorf("admitted = %d, want exactly 1; the per-pod advisory lock must cover the record of the admission so the bound is not overrun", got)
		}
	})

	// Without any session row write, the reservation records alone bound
	// the admissions: the F-12.4.38 window in which every admission decided
	// before the first bind observed the same occupancy.
	t.Run("bounds admissions whose rows are never persisted", func(t *testing.T) {
		pod := "pod-" + newUUID(t)[:8]
		const racers, bound = 16, 3
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				if _, ok, err := store.ReserveSlotUnderLock(ctx, pod, newUUID(t), bound); err == nil && ok {
					atomic.AddInt32(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != bound {
			t.Errorf("admitted = %d, want exactly %d", got, bound)
		}
		if n := countReservations(t, ctx, pg, pod); n != bound {
			t.Errorf("reservation records = %d, want %d", n, bound)
		}
	})

	// Many pods with racers that admit, wait a random interval, then bind or
	// abandon (a release deletes the record) under load. At no instant may a
	// pod's occupancy exceed its bound; the check reads the occupancy the
	// gate itself counts after every admission.
	t.Run("holds the bound across pods with random binds and releases", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		const pods, racers, bound = 4, 24, 3
		var wg sync.WaitGroup
		peaks := make([]*atomic.Int32, pods)
		for p := 0; p < pods; p++ {
			pod := fmt.Sprintf("pod-%s-%d", newUUID(t)[:8], p)
			var held atomic.Int32
			peak := &atomic.Int32{}
			peaks[p] = peak
			for i := 0; i < racers; i++ {
				wg.Add(1)
				go func(seed uint64) {
					defer wg.Done()
					rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
					id := newUUID(t)
					time.Sleep(time.Duration(rng.IntN(5)) * time.Millisecond)
					_, ok, err := store.ReserveSlotUnderLock(ctx, pod, id, bound)
					if err != nil || !ok {
						return
					}
					n := held.Add(1)
					for {
						cur := peak.Load()
						if n <= cur || peak.CompareAndSwap(cur, n) {
							break
						}
					}
					time.Sleep(time.Duration(rng.IntN(10)) * time.Millisecond)
					if rng.IntN(2) == 0 {
						mustCreateSlotSession(t, ctx, store, tenant, pod, id, session.StateRunning)
						return // a bound session keeps its slot
					}
					// An abandoned create: the rollback releases the slot and
					// deletes the record. held rises after an admission
					// commits and drops before its record is deleted, so it
					// never exceeds the occupancy the gate counts, and a peak
					// above the bound is an overrun.
					held.Add(-1)
					if err := store.DeleteSlotReservation(ctx, pod, id); err != nil {
						t.Errorf("DeleteSlotReservation: %v", err)
					}
				}(uint64(p*racers + i + 1))
			}
		}
		wg.Wait()
		for p, peak := range peaks {
			if got := peak.Load(); got > bound {
				t.Errorf("pod %d: peak occupancy %d exceeds the bound %d", p, got, bound)
			}
		}
	})

	t.Run("counts a Redis-path record and a bound session once", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		pod := "pod-" + newUUID(t)[:8]
		a := newUUID(t)
		ins, err := store.RecordSlotReservation(ctx, pod, a)
		if err != nil || !ins {
			t.Fatalf("RecordSlotReservation = (%v, %v), want inserted", ins, err)
		}
		if ins, err := store.RecordSlotReservation(ctx, pod, a); err != nil || ins {
			t.Fatalf("second RecordSlotReservation = (%v, %v), want (false, nil)", ins, err)
		}
		if _, ok, _ := store.ReserveSlotUnderLock(ctx, pod, newUUID(t), 1); ok {
			t.Fatal("gate admitted over a Redis-path record at bound 1")
		}
		// a binds: its row and its record count once.
		mustCreateSlotSession(t, ctx, store, tenant, pod, a, session.StateRunning)
		count, ok, err := store.ReserveSlotUnderLock(ctx, pod, newUUID(t), 2)
		if err != nil || !ok || count != 2 {
			t.Fatalf("admission beside a = (%d, %v, %v), want (2, true, nil)", count, ok, err)
		}
		// The admitting session is not counted against itself.
		count, ok, err = store.ReserveSlotUnderLock(ctx, pod, a, 2)
		if err != nil || !ok || count != 2 {
			t.Fatalf("re-admission of a = (%d, %v, %v), want (2, true, nil)", count, ok, err)
		}
		if err := store.DeleteSlotReservation(ctx, pod, a); err != nil {
			t.Fatalf("DeleteSlotReservation: %v", err)
		}
		if err := store.DeleteSlotReservation(ctx, pod, a); err != nil {
			t.Fatalf("second DeleteSlotReservation: %v", err)
		}
		if err := store.DeleteSlotReservation(ctx, pod, "not-a-uuid"); err != nil {
			t.Fatalf("DeleteSlotReservation of a non-UUID: %v", err)
		}
	})

	t.Run("refuses a session id that is not a UUID", func(t *testing.T) {
		pod := "pod-" + newUUID(t)[:8]
		if _, _, err := store.ReserveSlotUnderLock(ctx, pod, "not-a-uuid", 2); err == nil {
			t.Error("ReserveSlotUnderLock with a non-UUID session returned no error")
		}
		if _, err := store.RecordSlotReservation(ctx, pod, "not-a-uuid"); err == nil {
			t.Error("RecordSlotReservation with a non-UUID session returned no error")
		}
		if _, err := store.RecordSlotReservation(ctx, "", newUUID(t)); err == nil {
			t.Error("RecordSlotReservation with no pod returned no error")
		}
	})
}

// spec: §12.4 (Redis failure behavior), §4.6.1 (Orphaned SandboxClaim
// detection).
// diagnosis: ReclaimSlotReservations deleted a record that still counts a
// reservation in progress (an under-count that reopens the F-12.4.38
// overrun), or kept a record whose create ended without a row or whose
// session ended long ago (a permanent over-count).
func TestReclaimSlotReservations(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	pod := "pod-" + newUUID(t)[:8]
	now := time.Now().UTC()
	cutoff := now.Add(-15 * time.Minute)
	old := cutoff.Add(-time.Minute)

	record := func(id string, at time.Time) {
		t.Helper()
		if _, err := store.RecordSlotReservation(ctx, pod, id); err != nil {
			t.Fatalf("RecordSlotReservation: %v", err)
		}
		if _, err := pg.Pool.Exec(ctx,
			`UPDATE slot_reservations SET reserved_at = $3 WHERE pod_id = $1 AND session_id = $2::uuid`,
			pod, id, at); err != nil {
			t.Fatalf("backdate record: %v", err)
		}
	}
	terminal := func(id string, st session.State, at time.Time) {
		t.Helper()
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, RuntimeRef: "echo", State: st, TerminatedAt: at,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	absentOld, absentNew := newUUID(t), newUUID(t)
	liveOld, termOld, termRecent := newUUID(t), newUUID(t), newUUID(t)
	boundOld := newUUID(t)
	record(absentOld, old)
	record(absentNew, now)
	record(liveOld, old)
	mustCreateSlotSession(t, ctx, store, tenant, "", liveOld, session.StateCreated)
	record(termOld, old)
	terminal(termOld, session.StateFailed, old)
	record(termRecent, old)
	terminal(termRecent, session.StateCancelled, now)
	record(boundOld, old)
	mustCreateSlotSession(t, ctx, store, tenant, pod, boundOld, session.StateRunning)

	n, err := store.ReclaimSlotReservations(ctx, cutoff)
	if err != nil {
		t.Fatalf("ReclaimSlotReservations: %v", err)
	}
	if n != 2 {
		t.Errorf("reclaimed %d records, want 2 (the absent and the long-terminal session)", n)
	}
	kept := reservationIDs(t, ctx, pg, pod)
	for _, id := range []string{absentNew, liveOld, termRecent, boundOld} {
		if !kept[id] {
			t.Errorf("record %s was reclaimed, want kept", id)
		}
	}
	for _, id := range []string{absentOld, termOld} {
		if kept[id] {
			t.Errorf("record %s was kept, want reclaimed", id)
		}
	}
	if n, err := store.ReclaimSlotReservations(ctx, cutoff); err != nil || n != 0 {
		t.Errorf("second reclaim = (%d, %v), want (0, nil)", n, err)
	}
}

func mustCreateSlotSession(t *testing.T, ctx context.Context, store *pgstore.Store, tenant, pod, id string, state session.State) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            id,
		TenantID:      tenant,
		State:         state,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Errorf("create slot session on %s: %v", pod, err)
	}
}

func countReservations(t *testing.T, ctx context.Context, pg *containers.Postgres, pod string) int {
	t.Helper()
	return len(reservationIDs(t, ctx, pg, pod))
}

func reservationIDs(t *testing.T, ctx context.Context, pg *containers.Postgres, pod string) map[string]bool {
	t.Helper()
	rows, err := pg.Pool.Query(ctx, `SELECT session_id::text FROM slot_reservations WHERE pod_id = $1`, pod)
	if err != nil {
		t.Fatalf("read reservation records: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan reservation record: %v", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read reservation records: %v", err)
	}
	return out
}
