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

// spec: §24.11 lines 135-136 — GetByID resolves a session across tenants
// for the platform-admin investigation surface, unlike the tenant-scoped
// Get. F-24.11.2.
func TestGetByIDResolvesAcrossTenants(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateRunning})

	got, err := s.GetByID(ctx, "sess_1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TenantID != "acme" || got.State != session.StateRunning {
		t.Errorf("GetByID = %+v, want tenant acme / running", got)
	}
}

func TestGetByIDMissingReturnsNotFound(t *testing.T) {
	s := memstore.New()
	_, err := s.GetByID(context.Background(), "missing")
	if !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("GetByID missing: want ErrNotFound, got %v", err)
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

// TestListFiltersByUserID asserts the §11.4 full_revoke step-1
// SessionStore lookup narrows to the invalidation subject, so a tenant
// with many sessions does not read tenant-wide on every revoke.
// spec: §11.4 line 256.
func TestListFiltersByUserID_spec_11_4_256(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_a1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_a2", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCreated})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_b1", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning})

	got, err := s.List(ctx, "acme", sessionstore.ListFilter{UserID: "alice@acme.com"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("filtered List: want 2 alice rows, got %d (%v)", len(got), got)
	}
	for _, row := range got {
		if row.UserID != "alice@acme.com" {
			t.Errorf("user filter leaked a foreign-user row: %s/%s", row.UserID, row.ID)
		}
	}
}

// TestListUserIDIsTenantScoped asserts a UserID match in a peer tenant
// is not surfaced by the §11.4 lookup. spec: §11.4 line 256.
func TestListUserIDIsTenantScoped_spec_11_4_256(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "acme_a", TenantID: "acme", UserID: "alice", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "globex_a", TenantID: "globex", UserID: "alice", State: session.StateRunning})

	got, _ := s.List(ctx, "acme", sessionstore.ListFilter{UserID: "alice"})
	if len(got) != 1 || got[0].ID != "acme_a" {
		t.Errorf("UserID filter must respect tenant scope; got %v", got)
	}
}

// TestListFiltersByLabels asserts the §15.1 line 598 labels filter is
// AND-containment: a row matches only when its Labels map contains every
// requested key=value pair. F-15.1.15.
func TestListFiltersByLabels_spec_15_1_598(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "m1", TenantID: "acme", State: session.StateRunning, Labels: map[string]string{"team": "payments", "tier": "gold"}})
	_ = s.Create(ctx, sessionstore.Session{ID: "m2", TenantID: "acme", State: session.StateRunning, Labels: map[string]string{"team": "payments", "tier": "silver"}})
	_ = s.Create(ctx, sessionstore.Session{ID: "m3", TenantID: "acme", State: session.StateRunning, Labels: map[string]string{"team": "search"}})
	_ = s.Create(ctx, sessionstore.Session{ID: "m4", TenantID: "acme", State: session.StateRunning})

	// Single-key match returns both payments rows.
	got, _ := s.List(ctx, "acme", sessionstore.ListFilter{Labels: map[string]string{"team": "payments"}})
	if len(got) != 2 {
		t.Fatalf("single-label filter: want 2 rows, got %d (%v)", len(got), ids(got))
	}
	// Two-key AND-containment narrows to the single gold row.
	got, _ = s.List(ctx, "acme", sessionstore.ListFilter{Labels: map[string]string{"team": "payments", "tier": "gold"}})
	if len(got) != 1 || got[0].ID != "m1" {
		t.Errorf("AND-containment filter: want [m1], got %v", ids(got))
	}
	// A value mismatch matches nothing.
	got, _ = s.List(ctx, "acme", sessionstore.ListFilter{Labels: map[string]string{"team": "payments", "tier": "bronze"}})
	if len(got) != 0 {
		t.Errorf("value-mismatch filter: want 0 rows, got %v", ids(got))
	}
	// An empty label filter matches every row.
	got, _ = s.List(ctx, "acme", sessionstore.ListFilter{})
	if len(got) != 4 {
		t.Errorf("no-label filter: want 4 rows, got %d", len(got))
	}
}

