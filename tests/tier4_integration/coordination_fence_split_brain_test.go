//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration coverage for the §10.1 coordination_generation
// split-brain fence across two gateway replicas. Both replicas run the
// production coordination Sweeper: replica-1 is the coordinating replica that
// binds the running session and holds its Redis lease at generation 1 through
// its own sweep, and replica-2 is the survivor whose Sweeper takes the session
// over once replica-1 crashes. Both share a real Redis lease store and the
// shared session store backed by the production Postgres pgstore, so the
// coordination_generation the fence turns on is exercised over the same Postgres
// CAS production uses, and a real in-process §4.7 adapter models the
// still-running pod. The test drives a coordinator handoff — replica-1 stops
// sweeping and its lease is gone, the survivor's Sweeper adopts the orphan,
// bumps coordination_generation over Postgres, and re-fences the pod — then
// asserts the previous coordinator's next session-mutating RPC is rejected by
// the pod's generation fence once the generation advanced. This builds the
// reusable two-replica coordination harness the TEST-GAPS.md T-4.2.4 finding
// requires over shared Postgres and Redis rather than in-process fakes, so the
// fence is the real adapter fence, the generation bump is a real Postgres CAS,
// and the lease handoff is a real cross-replica Redis lease driven by two real
// Sweepers.
package tier4_integration_test

