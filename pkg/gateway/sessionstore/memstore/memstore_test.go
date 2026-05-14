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
