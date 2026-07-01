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

// spec: §15.1 lines 818-819 — IsSuspended reports the operator
// suspension marker, orthogonal to the §12.8 deletion lifecycle. An
// active or even disabling tenant may carry it. F-15.1.3.
func TestIsSuspended_spec_15_1_818(t *testing.T) {
	if (Tenant{}).IsSuspended() {
		t.Error("a zero tenant must not be suspended")
	}
	if !(Tenant{Suspended: true}).IsSuspended() {
		t.Error("Suspended=true must report IsSuspended")
	}
	// Suspension is independent of the deletion lifecycle: a suspended,
	// still-active tenant is both suspended and accepts-new-work=true at
	// the store layer (the gateway gate rejects it on suspension first).
	tn := Tenant{State: TenantStateActive, Suspended: true}
	if !tn.IsSuspended() || !tn.AcceptsNewWork() {
		t.Errorf("active+suspended tenant: IsSuspended=%v AcceptsNewWork=%v, want true/true",
			tn.IsSuspended(), tn.AcceptsNewWork())
	}
}

// spec: §15.1 lines 818-819 — the in-memory store round-trips the
// suspension marker, reason, operator, and timestamp through Update.
// F-15.1.3.
func TestMemoryUpdatePersistsSuspension_spec_15_1_818(t *testing.T) {
	store := NewMemory()
	_ = store.Create(context.Background(), Tenant{ID: "acme"})
	at := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	if _, err := store.Update(context.Background(), "acme", func(tn *Tenant) error {
		tn.Suspended = true
		tn.SuspendedReason = "abuse"
		tn.SuspendedBy = "admin@acme.com"
		tn.SuspendedAt = at
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme")
	if !got.Suspended || got.SuspendedReason != "abuse" || got.SuspendedBy != "admin@acme.com" || !got.SuspendedAt.Equal(at) {
		t.Errorf("round-trip = %+v, want suspended with reason/operator/timestamp", got)
	}

	// Clearing the marker on resume is likewise durable.
	if _, err := store.Update(context.Background(), "acme", func(tn *Tenant) error {
		tn.Suspended = false
		tn.SuspendedReason = ""
		tn.SuspendedBy = ""
		tn.SuspendedAt = time.Time{}
		return nil
	}); err != nil {
		t.Fatalf("resume update: %v", err)
	}
	got, _ = store.Get(context.Background(), "acme")
	if got.Suspended || got.SuspendedReason != "" || !got.SuspendedAt.IsZero() {
		t.Errorf("cleared row = %+v, want no suspension metadata", got)
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
