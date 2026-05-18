//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4 runtime/pool tenant-access registry,
// exercising the Postgres-backed pkg/gateway/tenantaccessstore/pgstore
// against a real container. Covers the grant/list round-trip, the
// idempotent re-grant, revoke, ListForTenant, the kind/resource
// scoping of the runtime_tenant_access and pool_tenant_access join
// tables, and the argument validation.
package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	tenantaccesspg "github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// tenantaccessRuntime seeds a runtime_definitions row and returns its
// name. The join table's runtime_name column is a foreign key into
// runtime_definitions, so a grant needs a parent runtime to reference.
// runtime_definitions is platform-global (no lenny_tenant_guard), so a
// plain INSERT outside a tenant context is sufficient.
func tenantaccessRuntime(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	name := "rt-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO runtime_definitions
		   (name, type, image, execution_mode, isolation_profile, integration_level)
		 VALUES ($1, 'agent', 'ghcr.io/acme/echo', 'session', 'standard', 'basic')`,
		name); err != nil {
		t.Fatalf("seed runtime %q: %v", name, err)
	}
	return name
}

// tenantaccessPool seeds a sandbox_warm_pools row and returns its name.
// The join table's pool_name column is a foreign key into
// sandbox_warm_pools, which is platform-global like runtime_definitions.
func tenantaccessPool(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	name := "pool-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO sandbox_warm_pools (name) VALUES ($1)`, name); err != nil {
		t.Fatalf("seed pool %q: %v", name, err)
	}
	return name
}

// tenantaccessTenantIDs collects the tenant ids from a grant slice.
func tenantaccessTenantIDs(grants []tenantaccessstore.Grant) []string {
	out := make([]string, len(grants))
	for i, g := range grants {
		out[i] = g.TenantID
	}
	return out
}

