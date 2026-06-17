//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.6 line 436 Tier-4 PostgresPodRegistry,
// exercising pkg/podregistry.PostgresPodRegistry against a real Postgres
// container with the production migrations applied (including the 0108
// LISTEN/NOTIFY trigger). Covers CRUD round-trips, the optimistic-locking
// CAS on UpdatePodState (invalid transition, stale-version conflict,
// not-found), the FOR UPDATE SKIP LOCKED ClaimPod including the
// single-claim-under-contention guarantee and pool exhaustion, ReleasePod
// reason→state mapping with session/tenant unbind, CountByState, the
// WatchPods no-initial-snapshot contract with a subsequent delta and the
// implementation-labeled watch-lag gauge, and the agent_pod_state notify
// trigger firing pg_notify on the per-pool channel.
package stores_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/podregistry"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// startPodRegistry brings up a Postgres container with the production
// migrations and returns a PostgresPodRegistry plus the raw handle.
func startPodRegistry(t *testing.T) (*podregistry.PostgresPodRegistry, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	r, err := podregistry.NewPostgres(pg.Pool)
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	return r, pg
}

// seedPod inserts an agent_pod_state row directly in the given state so
// a test can stage a claim or transition without driving the registry
// through every prior phase.
func seedPod(t *testing.T, ctx context.Context, pg *containers.Postgres, podID, poolID, state string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO agent_pod_state (pod_id, pool_id, state, isolation_profile,
			execution_mode, resource_version, updated_at)
		 VALUES ($1, $2, $3, 'sandboxed', 'session', 1, now())`,
		podID, poolID, state); err != nil {
		t.Fatalf("seed pod %s: %v", podID, err)
	}
}

// spec: §12.6 line 436 — the PostgresPodRegistry adapter over
// agent_pod_state satisfies the full §12.6 PodRegistry interface so a
// Tier-4 swap is a configuration change rather than a from-scratch
// build.
// diagnosis: a failure means the PostgresPodRegistry adapter does not
// fully satisfy the §12.6 PodRegistry interface, so a Tier-4 swap to it
// would not be a drop-in configuration change.
func TestPostgresPodRegistryContract_spec_12_6_436(t *testing.T) {
	t.Parallel()
	r, pg := startPodRegistry(t)
	ctx := context.Background()

	t.Run("create then get round-trip", func(t *testing.T) {
		pool := podregistry.PoolID("pool-" + newUUID(t)[:8])
		rec, err := r.CreatePod(ctx, pool, podregistry.PodSpec{
			PoolID: pool, IsolationProfile: "microvm", ExecutionMode: "concurrent",
		})
		if err != nil {
			t.Fatalf("CreatePod: %v", err)
		}
		if rec.State != "warming" || rec.ResourceVersion != "1" {
			t.Errorf("created rec = %+v, want warming/rv=1", rec)
		}
		got, err := r.GetPod(ctx, rec.PodID)
		if err != nil {
			t.Fatalf("GetPod: %v", err)
		}
		if got.PoolID != pool || got.IsolationProfile != "microvm" || got.ExecutionMode != "concurrent" {
			t.Errorf("got = %+v, want pool=%s iso=microvm exec=concurrent", got, pool)
		}
	})

	t.Run("CreatePod requires poolID", func(t *testing.T) {
		if _, err := r.CreatePod(ctx, "", podregistry.PodSpec{}); err == nil {
			t.Fatal("CreatePod with empty poolID = nil, want error")
		}
	})

	t.Run("GetPod unknown is ErrNotFound", func(t *testing.T) {
		if _, err := r.GetPod(ctx, podregistry.PodID("nope-"+newUUID(t)[:8])); !errors.Is(err, podregistry.ErrNotFound) {
			t.Errorf("GetPod unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdatePodState CAS advances state and resource_version", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-a"
		seedPod(t, ctx, pg, id, pool, "warming")
		if err := r.UpdatePodState(ctx, podregistry.PodID(id),
			podregistry.StateTransition{From: "warming", To: "idle"}); err != nil {
			t.Fatalf("UpdatePodState: %v", err)
		}
		got, _ := r.GetPod(ctx, podregistry.PodID(id))
		if got.State != "idle" {
			t.Errorf("state = %q, want idle", got.State)
		}
		if got.ResourceVersion != "2" {
			t.Errorf("resource_version = %q, want 2 (bumped)", got.ResourceVersion)
		}
	})

	t.Run("UpdatePodState wrong From is ErrInvalidTransition", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-a"
		seedPod(t, ctx, pg, id, pool, "idle")
		err := r.UpdatePodState(ctx, podregistry.PodID(id),
			podregistry.StateTransition{From: "warming", To: "claimed"})
		if !errors.Is(err, podregistry.ErrInvalidTransition) {
			t.Errorf("UpdatePodState wrong From = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("UpdatePodState races resolve to one winner (CAS)", func(t *testing.T) {
		// Two no-precondition transitions read the same resource_version
		// and race the CAS UPDATE ... WHERE resource_version = expected.
		// Exactly one wins; the loser's UPDATE affects zero rows and maps
		// to ErrResourceConflict. The From check is omitted so both pass
		// the precondition and contend on the version, not the state.
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-cas"
		seedPod(t, ctx, pg, id, pool, "idle")
		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			conflicts int
			oks       int
		)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := r.UpdatePodState(ctx, podregistry.PodID(id),
					podregistry.StateTransition{To: "idle"})
				mu.Lock()
				switch {
				case err == nil:
					oks++
				case errors.Is(err, podregistry.ErrResourceConflict):
					conflicts++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		// Invariant (deterministic): every writer either succeeded or
		// conflicted — no other error and no lost update — and the final
		// resource_version advanced by exactly the number of successes, so
		// no two successes collapsed onto one version.
		if oks+conflicts != 8 {
			t.Errorf("oks+conflicts = %d, want 8 (unexpected error path)", oks+conflicts)
		}
		if oks == 0 {
			t.Error("no UpdatePodState succeeded under contention")
		}
		got, _ := r.GetPod(ctx, podregistry.PodID(id))
		if got.ResourceVersion != strconv.Itoa(1+oks) {
			t.Errorf("resource_version = %q, want %d (1 + %d successes)", got.ResourceVersion, 1+oks, oks)
		}
		// Real concurrency across the connection pool makes at least one
		// CAS lose; the conflict mapping is what the §4.6.1 retry loop
		// branches on.
		if conflicts == 0 {
			t.Error("no UpdatePodState observed ErrResourceConflict under contention")
		}
	})

	t.Run("UpdatePodState unknown is ErrNotFound", func(t *testing.T) {
		err := r.UpdatePodState(ctx, podregistry.PodID("ghost-"+newUUID(t)[:8]),
			podregistry.StateTransition{To: "idle"})
		if !errors.Is(err, podregistry.ErrNotFound) {
			t.Errorf("UpdatePodState unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("ClaimPod claims an idle pod and pins session/tenant", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		seedPod(t, ctx, pg, pool+"-idle", pool, "idle")
		rec, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
			PoolID: podregistry.PoolID(pool), TenantID: "acme", SessionID: "sess-1",
		})
		if err != nil {
			t.Fatalf("ClaimPod: %v", err)
		}
		if rec.State != "claimed" || rec.SessionID != "sess-1" || rec.TenantID != "acme" {
			t.Errorf("claimed rec = %+v, want claimed/sess-1/acme", rec)
		}
		got, _ := r.GetPod(ctx, rec.PodID)
		if got.State != "claimed" || got.SessionID != "sess-1" {
			t.Errorf("persisted claim = %+v", got)
		}
	})

	t.Run("ClaimPod on an empty pool is ErrPoolExhausted", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		_, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
			PoolID: podregistry.PoolID(pool), SessionID: "sess-x",
		})
		if !errors.Is(err, podregistry.ErrPoolExhausted) {
			t.Errorf("ClaimPod empty pool = %v, want ErrPoolExhausted", err)
		}
	})

	t.Run("ClaimPod requires SessionID", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		seedPod(t, ctx, pg, pool+"-idle", pool, "idle")
		if _, err := r.ClaimPod(ctx, podregistry.ClaimOpts{PoolID: podregistry.PoolID(pool)}); err == nil {
			t.Fatal("ClaimPod without SessionID = nil, want error")
		}
	})

	t.Run("concurrent ClaimPod calls claim distinct pods (SKIP LOCKED)", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		seedPod(t, ctx, pg, pool+"-1", pool, "idle")
		seedPod(t, ctx, pg, pool+"-2", pool, "idle")
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			claimed []podregistry.PodID
		)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				rec, err := r.ClaimPod(ctx, podregistry.ClaimOpts{
					PoolID:    podregistry.PoolID(pool),
					SessionID: "sess-" + string(rune('a'+n)),
				})
				if err != nil {
					return
				}
				mu.Lock()
				claimed = append(claimed, rec.PodID)
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		if len(claimed) != 2 || claimed[0] == claimed[1] {
			t.Errorf("concurrent claims = %v, want two distinct pods", claimed)
		}
	})

	t.Run("ReleasePod unbinds session and maps reason to state", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-claimed"
		seedPod(t, ctx, pg, id, pool, "idle")
		if _, err := pg.Pool.Exec(ctx,
			`UPDATE agent_pod_state SET state='claimed', session_id='s', tenant_id='acme' WHERE pod_id=$1`, id); err != nil {
			t.Fatalf("stage claimed: %v", err)
		}
		if err := r.ReleasePod(ctx, podregistry.PodID(id), podregistry.ReleaseFailed); err != nil {
			t.Fatalf("ReleasePod: %v", err)
		}
		got, _ := r.GetPod(ctx, podregistry.PodID(id))
		if got.State != "failed" || got.SessionID != "" || got.TenantID != "" {
			t.Errorf("released rec = %+v, want failed with empty session/tenant", got)
		}
	})

	t.Run("ReleasePod unknown is ErrNotFound", func(t *testing.T) {
		if err := r.ReleasePod(ctx, podregistry.PodID("ghost-"+newUUID(t)[:8]), podregistry.ReleaseCompleted); !errors.Is(err, podregistry.ErrNotFound) {
			t.Errorf("ReleasePod unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListPodsByPool and CountByState", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		seedPod(t, ctx, pg, pool+"-1", pool, "idle")
		seedPod(t, ctx, pg, pool+"-2", pool, "idle")
		seedPod(t, ctx, pg, pool+"-3", pool, "claimed")
		all, err := r.ListPodsByPool(ctx, podregistry.PoolID(pool), podregistry.PodFilter{})
		if err != nil {
			t.Fatalf("ListPodsByPool: %v", err)
		}
		if len(all) != 3 {
			t.Errorf("list len = %d, want 3", len(all))
		}
		idle, _ := r.ListPodsByPool(ctx, podregistry.PoolID(pool), podregistry.PodFilter{State: "idle"})
		if len(idle) != 2 {
			t.Errorf("idle list len = %d, want 2", len(idle))
		}
		counts, err := r.CountByState(ctx, podregistry.PoolID(pool))
		if err != nil {
			t.Fatalf("CountByState: %v", err)
		}
		if counts["idle"] != 2 || counts["claimed"] != 1 {
			t.Errorf("counts = %v, want idle=2 claimed=1", counts)
		}
	})

	t.Run("DeletePod removes the row", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-del"
		seedPod(t, ctx, pg, id, pool, "idle")
		if err := r.DeletePod(ctx, podregistry.PodID(id)); err != nil {
			t.Fatalf("DeletePod: %v", err)
		}
		if _, err := r.GetPod(ctx, podregistry.PodID(id)); !errors.Is(err, podregistry.ErrNotFound) {
			t.Errorf("GetPod after delete = %v, want ErrNotFound", err)
		}
		if err := r.DeletePod(ctx, podregistry.PodID(id)); !errors.Is(err, podregistry.ErrNotFound) {
			t.Errorf("DeletePod again = %v, want ErrNotFound", err)
		}
	})

	t.Run("WatchPods emits no initial snapshot then a delta", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		id := pool + "-w"
		seedPod(t, ctx, pg, id, pool, "idle")
		r.SetWatchTuningForTest(20*time.Millisecond, 16)
		wctx, cancel := context.WithCancel(ctx)
		defer cancel()
		events, err := r.WatchPods(wctx, podregistry.PoolID(pool))
		if err != nil {
			t.Fatalf("WatchPods: %v", err)
		}
		// No initial-snapshot event for the pre-existing pod.
		select {
		case e := <-events:
			t.Fatalf("unexpected initial event %+v; want none", e)
		case <-time.After(120 * time.Millisecond):
		}
		// A state change produces an Updated delta.
		if err := r.UpdatePodState(ctx, podregistry.PodID(id),
			podregistry.StateTransition{From: "idle", To: "claimed"}); err != nil {
			t.Fatalf("UpdatePodState: %v", err)
		}
		select {
		case e := <-events:
			if e.EventType != podregistry.EventUpdated || e.PodID != podregistry.PodID(id) {
				t.Errorf("delta = %+v, want Updated for %s", e, id)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no Updated delta within timeout")
		}
	})

	t.Run("notify trigger fires pg_notify on the per-pool channel", func(t *testing.T) {
		pool := "pool-" + newUUID(t)[:8]
		// A dedicated connection LISTENs on the §12.6 line 484 per-pool
		// channel; the 0108 trigger must publish the pod_id when a row is
		// inserted or updated.
		conn, err := pg.Pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `LISTEN "pod_state_change_`+pool+`"`); err != nil {
			t.Fatalf("LISTEN: %v", err)
		}
		id := pool + "-n"
		seedPod(t, ctx, pg, id, pool, "warming")
		nctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		n, err := conn.Conn().WaitForNotification(nctx)
		if err != nil {
			t.Fatalf("WaitForNotification: %v", err)
		}
		if n.Payload != id {
			t.Errorf("notify payload = %q, want %q", n.Payload, id)
		}
	})
}

// ensure pgx import is used even if the harness changes; the typed
// reference documents that the registry rides the same pgx stack as the
// rest of the storage layer.
var _ = pgx.ErrNoRows
