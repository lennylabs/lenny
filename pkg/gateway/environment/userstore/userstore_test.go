// SPDX-License-Identifier: MIT

package userstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
)

// spec: §10.2 RBAC + tenant scoping; §11.4 disable/delete.

func TestCreateAndGet(t *testing.T) {
	s := userstore.NewMemory()
	u := userstore.User{
		Subject:     "alice@acme.com",
		TenantID:    "acme",
		Email:       "alice@acme.com",
		DisplayName: "Alice",
		Roles:       []auth.Role{auth.RoleUser},
	}
	if err := s.Create(context.Background(), u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "Alice" || len(got.Roles) != 1 {
		t.Errorf("Get: %+v", got)
	}
}

func TestCreateRejectsBadTenant(t *testing.T) {
	s := userstore.NewMemory()
	if err := s.Create(context.Background(), userstore.User{Subject: "alice", TenantID: "bad space"}); err == nil {
		t.Error("Create with bad tenant should fail")
	}
}

func TestCreateRejectsBadSubject(t *testing.T) {
	s := userstore.NewMemory()
	for _, sub := range []string{"", "with space", "with\ttab"} {
		err := s.Create(context.Background(), userstore.User{Subject: sub, TenantID: "acme"})
		if err == nil {
			t.Errorf("Create with subject %q should fail", sub)
		}
	}
}

func TestCreateRejectsMalformedRoleName(t *testing.T) {
	s := userstore.NewMemory()
	// A custom role name (e.g. "super-user") is well-formed and accepted;
	// a syntactically malformed name is rejected. The store validates
	// role-name syntax — custom-role existence is checked at the admin
	// layer, which has the tenant's custom-role registry.
	err := s.Create(context.Background(), userstore.User{
		Subject:  "alice",
		TenantID: "acme",
		Roles:    []auth.Role{"Bad Role"},
	})
	if err == nil {
		t.Error("Create with a malformed role name should fail")
	}
}

func TestCreateAcceptsCustomRoleName(t *testing.T) {
	s := userstore.NewMemory()
	// A well-formed non-built-in role name is a §10.2 custom-role
	// reference and is accepted by the storage layer.
	err := s.Create(context.Background(), userstore.User{
		Subject:  "alice",
		TenantID: "acme",
		Roles:    []auth.Role{"session-manager"},
	})
	if err != nil {
		t.Errorf("Create with a well-formed custom role name: got %v, want nil", err)
	}
}

func TestGetRejectsCrossTenant(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "alice", TenantID: "acme"})
	if _, err := s.Get(context.Background(), "globex", "alice"); !errors.Is(err, userstore.ErrNotFound) {
		t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "alice", TenantID: "acme"})
	row, _ := s.Get(context.Background(), "acme", "alice")
	prev := row.UpdatedAt

	updated, err := s.Update(context.Background(), "acme", "alice", func(u *userstore.User) error {
		u.DisplayName = "Alice 2"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.UpdatedAt.After(prev) {
		t.Errorf("UpdatedAt did not advance")
	}
}

func TestUpdateRejectsMalformedRoleNameAfterMutation(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "alice", TenantID: "acme"})
	_, err := s.Update(context.Background(), "acme", "alice", func(u *userstore.User) error {
		u.Roles = []auth.Role{"Bad Role"}
		return nil
	})
	if err == nil {
		t.Error("Update with a malformed role name should fail")
	}
}

func TestListExcludesDisabledAndDeletedByDefault(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "active", TenantID: "acme"})
	_ = s.Create(context.Background(), userstore.User{Subject: "disabled", TenantID: "acme", Disabled: true})
	_ = s.Create(context.Background(), userstore.User{Subject: "deleted", TenantID: "acme"})
	_ = s.SoftDelete(context.Background(), "acme", "deleted", time.Now())

	rows, _ := s.List(context.Background(), "acme", userstore.ListFilter{})
	if len(rows) != 1 || rows[0].Subject != "active" {
		t.Errorf("List default: got %+v", rows)
	}
	all, _ := s.List(context.Background(), "acme", userstore.ListFilter{
		IncludeDeleted: true, IncludeDisabled: true,
	})
	if len(all) != 3 {
		t.Errorf("List include-all: got %d", len(all))
	}
}

func TestListTenantScoped(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "a", TenantID: "acme"})
	_ = s.Create(context.Background(), userstore.User{Subject: "b", TenantID: "globex"})
	acmeRows, _ := s.List(context.Background(), "acme", userstore.ListFilter{})
	if len(acmeRows) != 1 || acmeRows[0].Subject != "a" {
		t.Errorf("acme list: %+v", acmeRows)
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "alice", TenantID: "acme"})
	first := time.Now()
	if err := s.SoftDelete(context.Background(), "acme", "alice", first); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "acme", "alice", first.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete 2 idempotent: got %v", err)
	}
	row, _ := s.Get(context.Background(), "acme", "alice")
	if !row.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt overwritten: got %v, want %v", row.DeletedAt, first)
	}
}

