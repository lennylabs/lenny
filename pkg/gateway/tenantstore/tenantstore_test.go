// SPDX-License-Identifier: MIT

package tenantstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.5/§12.8 tenant registry; §10.2 IsRegistered consumer.

func TestCreateAndGet(t *testing.T) {
	s := tenantstore.NewMemory()
	if err := s.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "Acme Corp"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "acme" || got.DisplayName != "Acme Corp" {
		t.Errorf("Get: got %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps must be set on Create: %+v", got)
	}
}

func TestCreateRejectsDuplicates(t *testing.T) {
	s := tenantstore.NewMemory()
	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	if err := s.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); !errors.Is(err, tenantstore.ErrAlreadyExists) {
		t.Errorf("dupe Create: got %v, want ErrAlreadyExists", err)
	}
}

func TestCreateRejectsInvalidID(t *testing.T) {
	s := tenantstore.NewMemory()
	for _, id := range []string{"", "with space", "with/slash", ""} {
		if err := s.Create(context.Background(), tenantstore.Tenant{ID: id}); err == nil {
			t.Errorf("Create(%q) should fail", id)
		}
	}
}

func TestGetMissing(t *testing.T) {
	s := tenantstore.NewMemory()
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, tenantstore.ErrNotFound) {
		t.Errorf("Get missing: got %v, want ErrNotFound", err)
	}
}

func TestUpdateAdvancesUpdatedAt(t *testing.T) {
	s := tenantstore.NewMemory()
	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	row, _ := s.Get(context.Background(), "acme")
	prev := row.UpdatedAt

	updated, err := s.Update(context.Background(), "acme", func(t *tenantstore.Tenant) error {
		t.DisplayName = "Acme Holdings"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "Acme Holdings" {
		t.Errorf("DisplayName: got %q", updated.DisplayName)
	}
	if !updated.UpdatedAt.After(prev) {
		t.Errorf("UpdatedAt did not advance: prev=%v, got=%v", prev, updated.UpdatedAt)
	}
}

func TestListExcludesDeletedByDefault(t *testing.T) {
	s := tenantstore.NewMemory()
	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "globex"})
	_ = s.SoftDelete(context.Background(), "globex", time.Now())

	rows, _ := s.List(context.Background(), tenantstore.ListFilter{})
	if len(rows) != 1 || rows[0].ID != "acme" {
		t.Errorf("List active: got %+v", rows)
	}

	all, _ := s.List(context.Background(), tenantstore.ListFilter{IncludeDeleted: true})
	if len(all) != 2 {
		t.Errorf("List include-deleted: got %d rows", len(all))
	}
}

func TestSoftDeleteIsIdempotent(t *testing.T) {
	s := tenantstore.NewMemory()
	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	now := time.Now()
	if err := s.SoftDelete(context.Background(), "acme", now); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "acme", now.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete idempotent: got %v, want nil", err)
	}
	row, _ := s.Get(context.Background(), "acme")
	if !row.DeletedAt.Equal(now) {
		t.Errorf("DeletedAt was overwritten on second SoftDelete: got %v, want %v",
			row.DeletedAt, now)
	}
}

func TestSoftDeleteMissingReturnsNotFound(t *testing.T) {
	s := tenantstore.NewMemory()
	err := s.SoftDelete(context.Background(), "ghost", time.Now())
	if !errors.Is(err, tenantstore.ErrNotFound) {
		t.Errorf("SoftDelete missing: got %v", err)
	}
}

func TestIsRegisteredImplementsAuthRegistry(t *testing.T) {
	s := tenantstore.NewMemory()
	var _ auth.TenantRegistry = s

	_ = s.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	ok, err := s.IsRegistered("acme")
	if err != nil || !ok {
		t.Errorf("IsRegistered(active): got %v, %v", ok, err)
	}
	ok, _ = s.IsRegistered("missing")
	if ok {
		t.Errorf("IsRegistered(missing) should return false")
	}
	_ = s.SoftDelete(context.Background(), "acme", time.Now())
	ok, _ = s.IsRegistered("acme")
	if ok {
		t.Errorf("IsRegistered(deleted) should return false")
	}
}

func TestListIsCreatedAtDescending(t *testing.T) {
	s := tenantstore.NewMemory()
	for _, id := range []string{"a", "b", "c"} {
		_ = s.Create(context.Background(), tenantstore.Tenant{
			ID:        id,
			CreatedAt: time.Date(2026, 1, int(id[0]-'a')+1, 0, 0, 0, 0, time.UTC),
		})
	}
	rows, _ := s.List(context.Background(), tenantstore.ListFilter{})
	if len(rows) != 3 || rows[0].ID != "c" || rows[2].ID != "a" {
		t.Errorf("List order: got %+v", rows)
	}
}

func TestIsActive(t *testing.T) {
	live := tenantstore.Tenant{ID: "x"}
	if !live.IsActive() {
		t.Error("IsActive on undeleted: want true")
	}
	dead := tenantstore.Tenant{ID: "x", DeletedAt: time.Now()}
	if dead.IsActive() {
		t.Error("IsActive on deleted: want false")
	}
}
