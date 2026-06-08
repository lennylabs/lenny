// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func seedResumePool(t *testing.T, s *poolstore.Memory, name string) {
	t.Helper()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:             name,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// spec: §4.6.2 item 3 condition (c) — BumpResumeEpoch advances the
// cross-process resume signal monotonically and leaves Generation
// untouched, since a resume is not a configuration change.
func TestBumpResumeEpochAdvancesWithoutGeneration_spec_4_6_2(t *testing.T) {
	s := poolstore.NewMemory()
	seedResumePool(t, s, "p")
	before, err := s.Get(context.Background(), "p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	e1, err := s.BumpResumeEpoch(context.Background(), "p")
	if err != nil {
		t.Fatalf("BumpResumeEpoch: %v", err)
	}
	if e1 != 1 {
		t.Errorf("first bump epoch = %d, want 1", e1)
	}
	e2, err := s.BumpResumeEpoch(context.Background(), "p")
	if err != nil {
		t.Fatalf("BumpResumeEpoch: %v", err)
	}
	if e2 != 2 {
		t.Errorf("second bump epoch = %d, want 2", e2)
	}

	after, err := s.Get(context.Background(), "p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.ReconciliationResumeEpoch != 2 {
		t.Errorf("stored epoch = %d, want 2", after.ReconciliationResumeEpoch)
	}
	if after.Generation != before.Generation {
		t.Errorf("Generation changed from %d to %d; a resume must not be a config change",
			before.Generation, after.Generation)
	}
}

// spec: §4.6.2 item 3 condition (c) — a resume targeting an unknown pool
// reports ErrNotFound; an admin PUT must not silently create a row.
func TestBumpResumeEpochUnknownPool_spec_4_6_2(t *testing.T) {
	s := poolstore.NewMemory()
	if _, err := s.BumpResumeEpoch(context.Background(), "missing"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Fatalf("BumpResumeEpoch unknown = %v, want ErrNotFound", err)
	}
}

// spec: §4.6.2 item 3 condition (c) — a soft-deleted pool has no
// reconciliation to resume, so the bump reports ErrNotFound.
func TestBumpResumeEpochSoftDeleted_spec_4_6_2(t *testing.T) {
	s := poolstore.NewMemory()
	seedResumePool(t, s, "p")
	if err := s.SoftDelete(context.Background(), "p", time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := s.BumpResumeEpoch(context.Background(), "p"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Fatalf("BumpResumeEpoch soft-deleted = %v, want ErrNotFound", err)
	}
}

// spec: §4.6.2 — an admin Update preserves the resume epoch (the mutate
// closure does not touch it) so an unrelated config write does not reset
// a pending resume request.
func TestUpdatePreservesResumeEpoch_spec_4_6_2(t *testing.T) {
	s := poolstore.NewMemory()
	seedResumePool(t, s, "p")
	if _, err := s.BumpResumeEpoch(context.Background(), "p"); err != nil {
		t.Fatalf("BumpResumeEpoch: %v", err)
	}
	updated, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = 5
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.ReconciliationResumeEpoch != 1 {
		t.Errorf("resume epoch after Update = %d, want 1 (preserved)", updated.ReconciliationResumeEpoch)
	}
}