// spec: §4 runtime/pool tenant-access join tables.
// diagnosis: the Postgres-backed tenant-access registry in
// pkg/gateway/tenantaccessstore/pgstore did not behave as specified.
// Grant and List must round-trip a grant, a re-grant must be idempotent
// (created=false, no duplicate row), Revoke must remove a grant and
// return ErrNotFound for a missing one, List must order tenant-id
// ascending and ListForTenant must order resource-name ascending,
// grants must be scoped independently by kind and resource, and empty
// or unrecognised arguments must return ErrInvalidArg.
func TestTenantAccessStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := tenantaccesspg.New(pg.Pool)
	ctx := context.Background()

	t.Run("grant and list round-trip", func(t *testing.T) {
		runtime := tenantaccessRuntime(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		at := time.Date(2026, 5, 16, 9, 30, 0, 0, time.UTC)

		created, err := store.Grant(ctx, tenantaccessstore.KindRuntime, runtime, tenant, "admin@platform", at)
		if err != nil {
			t.Fatalf("Grant: %v", err)
		}
		if !created {
			t.Error("first Grant must report created=true")
		}
		grants, err := store.List(ctx, tenantaccessstore.KindRuntime, runtime)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(grants) != 1 {
			t.Fatalf("List: got %d grants, want 1", len(grants))
		}
		if grants[0].TenantID != tenant || grants[0].GrantedBy != "admin@platform" {
			t.Errorf("grant = %+v, want tenant=%s / admin@platform", grants[0], tenant)
		}
		if !grants[0].GrantedAt.Equal(at) {
			t.Errorf("GrantedAt = %v, want %v", grants[0].GrantedAt, at)
		}
	})

	t.Run("grant defaults a zero timestamp", func(t *testing.T) {
		pool := tenantaccessPool(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		before := time.Now().UTC()
		if _, err := store.Grant(ctx, tenantaccessstore.KindPool, pool, tenant, "admin", time.Time{}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		grants, err := store.List(ctx, tenantaccessstore.KindPool, pool)
		if err != nil || len(grants) != 1 {
			t.Fatalf("List: got %d grants (%v), want 1", len(grants), err)
		}
		if grants[0].GrantedAt.Before(before) {
			t.Errorf("GrantedAt = %v, want a defaulted instant at or after %v", grants[0].GrantedAt, before)
		}
	})

	t.Run("re-granting an existing grant is idempotent", func(t *testing.T) {
		pool := tenantaccessPool(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Grant(ctx, tenantaccessstore.KindPool, pool, tenant, "admin", time.Time{}); err != nil {
			t.Fatalf("first Grant: %v", err)
		}
		created, err := store.Grant(ctx, tenantaccessstore.KindPool, pool, tenant, "other-admin", time.Time{})
		if err != nil {
			t.Fatalf("second Grant: %v", err)
		}
		if created {
			t.Error("re-granting an existing grant must report created=false")
		}
		grants, _ := store.List(ctx, tenantaccessstore.KindPool, pool)
		if len(grants) != 1 {
			t.Errorf("a re-grant must not duplicate the entry: got %d", len(grants))
		}
		// The idempotent re-grant must leave the original row untouched.
		if len(grants) == 1 && grants[0].GrantedBy != "admin" {
			t.Errorf("re-grant overwrote GrantedBy: got %q, want admin", grants[0].GrantedBy)
		}
	})

	t.Run("grants are scoped independently by kind", func(t *testing.T) {
		// A runtime and a pool created with the same name must not share
		// grants: each kind has its own join table.
		shared := "shared-" + newUUID(t)[:8]
		runtimeTenant := freshTenant(t, ctx, pg)
		poolTenant := freshTenant(t, ctx, pg)
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO runtime_definitions
			   (name, type, image, execution_mode, isolation_profile, integration_level)
			 VALUES ($1, 'agent', 'ghcr.io/acme/echo', 'session', 'standard', 'basic')`,
			shared); err != nil {
			t.Fatalf("seed runtime %q: %v", shared, err)
		}
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO sandbox_warm_pools (name) VALUES ($1)`, shared); err != nil {
			t.Fatalf("seed pool %q: %v", shared, err)
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, shared, runtimeTenant, "a", time.Time{}); err != nil {
			t.Fatalf("Grant runtime: %v", err)
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindPool, shared, poolTenant, "a", time.Time{}); err != nil {
			t.Fatalf("Grant pool: %v", err)
		}
		rt, _ := store.List(ctx, tenantaccessstore.KindRuntime, shared)
		if len(rt) != 1 || rt[0].TenantID != runtimeTenant {
			t.Errorf("runtime grants = %v, want only %s", tenantaccessTenantIDs(rt), runtimeTenant)
		}
		pool, _ := store.List(ctx, tenantaccessstore.KindPool, shared)
		if len(pool) != 1 || pool[0].TenantID != poolTenant {
			t.Errorf("pool grants = %v, want only %s", tenantaccessTenantIDs(pool), poolTenant)
		}
	})

	t.Run("list orders tenant-id ascending", func(t *testing.T) {
		runtime := tenantaccessRuntime(t, ctx, pg)
		// Force a known sort order with explicit ids; freshTenant ids are
		// random, so seed tenants directly.
		ids := []string{
			"ta-zzz-" + newUUID(t)[:6],
			"ta-aaa-" + newUUID(t)[:6],
			"ta-mmm-" + newUUID(t)[:6],
		}
		for _, id := range ids {
			if _, err := pg.Pool.Exec(ctx,
				`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
				t.Fatalf("seed tenant %q: %v", id, err)
			}
			if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, runtime, id, "a", time.Time{}); err != nil {
				t.Fatalf("Grant %s: %v", id, err)
			}
		}
		grants, err := store.List(ctx, tenantaccessstore.KindRuntime, runtime)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		got := tenantaccessTenantIDs(grants)
		if len(got) != 3 || !(got[0] < got[1] && got[1] < got[2]) {
			t.Errorf("List order = %v, want tenant-id ascending", got)
		}
	})

	t.Run("revoke removes a grant and rejects a missing one", func(t *testing.T) {
		runtime := tenantaccessRuntime(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, runtime, tenant, "a", time.Time{}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, runtime, tenant); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		grants, _ := store.List(ctx, tenantaccessstore.KindRuntime, runtime)
		if len(grants) != 0 {
			t.Errorf("after Revoke: got %d grants, want 0", len(grants))
		}
		// Revoking the now-missing grant returns ErrNotFound.
		if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, runtime, tenant); !errors.Is(err, tenantaccessstore.ErrNotFound) {
			t.Errorf("Revoke missing grant: got %v, want ErrNotFound", err)
		}
		// A grant for a never-seen tenant is also ErrNotFound.
		if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, runtime, "ghost-tenant"); !errors.Is(err, tenantaccessstore.ErrNotFound) {
			t.Errorf("Revoke unknown tenant: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list for tenant is the kind-scoped inverse of list", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		// tenant is granted two runtimes and one pool; other one runtime.
		rtA, rtB := tenantaccessRuntime(t, ctx, pg), tenantaccessRuntime(t, ctx, pg)
		pool := tenantaccessPool(t, ctx, pg)
		for _, rt := range []string{rtA, rtB} {
			if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, rt, tenant, "a", time.Time{}); err != nil {
				t.Fatalf("Grant runtime %s: %v", rt, err)
			}
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindPool, pool, tenant, "a", time.Time{}); err != nil {
			t.Fatalf("Grant pool: %v", err)
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, rtA, other, "a", time.Time{}); err != nil {
			t.Fatalf("Grant runtime for other tenant: %v", err)
		}

		rts, err := store.ListForTenant(ctx, tenantaccessstore.KindRuntime, tenant)
		if err != nil {
			t.Fatalf("ListForTenant runtimes: %v", err)
		}
		wantRuntimes := []string{rtA, rtB}
		if rtB < rtA {
			wantRuntimes = []string{rtB, rtA}
		}
		if len(rts) != 2 || rts[0] != wantRuntimes[0] || rts[1] != wantRuntimes[1] {
			t.Errorf("tenant runtimes = %v, want %v (resource-name ascending)", rts, wantRuntimes)
		}
		// The pool grant must not surface under the runtime kind.
		for _, name := range rts {
			if name == pool {
				t.Errorf("ListForTenant runtime kind leaked pool %q", pool)
			}
		}
		pools, err := store.ListForTenant(ctx, tenantaccessstore.KindPool, tenant)
		if err != nil {
			t.Fatalf("ListForTenant pools: %v", err)
		}
		if len(pools) != 1 || pools[0] != pool {
			t.Errorf("tenant pools = %v, want [%s]", pools, pool)
		}
		// other was granted only rtA.
		otherRuntimes, _ := store.ListForTenant(ctx, tenantaccessstore.KindRuntime, other)
		if len(otherRuntimes) != 1 || otherRuntimes[0] != rtA {
			t.Errorf("other tenant runtimes = %v, want [%s]", otherRuntimes, rtA)
		}
	})

	t.Run("list and list-for-tenant return empty for unknown keys", func(t *testing.T) {
		grants, err := store.List(ctx, tenantaccessstore.KindPool, "pool-never-created")
		if err != nil {
			t.Fatalf("List unknown resource: %v", err)
		}
		if len(grants) != 0 {
			t.Errorf("unknown resource: got %d grants, want 0", len(grants))
		}
		out, err := store.ListForTenant(ctx, tenantaccessstore.KindRuntime, freshTenant(t, ctx, pg))
		if err != nil {
			t.Fatalf("ListForTenant no grants: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("a tenant with no grants: got %v, want empty", out)
		}
	})

	t.Run("empty and unrecognised arguments are rejected", func(t *testing.T) {
		runtime := tenantaccessRuntime(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Grant(ctx, "bogus", runtime, tenant, "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("Grant with a bad kind: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "", tenant, "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("Grant with an empty resource: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, runtime, "", "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("Grant with an empty tenant: got %v, want ErrInvalidArg", err)
		}
		if err := store.Revoke(ctx, "bogus", runtime, tenant); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("Revoke with a bad kind: got %v, want ErrInvalidArg", err)
		}
		if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, "", tenant); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("Revoke with an empty resource: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.List(ctx, "bogus", runtime); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("List with a bad kind: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.List(ctx, tenantaccessstore.KindRuntime, ""); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("List with an empty resource: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.ListForTenant(ctx, "bogus", tenant); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("ListForTenant with a bad kind: got %v, want ErrInvalidArg", err)
		}
		if _, err := store.ListForTenant(ctx, tenantaccessstore.KindRuntime, ""); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
			t.Errorf("ListForTenant with an empty tenant: got %v, want ErrInvalidArg", err)
		}
	})

	// A defensive check that the join tables really are distinct
	// physical tables; a single mis-routed table would let a runtime
	// name collide with a pool name across kinds.
	t.Run("a granted runtime name is not visible as a pool", func(t *testing.T) {
		runtime := tenantaccessRuntime(t, ctx, pg)
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, runtime, tenant, "a", time.Time{}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		pools, err := store.List(ctx, tenantaccessstore.KindPool, runtime)
		if err != nil {
			t.Fatalf("List pool: %v", err)
		}
		if len(pools) != 0 {
			t.Errorf("runtime grant surfaced under the pool kind: %v", tenantaccessTenantIDs(pools))
		}
		if !strings.HasPrefix(runtime, "rt-") {
			t.Fatalf("unexpected runtime name %q", runtime)
		}
	})
}
