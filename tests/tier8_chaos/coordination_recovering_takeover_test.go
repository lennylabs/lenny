// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos coverage for coordinator failover of sessions outside running:
// a suspended session that still holds its pod, a resume_pending session whose
// pod failed, and a suspended session whose pod was released. The coordinating
// replica's lease is left to lapse in real Redis, which is the injected crash,
// and the survivor runs the production Sweeper over the production Postgres
// session store.
package tier8_chaos_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/coordfixture"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §10.1.2
// (Coordinator Handoff Protocol), §10.1.5 (Stale Replica Behavior)
//
// diagnosis: a coordinating replica died while its session was suspended with
// a held pod, in resume_pending, or suspended with no pod, and the survivor
// did not take the session over as the spec requires. Either a held pod was
// not re-adopted and re-fenced (so it self-terminates in hold state and the
// stale coordinator is not fenced out), a resume_pending session was not
// adopted or had its failed pod re-adopted, or a podless suspended session
// gained a lease it must not hold.
func TestCoordinatorFailoverOutsideRunning_spec_10_1_1(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()
	sessions := sessionpg.New(pg.Pool)

	// seed creates one session row in its own tenant and returns the tenant.
	seed := func(t *testing.T, sessID string, state session.State, pod string) string {
		t.Helper()
		tenant := "t-" + uuid.NewString()
		seedChaosTenant(t, pg, tenant)
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: sessID, TenantID: tenant, State: state, PodAssignment: pod,
			CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return tenant
	}
	// crashHolder models a coordinator that held the lease on a short TTL and
	// died: the lease lapses in real Redis.
	crashHolder := func(t *testing.T, tenant, sessID string) {
		t.Helper()
		if _, err := leases.Acquire(ctx, tenant, sessID, "replica-1", time.Second); err != nil {
			t.Fatalf("coordinator acquire: %v", err)
		}
		waitForLapse(t, leases, tenant, sessID)
	}

	t.Run("held-pod suspended session is re-adopted and re-fenced", func(t *testing.T) {
		sessID := uuid.NewString()
		tenant := seed(t, sessID, session.StateSuspended, "pod-"+sessID)
		pod := coordfixture.StartPod(t, sessID)
		coordinator := coordfixture.NewReplica("replica-1", tenant, pod, sessions, leases, time.Second, sessID)
		if _, err := pod.Fence(ctx, sessID, 1); err != nil {
			t.Fatalf("initial fence: %v", err)
		}
		if _, err := coordinator.Sweeper.Sweep(ctx); err != nil {
			t.Fatalf("coordinator sweep: %v", err)
		}
		waitForLapse(t, leases, tenant, sessID)

		survivor := coordfixture.NewReplica("replica-2", tenant, pod, sessions, leases, 30*time.Second)
		if held, err := survivor.Sweeper.Sweep(ctx); err != nil || held != 1 {
			t.Fatalf("takeover Sweep: held=%d err=%v, want 1", held, err)
		}
		if got, _ := sessions.Get(ctx, tenant, sessID); got.CoordinationGeneration != 2 || got.State != session.StateSuspended {
			t.Fatalf("row after takeover = (%s, %d), want suspended at generation 2", got.State, got.CoordinationGeneration)
		}
		if pod.LastFenced(sessID) != 2 {
			t.Fatalf("pod fenced generation = %d, want 2", pod.LastFenced(sessID))
		}
		if !survivor.Bindings.Bound(sessID) || survivor.Bindings.BindingGeneration(sessID) != 2 {
			t.Fatalf("survivor binding bound=%v stamp=%d, want bound at 2", survivor.Bindings.Bound(sessID), survivor.Bindings.BindingGeneration(sessID))
		}
		if !pod.StaleRPCRejected(ctx, sessID, 1) {
			t.Error("the dead coordinator's RPC at generation 1 was not fenced out")
		}
		// The coordinator stalled rather than died: its next sweep sees the row
		// past its binding stamp and evicts the binding.
		if _, err := coordinator.Sweeper.Sweep(ctx); err != nil {
			t.Fatalf("stalled coordinator sweep: %v", err)
		}
		if coordinator.Bindings.Bound(sessID) || coordinator.Bindings.Evicted(sessID) != 1 {
			t.Errorf("stalled coordinator bound=%v evictions=%d, want evicted once", coordinator.Bindings.Bound(sessID), coordinator.Bindings.Evicted(sessID))
		}
	})

	t.Run("resume_pending session is adopted without re-adopting its failed pod", func(t *testing.T) {
		sessID := uuid.NewString()
		tenant := seed(t, sessID, session.StateResumePending, "pod-"+sessID)
		pod := coordfixture.StartPod(t, sessID)
		crashHolder(t, tenant, sessID)

		survivor := coordfixture.NewReplica("replica-2", tenant, pod, sessions, leases, 30*time.Second)
		for pass := 1; pass <= 2; pass++ {
			if held, err := survivor.Sweeper.Sweep(ctx); err != nil || held != 1 {
				t.Fatalf("Sweep %d: held=%d err=%v, want 1", pass, held, err)
			}
		}
		if got, _ := sessions.Get(ctx, tenant, sessID); got.CoordinationGeneration != 2 {
			t.Fatalf("generation = %d, want 2 (one bump across the takeover and the renew)", got.CoordinationGeneration)
		}
		if n := survivor.Readopter.CalledFor(sessID); n != 0 {
			t.Fatalf("readopt calls = %d, want 0 (the resume_pending pod failed)", n)
		}
		if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-2" {
			t.Fatalf("lease = %+v err=%v, want held by replica-2", lease, err)
		}
	})

	t.Run("podless suspended session gains no lease", func(t *testing.T) {
		sessID := uuid.NewString()
		tenant := seed(t, sessID, session.StateSuspended, "")
		pod := coordfixture.StartPod(t, sessID)
		crashHolder(t, tenant, sessID)

		survivor := coordfixture.NewReplica("replica-2", tenant, pod, sessions, leases, 30*time.Second)
		if held, err := survivor.Sweeper.Sweep(ctx); err != nil || held != 0 {
			t.Fatalf("Sweep: held=%d err=%v, want 0", held, err)
		}
		if _, err := leases.Get(ctx, tenant, sessID); !errors.Is(err, leasestore.ErrNotFound) {
			t.Fatalf("podless suspended session holds a lease (err=%v), want none", err)
		}
		if got, _ := sessions.Get(ctx, tenant, sessID); got.CoordinationGeneration != 1 {
			t.Fatalf("generation = %d, want 1 (no handoff)", got.CoordinationGeneration)
		}
	})
}