import (
	"context"
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

// spec: §10.1 (coordination_generation split-brain fence; coordinator handoff
// re-adopts the still-running pod; a stale coordinator's RPC is rejected),
// §4.2, §4.6.1 (coordinating replica holds the lease).
//
// diagnosis: a failure means the two-replica coordinator handoff did not fence
// the still-running pod to the post-handoff generation over shared Redis, so a
// stale coordinator could still drive the pod after the handoff advanced the
// generation — the split-brain the fence exists to prevent. The lease and the
// binding, or the fenced generation and the lease holder, diverged across the
// two replicas.
func TestCoordinationSplitBrainFenceAcrossTwoReplicas_spec_10_1(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	sessions := sessionpg.New(pg.Pool)
	ctx := context.Background()

	const tenant = "acme"
	seedTenant(t, pg, tenant)
	sessID := uuid.NewString()
	const ttl = 30 * time.Second

	// The session is running with a persisted pod assignment, coordinated by
	// replica-1 at generation 1 (its at-bind fence). The pod is fenced to 1.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: sessID, TenantID: tenant, State: session.StateRunning,
		PodAssignment: "pod-" + sessID, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	pod := coordfixture.StartPod(t, sessID)

	// replica-1 is a real coordinating gateway replica: its Sweeper binds the
	// session, and after its at-bind fence to generation 1 its sweep holds the
	// real Redis lease at that generation without bumping (a bound renew is not
	// a handoff).
	coordinator := coordfixture.NewReplica("replica-1", tenant, pod, sessions, leases, ttl, sessID)
	if _, err := pod.Fence(ctx, sessID, 1); err != nil {
		t.Fatalf("replica-1 at-bind fence to generation 1: %v", err)
	}
	if _, err := coordinator.Sweeper.Sweep(ctx); err != nil {
		t.Fatalf("replica-1 coordinating sweep: %v", err)
	}
	if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-1" {
		t.Fatalf("after replica-1 coordinating sweep lease holder = %+v err=%v, want replica-1", lease, err)
	}
	got, _ := sessions.Get(ctx, tenant, sessID)
	if got.CoordinationGeneration != 1 {
		t.Fatalf("coordination_generation after replica-1 sweep = %d, want 1 (a bound renew is not a handoff)", got.CoordinationGeneration)
	}

	// replica-2 is the survivor: a second real Sweeper sharing the same Redis
	// lease store and Postgres session store, holding no binding for the session.
	survivor := coordfixture.NewReplica("replica-2", tenant, pod, sessions, leases, ttl)

	// While replica-1's lease is live, replica-2's sweep skips the session on
	// ErrHeld: it never steals a live coordinator's lease.
	if held, err := survivor.Sweeper.Sweep(ctx); err != nil || held != 0 {
		t.Fatalf("pre-handoff survivor Sweep: held=%d err=%v, want 0 (replica-1's live lease is not stolen)", held, err)
	}
	if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-1" {
		t.Fatalf("pre-handoff lease holder = %+v err=%v, want replica-1", lease, err)
	}
	if pod.LastFenced(sessID) != 1 {
		t.Fatalf("pre-handoff pod fenced generation = %d, want 1", pod.LastFenced(sessID))
	}

	// replica-1 crashes: it stops sweeping and its Redis lease is gone. The
	// session is now a lapsed-lease still-running-pod orphan replica-2 adopts.
	if err := leases.Release(ctx, tenant, sessID, "replica-1"); err != nil {
		t.Fatalf("model replica-1 crash (lease gone): %v", err)
	}

	// replica-2's Sweeper adopts the orphan, bumps coordination_generation to
	// 2, re-adopts the still-running pod through the fence-first re-adopt, and
	// publishes the binding only after the fence acknowledged.
	held, err := survivor.Sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("takeover Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("takeover Sweep held = %d, want 1 (orphan adopted)", held)
	}
	got, _ = sessions.Get(ctx, tenant, sessID)
	if got.CoordinationGeneration != 2 {
		t.Fatalf("coordination_generation = %d, want 2 (handoff bumped once)", got.CoordinationGeneration)
	}
	if survivor.Readopter.Calls() != 1 || survivor.Readopter.Generations()[0] != 2 {
		t.Fatalf("fence calls = %v to generations %v, want one fence to generation 2", survivor.Readopter.Calls(), survivor.Readopter.Generations())
	}
	if !survivor.Bindings.Bound(sessID) {
		t.Fatalf("replica-2 did not publish the binding after the fence acknowledged")
	}
	if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-2" {
		t.Fatalf("post-handoff lease holder = %+v err=%v, want replica-2 (lease co-located with the binding)", lease, err)
	}

	// The pod is now fenced to the post-handoff generation.
	if pod.LastFenced(sessID) != 2 {
		t.Fatalf("post-handoff pod fenced generation = %d, want 2", pod.LastFenced(sessID))
	}

	// The split-brain fence: replica-1 is a stale coordinator, and its next
	// session-mutating RPC carries the pre-handoff generation 1. The pod
	// rejects it now that the generation advanced to 2.
	if !pod.StaleRPCRejected(ctx, sessID, 1) {
		t.Errorf("stale coordinator RPC at generation 1 was NOT rejected after the handoff advanced to 2 (split-brain)")
	}

	// The next survivor sweep observes the published binding and renews without
	// a second bump or fence, so the generation does not climb per sweep.
	if _, err := survivor.Sweeper.Sweep(ctx); err != nil {
		t.Fatalf("renew Sweep: %v", err)
	}
	got, _ = sessions.Get(ctx, tenant, sessID)
	if got.CoordinationGeneration != 2 {
		t.Errorf("coordination_generation = %d after renew sweep, want 2 (no re-bump per sweep)", got.CoordinationGeneration)
	}
	if survivor.Readopter.Calls() != 1 {
		t.Errorf("fence calls = %d after renew sweep, want 1 (fence fires once per handoff)", survivor.Readopter.Calls())
	}
}

