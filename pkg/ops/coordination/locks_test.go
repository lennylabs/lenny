// SPDX-License-Identifier: MIT

package coordination_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// acquire is a test helper that acquires a lock for caller.
func acquire(t *testing.T, s *coordination.MemStore, scope, caller string) *coordination.Lock {
	t.Helper()
	l, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: scope, Operation: "scale", TTLSeconds: 300, AcquiredBy: caller,
	})
	if err != nil {
		t.Fatalf("acquire %q: %v", scope, err)
	}
	return l
}

func TestAcquireGrantsAndPopulatesLock(t *testing.T) {
	s := coordination.NewMemStore()
	l := acquire(t, s, "pool:default-gvisor", "prod-watchdog")
	if l.ID == "" {
		t.Error("lock id is empty")
	}
	if l.AcquiredBy != "prod-watchdog" {
		t.Errorf("acquiredBy = %q, want prod-watchdog", l.AcquiredBy)
	}
	if l.LockStore != coordination.StoreMemory {
		t.Errorf("lockStore = %q, want memory for the Tier 3 store", l.LockStore)
	}
	if !l.ExpiresAt.After(l.AcquiredAt) {
		t.Error("expiresAt is not after acquiredAt — server did not compute the TTL")
	}
	if l.Revision != 0 {
		t.Errorf("revision = %d, want 0 on a fresh lock", l.Revision)
	}
}

func TestAcquireConflictsWhenScopeHeld(t *testing.T) {
	s := coordination.NewMemStore()
	acquire(t, s, "pool:default-gvisor", "agent-a")
	_, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: "pool:default-gvisor", Operation: "scale", TTLSeconds: 300, AcquiredBy: "agent-b",
	})
	// §25.4: a second acquire on a held scope fails with the conflict
	// code — there is no check-then-write window.
	if coordination.CodeOf(err) != coordination.ErrCodeConflict {
		t.Fatalf("err code = %q, want REMEDIATION_LOCK_CONFLICT", coordination.CodeOf(err))
	}
}

func TestAcquireSucceedsAfterExpiry(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	acquire(t, s, "pool:p", "agent-a")
	// Advance past the TTL: the scope is free again (§25.4 lazy expiry).
	now = now.Add(6 * time.Minute)
	if _, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: "pool:p", Operation: "scale", TTLSeconds: 300, AcquiredBy: "agent-b",
	}); err != nil {
		t.Fatalf("acquire after expiry: %v", err)
	}
}

func TestReleaseRequiresOwnership(t *testing.T) {
	s := coordination.NewMemStore()
	l := acquire(t, s, "pool:p", "owner")
	// §25.4: a non-owner cannot release; it must Steal instead.
	err := s.Release(coordination.WithCaller(context.Background(), "intruder"), l.ID)
	if coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Fatalf("err code = %q, want LOCK_NOT_OWNED", coordination.CodeOf(err))
	}
	// The owner can release.
	if err := s.Release(coordination.WithCaller(context.Background(), "owner"), l.ID); err != nil {
		t.Fatalf("owner release: %v", err)
	}
}

func TestReleaseUnknownLockIsNotFound(t *testing.T) {
	s := coordination.NewMemStore()
	err := s.Release(coordination.WithCaller(context.Background(), "owner"), "lock-nonexistent")
	if coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Fatalf("err code = %q, want REMEDIATION_LOCK_NOT_FOUND", coordination.CodeOf(err))
	}
}

func TestGetExpiredLockIsNotFound(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	l := acquire(t, s, "pool:p", "owner")
	now = now.Add(10 * time.Minute)
	// §25.4: Get on an expired lock returns NOT_FOUND so the agent
	// discovers it lost ownership rather than receiving silent success.
	_, err := s.Get(context.Background(), l.ID)
	if coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Fatalf("err code = %q, want REMEDIATION_LOCK_NOT_FOUND", coordination.CodeOf(err))
	}
}

