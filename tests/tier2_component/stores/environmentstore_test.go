//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.6 Environment registry, exercising the
// Postgres-backed pkg/gateway/environmentstore/pgstore against a real
// container with the production migrations applied. Covers CRUD, the
// jsonb-encoded nested Environment body, the sentinel errors,
// cross-tenant isolation, the SELECT ... FOR UPDATE mutate path,
// strict-monotonic UpdatedAt, the §10.6 admission validation, the
// name-ascending List ordering, and Delete semantics.
package stores_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore/pgstore"
)

// sampleEnvironment returns a fully-populated §10.6 Environment whose
// nested body (members, selectors, mcp filters, and the bilateral
// cross-environment declarations) exercises the jsonb round-trip.
func sampleEnvironment(tenant, name string) environmentstore.Environment {
	return environmentstore.Environment{
		Name:        name,
		TenantID:    tenant,
		Description: "security engineering workspace",
		Members: []environmentstore.Member{
			{
				Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-engineers"},
				Role:     environment.RoleAdmin,
			},
			{
				Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-readers"},
				Role:     environment.RoleViewer,
			},
		},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
		// spec: §10.6 lines 595-599 — the connectorSelector carries the
		// tag selector plus the capability allow/deny lists. F-10.6.3.
		ConnectorSelector: environmentstore.ConnectorSelector{
			Selector:            environment.Selector{MatchLabels: map[string]string{"tier": "internal"}},
			AllowedCapabilities: []string{"read", "search", "network"},
			DeniedCapabilities:  []string{"write", "delete", "execute", "admin"},
		},
		MCPRuntimeFilters: []environmentstore.MCPRuntimeFilter{
			{
				RuntimeSelector:     environment.Selector{MatchLabels: map[string]string{"type": "mcp"}},
				AllowedCapabilities: []string{"fs.read", "fs.write"},
				DeniedCapabilities:  []string{"net.raw"},
			},
		},
		DefaultDelegationPolicy: "security-policy",
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{
				Environment: "platform-team",
				Runtimes:    environment.Selector{MatchLabels: map[string]string{"shared": "true"}},
			},
		},
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{
				Environment: "*",
				Runtimes:    environment.Selector{MatchLabels: map[string]string{"team": "security"}},
			},
		},
	}
}

// spec: 10.6
// diagnosis: the Postgres-backed Environment registry in
// pkg/gateway/environmentstore/pgstore did not behave as specified.
// Create and Get must round-trip an environment including its
// jsonb-encoded nested body, the sentinel errors and cross-tenant
// isolation must hold, the SELECT ... FOR UPDATE mutate path must
// re-run the §10.6 admission validation and strictly advance
// UpdatedAt, List must order name-ascending and stay tenant-scoped,
// and Delete must report ErrNotFound for an absent row.
func TestEnvironmentStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := environmentpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip including the nested jsonb body", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := sampleEnvironment(tenant, "security-team")
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name != want.Name || got.TenantID != want.TenantID ||
			got.Description != want.Description ||
			got.DefaultDelegationPolicy != want.DefaultDelegationPolicy {
			t.Errorf("scalar field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create must stamp CreatedAt and UpdatedAt")
		}
		if !reflect.DeepEqual(got.Members, want.Members) {
			t.Errorf("Members lost in jsonb round-trip:\n got %+v\nwant %+v", got.Members, want.Members)
		}
		if !reflect.DeepEqual(got.RuntimeSelector, want.RuntimeSelector) ||
			!reflect.DeepEqual(got.ConnectorSelector, want.ConnectorSelector) {
			t.Errorf("selectors lost in jsonb round-trip:\n got %+v\nwant %+v", got, want)
		}
		if !reflect.DeepEqual(got.MCPRuntimeFilters, want.MCPRuntimeFilters) {
			t.Errorf("MCPRuntimeFilters lost in jsonb round-trip:\n got %+v\nwant %+v",
				got.MCPRuntimeFilters, want.MCPRuntimeFilters)
		}
		if !reflect.DeepEqual(got.CrossEnvOutbound, want.CrossEnvOutbound) ||
			!reflect.DeepEqual(got.CrossEnvInbound, want.CrossEnvInbound) {
			t.Errorf("cross-environment declarations lost in jsonb round-trip:\n got %+v\nwant %+v",
				got, want)
		}
	})

	t.Run("duplicate and invalid environments are rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		e := sampleEnvironment(tenant, "dup")
		if err := store.Create(ctx, e); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, e); !errors.Is(err, environmentstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		noName := sampleEnvironment(tenant, "")
		if err := store.Create(ctx, noName); err == nil {
			t.Error("Create with no name should be rejected")
		}
		badRole := sampleEnvironment(tenant, "bad-role")
		badRole.Members[0].Role = "superuser" // not a §10.6 role
		if err := store.Create(ctx, badRole); err == nil {
			t.Error("Create with an invalid member role should be rejected")
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "absent"); !errors.Is(err, environmentstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Create(ctx, sampleEnvironment(owner, "shared-name")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, "shared-name"); !errors.Is(err, environmentstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, re-validates, and advances updated_at", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		name := "env-u"
		if err := store.Create(ctx, sampleEnvironment(tenant, name)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, name)
		updated, err := store.Update(ctx, tenant, name, func(e *environmentstore.Environment) error {
			e.Description = "renamed"
			e.DefaultDelegationPolicy = "tighter-policy"
			e.RuntimeSelector = environment.Selector{MatchLabels: map[string]string{"team": "platform"}}
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Description != "renamed" || updated.DefaultDelegationPolicy != "tighter-policy" {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		persisted, _ := store.Get(ctx, tenant, name)
		if persisted.Description != "renamed" ||
			!reflect.DeepEqual(persisted.RuntimeSelector,
				environment.Selector{MatchLabels: map[string]string{"team": "platform"}}) {
			t.Errorf("mutation not persisted: %+v", persisted)
		}

		// A mutation to an invalid role is rejected and leaves the row intact.
		if _, err := store.Update(ctx, tenant, name, func(e *environmentstore.Environment) error {
			e.Members[0].Role = "superuser"
			return nil
		}); err == nil {
			t.Error("Update to an invalid role should be rejected")
		}
		afterReject, _ := store.Get(ctx, tenant, name)
		if afterReject.Members[0].Role != environment.RoleAdmin {
			t.Errorf("a rejected Update mutated stored state: role = %q", afterReject.Members[0].Role)
		}

		// A mutate error aborts the write.
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, tenant, name, func(*environmentstore.Environment) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}

		if _, err := store.Update(ctx, tenant, "ghost", func(*environmentstore.Environment) error {
			return nil
		}); !errors.Is(err, environmentstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list is tenant-scoped and name-ascending", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		for _, name := range []string{"env-b", "env-a", "env-c"} {
			if err := store.Create(ctx, sampleEnvironment(tenant, name)); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		if err := store.Create(ctx, sampleEnvironment(other, "env-z")); err != nil {
			t.Fatalf("Create env-z: %v", err)
		}
		got, err := store.List(ctx, tenant)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 || got[0].Name != "env-a" || got[1].Name != "env-b" || got[2].Name != "env-c" {
			t.Errorf("List = %v, want [env-a env-b env-c] name-ascending, tenant-scoped", environmentNamesOf(got))
		}
	})

	t.Run("delete removes the row and reports ErrNotFound when absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		name := "env-d"
		if err := store.Create(ctx, sampleEnvironment(tenant, name)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, tenant, name); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, name); !errors.Is(err, environmentstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, name); !errors.Is(err, environmentstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})
}

func environmentNamesOf(envs []environmentstore.Environment) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.Name
	}
	return out
}
