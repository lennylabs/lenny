// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos coverage for the §10.1 coordinator failover under an injected
// replica crash. Two replicas share a real Redis lease store and a shared
// session store, and a real in-process §4.7 adapter models the still-running
// pod. The crash is injected as a real Redis lease lapse: the coordinating
// replica's lease is acquired with a short TTL and left to expire, so the
// survivor's Sweeper observes a genuinely lapsed lease rather than an explicit
// release. The suite asserts the survivor adopts the lapsed lease, re-adopts
// the pod fence-first, publishes the serving binding on the acknowledgement and
// holds the connection so an idle taken-over session stays coordinated, that a
// dead held connection is evicted and re-adopted and re-fenced before the pod's
// hold-state self-termination, that a terminal fence failure relinquishes and
// backs off without climbing the generation on every sweep, and that a stale
// prior coordinator's RPC is rejected by the generation fence after the
// takeover.
package tier8_chaos_test

import (
	"context"
	"errors"
	"sync"
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

// waitForLapse polls the real Redis lease until it expires (ErrNotFound), so a
// caller injects a genuine coordinator crash as a TTL lapse rather than an
// explicit release. It fails the test if the lease has not lapsed within the
// deadline.
func waitForLapse(t *testing.T, leases leasestore.LeaseStore, tenant, sessID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := leases.Get(context.Background(), tenant, sessID); errors.Is(err, leasestore.ErrNotFound) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("lease for %s did not lapse within the deadline", sessID)
}

// spec: §10.1 (coordinator failover; CoordinatorFence; hold state on
// connection loss; relinquish-and-backoff; hold-state timeout), §4.6.1
// (coordinating replica holds the lease), §4.7 (single content consumer /
// Attach content stream), §4.2 line 156 (coordination_generation on handoff).
//
// diagnosis: a failure means the survivor did not recover the coordinator role
// on a real replica crash — the lapsed lease was not adopted and re-fenced, a
// dead held connection pinned the lease so the pod would self-terminate in hold
// state, a terminal fence failure climbed the generation on every sweep, or a
// stale coordinator was not fenced out after the takeover.
func TestCoordinatorFailoverCrashTakeover_spec_10_1(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	ctx := context.Background()
	const tenant = "acme"

	newSurvivor := func(sessions sessionstore.Store, bound *coordfixture.Bindings, readopter *coordfixture.FenceReadopter, opts func(*coordination.Options)) *coordination.Sweeper {
		o := coordination.Options{
			ReplicaID: "replica-2",
			TTL:       30 * time.Second,
			Interval:  time.Hour,
			Bindings:  bound,
			Readopter: readopter,
		}
		if opts != nil {
			opts(&o)
		}
		return coordination.NewSweeper(staticTenants{tenant}, sessions, leases, o)
	}

	// The survivor adopts a genuinely lapsed lease, re-adopts and re-fences the
	// still-running pod, publishes the binding, holds the connection so an idle
	// session stays coordinated, and fences a stale prior coordinator out.
	t.Run("survivor adopts lapsed lease, re-fences, and fences out the stale coordinator", func(t *testing.T) {
		const sessID = "crash-takeover"
		sessions := memstore.New()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateRunning,
			PodAssignment: "pod-" + sessID, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pod := coordfixture.StartPod(t, sessID)
		if _, err := pod.Fence(ctx, 1); err != nil {
			t.Fatalf("replica-1 initial fence: %v", err)
		}

		// Inject the crash: replica-1 holds the lease on a short TTL and dies,
		// so the lease lapses in real Redis.
		if _, err := leases.Acquire(ctx, tenant, sessID, "replica-1", time.Second); err != nil {
			t.Fatalf("replica-1 acquire: %v", err)
		}
		waitForLapse(t, leases, tenant, sessID)

		bound := coordfixture.NewBindings()
		readopter := &coordfixture.FenceReadopter{Pod: pod, Bindings: bound, Leases: leases, ReplicaID: "replica-2", TenantID: tenant}
		sw := newSurvivor(sessions, bound, readopter, nil)

		if held, err := sw.Sweep(ctx); err != nil || held != 1 {
			t.Fatalf("takeover Sweep: held=%d err=%v, want 1", held, err)
		}
		got, _ := sessions.Get(ctx, tenant, sessID)
		if got.CoordinationGeneration != 2 {
			t.Fatalf("coordination_generation = %d, want 2", got.CoordinationGeneration)
		}
		if pod.LastFenced() != 2 {
			t.Fatalf("pod fenced generation = %d, want 2", pod.LastFenced())
		}
		if lease, err := leases.Get(ctx, tenant, sessID); err != nil || lease.Holder != "replica-2" {
			t.Fatalf("lease holder = %+v err=%v, want replica-2", lease, err)
		}
		// The held connection keeps an idle taken-over session continuously
		// coordinated: the binding stays published and its channel alive
		// without any request, so the pod does not re-enter hold state and
		// self-terminate. spec: §10.1 (hold state on connection loss).
		if !bound.Bound(sessID) || !bound.ConnAlive(sessID) {
			t.Errorf("takeover did not hold the connection for the idle session (bound=%v alive=%v)", bound.Bound(sessID), bound.ConnAlive(sessID))
		}
		// The stale prior coordinator's RPC at the pre-handoff generation 1 is
		// rejected now that the generation advanced to 2.
		if !pod.StaleRPCRejected(ctx, 1) {
			t.Errorf("stale coordinator RPC at generation 1 was not fenced out after the takeover")
		}
	})

	// A dead held connection on a still-live survivor is evicted and the lease
	// released, so a subsequent sweep re-adopts and re-fences the pod before
	// its hold-state timeout and serves over the fresh binding.
	t.Run("dead held connection is evicted then re-adopted and re-fenced", func(t *testing.T) {
		const sessID = "dead-conn"
		sessions := memstore.New()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateRunning,
			PodAssignment: "pod-" + sessID, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pod := coordfixture.StartPod(t, sessID)
		if _, err := pod.Fence(ctx, 1); err != nil {
			t.Fatalf("initial fence: %v", err)
		}
		bound := coordfixture.NewBindings()
		readopter := &coordfixture.FenceReadopter{Pod: pod, Bindings: bound, Leases: leases, ReplicaID: "replica-2", TenantID: tenant}
		sw := newSurvivor(sessions, bound, readopter, nil)

		// First takeover establishes the serving binding at generation 2.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first takeover Sweep: %v", err)
		}
		if !bound.Bound(sessID) || pod.LastFenced() != 2 {
			t.Fatalf("first takeover incomplete: bound=%v fenced=%d", bound.Bound(sessID), pod.LastFenced())
		}

		// The held gateway-to-pod channel dies.
		bound.KillConn(sessID)

		// The next sweep probes the dead channel, evicts the binding, and
		// releases the lease rather than renewing it.
		if held, err := sw.Sweep(ctx); err != nil || held != 0 {
			t.Fatalf("dead-connection Sweep: held=%d err=%v, want 0 (lease released, not renewed)", held, err)
		}
		if bound.Evicted(sessID) != 1 {
			t.Fatalf("dead binding evictions = %d, want 1", bound.Evicted(sessID))
		}
		if _, err := leases.Get(ctx, tenant, sessID); !errors.Is(err, leasestore.ErrNotFound) {
			t.Fatalf("lease still held after dead-connection eviction (err=%v), want released", err)
		}

		// A subsequent sweep re-adopts the still-running pod, re-fences it to
		// the next generation, and re-publishes the serving binding, so serving
		// resumes over the fresh binding before the 120s hold-state timeout.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("re-adopt Sweep: %v", err)
		}
		if !bound.Bound(sessID) {
			t.Fatalf("dead-connection session was not re-adopted onto a fresh binding")
		}
		if pod.LastFenced() != 3 {
			t.Fatalf("re-adopt pod fenced generation = %d, want 3 (re-fenced after the dead-connection eviction)", pod.LastFenced())
		}
		got, _ := sessions.Get(ctx, tenant, sessID)
		if got.CoordinationGeneration != 3 {
			t.Errorf("coordination_generation = %d, want 3", got.CoordinationGeneration)
		}
	})

	// A terminal fence failure relinquishes the lease and the survivor records
	// a per-session adoption backoff, so the fixed sweep does not re-adopt
	// inside the window and the generation does not climb on every sweep.
	t.Run("terminal fence failure relinquishes and backs off without climbing the generation", func(t *testing.T) {
		const sessID = "fence-fails"
		sessions := memstore.New()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateRunning,
			PodAssignment: "pod-" + sessID, CreatedAt: time.Unix(1, 0).UTC(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pod := coordfixture.StartPod(t, sessID)
		// The re-adopt fence fails terminally for this session (the readopter
		// relinquishes the real Redis lease and returns an error).
		readopter := &coordfixture.FenceReadopter{
			Pod: pod, Bindings: coordfixture.NewBindings(), Leases: leases,
			ReplicaID: "replica-2", TenantID: tenant, Fail: map[string]bool{sessID: true},
		}
		now := time.Unix(3000, 0).UTC()
		var clockMu sync.Mutex
		clock := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
		advance := func(d time.Duration) { clockMu.Lock(); defer clockMu.Unlock(); now = now.Add(d) }

		sw := newSurvivor(sessions, coordfixture.NewBindings(), readopter, func(o *coordination.Options) {
			o.AdoptionBackoff = 10 * time.Second
			o.Clock = clock
		})

		// First sweep: adopt, bump to 1, fence fails, lease relinquished, backoff set.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, tenant, sessID)
		if got.CoordinationGeneration != 1 {
			t.Fatalf("generation = %d, want 1 (bump stays after relinquish)", got.CoordinationGeneration)
		}
		if _, err := leases.Get(ctx, tenant, sessID); !errors.Is(err, leasestore.ErrNotFound) {
			t.Fatalf("lease still held after terminal fence relinquish (err=%v), want released", err)
		}
		if readopter.Calls() != 1 {
			t.Fatalf("fence calls = %d, want 1", readopter.Calls())
		}

		// Inside the backoff window: no re-adopt, no re-bump, no re-fence.
		advance(5 * time.Second)
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("in-window Sweep: %v", err)
		}
		got, _ = sessions.Get(ctx, tenant, sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("generation = %d inside backoff window, want 1 (no per-sweep climb)", got.CoordinationGeneration)
		}
		if readopter.Calls() != 1 {
			t.Errorf("fence calls = %d inside backoff window, want 1", readopter.Calls())
		}

		// After the window elapses: re-adopted, generation climbs once more.
		advance(10 * time.Second)
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("post-window Sweep: %v", err)
		}
		got, _ = sessions.Get(ctx, tenant, sessID)
		if got.CoordinationGeneration != 2 {
			t.Errorf("generation = %d after the backoff elapsed, want 2 (re-adopted once)", got.CoordinationGeneration)
		}
		if readopter.Calls() != 2 {
			t.Errorf("fence calls = %d after the backoff elapsed, want 2", readopter.Calls())
		}
	})
}