func TestIsActive(t *testing.T) {
	if !(userstore.User{}).IsActive() {
		t.Error("blank user should be active")
	}
	if (userstore.User{Disabled: true}).IsActive() {
		t.Error("disabled user should not be active")
	}
	if (userstore.User{DeletedAt: time.Now()}).IsActive() {
		t.Error("deleted user should not be active")
	}
}

func TestValidateSubject(t *testing.T) {
	for _, s := range []string{"a", "alice@acme.com", "u1", "service-account/x"} {
		if err := userstore.ValidateSubject(s); err != nil {
			t.Errorf("ValidateSubject(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", " ", "with space"} {
		if err := userstore.ValidateSubject(s); err == nil {
			t.Errorf("ValidateSubject(%q) should fail", s)
		}
	}
}

// spec: §12.1 line 5 — DeleteByUser is mandatory on Store and the
// user is the primary entity of this store, so DeleteByUser purges
// the row hard.
func TestDeleteByUserHardPurges_spec_12_1(t *testing.T) {
	s := userstore.NewMemory()
	u := userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []auth.Role{auth.RoleUser}}
	_ = s.Create(context.Background(), u)
	n, err := s.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteByUser should report 1, got %d", n)
	}
	if _, err := s.Get(context.Background(), "acme", "alice@acme.com"); !errors.Is(err, userstore.ErrNotFound) {
		t.Errorf("user should be gone: %v", err)
	}
}

// spec: §12.1 line 5 — DeleteByUser is idempotent: a second call on
// the same scope returns 0 and nil per §12.8 erasure semantics.
func TestDeleteByUserIdempotent_spec_12_1(t *testing.T) {
	s := userstore.NewMemory()
	n, err := s.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil || n != 0 {
		t.Errorf("DeleteByUser on missing row: n=%d err=%v", n, err)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant hard-deletes
// every user row belonging to the tenant; other tenants are unaffected.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	s := userstore.NewMemory()
	_ = s.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []auth.Role{auth.RoleUser}})
	_ = s.Create(context.Background(), userstore.User{Subject: "bob@acme.com", TenantID: "acme", Roles: []auth.Role{auth.RoleUser}})
	_ = s.Create(context.Background(), userstore.User{Subject: "carol@globex.com", TenantID: "globex", Roles: []auth.Role{auth.RoleUser}})
	n, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme rows, got %d", n)
	}
	if _, err := s.Get(context.Background(), "acme", "alice@acme.com"); !errors.Is(err, userstore.ErrNotFound) {
		t.Errorf("alice should be gone")
	}
	if _, err := s.Get(context.Background(), "globex", "carol@globex.com"); err != nil {
		t.Errorf("carol@globex should survive: %v", err)
	}
}

// spec: §15.1 lines 826-828 — the platform-managed role assignment carries
// presence (RoleAssigned) plus operator/timestamp provenance. The Memory
// store must round-trip all three through Create/Update/Get so the role
// resolver (§10.2 line 294) and the tenant-users list see consistent
// values. F-15.1.3.
func TestRoleAssignmentRoundTrip_spec_15_1_826(t *testing.T) {
	s := userstore.NewMemory()
	ctx := context.Background()
	at := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, userstore.User{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles:          []auth.Role{auth.RoleTenantViewer},
		RoleAssigned:   true,
		RoleAssignedBy: "admin@acme.com",
		RoleAssignedAt: at,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.RoleAssigned || got.RoleAssignedBy != "admin@acme.com" || !got.RoleAssignedAt.Equal(at) {
		t.Fatalf("create round-trip: assigned=%v by=%q at=%v", got.RoleAssigned, got.RoleAssignedBy, got.RoleAssignedAt)
	}

	// Removing the assignment clears presence and provenance while the row
	// is retained — the §15.1 line 828 DELETE semantics.
	updated, err := s.Update(ctx, "acme", "alice@acme.com", func(u *userstore.User) error {
		u.Roles = nil
		u.RoleAssigned = false
		u.RoleAssignedBy = ""
		u.RoleAssignedAt = time.Time{}
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.RoleAssigned || updated.RoleAssignedBy != "" || !updated.RoleAssignedAt.IsZero() {
		t.Fatalf("removal: assigned=%v by=%q at=%v", updated.RoleAssigned, updated.RoleAssignedBy, updated.RoleAssignedAt)
	}
	// The row must still be readable after assignment removal.
	if _, err := s.Get(ctx, "acme", "alice@acme.com"); err != nil {
		t.Fatalf("row must survive assignment removal: %v", err)
	}
}