func TestExtendRenewsTTLAndRequiresOwnership(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	l := acquire(t, s, "pool:p", "owner")
	firstExpiry := l.ExpiresAt

	// A non-owner cannot extend.
	if _, err := s.ExtendAs(context.Background(), l.ID, 300, "intruder"); coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Fatalf("non-owner extend err = %q, want LOCK_NOT_OWNED", coordination.CodeOf(err))
	}
	now = now.Add(2 * time.Minute)
	extended, err := s.ExtendAs(context.Background(), l.ID, 300, "owner")
	if err != nil {
		t.Fatalf("owner extend: %v", err)
	}
	if !extended.ExpiresAt.After(firstExpiry) {
		t.Error("extend did not push expiresAt forward")
	}
	if extended.Revision != 1 {
		t.Errorf("revision = %d, want 1 after one extension", extended.Revision)
	}
}

func TestStealTransfersOwnershipAndRecordsPriorHolder(t *testing.T) {
	s := coordination.NewMemStore()
	l := acquire(t, s, "pool:p", "routine-agent")
	stolen, err := s.Steal(context.Background(), l.ID, coordination.StealRequest{
		Confirm: true, Reason: "warm-pool-exhaustion took priority", TTLSeconds: 300,
		AcquiredBy: "incident-agent",
	})
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if stolen.AcquiredBy != "incident-agent" {
		t.Errorf("acquiredBy = %q, want incident-agent", stolen.AcquiredBy)
	}
	if stolen.StolenFrom != "routine-agent" {
		t.Errorf("stolenFrom = %q, want routine-agent", stolen.StolenFrom)
	}
	if stolen.Revision != 1 {
		t.Errorf("revision = %d, want 1 after a steal", stolen.Revision)
	}
	// The prior holder can no longer release — the new holder owns it.
	if err := s.Release(coordination.WithCaller(context.Background(), "routine-agent"), l.ID); coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Errorf("prior holder release err = %q, want LOCK_NOT_OWNED after steal", coordination.CodeOf(err))
	}
}

func TestListReturnsActiveLocksOnly(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	acquire(t, s, "pool:a", "agent")
	acquire(t, s, "pool:b", "agent")
	locks, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(locks) != 2 {
		t.Fatalf("got %d locks, want 2", len(locks))
	}
	if locks[0].Scope != "pool:a" || locks[1].Scope != "pool:b" {
		t.Errorf("list order = %v, want sorted by scope", []string{locks[0].Scope, locks[1].Scope})
	}
	// After expiry the list is empty.
	now = now.Add(10 * time.Minute)
	locks, _ = s.List(context.Background())
	if len(locks) != 0 {
		t.Errorf("got %d locks after expiry, want 0", len(locks))
	}
}

func TestReapDropsExpiredLocks(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	acquire(t, s, "pool:a", "agent")
	now = now.Add(10 * time.Minute)
	if n := s.Reap(); n != 1 {
		t.Errorf("Reap returned %d, want 1 expired lock reaped", n)
	}
}

func TestIncrementEpochIsMonotonic(t *testing.T) {
	s := coordination.NewMemStore()
	if s.Epoch() != 0 {
		t.Errorf("initial epoch = %d, want 0", s.Epoch())
	}
	if e := s.IncrementEpoch(); e != 1 {
		t.Errorf("IncrementEpoch returned %d, want 1", e)
	}
	if e := s.IncrementEpoch(); e != 2 {
		t.Errorf("IncrementEpoch returned %d, want 2", e)
	}
}

func TestTTLClampedToCeiling(t *testing.T) {
	s := coordination.NewMemStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	l, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: "pool:p", Operation: "scale", TTLSeconds: 999999, AcquiredBy: "agent",
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// §25.4 ops.locks.maxTTLSeconds caps the TTL at one hour.
	if got := l.ExpiresAt.Sub(now); got > time.Hour {
		t.Errorf("TTL = %v, want clamped to 1h", got)
	}
}
