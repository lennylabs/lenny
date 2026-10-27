// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/bindrelease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// erasureUser is the user every harness session belongs to.
const erasureUser = "alice@acme.com"

// replicaForceTerminator is the drain's §24.11 forced terminal transition
// run on one replica, as the gateway's session-admin adapter runs it: it
// opens the replica's terminal-writer claim, writes failed with
// FORCE_TERMINATED under the row lock, and runs the replica's terminal
// hook for the transition it made.
type replicaForceTerminator struct {
	h *lifecycleharness.Harness
	r *lifecycleharness.Replica
}

func (f replicaForceTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	ctx = context.WithoutCancel(ctx)
	defer f.r.Server.BeginTerminal(id)()
	var prev session.State
	transitioned := false
	row, err := f.h.Store.Update(ctx, tenantID, id, func(s *sessionstore.Session) error {
		prev = s.State
		if session.IsTerminal(s.State) {
			return nil
		}
		s.State, s.FailureClass, s.FailureReason = session.StateFailed, session.FailureClassRuntime, "FORCE_TERMINATED"
		sessionstore.MarkTerminated(s, prev, f.h.Clock.Now())
		transitioned = true
		return nil
	})
	if err != nil || !transitioned {
		return row, prev, false, err
	}
	f.r.Server.OnSessionTerminal(ctx, prev, row)
	return row, prev, true, nil
}

// leaseCredential leases one credential from the harness pool for id at
// its row's coordination generation, as a bind with a provider does.
func leaseCredential(t *testing.T, h *lifecycleharness.Harness, id string) {
	t.Helper()
	if _, err := h.Creds.Assign(lifecycleharness.ProxyPool().Name, id, "", lifecycleharness.Tenant, h.Row(id).CoordinationGeneration); err != nil {
		t.Fatalf("lease a credential for %s: %v", id, err)
	}
}

// requireBound fails unless r's registry holding a binding for id equals want.
func requireBound(t *testing.T, r *lifecycleharness.Replica, id string, want bool) {
	t.Helper()
	if _, ok := r.Registry.Get(id); ok != want {
		t.Errorf("replica %s holds a binding for %s = %v, want %v", r.Name, id, ok, want)
	}
}

// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §11.4 (User Invalidation), §28.5.7
// (Gateway-to-store)
// diagnosis: the user-erasure drain ran on replica a, whose forced transition
// wrote the terminal state of a session bound on replica b, and b kept the
// binding, the pod's slot member, or the credential lease, because the drain
// did not hand the sessions it transitioned to the binding-release fan-out,
// or the fan-out did not reach b.
func TestUserErasureDrainReleasesABindingHeldOnThePeer_spec_12_8(t *testing.T) {
	ctx := context.Background()
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	a, b := h.Replicas[0], h.Replicas[1]
	onA, onB := h.StartSession(a), h.StartSession(b)
	pods := map[string]string{onA: h.Row(onA).PodAssignment, onB: h.Row(onB).PodAssignment}
	for id := range pods {
		leaseCredential(t, h, id)
	}

	res, err := admin.DrainSessions(ctx, h.Store, replicaForceTerminator{h: h, r: a},
		harnessPodTerminator{h: h, handling: a}, lifecycleharness.Tenant, erasureUser)
	if err != nil {
		t.Fatalf("erasure drain on replica a: %v (result %+v)", err, res)
	}
	if res.Terminated != 2 {
		t.Errorf("the drain transitioned %d sessions, want 2", res.Terminated)
	}

	// The fan-out released the peer's binding before the drain returned,
	// with no Sweeper pass in between.
	requireBound(t, b, onB, false)
	requireBound(t, a, onA, false)
	if n := len(h.CredLeases.LeasesBySession([]string{onA, onB})); n != 0 {
		t.Errorf("%d credential leases remain after the erasure drain, want none", n)
	}

	h.RunToQuiescence()
	for id, pod := range pods {
		if st := h.Row(id).State; st != session.StateFailed {
			t.Errorf("session %s = %s, want failed", id, st)
		}
		if n := h.Slots.Removals(pod, id); n != 1 {
			t.Errorf("removals of %s's member on %s = %d, want 1", id, pod, n)
		}
		requireMembers(t, h, pod)
	}
	h.Check(t)
}

// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §28.5.7 (Gateway-to-store)
// diagnosis: an erasure drain with no binding-release fan-out released the
// binding replica b holds, so the release the fan-out case observes does not
// come from the fan-out and that case asserts nothing.
func TestUserErasureDrainWithoutTheFanOutLeavesThePeerBinding_spec_12_8(t *testing.T) {
	ctx := context.Background()
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	a, b := h.Replicas[0], h.Replicas[1]
	onB := h.StartSession(b)
	leaseCredential(t, h, onB)

	if _, err := admin.DrainSessions(ctx, h.Store, replicaForceTerminator{h: h, r: a}, nil,
		lifecycleharness.Tenant, erasureUser); err != nil {
		t.Fatalf("erasure drain on replica a: %v", err)
	}
	requireBound(t, b, onB, true)
	if n := len(h.CredLeases.LeasesBySession([]string{onB})); n != 1 {
		t.Errorf("%d credential leases remain for %s, want its lease kept until a release", n, onB)
	}
}

// replicaReleaser is one replica's CH-BINDRELEASE subscriber-side releaser:
// it releases, through the replica's revoked-binding release, the bindings
// the replica holds among a request's sessions.
type replicaReleaser struct{ r *lifecycleharness.Replica }

func (l replicaReleaser) TerminateLocal(ctx context.Context, req bindrelease.Request) bindrelease.Result {
	res := terminateOn(ctx, l.r, req.TenantID, req.SessionIDs)
	return bindrelease.Result{PodsTerminated: res.PodsTerminated, FailedSessions: res.FailedSessions}
}

// spec: §28.5.7 (Gateway-to-store), §11.4 (User Invalidation)
// diagnosis: a CH-BINDRELEASE request published on replica a and naming two
// sessions bound on replica b, one whose primary row is terminal and one
// whose primary row is running, released the running session's binding or
// lease, or left the terminal session's binding or lease on b.
func TestBindReleaseSubscriberReleasesOnlyTerminalBindings_spec_28_5_7(t *testing.T) {
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	a, b := h.Replicas[0], h.Replicas[1]
	done, live := h.StartSession(b), h.StartSession(b)
	leaseCredential(t, h, done)
	leaseCredential(t, h, live)
	cancelOffCoordinator(t, h, done)

	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	propA := bindrelease.New(replicaReleaser{r: a}, pubsub.New(clientA), a.ID)
	propB := bindrelease.New(replicaReleaser{r: b}, pubsub.New(clientB), b.ID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go propB.Run(ctx)

	// A publish that precedes the subscription is dropped, so publish
	// until b's subscriber has released the terminal session's binding.
	req := bindrelease.Request{TenantID: lifecycleharness.Tenant, UserID: erasureUser, SessionIDs: []string{done, live}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		propA.Publish(ctx, req)
		if _, ok := b.Registry.Get(done); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica b never released the binding of terminal session %s", done)
		}
		time.Sleep(20 * time.Millisecond)
	}

	requireBound(t, b, live, true)
	if n := len(h.CredLeases.LeasesBySession([]string{done})); n != 0 {
		t.Errorf("%d credential leases remain for terminal session %s, want its lease returned", n, done)
	}
	if n := len(h.CredLeases.LeasesBySession([]string{live})); n != 1 {
		t.Errorf("%d credential leases remain for running session %s, want 1", n, live)
	}
	if st := h.Row(live).State; session.IsTerminal(st) {
		t.Errorf("running session %s = %s after the release request, want it live", live, st)
	}
}
