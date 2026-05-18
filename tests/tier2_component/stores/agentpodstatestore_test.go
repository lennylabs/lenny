//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.6.1 agent_pod_state mirror, exercising the
// Postgres-backed pkg/agentpodstate/pgstore against a real container
// with the production migrations applied. Covers the bulk-UPSERT insert
// path, the converging second Sync (changed rows updated, vanished rows
// deleted), pool-scoped isolation of the prune DELETE, the empty-pool
// no-op, and MirrorLagSeconds.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// podRow is one agent_pod_state row read back for assertions. The
// table is platform-global (§12.6), so the read needs no tenant
// context.
type podRow struct {
	poolID    string
	state     string
	tenantID  *string
	sessionID *string
	isolation string
	execMode  string
	rv        int64
	nodeName  *string
}

// getPodRow reads one agent_pod_state row by pod_id. The bool reports
// whether the row exists.
func getPodRow(t *testing.T, ctx context.Context, pg *containers.Postgres, podID string) (podRow, bool) {
	t.Helper()
	var r podRow
	err := pg.Pool.QueryRow(ctx,
		`SELECT pool_id, state, tenant_id, session_id, isolation_profile,
		        execution_mode, resource_version, node_name
		 FROM agent_pod_state WHERE pod_id = $1`, podID).
		Scan(&r.poolID, &r.state, &r.tenantID, &r.sessionID, &r.isolation,
			&r.execMode, &r.rv, &r.nodeName)
	if err != nil {
		return podRow{}, false
	}
	return r, true
}

