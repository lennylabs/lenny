// SPDX-License-Identifier: MIT

package userstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
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
