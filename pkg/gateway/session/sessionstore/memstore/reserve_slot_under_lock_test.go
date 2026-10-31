// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §12.4 — the Redis-outage capacity gate admits a slot only
// when the pod's live-session count is below the per-pod bound.
// diagnosis: ReserveSlotUnderLock counted terminal sessions or the wrong
// pod, so the fallback gate admitted or rejected incorrectly.
func TestReserveSlotUnderLockAdmitsBelowBound(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	// Two live slots on pod-1, one terminal (not counted).
	_ = s.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
	_ = s.Create(ctx, sessionstore.Session{ID: "s2", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
	_ = s.Create(ctx, sessionstore.Session{ID: "s3", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-1"})

	count, admitted, err := s.ReserveSlotUnderLock(ctx, "pod-1", "new", 4)
	if err != nil {
		t.Fatalf("ReserveSlotUnderLock: %v", err)
	}
	if !admitted {
		t.Fatal("admitted = false, want true (2 live slots < bound 4)")
	}
	if count != 3 {
		t.Errorf("count = %d, want 3 (2 live + this admission)", count)
	}
}

// spec: §12.4 — the gate rejects when the pod is at its bound.
// diagnosis: the fallback gate overran the per-pod bound during a Redis
// outage.
func TestReserveSlotUnderLockRejectsAtBound(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
	_ = s.Create(ctx, sessionstore.Session{ID: "s2", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})

	count, admitted, err := s.ReserveSlotUnderLock(ctx, "pod-1", "new", 2)
	if err != nil {
		t.Fatalf("ReserveSlotUnderLock: %v", err)
	}
	if admitted {
		t.Error("admitted = true, want false (pod at its bound of 2)")
	}
	if count != 2 {
		t.Errorf("count = %d, want the observed count 2", count)
	}
}

// spec: §12.4 — the count, the decision, and the record of the admission
// run under a per-pod lock, so two concurrent admissions cannot both observe
// the same free slot even though no racer writes its session row
// (F-12.4.38).
// diagnosis: the in-memory gate committed an admission without recording
// it, so racers that all decided before any session row named the pod each
// observed the same free slot and the pod overran its bound.
func TestReserveSlotUnderLockSerializes(t *testing.T) {
	for _, tc := range []struct {
		live, bound int
	}{{0, 1}, {1, 2}, {0, 4}, {3, 4}, {5, 8}} {
		t.Run(fmt.Sprintf("live %d bound %d", tc.live, tc.bound), func(t *testing.T) {
			s := memstore.New()
			ctx := context.Background()
			for i := 0; i < tc.live; i++ {
				_ = s.Create(ctx, sessionstore.Session{ID: fmt.Sprintf("live-%d", i), TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
			}
			const racers = 32
			var admittedCount int32
			var wg sync.WaitGroup
			wg.Add(racers)
			for i := 0; i < racers; i++ {
				go func(i int) {
					defer wg.Done()
					_, admitted, err := s.ReserveSlotUnderLock(ctx, "pod-1", fmt.Sprintf("racer-%d", i), int32(tc.bound))
					if err == nil && admitted {
						atomic.AddInt32(&admittedCount, 1)
					}
				}(i)
			}
			wg.Wait()
			if want := int32(tc.bound - tc.live); admittedCount != want {
				t.Errorf("admitted = %d, want exactly %d (the free slots)", admittedCount, want)
			}
		})
	}
}

// spec: §12.4 — the gate counts a session once whether its row names the
// pod, it holds a reservation record, or both, and it never counts the
// session it is admitting, so a retried admission is admitted again.
// diagnosis: the gate double-counted a bound session's reservation, or
// counted the admitting session against itself.
func TestReserveSlotUnderLockCountsEachSessionOnce(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if _, ok, err := s.ReserveSlotUnderLock(ctx, "pod-1", "a", 2); err != nil || !ok {
		t.Fatalf("first admission = (%v, %v), want admitted", ok, err)
	}
	// The create persists a's row naming the pod; its record remains.
	_ = s.Create(ctx, sessionstore.Session{ID: "a", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
	count, ok, err := s.ReserveSlotUnderLock(ctx, "pod-1", "b", 2)
	if err != nil || !ok || count != 2 {
		t.Fatalf("second admission = (%d, %v, %v), want (2, true, nil): a counts once", count, ok, err)
	}
	// A retry of b's admission does not count b against itself.
	count, ok, err = s.ReserveSlotUnderLock(ctx, "pod-1", "b", 2)
	if err != nil || !ok || count != 2 {
		t.Fatalf("retried admission = (%d, %v, %v), want (2, true, nil)", count, ok, err)
	}
	if _, ok, _ := s.ReserveSlotUnderLock(ctx, "pod-1", "c", 2); ok {
		t.Fatal("third session admitted on a pod at its bound of 2")
	}
	// The release deletes b's record, freeing the slot.
	if err := s.DeleteSlotReservation(ctx, "pod-1", "b"); err != nil {
		t.Fatalf("DeleteSlotReservation: %v", err)
	}
	if _, ok, _ := s.ReserveSlotUnderLock(ctx, "pod-1", "c", 2); !ok {
		t.Fatal("third session refused after b's release")
	}
}

// spec: §12.4 — a record written by RecordSlotReservation, the Redis fast
// path's record, is counted by the gate; a second record of the same
// session reports no insert.
// diagnosis: a reservation the Redis path admitted was invisible to the
// gate a later outage started.
func TestRecordSlotReservationIsCounted(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	ins, err := s.RecordSlotReservation(ctx, "pod-1", "a")
	if err != nil || !ins {
		t.Fatalf("RecordSlotReservation = (%v, %v), want inserted", ins, err)
	}
	if ins, _ := s.RecordSlotReservation(ctx, "pod-1", "a"); ins {
		t.Error("second RecordSlotReservation reported an insert")
	}
	if _, ok, _ := s.ReserveSlotUnderLock(ctx, "pod-1", "b", 1); ok {
		t.Error("gate admitted b over a's Redis-path record at bound 1")
	}
	if _, err := s.RecordSlotReservation(ctx, "", "a"); err == nil {
		t.Error("RecordSlotReservation with no pod returned no error")
	}
	if _, _, err := s.ReserveSlotUnderLock(ctx, "pod-1", "", 1); err == nil {
		t.Error("ReserveSlotUnderLock with no session returned no error")
	}
}

// spec: §12.4, §4.6.1 (Orphaned SandboxClaim detection) — the reclaim
// deletes a record older than the cutoff whose session row is absent,
// terminal since before the cutoff, or non-terminal on another pod, and keeps
// every other record.
// diagnosis: the reclaim deleted a record that still counts a live
// reservation (an under-count) or kept an abandoned one forever.
func TestReclaimSlotReservations(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	clock := now.Add(-time.Hour)
	s := memstore.NewWithClock(func() time.Time { return clock })
	ctx := context.Background()
	cutoff := now.Add(-15 * time.Minute)
	for _, id := range []string{"absent-old", "live-old", "bound-old", "moved-old", "term-old", "term-recent", "term-nostamp"} {
		if _, err := s.RecordSlotReservation(ctx, "pod-1", id); err != nil {
			t.Fatal(err)
		}
	}
	clock = now
	if _, err := s.RecordSlotReservation(ctx, "pod-1", "absent-new"); err != nil {
		t.Fatal(err)
	}
	_ = s.Create(ctx, sessionstore.Session{ID: "live-old", TenantID: "acme", State: session.StateCreated})
	_ = s.Create(ctx, sessionstore.Session{ID: "bound-old", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"})
	_ = s.Create(ctx, sessionstore.Session{ID: "moved-old", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-2"})
	_ = s.Create(ctx, sessionstore.Session{ID: "term-old", TenantID: "acme", State: session.StateFailed, TerminatedAt: cutoff.Add(-time.Minute)})
	_ = s.Create(ctx, sessionstore.Session{ID: "term-recent", TenantID: "acme", State: session.StateFailed, TerminatedAt: cutoff.Add(time.Minute)})
	_ = s.Create(ctx, sessionstore.Session{ID: "term-nostamp", TenantID: "acme", State: session.StateFailed})

	n, err := s.ReclaimSlotReservations(ctx, cutoff)
	if err != nil || n != 3 {
		t.Fatalf("ReclaimSlotReservations = (%d, %v), want (3, nil): absent-old, moved-old, and term-old", n, err)
	}
	// Five occupants remain: the gate at bound 6 admits one more, at bound 5 none.
	if _, ok, _ := s.ReserveSlotUnderLock(ctx, "pod-1", "probe", 5); ok {
		t.Error("gate admitted at bound 5 with five kept records")
	}
	if _, ok, _ := s.ReserveSlotUnderLock(ctx, "pod-1", "probe", 6); !ok {
		t.Error("gate refused at bound 6 with five kept records")
	}
}

// spec: §12.4 — an empty pod id matches no slot.
func TestReserveSlotUnderLockEmptyPod(t *testing.T) {
	s := memstore.New()
	count, admitted, err := s.ReserveSlotUnderLock(context.Background(), "", "a", 4)
	if err != nil {
		t.Fatalf("ReserveSlotUnderLock: %v", err)
	}
	if admitted || count != 0 {
		t.Errorf("empty pod: count=%d admitted=%v, want 0/false", count, admitted)
	}
}
