// SPDX-License-Identifier: MIT

package customrolestore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
)

// spec: §10.2 custom roles.

func sampleRole(tenant, name string) customrolestore.CustomRole {
	return customrolestore.CustomRole{
		TenantID: tenant,
		Name:     name,
		Permissions: []auth.Permission{
			auth.PermManageOwnSessions, auth.PermViewUsage,
		},
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "session-manager")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "session-manager")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Permissions) != 2 {
		t.Errorf("got %d permissions, want 2", len(got.Permissions))
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Get(ctx, "globex", "r1"); !errors.Is(err, customrolestore.ErrNotFound) {
		t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := store.Create(ctx, sampleRole("acme", "r1")); !errors.Is(err, customrolestore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
	// The same name under another tenant is not a duplicate.
	if err := store.Create(ctx, sampleRole("globex", "r1")); err != nil {
		t.Errorf("same name, different tenant: got %v, want nil", err)
	}
}

func TestCreateRejectsPermissionExceedingTenantAdmin(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	r := sampleRole("acme", "overreach")
	// §10.2: a custom role may not exceed the tenant-admin permission set.
	r.Permissions = append(r.Permissions, auth.PermManagePlatformSettings)
	if err := store.Create(ctx, r); err == nil {
		t.Error("a permission outside the tenant-admin set must be rejected")
	}
}

func TestCreateRejectsUnknownPermission(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	r := sampleRole("acme", "bogus")
	r.Permissions = []auth.Permission{"manage_everything"}
	if err := store.Create(ctx, r); err == nil {
		t.Error("an unrecognised permission must be rejected")
	}
}

func TestCreateRejectsBuiltinRoleName(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	// §10.2: a custom role name must not collide with a built-in role.
	if err := store.Create(ctx, sampleRole("acme", "tenant-admin")); err == nil {
		t.Error("a custom role named after a built-in role must be rejected")
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	for _, name := range []string{"", "Has Space", "UPPER"} {
		if err := store.Create(ctx, sampleRole("acme", name)); err == nil {
			t.Errorf("Create with name %q: expected a validation error", name)
		}
	}
}

func TestUpdateMutatesRole(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := store.Update(ctx, "acme", "r1", func(r *customrolestore.CustomRole) error {
		r.Permissions = []auth.Permission{auth.PermManageRuntimes}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Permissions) != 1 || updated.Permissions[0] != auth.PermManageRuntimes {
		t.Errorf("updated permissions = %+v, want [manage_runtimes]", updated.Permissions)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Error("Update must advance UpdatedAt past CreatedAt")
	}
}

func TestUpdateRejectsExceedingPermission(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := store.Update(ctx, "acme", "r1", func(r *customrolestore.CustomRole) error {
		r.Permissions = []auth.Permission{auth.PermAccessCrossTenantData}
		return nil
	})
	if err == nil {
		t.Error("Update to a permission outside tenant-admin must be rejected")
	}
}

func TestUpdateMissing(t *testing.T) {
	store := customrolestore.NewMemory()
	_, err := store.Update(context.Background(), "acme", "ghost", func(*customrolestore.CustomRole) error {
		return nil
	})
	if !errors.Is(err, customrolestore.ErrNotFound) {
		t.Errorf("Update missing role: got %v, want ErrNotFound", err)
	}
}

func TestListIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, sampleRole("acme", "r-b")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, sampleRole("globex", "r-c")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	acme, _ := store.List(ctx, "acme")
	if len(acme) != 2 || acme[0].Name != "r-a" || acme[1].Name != "r-b" {
		t.Errorf("List acme = %+v, want [r-a r-b]", acme)
	}
	globex, _ := store.List(ctx, "globex")
	if len(globex) != 1 || globex[0].Name != "r-c" {
		t.Errorf("List globex = %+v, want [r-c]", globex)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, "acme", "r1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "acme", "r1"); !errors.Is(err, customrolestore.ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	store := customrolestore.NewMemory()
	if err := store.Delete(context.Background(), "acme", "ghost"); !errors.Is(err, customrolestore.ErrNotFound) {
		t.Errorf("Delete missing: got %v, want ErrNotFound", err)
	}
}

func TestStoredRoleIsolatedFromCallerMutation(t *testing.T) {
	ctx := context.Background()
	store := customrolestore.NewMemory()
	if err := store.Create(ctx, sampleRole("acme", "r1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := store.Get(ctx, "acme", "r1")
	got.Permissions[0] = auth.PermManageQuotas

	fresh, _ := store.Get(ctx, "acme", "r1")
	if fresh.Permissions[0] != auth.PermManageOwnSessions {
		t.Errorf("stored role mutated through a returned copy: %v", fresh.Permissions[0])
	}
}
