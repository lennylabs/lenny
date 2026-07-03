// SPDX-License-Identifier: MIT

package sessioncheckpointmeta

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedNow() func() time.Time {
	t := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// spec: §10.1 lines 165-166, 393 — the gateway writes one row per
// session after BarrierAck; a later barrier overwrites it. Upsert then
// Get round-trips the retained fields (barrier_id, checkpoint_ref,
// workspace_recovery_fraction) including the nullable
// workspace-recovery fraction (§10.1 line 393).
func TestMemoryStoreUpsertGetRoundTrip_spec_10_1(t *testing.T) {
	m := NewMemoryStore(fixedNow())
	ctx := context.Background()
	frac := 0.75
	rec := Record{
		TenantID:                  "acme",
		SessionID:                 "sess-1",
		CoordinationGeneration:    3,
		BarrierID:                 "1",
		CheckpointRef:             "ckpt-abc",
		WorkspaceRecoveryFraction: &frac,
	}
	if err := m.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := m.Get(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 3 || got.BarrierID != "1" ||
		got.CheckpointRef != "ckpt-abc" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.WorkspaceRecoveryFraction == nil || *got.WorkspaceRecoveryFraction != 0.75 {
		t.Errorf("fraction not round-tripped: %v", got.WorkspaceRecoveryFraction)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not stamped")
	}
}

// spec: §10.1 lines 165-166 — a barrier on a session that already has a
// row replaces the prior generation's metadata.
func TestMemoryStoreUpsertOverwrites_spec_10_1(t *testing.T) {
	m := NewMemoryStore(fixedNow())
	ctx := context.Background()
	_ = m.Upsert(ctx, Record{TenantID: "acme", SessionID: "s", CoordinationGeneration: 1, BarrierID: "1", CheckpointRef: "ck1"})
	if err := m.Upsert(ctx, Record{TenantID: "acme", SessionID: "s", CoordinationGeneration: 2, BarrierID: "2", CheckpointRef: "ck2"}); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, err := m.Get(ctx, "acme", "s")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 2 || got.BarrierID != "2" || got.CheckpointRef != "ck2" {
		t.Errorf("overwrite did not take: %+v", got)
	}
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	m := NewMemoryStore(fixedNow())
	if _, err := m.Get(context.Background(), "acme", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreUpsertRequiresIDs(t *testing.T) {
	m := NewMemoryStore(fixedNow())
	ctx := context.Background()
	if err := m.Upsert(ctx, Record{SessionID: "s"}); err == nil {
		t.Error("empty tenant id should error")
	}
	if err := m.Upsert(ctx, Record{TenantID: "acme"}); err == nil {
		t.Error("empty session id should error")
	}
}

// spec: §12.1 line 5 / §12.8 — the mandatory erasure pair removes a
// user's session rows and a whole tenant's rows.
func TestMemoryStoreErasure_spec_12_8(t *testing.T) {
	m := NewMemoryStore(fixedNow())
	ctx := context.Background()
	_ = m.Upsert(ctx, Record{TenantID: "acme", SessionID: "s1"})
	_ = m.Upsert(ctx, Record{TenantID: "acme", SessionID: "s2"})
	_ = m.Upsert(ctx, Record{TenantID: "globex", SessionID: "s3"})

	if err := m.DeleteByUser(ctx, "acme", "alice", []string{"s1"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, err := m.Get(ctx, "acme", "s1"); !errors.Is(err, ErrNotFound) {
		t.Error("s1 should be erased")
	}
	if _, err := m.Get(ctx, "acme", "s2"); err != nil {
		t.Error("s2 should survive user erasure")
	}

	if err := m.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, err := m.Get(ctx, "acme", "s2"); !errors.Is(err, ErrNotFound) {
		t.Error("s2 should be erased by tenant erasure")
	}
	if _, err := m.Get(ctx, "globex", "s3"); err != nil {
		t.Error("globex row must survive acme tenant erasure")
	}
	// Idempotent.
	if err := m.DeleteByTenant(ctx, "acme"); err != nil {
		t.Errorf("second DeleteByTenant should be a no-op: %v", err)
	}
}