// TestListExcludeDeriveFailures asserts the §15.1 lines 652/661
// `?includeDeriveFailures=false` behaviour: derive_failure audit rows are
// returned by default and dropped only when the flag is set. F-15.1.14.
func TestListExcludeDeriveFailures_spec_15_1_652(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "live", TenantID: "acme", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "df", TenantID: "acme", State: session.StateFailed, FailureClass: session.FailureClassDeriveFailure})
	_ = s.Create(ctx, sessionstore.Session{ID: "rf", TenantID: "acme", State: session.StateFailed, FailureClass: session.FailureClassRuntime})

	// Default: derive_failure row included.
	got, _ := s.List(ctx, "acme", sessionstore.ListFilter{})
	if len(got) != 3 {
		t.Fatalf("default list: want 3 rows (incl derive_failure), got %d (%v)", len(got), ids(got))
	}
	// ExcludeDeriveFailures drops only the derive_failure row, keeping the
	// runtime_failure row.
	got, _ = s.List(ctx, "acme", sessionstore.ListFilter{ExcludeDeriveFailures: true})
	if len(got) != 2 {
		t.Fatalf("exclude list: want 2 rows, got %d (%v)", len(got), ids(got))
	}
	for _, row := range got {
		if row.FailureClass == session.FailureClassDeriveFailure {
			t.Errorf("derive_failure row leaked past ExcludeDeriveFailures: %s", row.ID)
		}
	}
}

func ids(rows []sessionstore.Session) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
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

// TestUpdateClampsLastSeqMonotonically covers F-7.3.3 / §7.3 line 397:
// sessions.last_seq is monotonic and an accidental rollback in the
// mutate callback must be clamped back to the prior value. The
// production pgstore enforces the same floor via GREATEST in updateSQL.
func TestUpdateClampsLastSeqMonotonically(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{
		ID: "sess_1", TenantID: "acme", State: session.StateRunning,
		LastSeq: 42,
	})
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.LastSeq = 7
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.LastSeq != 42 {
		t.Errorf("LastSeq: want 42 (clamped), got %d", updated.LastSeq)
	}
}

