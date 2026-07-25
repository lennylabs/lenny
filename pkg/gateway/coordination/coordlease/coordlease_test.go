// SPDX-License-Identifier: MIT

package coordlease

import (
	"context"
	"errors"
	"testing"
)

// spec: §10.1 line 165 — Upsert records the held lease and
// ListHeldByReplica returns it as part of the barrier-target set.
func TestMemoryStoreUpsertAndListHeldByReplica_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	if err := s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1", CoordinationGeneration: 3}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Upsert(ctx, Lease{TenantID: "globex", SessionID: "s2", CoordinatorReplica: "rep-1", CoordinationGeneration: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// A lease held by a different replica is not part of rep-1's set.
	if err := s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s3", CoordinatorReplica: "rep-2"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	held, err := s.ListHeldByReplica(ctx, "rep-1")
	if err != nil {
		t.Fatalf("ListHeldByReplica: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("held = %d leases, want 2 (cross-tenant query)", len(held))
	}
	for _, l := range held {
		if l.CoordinatorReplica != "rep-1" {
			t.Errorf("held lease replica = %q, want rep-1", l.CoordinatorReplica)
		}
		if l.SessionID == "s1" && l.CoordinationGeneration != 3 {
			t.Errorf("s1 generation = %d, want 3", l.CoordinationGeneration)
		}
	}
}

// spec: §10.1 line 165 — a cross-replica handoff overwrites
// coordinator_replica with the new holder, so the prior holder's
// barrier-target set no longer returns the session.
func TestMemoryStoreUpsertHandoffOverwritesReplica_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1"})
	// rep-2 takes over.
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-2", CoordinationGeneration: 5})

	held1, _ := s.ListHeldByReplica(ctx, "rep-1")
	if len(held1) != 0 {
		t.Fatalf("rep-1 still holds %d leases after handoff, want 0", len(held1))
	}
	held2, _ := s.ListHeldByReplica(ctx, "rep-2")
	if len(held2) != 1 || held2[0].CoordinationGeneration != 5 {
		t.Fatalf("rep-2 held = %+v, want one lease at generation 5", held2)
	}
}

// spec: §10.1 line 165 — Release excludes the row from the barrier-target
// query; it is idempotent and monotonic.
func TestMemoryStoreReleaseExcludesFromTargets_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1"})
	if err := s.Release(ctx, "acme", "s1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Idempotent second release and a release of a missing row are no-ops.
	if err := s.Release(ctx, "acme", "s1"); err != nil {
		t.Fatalf("repeat Release: %v", err)
	}
	if err := s.Release(ctx, "acme", "missing"); err != nil {
		t.Fatalf("Release missing: %v", err)
	}
	held, _ := s.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 0 {
		t.Fatalf("released lease still in target set: %+v", held)
	}
	// A re-acquire (handoff back) clears released_at.
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1"})
	held, _ = s.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 1 {
		t.Fatalf("re-acquired lease not in target set: %+v", held)
	}
}

// spec: §4.6.1 — GetBySession resolves the recorded coordinator identity
// and dialable address for the eviction drive, and a released lease
// resolves no coordinator (found=false), replicating the released_at IS
// NULL filter ListHeldByReplica applies.
func TestMemoryStoreGetBySession_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	_ = s.Upsert(ctx, Lease{
		TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1",
		CoordinatorAddress: "10.0.0.1:50054", CoordinationGeneration: 4,
	})

	got, found, err := s.GetBySession(ctx, "acme", "s1")
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if !found {
		t.Fatal("GetBySession found = false, want true for an active lease")
	}
	if got.CoordinatorReplica != "rep-1" || got.CoordinatorAddress != "10.0.0.1:50054" || got.CoordinationGeneration != 4 {
		t.Fatalf("GetBySession = %+v, want rep-1 / 10.0.0.1:50054 / gen 4", got)
	}

	// A missing session resolves no coordinator.
	if _, found, _ := s.GetBySession(ctx, "acme", "missing"); found {
		t.Fatal("GetBySession(missing) found = true, want false")
	}

	// A released lease resolves no coordinator: the released_at IS NULL
	// filter means a session whose coordinator relinquished routes nowhere
	// rather than to a stale holder.
	if err := s.Release(ctx, "acme", "s1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, _ := s.GetBySession(ctx, "acme", "s1"); found {
		t.Fatal("GetBySession found = true after Release, want false (released resolves no coordinator)")
	}
}

func TestMemoryStoreUpsertRejectsEmptyIDs(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	for _, l := range []Lease{
		{SessionID: "s1", CoordinatorReplica: "r"},
		{TenantID: "acme", CoordinatorReplica: "r"},
		{TenantID: "acme", SessionID: "s1"},
	} {
		if err := s.Upsert(ctx, l); err == nil {
			t.Errorf("Upsert(%+v) = nil error, want rejection", l)
		}
	}
}

// spec: §12.1 line 5 — the mandatory erasure primitives reject an empty
// scope and otherwise remove the scoped rows.
func TestMemoryStoreErasure_spec_12_1_5(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(nil)
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1"})
	_ = s.Upsert(ctx, Lease{TenantID: "acme", SessionID: "s2", CoordinatorReplica: "rep-1"})
	_ = s.Upsert(ctx, Lease{TenantID: "globex", SessionID: "s3", CoordinatorReplica: "rep-1"})

	if err := s.DeleteByUser(ctx, "", "u", []string{"s1"}); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("DeleteByUser empty tenant = %v, want ErrEmptyScope", err)
	}
	if err := s.DeleteByUser(ctx, "acme", "u", []string{"s1"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	held, _ := s.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 2 {
		t.Fatalf("after DeleteByUser held = %d, want 2", len(held))
	}

	if err := s.DeleteByTenant(ctx, ""); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("DeleteByTenant empty = %v, want ErrEmptyScope", err)
	}
	if err := s.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	held, _ = s.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 1 || held[0].TenantID != "globex" {
		t.Fatalf("after DeleteByTenant held = %+v, want only globex", held)
	}
}
