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
