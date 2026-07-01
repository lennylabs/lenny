//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.2 custom-role registry, exercising the
// Postgres-backed pkg/gateway/customrolestore/pgstore against a real
// container with the production migrations applied. Covers CRUD, the
// sentinel errors, the §10.2 structural validation, cross-tenant
// isolation, the SELECT ... FOR UPDATE mutate path with
// strict-monotonic UpdatedAt, the name-ordered List, and Delete.
package stores_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore/pgstore"
)

// spec: 10.2
// diagnosis: the Postgres-backed custom-role registry in
// pkg/gateway/customrolestore/pgstore did not behave as specified.
// Create and Get must round-trip a custom role including its
// permission set, validation must reject duplicates, malformed and
// built-in-colliding names, and permissions that exceed the
// tenant-admin set, cross-tenant Get must return ErrNotFound, Update
// must re-validate and strictly advance UpdatedAt, List must order by
// name within the tenant, and Delete must remove the row.
func TestCustomRoleStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := customrolepg.New(pg.Pool)
	ctx := context.Background()

	sampleRole := func(tenant, name string) customrolestore.CustomRole {
		return customrolestore.CustomRole{
			TenantID: tenant,
			Name:     name,
			Permissions: []auth.Permission{
				auth.PermManageOwnSessions, auth.PermViewUsage,
			},
		}
	}

	t.Run("create and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := sampleRole(tenant, "session-manager")
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.TenantID != tenant || got.Name != want.Name {
			t.Errorf("key mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !slices.Equal(got.Permissions, want.Permissions) {
			t.Errorf("Permissions mismatch: got %v want %v", got.Permissions, want.Permissions)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create must stamp CreatedAt and UpdatedAt")
		}
	})

	t.Run("duplicate and invalid roles are rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, sampleRole(tenant, "r1")); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, sampleRole(tenant, "r1")); !errors.Is(err, customrolestore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		// A malformed name is rejected before reaching Postgres.
		for _, name := range []string{"", "Has Space", "UPPER"} {
			if err := store.Create(ctx, sampleRole(tenant, name)); err == nil {
				t.Errorf("Create with name %q: expected a validation error", name)
			}
		}
		// §10.2: a custom role name must not collide with a built-in role.
		if err := store.Create(ctx, sampleRole(tenant, "tenant-admin")); err == nil {
			t.Error("a custom role named after a built-in role must be rejected")
		}
		// §10.2: a custom role may not exceed the tenant-admin set.
		over := sampleRole(tenant, "overreach")
		over.Permissions = append(over.Permissions, auth.PermManagePlatformSettings)
		if err := store.Create(ctx, over); err == nil {
			t.Error("a permission outside the tenant-admin set must be rejected")
		}
		// An unrecognised permission must be rejected.
		bogus := sampleRole(tenant, "bogus")
		bogus.Permissions = []auth.Permission{"manage_everything"}
		if err := store.Create(ctx, bogus); err == nil {
			t.Error("an unrecognised permission must be rejected")
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "nobody"); !errors.Is(err, customrolestore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Create(ctx, sampleRole(owner, "shared-name")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, "shared-name"); !errors.Is(err, customrolestore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
		// The same name under another tenant is not a duplicate.
		if err := store.Create(ctx, sampleRole(intruder, "shared-name")); err != nil {
			t.Errorf("same name, different tenant: got %v, want nil", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, sampleRole(tenant, "r1")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, "r1")
		updated, err := store.Update(ctx, tenant, "r1", func(r *customrolestore.CustomRole) error {
			r.Permissions = []auth.Permission{auth.PermManageRuntimes}
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !slices.Equal(updated.Permissions, []auth.Permission{auth.PermManageRuntimes}) {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		persisted, _ := store.Get(ctx, tenant, "r1")
		if !slices.Equal(persisted.Permissions, []auth.Permission{auth.PermManageRuntimes}) {
			t.Errorf("Update not persisted: %+v", persisted)
		}
		// §10.2: an Update to a permission outside tenant-admin is rejected.
		if _, err := store.Update(ctx, tenant, "r1", func(r *customrolestore.CustomRole) error {
			r.Permissions = []auth.Permission{auth.PermAccessCrossTenantData}
			return nil
		}); err == nil {
			t.Error("Update to a permission outside tenant-admin must be rejected")
		}
		// A mutate error aborts the write.
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, tenant, "r1", func(*customrolestore.CustomRole) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}
		if _, err := store.Update(ctx, tenant, "ghost", func(*customrolestore.CustomRole) error {
			return nil
		}); !errors.Is(err, customrolestore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list is tenant-scoped and name-ordered", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		for _, name := range []string{"r-b", "r-a"} {
			if err := store.Create(ctx, sampleRole(tenant, name)); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		if err := store.Create(ctx, sampleRole(other, "r-c")); err != nil {
			t.Fatalf("Create r-c: %v", err)
		}
		got, err := store.List(ctx, tenant)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 || got[0].Name != "r-a" || got[1].Name != "r-b" {
			t.Errorf("List = %v, want [r-a r-b] in name order", namesOf(got))
		}
		otherRoles, _ := store.List(ctx, other)
		if len(otherRoles) != 1 || otherRoles[0].Name != "r-c" {
			t.Errorf("List other tenant = %v, want [r-c]", namesOf(otherRoles))
		}
	})

	t.Run("delete removes the row", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, sampleRole(tenant, "r1")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, tenant, "r1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, "r1"); !errors.Is(err, customrolestore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, "r1"); !errors.Is(err, customrolestore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})
}

func namesOf(roles []customrolestore.CustomRole) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = r.Name
	}
	return out
}
