// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-tenant isolation of §12.8 tenant deletion. Deleting one
// tenant must never reach another tenant's rows, objects, keys, leases,
// TokenStore rows, or bearers. The first test runs a tenant deletion through
// Phase 4 over the production Postgres stores with a twin tenant sharing the
// blob store, the manifests, the lease pool, and the TokenStore, and asserts
// the twin is unchanged and still registered while the deleted tenant's
// registry entry refuses its bearers. The second runs the two Redis-resident
// Phase 4 primitives, the experiment sticky-cache sweep and the billing
// stream purge, against a real Redis holding a twin tenant and a tenant whose
// ID extends the deleted tenant's ID.
package tier9_security_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecyclefence"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 10.2 (Authentication)
// diagnosis: a twin change means a Phase 3 step or a Phase 4 entry is not
// tenant-scoped and deleted, returned, or rewrote another tenant's data; the
// deleted tenant still registered means its bearers are admitted after
// Phase 3; the twin unregistered means the registry refuses a live tenant.
func TestTenantDeletionNeverTouchesAnotherTenant_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	ctx := context.Background()
	s, err := lifecyclefence.PostgresStores(ctx, pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	w := lifecyclefence.NewWorld(t, s, 2)
	tw := w.NewTwin(t, 2)
	d := w.NewDeletion()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Complete(ctx, 12); err != nil {
		t.Fatal(err)
	}
	registry := tenantpg.New(pg.Pool)
	for tenant, want := range map[string]bool{w.Tenant: false, tw.Tenant: true} {
		got, err := registry.IsRegistered(tenant)
		if err != nil || got != want {
			t.Errorf("IsRegistered(%s) = %v, %v; want %v", tenant, got, err, want)
		}
	}
	w.Check(t)
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4), 11.2.1 (billing write-ahead stream)
// diagnosis: a globex or acme2 key gone after deleting acme means the sticky
// sweep's glob or the stream purge's key is not bounded by the tenant
// prefix's delimiter, so deleting one tenant erases another's sticky
// assignments or staged billing events; an acme key left means the sweep or
// the purge missed the deleted tenant.
func TestTenantRedisPurgesStayInsideTheTenantPrefix_spec_12_8(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	tenants := []string{"acme", "acme2", "globex"}

	cache := experimentsticky.NewRedis(rd.Client)
	tier, err := redisstream.New(redisstream.Options{Client: rd.Client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range tenants {
		for i := 0; i < 3; i++ {
			if err := cache.Put(ctx, tenant, "exp-1", fmt.Sprintf("user-%d", i), "treatment"); err != nil {
				t.Fatalf("Put(%s): %v", tenant, err)
			}
			e := billingstore.Event{TenantID: tenant, UserID: "alice", SequenceNumber: uint64(i + 1), EventType: billingstore.EventSessionCreated}
			if err := tier.Publish(ctx, e); err != nil {
				t.Fatalf("Publish(%s): %v", tenant, err)
			}
		}
	}

	if n, err := cache.DeleteByTenant(ctx, "acme"); err != nil || n != 3 {
		t.Fatalf("sticky DeleteByTenant(acme) = (%d, %v), want (3, nil)", n, err)
	}
	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 3 {
		t.Fatalf("PurgeTenant(acme) = (%d, %v), want (3, nil)", n, err)
	}
	for _, tenant := range tenants {
		want := 3
		if tenant == "acme" {
			want = 0
		}
		keys, err := rd.Client.Keys(ctx, rediskeys.TenantPrefix(tenant)+"exp:*:sticky:*").Result()
		if err != nil || len(keys) != want {
			t.Errorf("%s sticky keys = %v (%v), want %d", tenant, keys, err, want)
		}
		l, err := rd.Client.XLen(ctx, rediskeys.TenantPrefix(tenant)+"billing:stream").Result()
		if err != nil && want != 0 || int(l) != want {
			t.Errorf("%s billing stream length = %d (%v), want %d", tenant, l, err, want)
		}
	}
}
