// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

func TestCreateAndGet(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if err := s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != session.StateCreated {
		t.Errorf("State: want created, got %q", got.State)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set by Create")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated})
	err := s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated})
	if !errors.Is(err, sessionstore.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestGetRejectsCrossTenant(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated})
	// Foreign tenant must see ErrNotFound — never ErrCrossTenant or similar.
	_, err := s.Get(ctx, "globex", "sess_1")
	if !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("cross-tenant Get must return ErrNotFound (no existence leak), got %v", err)
	}
}

func TestUpdateTransitionsState(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated})
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.State = session.StateFinalizing
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.State != session.StateFinalizing {
		t.Errorf("State after Update: want finalizing, got %q", updated.State)
	}
	if updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Errorf("UpdatedAt should advance past CreatedAt")
	}
}

func TestUpdateRejectsMissing(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_, err := s.Update(ctx, "acme", "sess_missing", func(*sessionstore.Session) error { return nil })
	if !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListFiltersByState(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	for i, state := range []session.State{session.StateCreated, session.StateRunning, session.StateCompleted} {
		_ = s.Create(ctx, sessionstore.Session{
			ID:       string(rune('a' + i)),
			TenantID: "acme",
			State:    state,
		})
	}
	got, _ := s.List(ctx, "acme", sessionstore.ListFilter{State: session.StateRunning})
	if len(got) != 1 || got[0].State != session.StateRunning {
		t.Errorf("filtered List: want 1 running row, got %v", got)
	}
}

func TestListExcludesForeignTenant(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "a", TenantID: "acme", State: session.StateCreated})
	_ = s.Create(ctx, sessionstore.Session{ID: "b", TenantID: "globex", State: session.StateCreated})
	got, _ := s.List(ctx, "acme", sessionstore.ListFilter{})
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("List must exclude foreign tenant rows; got %v", got)
	}
}

func TestDeleteAndGetMissing(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated})
	if err := s.Delete(ctx, "acme", "sess_1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "acme", "sess_1"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
}

func TestDeleteByUserRemovesAllUserSessions(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	for _, id := range []string{"sess_a1", "sess_a2"} {
		_ = s.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning})
	}
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_b1", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning})

	deleted, err := s.DeleteByUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	for _, id := range []string{"sess_a1", "sess_a2"} {
		if _, err := s.Get(ctx, "acme", id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("session %s should be erased", id)
		}
	}
	if _, err := s.Get(ctx, "acme", "sess_b1"); err != nil {
		t.Errorf("another user's session must survive the erasure: %v", err)
	}
}

func TestDeleteByUserScopedByTenant(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_acme", TenantID: "acme", UserID: "alice", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_globex", TenantID: "globex", UserID: "alice", State: session.StateRunning})

	deleted, err := s.DeleteByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 — only the acme session", deleted)
	}
	if _, err := s.Get(ctx, "globex", "sess_globex"); err != nil {
		t.Errorf("the same user's session in another tenant must survive: %v", err)
	}
}

func TestDeleteByUserNoSessionsIsNoOp(t *testing.T) {
	s := memstore.New()
	deleted, err := s.DeleteByUser(context.Background(), "acme", "nobody@acme.com")
	if err != nil {
		t.Errorf("erasing a user with no sessions should not error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

// spec: §4.2 line 156 — newly created sessions are written at
// schema_version=1 by default. Recovery and coordination generations
// start at zero. Cwd and PodAssignment are empty.
func TestCreateDefaultsSessionRecordFields(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if err := s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateCreated}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(ctx, "acme", "sess_1")
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion: want 1, got %d", got.SchemaVersion)
	}
	if got.RecoveryGeneration != 0 {
		t.Errorf("RecoveryGeneration: want 0, got %d", got.RecoveryGeneration)
	}
	if got.CoordinationGeneration != 0 {
		t.Errorf("CoordinationGeneration: want 0, got %d", got.CoordinationGeneration)
	}
	if got.Cwd != "" {
		t.Errorf("Cwd: want empty, got %q", got.Cwd)
	}
	if got.PodAssignment != "" {
		t.Errorf("PodAssignment: want empty, got %q", got.PodAssignment)
	}
}

// spec: §4.2 line 156 — recovery_generation and coordination_generation
// are monotonically non-decreasing across every state transition; never
// rolled back, never reset. The store clamps an accidental decrement
// in the mutate callback back to the prior value.
func TestUpdateClampsGenerationCountersMonotonically(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{
		ID: "sess_1", TenantID: "acme", State: session.StateRunning,
		RecoveryGeneration: 3, CoordinationGeneration: 5,
	})
	// Try to roll back both counters — the store must clamp them.
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.RecoveryGeneration = 1
		row.CoordinationGeneration = 0
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RecoveryGeneration != 3 {
		t.Errorf("RecoveryGeneration: want 3 (clamped), got %d", updated.RecoveryGeneration)
	}
	if updated.CoordinationGeneration != 5 {
		t.Errorf("CoordinationGeneration: want 5 (clamped), got %d", updated.CoordinationGeneration)
	}
}

// spec: §4.2 line 156 — both counters advance monotonically on
// legitimate increments.
func TestUpdateAdvancesGenerationCounters(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateRunning})
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.RecoveryGeneration++
		row.CoordinationGeneration += 2
		row.PodAssignment = "pod-xyz"
		row.Cwd = "/workspace"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RecoveryGeneration != 1 {
		t.Errorf("RecoveryGeneration: want 1, got %d", updated.RecoveryGeneration)
	}
	if updated.CoordinationGeneration != 2 {
		t.Errorf("CoordinationGeneration: want 2, got %d", updated.CoordinationGeneration)
	}
	if updated.PodAssignment != "pod-xyz" {
		t.Errorf("PodAssignment: want pod-xyz, got %q", updated.PodAssignment)
	}
	if updated.Cwd != "/workspace" {
		t.Errorf("Cwd: want /workspace, got %q", updated.Cwd)
	}
}

// spec: §4.2 line 160 — the pod_assignment field is the cross-replica
// source of truth for the pod-to-session binding. Get must read back
// what Update wrote so a fresh replica observes the binding without
// touching the in-memory Registry.
func TestPodAssignmentReadBackAcrossReadAfterWrite(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateRunning})
	if _, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.PodAssignment = "agent-pod-42"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(ctx, "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PodAssignment != "agent-pod-42" {
		t.Errorf("read-after-write PodAssignment: want agent-pod-42, got %q", got.PodAssignment)
	}
}

// spec: §4.2 line 156 — concurrent Updates on the same row maintain
// the monotonicity floor even when mutate callbacks race. The store's
// mutex serializes the read-mutate-write so successive bumps preserve
// the latest counter value.
func TestUpdateConcurrentGenerationBumpsPreserveMonotonicity(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateRunning})
	const n = 50
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
				row.CoordinationGeneration++
				return nil
			})
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	got, err := s.Get(ctx, "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != int64(n) {
		t.Errorf("CoordinationGeneration: want %d, got %d", n, got.CoordinationGeneration)
	}
}

// spec: §4.2 line 156 — a Create call carrying an explicit schema
// version preserves it (so a forward-compat path can write a
// non-default value without the store overriding it).
func TestCreatePreservesExplicitSchemaVersion(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if err := s.Create(ctx, sessionstore.Session{
		ID: "sess_1", TenantID: "acme", State: session.StateCreated,
		SchemaVersion: 7,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(ctx, "acme", "sess_1")
	if got.SchemaVersion != 7 {
		t.Errorf("SchemaVersion: want 7 (preserved), got %d", got.SchemaVersion)
	}
}
