//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.1 line 165 coordination_lease barrier-target
// mirror. Exercises pkg/gateway/coordlease/pgstore against a real Postgres
// container with the production migrations (including 0164) applied: the
// Upsert/ListHeldByReplica round-trip, the cross-replica handoff overwrite
// (the prior holder's barrier-target set no longer returns the session),
// the Release that excludes a terminal session, and the §12.1 erasure
// primitives. F-10.1.19 / F-11.3.15.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func newCoordLeaseStore(t *testing.T) *coordleasepg.Store {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return coordleasepg.New(pg.Pool, nil)
}

// spec: §10.1 line 165 — the barrier-target query is cross-tenant per
// replica; a handoff overwrites coordinator_replica; Release excludes a
// terminal session.
// diagnosis: a failure means the coordination-lease store mishandles a
// barrier-target query, a coordinator handoff, or terminal-session
// exclusion on Release, breaking §10.1 cross-replica coordination.
func TestCoordLeasePgRoundTrip_spec_10_1_165(t *testing.T) {
	store := newCoordLeaseStore(t)
	ctx := context.Background()

	mustUpsert := func(tenant, sess, replica string, gen int64) {
		if err := store.Upsert(ctx, coordlease.Lease{
			TenantID: tenant, SessionID: sess, CoordinatorReplica: replica, CoordinationGeneration: gen,
		}); err != nil {
			t.Fatalf("upsert %s/%s: %v", tenant, sess, err)
		}
	}
	mustUpsert("acme", "s1", "rep-1", 3)
	mustUpsert("globex", "s2", "rep-1", 1)
	mustUpsert("acme", "s3", "rep-2", 7)

	held, err := store.ListHeldByReplica(ctx, "rep-1")
	if err != nil {
		t.Fatalf("ListHeldByReplica: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("rep-1 held = %d, want 2 (cross-tenant)", len(held))
	}

	// Handoff: rep-2 takes s1; rep-1's set drops it.
	mustUpsert("acme", "s1", "rep-2", 4)
	held, _ = store.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 1 || held[0].SessionID != "s2" {
		t.Fatalf("after handoff rep-1 held = %+v, want only s2", held)
	}
	held2, _ := store.ListHeldByReplica(ctx, "rep-2")
	if len(held2) != 2 {
		t.Fatalf("rep-2 held = %d, want 2 (s1 handed in + s3)", len(held2))
	}

	// Release s2 (terminal); rep-1's set is now empty.
	if err := store.Release(ctx, "globex", "s2"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := store.Release(ctx, "globex", "s2"); err != nil {
		t.Fatalf("idempotent Release: %v", err)
	}
	if held, _ := store.ListHeldByReplica(ctx, "rep-1"); len(held) != 0 {
		t.Fatalf("after release rep-1 held = %+v, want empty", held)
	}
}

// spec: §4.6.1 — GetBySession resolves the recorded coordinator identity
// and its dialable coordinator_address for the eviction-drive routing
// read; a handoff overwrites both; and a released lease resolves no
// coordinator (the released_at IS NULL filter), so a NULL address
// (Sweeper-written pre-seed row) collapses to the empty string.
// diagnosis: a failure means the by-session coordinator read returns the
// wrong holder or address, or resolves a released lease, breaking §4.6.1
// eviction-drive routing and the cross-replica forward hop.
func TestCoordLeasePgGetBySession_spec_4_6_1(t *testing.T) {
	store := newCoordLeaseStore(t)
	ctx := context.Background()

	// Seed with a dialable address, as the bind-time seed does.
	if err := store.Upsert(ctx, coordlease.Lease{
		TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1",
		CoordinatorAddress: "10.0.0.1:50054", CoordinationGeneration: 2,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, found, err := store.GetBySession(ctx, "acme", "s1")
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if !found || got.CoordinatorReplica != "rep-1" || got.CoordinatorAddress != "10.0.0.1:50054" || got.CoordinationGeneration != 2 {
		t.Fatalf("GetBySession = %+v found=%v, want rep-1 / 10.0.0.1:50054 / gen 2", got, found)
	}

	// A handoff overwrites both the identity and the address.
	if err := store.Upsert(ctx, coordlease.Lease{
		TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-2",
		CoordinatorAddress: "10.0.0.2:50054", CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("handoff Upsert: %v", err)
	}
	got, _, _ = store.GetBySession(ctx, "acme", "s1")
	if got.CoordinatorReplica != "rep-2" || got.CoordinatorAddress != "10.0.0.2:50054" {
		t.Fatalf("after handoff GetBySession = %+v, want rep-2 / 10.0.0.2:50054", got)
	}

	// A Sweeper-written row that records no address reads the empty string
	// rather than failing the NULL scan.
	if err := store.Upsert(ctx, coordlease.Lease{
		TenantID: "globex", SessionID: "s2", CoordinatorReplica: "rep-1", CoordinationGeneration: 1,
	}); err != nil {
		t.Fatalf("Upsert no-address: %v", err)
	}
	if got, found, _ := store.GetBySession(ctx, "globex", "s2"); !found || got.CoordinatorAddress != "" {
		t.Fatalf("no-address GetBySession = %+v found=%v, want found with empty address", got, found)
	}

	// A missing session resolves no coordinator.
	if _, found, _ := store.GetBySession(ctx, "acme", "missing"); found {
		t.Fatal("GetBySession(missing) found = true, want false")
	}

	// A released lease resolves no coordinator.
	if err := store.Release(ctx, "acme", "s1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, _ := store.GetBySession(ctx, "acme", "s1"); found {
		t.Fatal("GetBySession found = true after Release, want false")
	}
}

// spec: §12.1 line 5 — the mandatory erasure primitives remove the scoped
// rows and reject an empty scope.
// diagnosis: a failure means the coordination-lease erasure primitive
// removes the wrong rows or accepts an empty scope, breaching the §12.1
// scoped-erasure contract.
func TestCoordLeasePgErasure_spec_12_1_5(t *testing.T) {
	store := newCoordLeaseStore(t)
	ctx := context.Background()
	_ = store.Upsert(ctx, coordlease.Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1"})
	_ = store.Upsert(ctx, coordlease.Lease{TenantID: "acme", SessionID: "s2", CoordinatorReplica: "rep-1"})
	_ = store.Upsert(ctx, coordlease.Lease{TenantID: "globex", SessionID: "s3", CoordinatorReplica: "rep-1"})

	if err := store.DeleteByUser(ctx, "acme", "u", []string{"s1"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if held, _ := store.ListHeldByReplica(ctx, "rep-1"); len(held) != 2 {
		t.Fatalf("after DeleteByUser held = %d, want 2", len(held))
	}
	if err := store.DeleteByTenant(ctx, ""); err == nil {
		t.Fatal("DeleteByTenant empty scope = nil, want error")
	}
	if err := store.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	held, _ := store.ListHeldByReplica(ctx, "rep-1")
	if len(held) != 1 || held[0].TenantID != "globex" {
		t.Fatalf("after DeleteByTenant held = %+v, want only globex", held)
	}
}
