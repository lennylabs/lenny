// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a local concurrency coverage for the §10.1 co-location invariant under
// a concurrent coordinator handoff. Two replicas share an in-process
// thread-safe lease store and session store, a real in-process §4.7 adapter
// models the still-running pod, and the survivor's Sweeper runs concurrently
// with request-path probes. A second session starts running with a pod still
// assigned, so the survivor's takeover sweep observes it as an adoptable orphan
// in its List snapshot; a lease-acquire hook then transitions it to a terminal
// state during the takeover, in the exact window between the sweep's adopt
// decision and the atomic handoff bump. The §10.1 fail-closed guard must refuse
// the bump so a session that goes terminal during takeover is not resurrected.
// The test asserts, under -race, that the lease holder holds the binding it
// re-established at takeover, that a request landing in the pre-publish window
// fails closed rather than serving over an unpublished binding, and that the
// session that went terminal during takeover is never re-adopted (its readopter
// is never called and its coordination_generation is never bumped).
package tier7a_load_local_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/coordfixture"
)

// syncLeases is a concurrency-safe in-memory leasestore.LeaseStore for the
// race test: Acquire is holder-fenced (a held lease is re-acquired only by its
// current holder), matching the real leasestore contract the co-location
// invariant relies on.
type syncLeases struct {
	mu      sync.Mutex
	holders map[string]string
	// afterAcquire, when set, runs after a successful Acquire, with the lock
	// released. It lets a test inject a concurrent session-state transition in
	// the narrow window between the sweep's adopt decision (which acquires the
	// lease) and the atomic handoff bump that follows, so the fail-closed
	// terminal guard is driven deterministically rather than raced for.
	afterAcquire func(tenantID, sessionID, holder string)
}

func newSyncLeases() *syncLeases { return &syncLeases{holders: map[string]string{}} }

func slk(t, s string) string { return t + "/" + s }

