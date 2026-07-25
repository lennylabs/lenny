//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.6.1 coordination-lease mirror address write:
// the sweeper records its dialable inter-replica address as the mirror
// row's coordinator_address, so the eviction-forward hop resolves a
// routable target for the current coordinator. Exercises the sweeper
// (pkg/gateway/coordination) against a real Redis lease store and a real
// Postgres-backed coordlease mirror with the production migrations
// (including 0180) applied.
package coordination_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 4.6.1
// diagnosis: a failure means the sweeper does not stamp its dialable
// inter-replica address into the coordination_lease mirror, or a
// cross-replica handoff fails to overwrite it, so the §4.6.1 eviction
// drive cannot resolve a routable coordinator and the forward hop dials
// nowhere or a stale predecessor.
func TestSweeperMirrorsCoordinatorAddress_spec_4_6_1(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	mirror := coordleasepg.New(pg.Pool, nil)

	sessions := memstore.New()
	seedSession(t, sessions, "acme", "s1", session.StateRunning)

	sw := coordination.NewSweeper(staticLister{"acme"}, sessions, leases, coordination.Options{
		ReplicaID:           "rep-1",
		InterReplicaAddress: "10.0.0.1:50054",
		TTL:                 30 * time.Second,
		Interval:            time.Hour,
		Mirror:              mirror,
	})
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("rep-1 Sweep: %v", err)
	}

	got, found, err := mirror.GetBySession(ctx, "acme", "s1")
	if err != nil || !found {
		t.Fatalf("GetBySession after seed sweep: found=%v err=%v", found, err)
	}
	if got.CoordinatorReplica != "rep-1" || got.CoordinatorAddress != "10.0.0.1:50054" {
		t.Fatalf("mirror row = (%q, %q), want (rep-1, 10.0.0.1:50054)",
			got.CoordinatorReplica, got.CoordinatorAddress)
	}

	// A cross-replica handoff overwrites both the identity and the address.
	// The lease lapses, and rep-2 (a distinct replica with its own address)
	// acquires it on its own sweep.
	if err := leases.Release(ctx, "acme", "s1", "rep-1"); err != nil {
		t.Fatalf("release rep-1 lease: %v", err)
	}
	swB := coordination.NewSweeper(staticLister{"acme"}, sessions, leases, coordination.Options{
		ReplicaID:           "rep-2",
		InterReplicaAddress: "10.0.0.2:50054",
		TTL:                 30 * time.Second,
		Interval:            time.Hour,
		Mirror:              mirror,
	})
	if _, err := swB.Sweep(ctx); err != nil {
		t.Fatalf("rep-2 Sweep: %v", err)
	}

	got, _, err = mirror.GetBySession(ctx, "acme", "s1")
	if err != nil {
		t.Fatalf("GetBySession after handoff: %v", err)
	}
	if got.CoordinatorReplica != "rep-2" || got.CoordinatorAddress != "10.0.0.2:50054" {
		t.Fatalf("after handoff mirror row = (%q, %q), want (rep-2, 10.0.0.2:50054)",
			got.CoordinatorReplica, got.CoordinatorAddress)
	}
}
