//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §5.1 line 49 Postgres-backed per-tenant runtime
// capability override store (pkg/gateway/runtimecapoverride/pgstore)
// against a real container with the production migrations applied. Covers
// the JSONB override round-trip, upsert, delete idempotency, list, and
// the §12.3 tenant-isolation RLS guard (an override written under one
// tenant is invisible to another). F-5.1.20.
package runtimecapoverride_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	capoverridepg "github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func ptr[T any](v T) *T { return &v }

func startStore(t *testing.T) (*capoverridepg.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return capoverridepg.New(pg.Pool), pg
}

func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	id := "t-" + hex.EncodeToString(b[:])
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

func TestPgStoreRoundTrip_spec_5_1_49(t *testing.T) {
	ctx := context.Background()
	store, pg := startStore(t)
	tenant := freshTenant(t, ctx, pg)

	// Absent override.
	if _, ok, err := store.Get(ctx, tenant, "claude-code"); err != nil || ok {
		t.Fatalf("Get absent: ok=%v err=%v", ok, err)
	}

	ov := runtimestore.CapabilityOverride{
		Interaction:          ptr(runtimestore.InteractionOneShot),
		InjectionSupported:   ptr(false),
		SDKWarmBlockingPaths: ptr([]string{"CLAUDE.md", "tenant-secret.txt"}),
	}
	if err := store.Put(ctx, tenant, "claude-code", ov); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := store.Get(ctx, tenant, "claude-code")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Interaction == nil || *got.Interaction != runtimestore.InteractionOneShot {
		t.Errorf("Interaction round-trip: %+v", got.Interaction)
	}
	if got.InjectionSupported == nil || *got.InjectionSupported {
		t.Errorf("InjectionSupported round-trip: %+v", got.InjectionSupported)
	}
	if got.SDKWarmBlockingPaths == nil || len(*got.SDKWarmBlockingPaths) != 2 {
		t.Errorf("SDKWarmBlockingPaths round-trip: %+v", got.SDKWarmBlockingPaths)
	}

	// Upsert overwrites.
	if err := store.Put(ctx, tenant, "claude-code", runtimestore.CapabilityOverride{
		PreConnect: ptr(true),
	}); err != nil {
		t.Fatalf("upsert Put: %v", err)
	}
	got2, _, _ := store.Get(ctx, tenant, "claude-code")
	if got2.Interaction != nil {
		t.Errorf("upsert did not overwrite: %+v", got2)
	}
	if got2.PreConnect == nil || !*got2.PreConnect {
		t.Errorf("upsert PreConnect: %+v", got2.PreConnect)
	}

	// List.
	list, err := store.List(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(list))
	}

	// Delete + idempotent re-delete.
	if err := store.Delete(ctx, tenant, "claude-code"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := store.Get(ctx, tenant, "claude-code"); ok {
		t.Error("override present after delete")
	}
	if err := store.Delete(ctx, tenant, "claude-code"); err != nil {
		t.Errorf("re-Delete: %v", err)
	}
}

// spec: §12.3 — the RLS guard isolates overrides per tenant: an override
// written under tenant A is invisible to a Get/List run under tenant B.
func TestPgStoreTenantIsolation_spec_12_3(t *testing.T) {
	ctx := context.Background()
	store, pg := startStore(t)
	tenantA := freshTenant(t, ctx, pg)
	tenantB := freshTenant(t, ctx, pg)

	if err := store.Put(ctx, tenantA, "claude-code",
		runtimestore.CapabilityOverride{InjectionSupported: ptr(false)}); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if _, ok, err := store.Get(ctx, tenantB, "claude-code"); err != nil || ok {
		t.Errorf("tenant B should not see tenant A's override: ok=%v err=%v", ok, err)
	}
	list, err := store.List(ctx, tenantB)
	if err != nil || len(list) != 0 {
		t.Errorf("tenant B list should be empty: err=%v len=%d", err, len(list))
	}
}

var _ runtimecapoverride.Store = (*capoverridepg.Store)(nil)
