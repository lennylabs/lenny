//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration coverage for the §10.1 coordination_generation
// split-brain fence across two gateway replicas. Two coordination Sweepers
// (two replica ids) share a real Redis lease store and a shared session store,
// and a real in-process §4.7 adapter models the still-running pod. The test
// drives a coordinator handoff — the coordinating replica's Redis lease lapses,
// the survivor's Sweeper adopts the orphan, bumps coordination_generation, and
// re-fences the pod — then asserts the previous coordinator's next
// session-mutating RPC is rejected by the pod's generation fence once the
// generation advanced. This builds the two-replica coordination harness the
// TEST-GAPS.md T-4.2.4 finding requires, modeling the two replicas as two
// Sweepers over shared Redis rather than in-process fakes, so the fence is the
// real adapter fence and the lease handoff is a real cross-replica Redis lease.
package tier4_integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/coordfixture"
)

// staticTenants is a coordination.TenantLister over a fixed slice.
type staticTenants []string

func (s staticTenants) ListTenants(context.Context) ([]string, error) { return []string(s), nil }

// spec: §10.1 (coordination_generation split-brain fence; coordinator handoff
// re-adopts the still-running pod; a stale coordinator's RPC is rejected),
// §4.2 line 156 (coordination_generation incremented on coordinator handoff
// across gateway replicas), §4.6.1 (coordinating replica holds the lease).
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
	sessions := memstore.New()
	ctx := context.Background()

	const tenant = "acme"
	const sessID = "split-brain"
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
	if _, err := pod.Fence(ctx, 1); err != nil {
		t.Fatalf("replica-1 initial fence to generation 1: %v", err)
	}

	// replica-1 holds the lease and the binding (co-located from bind time).
	if _, err := leases.Acquire(ctx, tenant, sessID, "replica-1", ttl); err != nil {
		t.Fatalf("replica-1 at-bind acquire: %v", err)
	}
	bound1 := coordfixture.NewBindings()
	bound1.Publish(sessID)

	// replica-2's Sweeper shares the same Redis lease store and session store
	// and holds no binding for the session yet.
	bound2 := coordfixture.NewBindings()
	readopter := &coordfixture.FenceReadopter{
		Pod: pod, Bindings: bound2, Leases: leases, ReplicaID: "replica-2", TenantID: tenant,
	}
	sweep2 := coordination.NewSweeper(staticTenants{tenant}, sessions, leases, coordination.Options{
		ReplicaID: "replica-2",
		TTL:       ttl,
		Interval:  time.Hour,
		Bindings:  bound2,
		Readopter: readopter,
	})

	// Before the handoff replica-2 holds no binding, and while replica-1's
	// lease is live replica-2's sweep skips the session on ErrHeld: it never
	// steals a live coordinator's lease.
	if held, err := sweep2.Sweep(ctx); err != nil || held != 0 {
		t.Fatalf("pre-handoff Sweep: held=%d err=%v, want 0 (replica-1's live lease is not stolen)", held, err)
	}
	if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-1" {
		t.Fatalf("pre-handoff lease holder = %+v err=%v, want replica-1", lease, err)
	}
	if pod.LastFenced() != 1 {
		t.Fatalf("pre-handoff pod fenced generation = %d, want 1", pod.LastFenced())
	}

	// replica-1 crashes: its Redis lease lapses (modeled by the lease being
	// released on its TTL). The session is now a lapsed-lease still-running-pod
	// orphan replica-2 does not bind.
	if err := leases.Release(ctx, tenant, sessID, "replica-1"); err != nil {
		t.Fatalf("model replica-1 crash (lease lapse): %v", err)
	}

	// replica-2's Sweeper adopts the orphan, bumps coordination_generation to
	// 2, re-adopts the still-running pod through the fence-first re-adopt, and
	// publishes the binding only after the fence acknowledged.
	held, err := sweep2.Sweep(ctx)
	if err != nil {
		t.Fatalf("takeover Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("takeover Sweep held = %d, want 1 (orphan adopted)", held)
	}
	got, _ := sessions.Get(ctx, tenant, sessID)
	if got.CoordinationGeneration != 2 {
		t.Fatalf("coordination_generation = %d, want 2 (handoff bumped once)", got.CoordinationGeneration)
	}
	if readopter.Calls() != 1 || readopter.Generations()[0] != 2 {
		t.Fatalf("fence calls = %v to generations %v, want one fence to generation 2", readopter.Calls(), readopter.Generations())
	}
	if !bound2.Bound(sessID) {
		t.Fatalf("replica-2 did not publish the binding after the fence acknowledged")
	}
	if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-2" {
		t.Fatalf("post-handoff lease holder = %+v err=%v, want replica-2 (lease co-located with the binding)", lease, err)
	}

	// The pod is now fenced to the post-handoff generation.
	if pod.LastFenced() != 2 {
		t.Fatalf("post-handoff pod fenced generation = %d, want 2", pod.LastFenced())
	}

	// The split-brain fence: replica-1 is a stale coordinator, and its next
	// session-mutating RPC carries the pre-handoff generation 1. The pod
	// rejects it now that the generation advanced to 2.
	if !pod.StaleRPCRejected(ctx, 1) {
		t.Errorf("stale coordinator RPC at generation 1 was NOT rejected after the handoff advanced to 2 (split-brain)")
	}

	// The next sweep observes the published binding and renews without a
	// second bump or fence, so the generation does not climb per sweep.
	if _, err := sweep2.Sweep(ctx); err != nil {
		t.Fatalf("renew Sweep: %v", err)
	}
	got, _ = sessions.Get(ctx, tenant, sessID)
	if got.CoordinationGeneration != 2 {
		t.Errorf("coordination_generation = %d after renew sweep, want 2 (no re-bump per sweep)", got.CoordinationGeneration)
	}
	if readopter.Calls() != 1 {
		t.Errorf("fence calls = %d after renew sweep, want 1 (fence fires once per handoff)", readopter.Calls())
	}
}