// TestUpdateAdvancesLastSeq covers F-7.3.3 / §7.3 line 397: a legitimate
// advance writes through the store and round-trips on subsequent reads.
func TestUpdateAdvancesLastSeq(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_1", TenantID: "acme", State: session.StateRunning})
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.LastSeq = 5
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.LastSeq != 5 {
		t.Errorf("LastSeq: want 5, got %d", updated.LastSeq)
	}
	got, err := s.Get(ctx, "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastSeq != 5 {
		t.Errorf("after re-read, LastSeq: want 5, got %d", got.LastSeq)
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

// spec: §5.2 line 521 — GetActiveSlotsByPod counts only the live
// (non-terminal) sessions bound to a pod, across every tenant, and
// excludes sessions on other pods.
func TestGetActiveSlotsByPod_spec_5_2_521(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	// Two live sessions on pod-a for tenant acme.
	_ = s.Create(ctx, sessionstore.Session{ID: "a1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-a"})
	_ = s.Create(ctx, sessionstore.Session{ID: "a2", TenantID: "acme", State: session.StateStarting, PodAssignment: "pod-a"})
	// A terminal session on pod-a must not count.
	_ = s.Create(ctx, sessionstore.Session{ID: "a3", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-a"})
	// A live session on a different pod must not count toward pod-a.
	_ = s.Create(ctx, sessionstore.Session{ID: "b1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-b"})
	// A session with no pod binding must not count.
	_ = s.Create(ctx, sessionstore.Session{ID: "u1", TenantID: "acme", State: session.StateRunning, PodAssignment: ""})

	got, err := s.GetActiveSlotsByPod(ctx, "pod-a")
	if err != nil {
		t.Fatalf("GetActiveSlotsByPod: %v", err)
	}
	if got != 2 {
		t.Errorf("pod-a active slots = %d, want 2 (live only, this pod only)", got)
	}

	// pod-b has one live slot.
	if got, _ := s.GetActiveSlotsByPod(ctx, "pod-b"); got != 1 {
		t.Errorf("pod-b active slots = %d, want 1", got)
	}
	// A pod with no sessions returns zero, never an error.
	if got, err := s.GetActiveSlotsByPod(ctx, "pod-none"); err != nil || got != 0 {
		t.Errorf("pod-none = (%d, %v), want (0, nil)", got, err)
	}
	// An empty pod identity matches no slot.
	if got, _ := s.GetActiveSlotsByPod(ctx, ""); got != 0 {
		t.Errorf("empty pod = %d, want 0", got)
	}
}

// spec: §5.2 line 521 — the count aggregates across tenants because the
// rehydration path holds only the pod identity (a pod is pinned to one
// tenant by §5.2, but the query itself is pod-scoped).
func TestGetActiveSlotsByPodCrossTenant_spec_5_2_521(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	// Two tenants both reference pod-shared (a pathological pre-pinning
	// state the count must still tally rather than tenant-filter).
	_ = s.Create(ctx, sessionstore.Session{ID: "x", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-shared"})
	_ = s.Create(ctx, sessionstore.Session{ID: "y", TenantID: "globex", State: session.StateRunning, PodAssignment: "pod-shared"})

	got, err := s.GetActiveSlotsByPod(ctx, "pod-shared")
	if err != nil {
		t.Fatalf("GetActiveSlotsByPod: %v", err)
	}
	if got != 2 {
		t.Errorf("cross-tenant active slots = %d, want 2", got)
	}
}

// TestCreateStandaloneSessionIsItsOwnRoot_spec_8_9_1010 pins the §8.9
// line 1010 default: a session created without a parent has
// RootSessionID equal to its own id (a standalone session is the root
// of its own tree). F-8.9.8.
func TestCreateStandaloneSessionIsItsOwnRoot_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if err := s.Create(ctx, sessionstore.Session{
		ID: "sess_alone", TenantID: "acme", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(ctx, "acme", "sess_alone")
	if got.RootSessionID != "sess_alone" {
		t.Errorf("standalone session RootSessionID = %q, want %q (own id)", got.RootSessionID, "sess_alone")
	}
}

// TestCreateChildInheritsParentRoot_spec_8_9_1010 pins the §8.9 line
// 1010 inheritance: a session created with ParentSessionID inherits
// its parent's RootSessionID so all rows in one delegation tree share
// the same root_session_id. F-8.9.8.
func TestCreateChildInheritsParentRoot_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{
		ID: "sess_root_v", TenantID: "acme", State: session.StateRunning,
	})
	_ = s.Create(ctx, sessionstore.Session{
		ID: "sess_kid_v", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_root_v",
	})
	_ = s.Create(ctx, sessionstore.Session{
		ID: "sess_gc_v", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_kid_v",
	})
	for _, id := range []string{"sess_root_v", "sess_kid_v", "sess_gc_v"} {
		row, _ := s.Get(ctx, "acme", id)
		if row.RootSessionID != "sess_root_v" {
			t.Errorf("%s.RootSessionID = %q, want sess_root_v (inherited from tree root)", id, row.RootSessionID)
		}
	}
}

// TestCreateCallerProvidedRootWins_spec_8_9_1010 verifies the caller
// can stamp RootSessionID explicitly (the §8.2 delegation site path
// supplies it directly even when the row has no ParentSessionID at
// Create time). F-8.9.8.
func TestCreateCallerProvidedRootWins_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	if err := s.Create(ctx, sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		RootSessionID: "sess_external_root",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(ctx, "acme", "sess_x")
	if got.RootSessionID != "sess_external_root" {
		t.Errorf("RootSessionID = %q, want %q (caller-provided wins)", got.RootSessionID, "sess_external_root")
	}
}

// TestListByRootScopesToOneTree_spec_8_9_1010 pins the §8.9 single-
// shard tree-scoped projection: ListByRoot returns only the rows
// belonging to the requested tree, regardless of how many trees share
// the tenant. F-8.9.7.
func TestListByRootScopesToOneTree_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	// Tree A: sess_a + child sess_ac.
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_a", TenantID: "acme", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_ac", TenantID: "acme", State: session.StateRunning, ParentSessionID: "sess_a"})
	// Tree B: sess_b + child sess_bc.
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_b", TenantID: "acme", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_bc", TenantID: "acme", State: session.StateRunning, ParentSessionID: "sess_b"})
	// Foreign tenant tree must not leak through.
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_foreign", TenantID: "globex", State: session.StateRunning})

	got, err := s.ListByRoot(ctx, "acme", "sess_a")
	if err != nil {
		t.Fatalf("ListByRoot: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
	}
	if len(ids) != 2 || !ids["sess_a"] || !ids["sess_ac"] {
		t.Errorf("tree A = %v, want {sess_a, sess_ac}", ids)
	}
	// Tree B is independent.
	got, _ = s.ListByRoot(ctx, "acme", "sess_b")
	ids = map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
	}
	if len(ids) != 2 || !ids["sess_b"] || !ids["sess_bc"] {
		t.Errorf("tree B = %v, want {sess_b, sess_bc}", ids)
	}
}

