// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration scenarios for the resume driver, coordinator adoption,
// idle suspension, and held-message delivery. Each scenario runs two or more
// gateway session servers over the compose Postgres session store, one
// coordination lease store, one credential lease store, and the session
// DLQ, against a Kubernetes API server holding the warm pool, with an
// in-process adapter and a recording runtime behind each pod. The scenarios
// assert their own outcome and run no invariant checker.
//
// spec: §7.3 (Resume driver), §10.1.1 (Stateless Replicas and Per-Session
// Coordination), §7.2 (Interactive Session Model), §6.2 (Pod State Machine)
package tier4_integration_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// recoveryPasses bounds the checker passes a scenario waits for a recovery.
const recoveryPasses = 40

// newCompose builds a harness over the compose Postgres session store.
func newCompose(t *testing.T, replicas, pods int) *lifecycleharness.Harness {
	t.Helper()
	newStore, _ := lifecycleharness.SharedPostgresStore(t, &harnessPostgres)
	return lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: replicas, Pods: pods, CredentialPool: true, NewStore: newStore,
		ControlPlane: &harnessControlPlane,
	})
}

// leaseHolder returns the replica that holds the session's coordination
// lease, or "" when no lease is held.
func leaseHolder(t *testing.T, h *lifecycleharness.Harness, id string) string {
	t.Helper()
	lease, err := h.Leases.Get(context.Background(), lifecycleharness.Tenant, id)
	if errors.Is(err, leasestore.ErrNotFound) {
		return ""
	}
	if err != nil {
		t.Fatalf("read the lease of %s: %v", id, err)
	}
	return lease.Holder
}

