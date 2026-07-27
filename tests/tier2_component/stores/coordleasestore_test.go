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
