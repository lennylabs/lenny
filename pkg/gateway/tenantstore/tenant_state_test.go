// SPDX-License-Identifier: MIT

package tenantstore

import (
	"context"
	"testing"
	"time"
)

// spec: §12.8 line 865 — the TenantState enum is closed
// (active/disabling/deleting/deleted); the empty pre-lifecycle value is
// valid and read as active. F-12.8.12.
func TestValidTenantState_spec_12_8_865(t *testing.T) {
	for _, s := range append(AllTenantStates(), "") {
		if !ValidTenantState(s) {
			t.Errorf("ValidTenantState(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"paused", "ACTIVE", "gone"} {
		if ValidTenantState(s) {
			t.Errorf("ValidTenantState(%q) = true, want false", s)
		}
	}
}

// spec: §12.8 lines 865-873 — only `active` (and the empty value, read
// as active) accepts new work; every other state rejects it. F-12.8.12.
func TestAcceptsNewWork_spec_12_8_865(t *testing.T) {
	cases := map[string]bool{
		"":                   true,
		TenantStateActive:    true,
		TenantStateDisabling: false,
		TenantStateDeleting:  false,
		TenantStateDeleted:   false,
	}
	for state, want := range cases {
		if got := (Tenant{State: state}).AcceptsNewWork(); got != want {
			t.Errorf("Tenant{State:%q}.AcceptsNewWork() = %v, want %v", state, got, want)
		}
	}
}

// spec: §12.8 line 865 — a new tenant is born `active`; the in-memory
// store defaults the column so the admin API never reports an empty
// state for a freshly created tenant. F-12.8.12.
func TestMemoryCreateDefaultsActive_spec_12_8_865(t *testing.T) {
	store := NewMemory()
	if err := store.Create(context.Background(), Tenant{ID: "acme"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(context.Background(), "acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != TenantStateActive {
		t.Errorf("state = %q, want active", got.State)
	}
}

// spec: §12.8 Phase 6 — a soft-deleted tenant is a tombstone, so
// SoftDelete advances its TenantState to `deleted`. F-12.8.12.
func TestMemorySoftDeleteSetsDeletedState_spec_12_8_865(t *testing.T) {
	store := NewMemory()
	_ = store.Create(context.Background(), Tenant{ID: "acme"})
	if err := store.SoftDelete(context.Background(), "acme", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// A soft-deleted tenant is dropped from the default List, so read it
	// back with IncludeDeleted to inspect its tombstone state.
	rows, err := store.List(context.Background(), ListFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].State != TenantStateDeleted {
		t.Fatalf("tombstone state = %+v, want one row with state=deleted", rows)
	}
	if rows[0].AcceptsNewWork() {
		t.Error("a deleted tenant must not accept new work")
	}
}