func (l *syncLeases) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	l.mu.Lock()
	k := slk(tenantID, sessionID)
	if cur, ok := l.holders[k]; ok && cur != holder {
		l.mu.Unlock()
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	l.holders[k] = holder
	hook := l.afterAcquire
	l.mu.Unlock()
	if hook != nil {
		hook(tenantID, sessionID, holder)
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (l *syncLeases) Renew(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (l *syncLeases) Release(_ context.Context, tenantID, sessionID, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := slk(tenantID, sessionID)
	if cur, ok := l.holders[k]; ok && cur == holder {
		delete(l.holders, k)
	}
	return nil
}

func (l *syncLeases) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.holders[slk(tenantID, sessionID)]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (l *syncLeases) holder(tenantID, sessionID string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.holders[slk(tenantID, sessionID)]
	return h, ok
}

func (l *syncLeases) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (l *syncLeases) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

// staticTenants is a coordination.TenantLister over a fixed slice.
type staticTenants []string

func (s staticTenants) ListTenants(context.Context) ([]string, error) { return []string(s), nil }

// spec: §10.1 (coordinator handoff re-adopts the still-running pod; no
// operational RPC before the fence acknowledges), §4.6.1 (coordinating replica
// holds the lease), §4.7 (single content consumer / Attach content stream).
//
// diagnosis: a failure means the co-location invariant broke under a concurrent
// handoff — the lease holder served without the re-established binding, a
// request served over an unpublished pre-fence binding, or a terminal session
// was resurrected — or the Sweeper's takeover path is not race-clean.
func TestColocationInvariantUnderConcurrentHandoff_spec_10_1(t *testing.T) {
	const tenant = "acme"
	const live = "live"
	const terminating = "terminating"
	const ttl = 30 * time.Second

	leases := newSyncLeases()
	sessions := memstore.New()
	ctx := context.Background()

	if err := sessions.Create(ctx, sessionstore.Session{
		ID: live, TenantID: tenant, State: session.StateRunning,
		PodAssignment: "pod-" + live, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed live session: %v", err)
	}
	// A second session starts running with a pod still assigned, so the
	// survivor's sweep observes it as an adoptable orphan in its List snapshot.
	// A lease-acquire hook (installed below) transitions it to a terminal state
	// during the takeover, in the window between the sweep's adopt decision and
	// the atomic handoff bump, so the §10.1 fail-closed guard must refuse the
	// bump and the survivor must not resurrect it. This drives the concurrent
	// terminal-transition path rather than a statically terminal row that the
	// per-row terminal skip short-circuits before adoption is ever attempted.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: terminating, TenantID: tenant, State: session.StateRunning,
		PodAssignment: "pod-" + terminating, CoordinationGeneration: 1, CreatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed running session: %v", err)
	}

	// When the survivor acquires the second session's lease (the adopt decision
	// of its takeover), transition the session terminal before the handoff bump
	// runs on it. Fired at most once via sync.Once: after the transition the row
	// is terminal, so subsequent sweeps short-circuit it before Acquire.
	var termOnce sync.Once
	leases.afterAcquire = func(_, sessionID, holder string) {
		if sessionID != terminating || holder != "replica-2" {
			return
		}
		termOnce.Do(func() {
			if _, err := sessions.Update(ctx, tenant, terminating, func(row *sessionstore.Session) error {
				row.State = session.StateCompleted
				return nil
			}); err != nil {
				t.Errorf("transition second session terminal during takeover: %v", err)
			}
		})
	}

	pod := coordfixture.StartPod(t, live)
	if _, err := pod.Fence(ctx, live, 1); err != nil {
		t.Fatalf("initial fence: %v", err)
	}

	// replica-1 crashed before the test window: its lease already lapsed, so
	// the live session is a lapsed-lease orphan replica-2 will take over.
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

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Takeover driver: sweep replica-2 repeatedly until the takeover publishes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := sweep2.Sweep(ctx); err != nil {
				t.Errorf("concurrent Sweep: %v", err)
				return
			}
			if bound2.Bound(live) {
				return
			}
		}
	}()

	// Request-path probes: a request serves only over a published binding. In
	// the pre-publish window Bound is false and the request fails closed
	// (retries). Whenever it observes a bound session (a served request), the
	// co-location invariant must hold: replica-2 holds the lease.
	var failClosed, served int64
	var probeMu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if bound2.Bound(live) {
					h, ok := leases.holder(tenant, live)
					if !ok || h != "replica-2" {
						t.Errorf("served request over a binding held by replica-2 but lease holder = %q ok=%v (co-location broken)", h, ok)
					}
					probeMu.Lock()
					served++
					probeMu.Unlock()
				} else {
					probeMu.Lock()
					failClosed++
					probeMu.Unlock()
				}
			}
		}()
	}

	// Let the concurrent window run, then quiesce.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !bound2.Bound(live) {
		time.Sleep(2 * time.Millisecond)
	}
	// Give the probes a moment to observe the published binding.
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The takeover published the binding and the lease is co-located.
	if !bound2.Bound(live) {
		t.Fatalf("takeover did not publish the binding within the window")
	}
	if h, ok := leases.holder(tenant, live); !ok || h != "replica-2" {
		t.Fatalf("post-handoff lease holder = %q ok=%v, want replica-2", h, ok)
	}
	if pod.LastFenced(live) != 2 {
		t.Fatalf("pod fenced generation = %d, want 2 (fenced to the post-handoff generation)", pod.LastFenced(live))
	}
	got, _ := sessions.Get(ctx, tenant, live)
	if got.CoordinationGeneration != 2 {
		t.Fatalf("coordination_generation = %d, want 2 (handoff bumped once)", got.CoordinationGeneration)
	}

	// The session that went terminal during takeover was not resurrected: the
	// survivor observed it as an adoptable orphan and acquired its lease, but
	// the acquire-hook transition made it terminal before the atomic handoff
	// bump, so the §10.1 guard refused the bump and the survivor released the
	// lease and skipped the re-adopt. The readopter was therefore never called
	// for it, its coordination_generation was never bumped, and it holds neither
	// a lease nor a binding. The state assertion below (terminal) proves the
	// acquire-hook fired, which only happens when the survivor reached the adopt
	// decision and acquired the lease, so the guard was genuinely exercised
	// rather than the session being skipped before adoption was attempted.
	// Asserting the readopter call count and the generation directly pins the
	// fail-closed terminal guard.
	if n := readopter.CalledFor(terminating); n != 0 {
		t.Errorf("readopter called %d times for the session that went terminal during takeover, want 0 (must not be resurrected)", n)
	}
	gotTerm, _ := sessions.Get(ctx, tenant, terminating)
	if gotTerm.State != session.StateCompleted {
		t.Errorf("second session state = %q, want %q (the takeover transition should have landed)", gotTerm.State, session.StateCompleted)
	}
	if gotTerm.CoordinationGeneration != 1 {
		t.Errorf("session coordination_generation = %d, want 1, the §4.2 baseline (handoff bump refused after it went terminal)", gotTerm.CoordinationGeneration)
	}
	if _, ok := leases.holder(tenant, terminating); ok {
		t.Errorf("session that went terminal during takeover still holds a coordination lease, want none (released after the refused bump)")
	}
	if bound2.Bound(terminating) {
		t.Errorf("session that went terminal during takeover was bound by the survivor, want unbound (not resurrected)")
	}

	// The pre-publish window fails closed: the probes saw at least one
	// fail-closed observation before the binding was published, and every
	// served observation held the co-location invariant (asserted inline).
	probeMu.Lock()
	fc, sv := failClosed, served
	probeMu.Unlock()
	if fc == 0 {
		t.Errorf("no fail-closed observation recorded; the pre-publish window was not exercised")
	}
	if sv == 0 {
		t.Errorf("no served observation recorded after the takeover published the binding")
	}
}