// spec: §10.1.2 (the pod records and compares a fenced coordination generation
// per bound session), §10.1.8 (the barrier and the fence share that per-session
// gate), §4.2, §4.6.1 (coordinating replica holds the lease).
//
// diagnosis: a failure means a co-tenant session's coordinator handoff was
// refused by the generation another session on the same pod is fenced to. The
// survivor's fence for the lapsed session is rejected as
// coordinator_handoff_stale, the readopter relinquishes the lease, the Sweeper
// records an adoption backoff, and the session stays unadoptable while its pod
// keeps running. Re-check that the fenced generation is held on the session's
// slot registry entry in pkg/adapter/coordination.go rather than once per pod.
func TestCoTenantSessionHandoffIsNotFencedByItsNeighbour_spec_10_1_2(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	sessions := sessionpg.New(pg.Pool)
	ctx := context.Background()

	const tenant = "acme"
	seedTenant(t, pg, tenant)
	sessA := uuid.NewString()
	sessB := uuid.NewString()
	const ttl = 30 * time.Second

	// Two co-tenant sessions share one pod. Their coordination generations sit
	// far apart: sessA has been handed off repeatedly and stands at 7, sessB has
	// never been taken over and still carries a low value.
	for id, gen := range map[string]int64{sessA: 7, sessB: 2} {
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning,
			PodAssignment: "pod-cotenant", CoordinationGeneration: gen, CreatedAt: time.Unix(1, 0).UTC(),
		}); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	pod := coordfixture.StartPod(t, sessA)
	pod.StartSession(t, sessB)

	// replica-1 coordinates both sessions. Its at-bind fence for sessA records 7
	// on the pod; sessB is never fenced, because nothing fences a session that
	// starts normally and is never taken over.
	coordinator := coordfixture.NewReplica("replica-1", tenant, pod, sessions, leases, ttl, sessA, sessB)
	if _, err := pod.Fence(ctx, sessA, 7); err != nil {
		t.Fatalf("replica-1 at-bind fence of sessA to generation 7: %v", err)
	}
	if _, err := coordinator.Sweeper.Sweep(ctx); err != nil {
		t.Fatalf("replica-1 coordinating sweep: %v", err)
	}
	if pod.LastFenced(sessB) != 0 {
		t.Fatalf("pod holds fenced generation %d for the never-fenced co-tenant session, want 0", pod.LastFenced(sessB))
	}

	// replica-1 keeps coordinating sessA, so its lease on that session stays
	// live. Only sessB's lease lapses, which makes sessB alone a lapsed-lease
	// still-running-pod orphan.
	survivor := coordfixture.NewReplica("replica-2", tenant, pod, sessions, leases, ttl)
	if err := leases.Release(ctx, tenant, sessB, "replica-1"); err != nil {
		t.Fatalf("model the lapse of the co-tenant session's lease: %v", err)
	}

	// The survivor's Sweeper adopts sessB alone: sessA is skipped on ErrHeld
	// because replica-1 still holds its lease.
	held, err := survivor.Sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("co-tenant takeover Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("takeover Sweep held = %d, want 1 (only the lapsed co-tenant session is adopted)", held)
	}
	if n := survivor.Readopter.CalledFor(sessA); n != 0 {
		t.Fatalf("survivor re-adopted the live coordinator's session %d times, want 0", n)
	}

	// The handoff bumps only the adopted session's row and fences the pod for
	// that session alone. Against a pod-wide fenced generation the fence at 3 is
	// refused as coordinator_handoff_stale by the neighbour's 7.
	got, _ := sessions.Get(ctx, tenant, sessB)
	if got.CoordinationGeneration != 3 {
		t.Fatalf("co-tenant session coordination_generation = %d, want 3 (handoff bumped once from 2)", got.CoordinationGeneration)
	}
	res, ok := survivor.Readopter.FenceResult(sessB)
	if !ok {
		t.Fatalf("the survivor never fenced the adopted co-tenant session")
	}
	if !res.Accepted {
		t.Fatalf("the co-tenant session's fence at generation 3 was refused: %+v", res)
	}
	if res.GapDetected {
		t.Errorf("the co-tenant session's first fence within its binding reported a generation gap: %+v", res)
	}
	if res.LastFencedGeneration != 3 {
		t.Errorf("fence last_fenced_generation = %d, want 3", res.LastFencedGeneration)
	}
	if !survivor.Bindings.Bound(sessB) {
		t.Errorf("the survivor published no binding for the adopted co-tenant session, so its fence never acknowledged")
	}
	if pod.LastFenced(sessB) != 3 {
		t.Errorf("pod fenced generation for the adopted session = %d, want 3", pod.LastFenced(sessB))
	}

	// The neighbour is untouched: the pod still holds 7 for the session
	// replica-1 coordinates, and its row is unchanged.
	if pod.LastFenced(sessA) != 7 {
		t.Errorf("pod fenced generation for the live coordinator's session = %d, want 7 (a co-tenant fence must not move it)", pod.LastFenced(sessA))
	}
	gotA, _ := sessions.Get(ctx, tenant, sessA)
	if gotA.CoordinationGeneration != 7 {
		t.Errorf("live coordinator's session coordination_generation = %d, want 7", gotA.CoordinationGeneration)
	}
	if lease, err := leases.Get(ctx, tenant, sessA); err != nil || lease.Holder != "replica-1" {
		t.Errorf("live coordinator's lease = %+v err=%v, want replica-1", lease, err)
	}
}
