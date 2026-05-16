// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
)

// spec: §12.8 GDPR erasure-job registry.

func TestMemoryCreateAndGet(t *testing.T) {
	m := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	want := erasurejob.Job{
		ID:        "erasure_abc",
		TenantID:  "acme",
		UserID:    "alice",
		Phase:     erasurejob.PhaseInitiated,
		StartedAt: at,
	}
	if err := m.Create(context.Background(), want); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(context.Background(), "erasure_abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TenantID != "acme" || got.UserID != "alice" || got.Phase != erasurejob.PhaseInitiated {
		t.Errorf("Get = %+v, want acme/alice/initiated", got)
	}
	if !got.StartedAt.Equal(at) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, at)
	}
}

func TestMemoryCreateRejectsDuplicate(t *testing.T) {
	m := erasurejob.NewMemory()
	j := erasurejob.Job{ID: "erasure_dup", TenantID: "acme", UserID: "alice"}
	if err := m.Create(context.Background(), j); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := m.Create(context.Background(), j); !errors.Is(err, erasurejob.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
}

func TestMemoryCreateRejectsMissingID(t *testing.T) {
	m := erasurejob.NewMemory()
	if err := m.Create(context.Background(), erasurejob.Job{TenantID: "acme"}); !errors.Is(err, erasurejob.ErrMissingID) {
		t.Errorf("Create with empty ID: got %v, want ErrMissingID", err)
	}
}

func TestMemoryGetUnknownReturnsNotFound(t *testing.T) {
	m := erasurejob.NewMemory()
	if _, err := m.Get(context.Background(), "erasure_absent"); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Errorf("Get unknown: got %v, want ErrNotFound", err)
	}
}

func TestMemoryUpdateMutatesJob(t *testing.T) {
	m := erasurejob.NewMemory()
	if err := m.Create(context.Background(), erasurejob.Job{
		ID: "erasure_u", TenantID: "acme", UserID: "alice", Phase: erasurejob.PhaseInitiated,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := m.Update(context.Background(), "erasure_u", func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseStoreDeleting
		j.Total = 7
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Phase != erasurejob.PhaseStoreDeleting || updated.Total != 7 {
		t.Errorf("Update returned %+v, want store_deleting/7", updated)
	}
	got, _ := m.Get(context.Background(), "erasure_u")
	if got.Phase != erasurejob.PhaseStoreDeleting || got.Total != 7 {
		t.Errorf("Get after Update = %+v, want the mutation persisted", got)
	}
}

func TestMemoryUpdateUnknownReturnsNotFound(t *testing.T) {
	m := erasurejob.NewMemory()
	_, err := m.Update(context.Background(), "erasure_absent", func(*erasurejob.Job) error { return nil })
	if !errors.Is(err, erasurejob.ErrNotFound) {
		t.Errorf("Update unknown: got %v, want ErrNotFound", err)
	}
}

func TestMemoryUpdatePropagatesMutateError(t *testing.T) {
	m := erasurejob.NewMemory()
	_ = m.Create(context.Background(), erasurejob.Job{ID: "erasure_e", TenantID: "acme"})
	boom := errors.New("mutate failed")
	if _, err := m.Update(context.Background(), "erasure_e", func(*erasurejob.Job) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Errorf("Update: got %v, want the mutate error", err)
	}
}

func TestMemoryUpdateIsolatesStoredState(t *testing.T) {
	m := erasurejob.NewMemory()
	_ = m.Create(context.Background(), erasurejob.Job{ID: "erasure_iso", TenantID: "acme"})
	got, err := m.Update(context.Background(), "erasure_iso", func(j *erasurejob.Job) error {
		j.Deleted = map[string]int{"sessions": 3}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Mutating the returned map must not reach into the stored job.
	got.Deleted["sessions"] = 999
	reread, _ := m.Get(context.Background(), "erasure_iso")
	if reread.Deleted["sessions"] != 3 {
		t.Errorf("stored Deleted[sessions] = %d, want 3 — returned copy aliases stored state",
			reread.Deleted["sessions"])
	}
}

func TestPhaseTerminal(t *testing.T) {
	terminal := []erasurejob.Phase{erasurejob.PhaseCompleted, erasurejob.PhaseFailed}
	for _, p := range terminal {
		if !p.Terminal() {
			t.Errorf("phase %q should be terminal", p)
		}
	}
	ongoing := []erasurejob.Phase{
		erasurejob.PhaseInitiated, erasurejob.PhaseStoreDeleting,
		erasurejob.PhasePseudonymizing, erasurejob.PhaseVerifying,
	}
	for _, p := range ongoing {
		if p.Terminal() {
			t.Errorf("phase %q should not be terminal", p)
		}
	}
}

func TestNewIDFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := erasurejob.NewID()
		if !strings.HasPrefix(id, "erasure_") {
			t.Fatalf("NewID = %q, want erasure_ prefix", id)
		}
		if len(id) != len("erasure_")+16 {
			t.Fatalf("NewID = %q (length %d), want 16 hex chars after the prefix", id, len(id))
		}
		if seen[id] {
			t.Fatalf("NewID returned a duplicate id %q", id)
		}
		seen[id] = true
	}
}