// coTenantSessions are the two sessions the co-location cases below bind to one
// pod. The identifiers are ordered so the pod-level op lock's lexicographic
// promotion pick is deterministic, which keeps a failure readable; the cases
// assert nothing about which barrier returns first.
const (
	coTenantA = "cotenant-a"
	coTenantB = "cotenant-b"
)

// barrierAckDeadline stands in for the gateway's
// checkpointBarrierAckTimeoutSeconds: the single wall-clock window a drain
// barrier is bounded by, at the default §10.1.8 fixes for it. Each ack must
// land well inside it, so a barrier held open by a co-tenant session's gate
// shows up as a case failure rather than as a timeout the harness reports as a
// hang.
const barrierAckDeadline = 90 * time.Second

// barrierAckWellInside is the budget an ack that is not blocked on a co-tenant
// session's gate lands within. A barrier whose gate was replaced by its
// neighbour's blocks until the deadline above expires, so a budget a third of
// that window catches it while leaving a loaded host ample headroom.
const barrierAckWellInside = barrierAckDeadline / 3

// spec: §10.1.2 (the pod records and compares a fenced coordination generation
// per bound session), §10.1.8 (the drain barrier's gate shares that per-session
// value), §4.7 (CoordinatorFence).
//
// diagnosis: a failure means two coordinators fencing two co-tenant sessions of
// one pod at the same moment collided on one another's generation: a fence was
// refused as coordinator_handoff_stale, reported a generation gap in a lineage
// it does not belong to, or left the pod holding the neighbour's value. Under
// -race a report here means the per-session fenced generation is not guarded by
// the entry's own lock.
func TestConcurrentCoTenantFencesRecordTheirOwnGeneration_spec_10_1_2(t *testing.T) {
	ctx := context.Background()
	pod := coordfixture.StartPod(t, coTenantA)
	pod.StartSession(t, coTenantB)

	// Two coordinators fence their own session on the shared pod at once, at
	// generations far apart: the ordinary state when one session has been handed
	// off repeatedly and its neighbour has not.
	want := map[string]int64{coTenantA: 9, coTenantB: 2}
	var wg sync.WaitGroup
	for id, gen := range want {
		wg.Add(1)
		go func(id string, gen int64) {
			defer wg.Done()
			accepted, err := pod.Fence(ctx, id, gen)
			if err != nil {
				t.Errorf("fence %s to generation %d: %v", id, gen, err)
				return
			}
			if !accepted {
				t.Errorf("fence %s to generation %d was not accepted", id, gen)
			}
		}(id, gen)
	}
	wg.Wait()

	for id, gen := range want {
		if got := pod.LastFenced(id); got != gen {
			t.Errorf("pod fenced generation for %s = %d, want %d (each session records its own)", id, got, gen)
		}
	}
}

