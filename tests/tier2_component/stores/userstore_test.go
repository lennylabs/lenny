//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.2 user registry, exercising the
// Postgres-backed pkg/gateway/userstore/pgstore against a real
// container. Covers CRUD, tenant/subject/role validation,
// cross-tenant isolation, the soft-delete lifecycle, and the list
// filters.
package stores_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/userstore/pgstore"
)

// spec: 10.2
// diagnosis: the Postgres-backed user registry in
// pkg/gateway/userstore/pgstore did not behave as specified. Create
// and Get must round-trip a user, validation must reject duplicates,
// malformed subjects, and unrecognised roles, cross-tenant Get must
// return ErrNotFound, Update must re-validate roles, and the
// soft-delete lifecycle must honor the list filters.
func TestUserStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := userpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := userstore.User{
			Subject:     "auth0|alice",
			TenantID:    tenant,
			Email:       "alice@acme.com",
			DisplayName: "Alice",
			Roles:       []auth.Role{auth.RoleTenantAdmin, auth.RoleUser},
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.Subject)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Email != want.Email || got.DisplayName != want.DisplayName {
			t.Errorf("field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !slices.Equal(got.Roles, want.Roles) {
			t.Errorf("Roles mismatch: got %v want %v", got.Roles, want.Roles)
		}
		if !got.IsActive() {
			t.Error("freshly created user reports inactive")
		}
	})

	t.Run("duplicate, invalid subject, and invalid role are rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		u := userstore.User{Subject: "auth0|bob", TenantID: tenant}
		if err := store.Create(ctx, u); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, u); !errors.Is(err, userstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		if err := store.Create(ctx, userstore.User{
			Subject: "has spaces", TenantID: tenant,
		}); err == nil {
			t.Error("Create with an invalid subject should be rejected")
		}
		if err := store.Create(ctx, userstore.User{
			Subject: "auth0|carol", TenantID: tenant, Roles: []auth.Role{"superuser"},
		}); err == nil {
			t.Error("Create with an unrecognised role should be rejected")
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "auth0|nobody"); !errors.Is(err, userstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Create(ctx, userstore.User{Subject: "auth0|dave", TenantID: owner}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, "auth0|dave"); !errors.Is(err, userstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates roles", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		subject := "auth0|erin"
		if err := store.Create(ctx, userstore.User{
			Subject: subject, TenantID: tenant, DisplayName: "Before",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, subject)
		updated, err := store.Update(ctx, tenant, subject, func(u *userstore.User) error {
			u.DisplayName = "After"
			u.Roles = []auth.Role{auth.RoleBillingViewer}
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.DisplayName != "After" || !slices.Equal(updated.Roles, []auth.Role{auth.RoleBillingViewer}) {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		if _, err := store.Update(ctx, tenant, subject, func(u *userstore.User) error {
			u.Roles = []auth.Role{"not-a-role"}
			return nil
		}); err == nil {
			t.Error("Update to an unrecognised role should be rejected")
		}
		if _, err := store.Update(ctx, tenant, "auth0|ghost", func(*userstore.User) error {
			return nil
		}); !errors.Is(err, userstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list applies filters and ordering, soft-delete is idempotent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		mk := func(subject string, disabled bool) {
			t.Helper()
			if err := store.Create(ctx, userstore.User{
				Subject: subject, TenantID: tenant, Disabled: disabled,
			}); err != nil {
				t.Fatalf("Create %s: %v", subject, err)
			}
		}
		mk("auth0|aaa", false)
		mk("auth0|bbb", false)
		mk("auth0|ccc", true) // disabled

		active, err := store.List(ctx, tenant, userstore.ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(active) != 2 || active[0].Subject != "auth0|aaa" || active[1].Subject != "auth0|bbb" {
			t.Fatalf("List default: got %d users %v, want [aaa bbb]", len(active), subjectsOf(active))
		}
		withDisabled, _ := store.List(ctx, tenant, userstore.ListFilter{IncludeDisabled: true})
		if len(withDisabled) != 3 {
			t.Errorf("List IncludeDisabled: got %d, want 3", len(withDisabled))
		}

		at := time.Now().UTC()
		if err := store.SoftDelete(ctx, tenant, "auth0|aaa", at); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, "auth0|aaa", at.Add(time.Minute)); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, "auth0|ghost", at); !errors.Is(err, userstore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		deleted, err := store.Get(ctx, tenant, "auth0|aaa")
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
		remaining, _ := store.List(ctx, tenant, userstore.ListFilter{})
		if len(remaining) != 1 || remaining[0].Subject != "auth0|bbb" {
			t.Errorf("List after soft-delete: got %v, want [bbb]", subjectsOf(remaining))
		}
		all, _ := store.List(ctx, tenant, userstore.ListFilter{IncludeDeleted: true, IncludeDisabled: true})
		if len(all) != 3 {
			t.Errorf("List IncludeDeleted+IncludeDisabled: got %d, want 3", len(all))
		}
	})
}

func subjectsOf(users []userstore.User) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.Subject
	}
	return out
}
