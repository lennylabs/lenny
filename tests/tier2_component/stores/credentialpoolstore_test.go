//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 CredentialPool registry, exercising the
// Postgres-backed pkg/gateway/credentialpoolstore/pgstore against a
// real container. Covers CRUD, the §4.9 structural validation,
// cross-tenant isolation, the credential set round-trip, the
// Update-mutate path, the List filters, and the soft-delete lifecycle.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
)

// spec: 4.9
// diagnosis: the Postgres-backed CredentialPool registry in
// pkg/gateway/credentialpoolstore/pgstore did not behave as specified.
// Create and Get must round-trip a pool including its credential set,
// validation must reject duplicates and malformed pools, cross-tenant
// Get must return ErrNotFound, Update must re-validate, and the
// soft-delete lifecycle must honor the list filters.
func TestCredentialPoolStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := credentialpoolpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := credentialpoolstore.CredentialPool{
			TenantID:              tenant,
			Name:                  "anthropic-pool",
			Provider:              "anthropic_direct",
			AssignmentStrategy:    "least-loaded",
			MaxConcurrentSessions: 8,
			Credentials: []credentialpoolstore.Credential{
				{ID: "cred-a", SecretRef: "k8s://acme/anthropic-a"},
				{ID: "cred-b", SecretRef: "k8s://acme/anthropic-b"},
			},
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Provider != want.Provider || got.AssignmentStrategy != want.AssignmentStrategy ||
			got.MaxConcurrentSessions != want.MaxConcurrentSessions {
			t.Errorf("scalar mismatch:\n got %+v\nwant %+v", got, want)
		}
		if len(got.Credentials) != 2 || got.Credentials[0].ID != "cred-a" {
			t.Errorf("credential set lost in round-trip: %+v", got.Credentials)
		}
		if !got.IsActive() {
			t.Error("freshly created pool reports inactive")
		}
	})

	t.Run("duplicate and malformed pools are rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		p := credentialpoolstore.CredentialPool{TenantID: tenant, Name: "dup-pool", Provider: "github"}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, p); !errors.Is(err, credentialpoolstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "Bad Name", Provider: "github",
		}); err == nil {
			t.Error("Create with an invalid name should be rejected")
		}
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "no-provider",
		}); err == nil {
			t.Error("Create with no provider should be rejected")
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "ghost"); !errors.Is(err, credentialpoolstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: owner, Name: "owned", Provider: "vertex_ai",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, "owned"); !errors.Is(err, credentialpoolstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		name := "edit-pool"
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: name, Provider: "github", MaxConcurrentSessions: 2,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, name)
		updated, err := store.Update(ctx, tenant, name, func(p *credentialpoolstore.CredentialPool) error {
			p.MaxConcurrentSessions = 16
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.MaxConcurrentSessions != 16 {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		if _, err := store.Update(ctx, tenant, name, func(p *credentialpoolstore.CredentialPool) error {
			p.MaxConcurrentSessions = -1
			return nil
		}); err == nil {
			t.Error("Update to an invalid pool should be rejected")
		}
		if _, err := store.Update(ctx, tenant, "ghost", func(*credentialpoolstore.CredentialPool) error {
			return nil
		}); !errors.Is(err, credentialpoolstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list applies filters, soft-delete is idempotent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for _, n := range []string{"pool-a", "pool-b"} {
			if err := store.Create(ctx, credentialpoolstore.CredentialPool{
				TenantID: tenant, Name: n, Provider: "github",
			}); err != nil {
				t.Fatalf("Create %s: %v", n, err)
			}
		}
		active, err := store.List(ctx, tenant, credentialpoolstore.ListFilter{})
		if err != nil || len(active) != 2 {
			t.Fatalf("List: got %d err=%v, want 2", len(active), err)
		}
		at := time.Now().UTC()
		if err := store.SoftDelete(ctx, tenant, "pool-a", at); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, "pool-a", at.Add(time.Minute)); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, "ghost", at); !errors.Is(err, credentialpoolstore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		remaining, _ := store.List(ctx, tenant, credentialpoolstore.ListFilter{})
		if len(remaining) != 1 || remaining[0].Name != "pool-b" {
			t.Errorf("List after soft-delete: got %+v, want [pool-b]", remaining)
		}
		all, _ := store.List(ctx, tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})
		if len(all) != 2 {
			t.Errorf("List IncludeDeleted: got %d, want 2", len(all))
		}
	})
}