// spec: §10.1.8 (the barrier holds quiescence open and echoes the
// gateway-minted checkpoint id its own Checkpoint stream carried), §10.1.2 (the
// per-session generation gate).
//
// diagnosis: a failure means two co-tenant sessions drained together shared one
// barrier gate: an ack carried the neighbour's checkpoint id or no id at all,
// or a barrier blocked to its wall-clock deadline because the co-tenant's gate
// replaced its own. Under -race a report here means the gate on the slot
// registry entry is not guarded by its own lock.
func TestConcurrentCoTenantBarriersEchoTheirOwnCheckpointID_spec_10_1_8(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), barrierAckDeadline)
	defer cancel()
	pod := coordfixture.StartPod(t, coTenantA)
	pod.StartSession(t, coTenantB)

	gens := map[string]int64{coTenantA: 3, coTenantB: 5}
	for id, gen := range gens {
		if _, err := pod.Fence(ctx, id, gen); err != nil {
			t.Fatalf("fence %s: %v", id, err)
		}
	}

	type ack struct {
		res     adapterclient.CheckpointBarrierResult
		err     error
		elapsed time.Duration
	}
	acks := map[string]chan ack{
		coTenantA: make(chan ack, 1),
		coTenantB: make(chan ack, 1),
	}
	for id, gen := range gens {
		go func(id string, gen int64) {
			started := time.Now()
			res, err := pod.Client.CheckpointBarrier(ctx, id, gen, "barrier-"+id)
			acks[id] <- ack{res, err, time.Since(started)}
		}(id, gen)
	}
	pod.WaitBarrierWaiting(t, coTenantA)
	pod.WaitBarrierWaiting(t, coTenantB)

	// Both gateway-driven Checkpoint streams are opened against the held pod at
	// once, each carrying its own session's checkpoint id. The pod-level op lock
	// admits one checkpoint at a time and queues the co-tenant behind the running
	// one, so the second stream links once the first releases the lock.
	var wg sync.WaitGroup
	for id := range gens {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			driveCheckpointStream(ctx, t, pod, id, "gw-ckpt-"+id)
		}(id)
	}
	wg.Wait()

	for id := range gens {
		got := <-acks[id]
		if got.err != nil {
			t.Fatalf("%s barrier: %v", id, got.err)
		}
		if want := "gw-ckpt-" + id; got.res.CheckpointRef != want {
			t.Errorf("%s checkpoint_ref = %q, want %q (its own stream's id, never empty and never the co-tenant's)",
				id, got.res.CheckpointRef, want)
		}
		if got.res.BarrierID != "barrier-"+id {
			t.Errorf("%s barrier_id = %q, want %q", id, got.res.BarrierID, "barrier-"+id)
		}
		if got.elapsed >= barrierAckWellInside {
			t.Errorf("%s barrier acked after %s, want inside %s, well inside the %s ack window",
				id, got.elapsed, barrierAckWellInside, barrierAckDeadline)
		}
	}
}

// driveCheckpointStream opens the gateway-driven Checkpoint stream for the
// session, sends the CheckpointStart carrying the gateway-minted checkpoint id
// the session's waiting barrier links, waits for the adapter's first frame so
// the link has landed, and ends the stream so the linked barrier is released.
// It models what the gateway does against a quiesced pod without uploading a
// chunk.
func driveCheckpointStream(ctx context.Context, t *testing.T, pod *coordfixture.Pod, sessionID, checkpointID string) {
	t.Helper()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := pod.Client.Checkpoint(streamCtx)
	if err != nil {
		t.Errorf("open Checkpoint stream for %s: %v", sessionID, err)
		return
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{
			Start: &adapterv1.CheckpointStart{
				SessionId:    &adapterv1.SessionId{Value: sessionID},
				CheckpointId: checkpointID,
			},
		},
	}); err != nil {
		t.Errorf("send CheckpointStart for %s: %v", sessionID, err)
		return
	}
	if _, err := stream.Recv(); err != nil {
		t.Errorf("await the adapter's first stream frame for %s: %v", sessionID, err)
	}
}