// spec: 7.3 (Resume driver), 10.1.1 (Stateless Replicas and Per-Session
// Coordination)
// diagnosis: a crashed runtime's session did not return to running without
// a client call, so the resume driver on the coordinating replica did not
// pick up the resume_pending row; or a POST /resume served by a replica that
// does not coordinate the session did not lead to running, so the call is
// still a synchronous restore on the serving replica rather than a store
// write the coordinator's driver acts on.
func TestResumeDriverRestoresWithoutAClientCall_spec_7_3(t *testing.T) {
	h := newCompose(t, 2, 16)
	coordinator, peer := h.Replicas[0], h.Replicas[1]

	t.Run("pod crash", func(t *testing.T) {
		id := h.StartSession(coordinator)
		before := h.Row(id)
		lifecycleharness.Report(t, coordinator, id, string(session.FailureRuntimeCrash))
		if st := h.Row(id).State; st != session.StateResumePending && st != session.StateResuming && st != session.StateRunning {
			t.Fatalf("after the crash report the session is %q, want it recovering", st)
		}
		row := h.AwaitState(id, session.StateRunning, recoveryPasses)
		if row.State != session.StateRunning || row.RecoveryGeneration <= before.RecoveryGeneration || row.RetryCount != before.RetryCount+1 {
			t.Fatalf("after the crash the session is %q at recovery generation %d with %d retries, want running after one restore (was generation %d, %d retries)",
				row.State, row.RecoveryGeneration, row.RetryCount, before.RecoveryGeneration, before.RetryCount)
		}
		if code, body := h.Send(coordinator, id, "after-crash"); code != http.StatusOK {
			t.Fatalf("message after the restore = %d %v", code, body)
		}
	})

	t.Run("POST /resume on the peer", func(t *testing.T) {
		id := h.StartSession(coordinator)
		for i := 0; i <= lifecycleharness.MaxRetries; i++ {
			lifecycleharness.Report(t, coordinator, id, string(session.FailureRuntimeCrash))
			h.AwaitState(id, session.StateRunning, recoveryPasses)
		}
		if st := h.Row(id).State; st != session.StateAwaitingClientAction {
			t.Fatalf("after the retry budget the session is %q, want awaiting_client_action", st)
		}
		code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
		if code != http.StatusOK || body["state"] != string(session.StateResumePending) {
			t.Fatalf("POST /resume on the peer = %d %v, want 200 resume_pending", code, body)
		}
		if row := h.AwaitState(id, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
	})
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 7.3
// (Resume driver)
// diagnosis: a resume_pending session whose coordinating replica died, or
// whose lease key was deleted under a live coordinator, was not adopted and
// restored by the peer; or the original coordinator kept acting on the
// session after it lost the lease: it renewed the lease, bound the session,
// or left a pod claimed.
func TestPeerAdoptsAResumePendingSession_spec_10_1_1(t *testing.T) {
	t.Run("holder killed", func(t *testing.T) {
		h := newCompose(t, 2, 0)
		holder, peer := h.Replicas[0], h.Replicas[1]
		h.Cluster.AddIdlePod(t)
		id := h.StartSession(holder)
		// No spare pod: the holder's restore cannot claim, so the row
		// stays resume_pending until the holder is gone.
		lifecycleharness.Report(t, holder, id, string(session.FailureRuntimeCrash))
		if st := h.Row(id).State; st != session.StateResumePending {
			t.Fatalf("after the crash with no spare pod the session is %q, want resume_pending", st)
		}
		h.Crash(holder)
		h.Cluster.AddIdlePod(t)
		row := h.AwaitState(id, session.StateRunning, recoveryPasses)
		if row.State != session.StateRunning {
			t.Fatalf("the peer left the session %q, want running", row.State)
		}
		if got := leaseHolder(t, h, id); got != peer.ID {
			t.Errorf("lease holder = %q, want the peer %q", got, peer.ID)
		}
		if h.Bound(id) != peer {
			t.Error("the peer does not bind the restored session")
		}
	})

	t.Run("live holder's lease key deleted", func(t *testing.T) {
		h := newCompose(t, 2, 0)
		holder, peer := h.Replicas[0], h.Replicas[1]
		h.Cluster.AddIdlePod(t)
		id := h.StartSession(holder)
		lifecycleharness.Report(t, holder, id, string(session.FailureRuntimeCrash))
		if st := h.Row(id).State; st != session.StateResumePending {
			t.Fatalf("after the crash with no spare pod the session is %q, want resume_pending", st)
		}
		h.Redis.Del(leasestore.Key(lifecycleharness.Tenant, id))
		if got := leaseHolder(t, h, id); got != "" {
			t.Fatalf("lease still held by %q after deleting its key; the key layout changed", got)
		}
		h.Cluster.AddIdlePod(t)
		// The peer's Sweeper reaches the unheld lease first; the holder's
		// Sweeper then finds its lease taken and must not act.
		ctx := context.Background()
		if _, err := peer.Sweeper.Sweep(ctx); err != nil {
			t.Fatalf("peer sweep: %v", err)
		}
		if got := leaseHolder(t, h, id); got != peer.ID {
			t.Fatalf("after the peer's sweep the lease holder is %q, want the peer %q", got, peer.ID)
		}
		if _, err := holder.Sweeper.Sweep(ctx); err != nil {
			t.Fatalf("holder sweep: %v", err)
		}
		row := h.AwaitState(id, session.StateRunning, recoveryPasses)
		if row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
		for i := 0; i < 3; i++ {
			h.Pass()
		}
		if got := leaseHolder(t, h, id); got != peer.ID {
			t.Errorf("lease holder = %q, want the peer %q: the original holder re-took the lease", got, peer.ID)
		}
		if _, ok := holder.Registry.Get(id); ok {
			t.Error("the original holder still binds the session")
		}
		claims, err := h.Cluster.BoundClaims(context.Background())
		if err != nil {
			t.Fatalf("list claims: %v", err)
		}
		if len(claims) != 1 || claims[0] != h.Row(id).PodAssignment {
			t.Errorf("bound claims = %v, want only the peer's pod %q", claims, h.Row(id).PodAssignment)
		}
	})
}