// poolRowCount counts agent_pod_state rows for one pool.
func poolRowCount(t *testing.T, ctx context.Context, pg *containers.Postgres, poolID string) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_pod_state WHERE pool_id = $1`, poolID).
		Scan(&n); err != nil {
		t.Fatalf("poolRowCount: %v", err)
	}
	return n
}

// spec: 4.6.1
// diagnosis: the Postgres-backed agent_pod_state mirror in
// pkg/agentpodstate/pgstore did not behave as specified. Sync must
// bulk-UPSERT the observed Sandbox set keyed on pod_id, a second Sync
// must converge the mirror by updating changed rows and deleting rows
// no longer observed, the prune DELETE must be scoped to the pool so it
// never removes another pool's rows, an empty observed set must clear
// only the target pool, and MirrorLagSeconds must report
// now() - max(updated_at) (0 for a pool with no rows).
func TestAgentPodStateStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := agentpodstatepg.New(pg.Pool)
	ctx := context.Background()

	t.Run("sync inserts the observed rows", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		observed := []agentpodstate.PodState{
			{
				PodID: pool + "-a", PoolID: pool, State: "idle",
				IsolationProfile: "sandboxed", ExecutionMode: "session",
				ResourceVersion: 1001, NodeName: "node-1",
			},
			{
				PodID: pool + "-b", PoolID: pool, State: "warming",
				IsolationProfile: "sandboxed", ExecutionMode: "session",
				ResourceVersion: 1002,
			},
		}
		if err := store.Sync(ctx, pool, observed); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		got, ok := getPodRow(t, ctx, pg, pool+"-a")
		if !ok {
			t.Fatalf("row %s-a was not inserted", pool)
		}
		if got.poolID != pool || got.state != "idle" || got.isolation != "sandboxed" ||
			got.execMode != "session" || got.rv != 1001 {
			t.Errorf("row %s-a mismatch: %+v", pool, got)
		}
		if got.nodeName == nil || *got.nodeName != "node-1" {
			t.Errorf("row %s-a node_name = %v, want node-1", pool, got.nodeName)
		}
		// An idle/warm pod carries no session: tenant_id and session_id
		// must mirror as SQL NULL, not the empty string, so the partial
		// indexes stay accurate.
		if got.tenantID != nil || got.sessionID != nil {
			t.Errorf("row %s-a tenant/session = %v/%v, want NULL/NULL",
				pool, got.tenantID, got.sessionID)
		}
		warm, ok := getPodRow(t, ctx, pg, pool+"-b")
		if !ok {
			t.Fatalf("row %s-b was not inserted", pool)
		}
		if warm.state != "warming" || warm.nodeName != nil {
			t.Errorf("row %s-b mismatch: %+v", pool, warm)
		}
		if n := poolRowCount(t, ctx, pg, pool); n != 2 {
			t.Errorf("pool %s row count = %d, want 2", pool, n)
		}
	})

	t.Run("second sync updates changed rows and deletes vanished rows", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		first := []agentpodstate.PodState{
			{PodID: pool + "-a", PoolID: pool, State: "warming", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
			{PodID: pool + "-b", PoolID: pool, State: "warming", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
			{PodID: pool + "-c", PoolID: pool, State: "warming", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}
		if err := store.Sync(ctx, pool, first); err != nil {
			t.Fatalf("first Sync: %v", err)
		}
		// pod-a transitions warming -> idle; pod-b is unchanged; pod-c is
		// gone (drained and deleted). The mirror must converge.
		second := []agentpodstate.PodState{
			{PodID: pool + "-a", PoolID: pool, State: "idle", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 7, NodeName: "node-9"},
			{PodID: pool + "-b", PoolID: pool, State: "warming", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}
		if err := store.Sync(ctx, pool, second); err != nil {
			t.Fatalf("second Sync: %v", err)
		}
		gotA, ok := getPodRow(t, ctx, pg, pool+"-a")
		if !ok {
			t.Fatalf("row %s-a missing after second Sync", pool)
		}
		if gotA.state != "idle" || gotA.rv != 7 ||
			gotA.nodeName == nil || *gotA.nodeName != "node-9" {
			t.Errorf("row %s-a not updated: %+v", pool, gotA)
		}
		if _, ok := getPodRow(t, ctx, pg, pool+"-c"); ok {
			t.Errorf("row %s-c should be deleted after it left the observed set", pool)
		}
		if n := poolRowCount(t, ctx, pg, pool); n != 2 {
			t.Errorf("pool %s row count = %d after converging Sync, want 2", pool, n)
		}
	})

	t.Run("sync is pool-scoped and does not touch another pool", func(t *testing.T) {
		poolA := "pool-" + newUUID(t)[:8]
		poolB := "pool-" + newUUID(t)[:8]
		if err := store.Sync(ctx, poolA, []agentpodstate.PodState{
			{PodID: poolA + "-x", PoolID: poolA, State: "idle", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}); err != nil {
			t.Fatalf("Sync poolA: %v", err)
		}
		if err := store.Sync(ctx, poolB, []agentpodstate.PodState{
			{PodID: poolB + "-y", PoolID: poolB, State: "idle", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}); err != nil {
			t.Fatalf("Sync poolB: %v", err)
		}
		// Re-sync poolB to an empty set: this prunes every poolB row but
		// must leave poolA untouched.
		if err := store.Sync(ctx, poolB, nil); err != nil {
			t.Fatalf("empty Sync poolB: %v", err)
		}
		if n := poolRowCount(t, ctx, pg, poolB); n != 0 {
			t.Errorf("poolB row count = %d after empty Sync, want 0", n)
		}
		if n := poolRowCount(t, ctx, pg, poolA); n != 1 {
			t.Errorf("poolA row count = %d, want 1 (an unrelated pool must survive)", n)
		}
		if _, ok := getPodRow(t, ctx, pg, poolA+"-x"); !ok {
			t.Errorf("poolA row was deleted by a poolB Sync")
		}
	})

	t.Run("empty pool id is rejected", func(t *testing.T) {
		if err := store.Sync(ctx, "", nil); !errors.Is(err, agentpodstate.ErrEmptyPoolID) {
			t.Errorf("Sync with empty poolID: got %v, want ErrEmptyPoolID", err)
		}
		if _, err := store.MirrorLagSeconds(ctx, ""); !errors.Is(err, agentpodstate.ErrEmptyPoolID) {
			t.Errorf("MirrorLagSeconds with empty poolID: got %v, want ErrEmptyPoolID", err)
		}
	})

	t.Run("mirror lag is zero for an empty pool and non-negative after a sync", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		lag, err := store.MirrorLagSeconds(ctx, pool)
		if err != nil {
			t.Fatalf("MirrorLagSeconds (empty pool): %v", err)
		}
		if lag != 0 {
			t.Errorf("MirrorLagSeconds for a pool with no rows = %v, want 0", lag)
		}
		if err := store.Sync(ctx, pool, []agentpodstate.PodState{
			{PodID: pool + "-a", PoolID: pool, State: "idle", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		lag, err = store.MirrorLagSeconds(ctx, pool)
		if err != nil {
			t.Fatalf("MirrorLagSeconds (after Sync): %v", err)
		}
		// updated_at is now() at write time, so the lag is small and
		// non-negative. Allow a generous ceiling for slow CI.
		if lag < 0 || lag > 60 {
			t.Errorf("MirrorLagSeconds right after a Sync = %v, want a small non-negative value", lag)
		}
	})

	t.Run("mirror lag tracks the most recent sync", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		if err := store.Sync(ctx, pool, []agentpodstate.PodState{
			{PodID: pool + "-a", PoolID: pool, State: "warming", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 1},
		}); err != nil {
			t.Fatalf("first Sync: %v", err)
		}
		time.Sleep(1100 * time.Millisecond)
		stale, err := store.MirrorLagSeconds(ctx, pool)
		if err != nil {
			t.Fatalf("MirrorLagSeconds (stale): %v", err)
		}
		if stale < 1 {
			t.Errorf("lag after a 1.1s wait = %v, want at least 1s", stale)
		}
		// A re-sync advances updated_at to now(), so the lag drops.
		if err := store.Sync(ctx, pool, []agentpodstate.PodState{
			{PodID: pool + "-a", PoolID: pool, State: "idle", IsolationProfile: "standard", ExecutionMode: "session", ResourceVersion: 2},
		}); err != nil {
			t.Fatalf("second Sync: %v", err)
		}
		fresh, err := store.MirrorLagSeconds(ctx, pool)
		if err != nil {
			t.Fatalf("MirrorLagSeconds (fresh): %v", err)
		}
		if fresh >= stale {
			t.Errorf("lag did not drop after a re-sync: stale=%v fresh=%v", stale, fresh)
		}
	})
}