// TestListByRootEmptyRootReturnsNoRows_spec_8_9_1010 pins the empty-
// rootSessionID convention: an empty rootSessionID returns no rows
// rather than the whole tenant, so a typo cannot collapse to a tenant
// dump. F-8.9.7.
func TestListByRootEmptyRootReturnsNoRows_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_x", TenantID: "acme", State: session.StateRunning})
	got, err := s.ListByRoot(ctx, "acme", "")
	if err != nil {
		t.Fatalf("ListByRoot: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty rootSessionID returned %d rows, want 0", len(got))
	}
}

// TestListByRootCrossTenantIsolation_spec_8_9_1010 verifies a tree in
// one tenant is invisible to another tenant's ListByRoot. F-8.9.7.
func TestListByRootCrossTenantIsolation_spec_8_9_1010(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_acme_root", TenantID: "acme", State: session.StateRunning})
	got, err := s.ListByRoot(ctx, "globex", "sess_acme_root")
	if err != nil {
		t.Fatalf("ListByRoot: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("cross-tenant ListByRoot returned %d rows, want 0", len(got))
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant hard-deletes
// every session row belonging to the tenant; rows owned by other
// tenants survive unchanged.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_a1", TenantID: "acme", UserID: "alice", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_a2", TenantID: "acme", UserID: "bob", State: session.StateRunning})
	_ = s.Create(ctx, sessionstore.Session{ID: "sess_g1", TenantID: "globex", UserID: "carol", State: session.StateRunning})

	n, err := s.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme rows, got %d", n)
	}
	for _, id := range []string{"sess_a1", "sess_a2"} {
		if _, err := s.Get(ctx, "acme", id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("session %s should be erased", id)
		}
	}
	if _, err := s.Get(ctx, "globex", "sess_g1"); err != nil {
		t.Errorf("globex session must survive: %v", err)
	}
}

// spec: §12.1 line 5 — DeleteByTenant is idempotent: a second call on
// an empty scope returns (0, nil), never ErrNotFound.
func TestDeleteByTenantIdempotent_spec_12_1(t *testing.T) {
	s := memstore.New()
	n, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil || n != 0 {
		t.Errorf("DeleteByTenant on empty store: n=%d err=%v", n, err)
	}
}
