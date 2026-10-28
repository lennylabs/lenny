// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 multi-replica scenarios for the release of a concurrent-workspace
// slot and its per-pod claim at and after a terminal write, on the lifecycle
// harness over the compose Postgres session store. Each scenario ends with
// the harness checker, which asserts the slot-set invariants under the §7.1
// holder predicate: every pod's set holds every non-terminal session naming
// the pod, may hold a session naming it in its post-terminal window, and
// holds nothing else but its leaked: members; no pod with an empty set that
// no session holds keeps a bound claim; and no member is removed twice in
// one reservation.
//
// spec: §5.2 (Slot assignment atomicity), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse), §6.2 (Pod State Machine), §10.1.5
// (Stale Replica Behavior), §11.4 (User Invalidation), §7.1 (Post-terminal
// window), §4.6.1 (Warm Pool Controller (Pod Lifecycle)).
package tier4_integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// concurrentSlots is the maxConcurrentSessions of the scenarios' pool.
const concurrentSlots = 4

// newConcurrentCompose builds a two-replica harness on a concurrent pool
// over the compose Postgres session store.
func newConcurrentCompose(t *testing.T, opts lifecycleharness.Options) *lifecycleharness.Harness {
	t.Helper()
	if opts.Replicas == 0 {
		opts.Replicas = 2
	}
	if opts.Pods == 0 {
		opts.Pods = 3
	}
	opts.MaxConcurrentSessions = concurrentSlots
	return newComposeWith(t, opts)
}

// requireMembers fails unless the pod's slot set is exactly want.
func requireMembers(t *testing.T, h *lifecycleharness.Harness, pod string, want ...string) {
	t.Helper()
	got, err := h.SlotMembers(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("slot set of %s = %v, want %v", pod, got, want)
	}
	have := map[string]bool{}
	for _, m := range got {
		have[m] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("slot set of %s = %v, want %v", pod, got, want)
		}
	}
}

// requireClaim fails unless the pod's claim is in phase, or absent when
// phase is empty.
func requireClaim(t *testing.T, h *lifecycleharness.Harness, pod, phase string) {
	t.Helper()
	got, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if phase == "" && exists {
		t.Fatalf("claim of %s = %q, want it deleted", pod, got)
	}
	if phase != "" && got != phase {
		t.Fatalf("claim of %s = %q (exists %v), want %q", pod, got, exists, phase)
	}
}

// deleteSession DELETEs the session on r and fails on an error answer.
func deleteSession(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
	t.Helper()
	if code, body := h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil); code >= http.StatusBadRequest {
		t.Fatalf("DELETE %s on %s = %d %v", id, r.ID, code, body)
	}
}

// spec: §5.2 (Slot assignment atomicity), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse), §6.2 (Pod State Machine)
// diagnosis: a DELETE handled on one replica of a session that never
// started, created on another replica, released more than its own slot on
// the shared pod: the per-pod claim was deleted under the live sibling (the
// pod would drain and end the sibling), or the sibling's member was removed,
// or the session's own member stayed counted.
func TestNotStartedDeleteWithALiveSibling_spec_5_2(t *testing.T) {
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	a, b := h.Replicas[0], h.Replicas[1]
	sibling := h.StartSession(a)
	pod := h.Row(sibling).PodAssignment
	created := h.CreateSession(b)
	if got := h.Row(created).PodAssignment; got != pod {
		t.Fatalf("the created session reserved on %q, want the sibling's pod %q", got, pod)
	}
	deleteSession(t, h, a, created)
	h.RunToQuiescence()
	requireMembers(t, h, pod, sibling)
	requireClaim(t, h, pod, string(claimstate.Bound))
	if st := h.Row(sibling).State; st != session.StateRunning {
		t.Errorf("sibling = %s, want running", st)
	}
	if n := h.Slots.Removals(pod, created); n != 1 {
		t.Errorf("removals of the deleted session's member = %d, want 1", n)
	}
	h.Check(t)
}

// spec: §5.2 (Slot assignment atomicity), §7.2 (Interactive Session Model),
// §11.3 (created-state timeout)
// diagnosis: an abandoned created row on a concurrent pool did not end
// failed with CREATED_TIMEOUT, or its reserved slot stayed counted, or its
// release deleted the claim under the live sibling. The created-row release
// runs through the watchdog's terminal write and its post-terminal hook.
func TestAbandonedCreatedRowTimesOutAndReleasesItsSlot_spec_5_2(t *testing.T) {
	h := newConcurrentCompose(t, lifecycleharness.Options{MaxCreatedSeconds: 10})
	a, b := h.Replicas[0], h.Replicas[1]
	sibling := h.StartSession(a)
	pod := h.Row(sibling).PodAssignment
	created := h.CreateSession(b)
	if st := h.Row(created).State; st != session.StateCreated {
		t.Fatalf("created session = %s, want created", st)
	}
	// The created session shares the sibling's pod, so the slot-set and
	// claim checks below discriminate a release of the created slot alone
	// from a release that took the sibling's slot or its claim.
	if got := h.Row(created).PodAssignment; got != pod {
		t.Fatalf("the created session reserved on %q, want the sibling's pod %q", got, pod)
	}
	requireMembers(t, h, pod, sibling, created)
	row := h.AwaitState(created, session.StateFailed, 20)
	if row.State != session.StateFailed {
		t.Fatalf("abandoned created row = %s, want failed", row.State)
	}
	// Only the watchdog's created-state terminal write ends the row with
	// CREATED_TIMEOUT; another writer's failure would carry its own reason.
	if row.FailureReason != watchdog.ReasonCreatedTimeout {
		t.Fatalf("abandoned created row failed with reason %q, want %s", row.FailureReason, watchdog.ReasonCreatedTimeout)
	}
	h.RunToQuiescence()
	requireMembers(t, h, pod, sibling)
	requireClaim(t, h, pod, string(claimstate.Bound))
	if n := h.Slots.Removals(pod, created); n != 1 {
		t.Errorf("removals of the created session's member = %d, want 1", n)
	}
	h.Check(t)
}

// supersede moves the session's coordination from the replica that binds it
// to the peer, as a coordinator whose lease lapsed loses it: the lease is
// released and the peer's Sweeper takes the session over and re-adopts its
// pod at a higher generation. The old coordinator keeps its stale binding.
func supersede(t *testing.T, h *lifecycleharness.Harness, from, peer *lifecycleharness.Replica, id string) {
	t.Helper()
	ctx := context.Background()
	before := h.Row(id).CoordinationGeneration
	if err := h.Leases.Release(ctx, lifecycleharness.Tenant, id, from.ID); err != nil {
		t.Fatalf("release the coordination lease: %v", err)
	}
	if _, err := peer.Sweeper.Sweep(ctx); err != nil {
		t.Fatalf("peer sweep: %v", err)
	}
	h.Settle()
	if _, ok := peer.Registry.Get(id); !ok {
		t.Fatalf("the peer did not re-adopt %s", id)
	}
	if _, ok := from.Registry.Get(id); !ok {
		t.Fatalf("the old coordinator dropped its binding before the scenario")
	}
	if gen := h.Row(id).CoordinationGeneration; gen <= before {
		t.Fatalf("coordination generation = %d after the takeover, want above %d", gen, before)
	}
}

// countForwards arms r's forward listener to count the requests it receives
// and returns the counter. The hook never faults the request.
func countForwards(r *lifecycleharness.Replica) *atomic.Int64 {
	var n atomic.Int64
	r.SetForwardFault(func(*http.Request) bool {
		n.Add(1)
		return false
	})
	return &n
}

// spec: §10.1.5 (Stale Replica Behavior), §5.2 (Slot assignment atomicity),
// §29.3 (Interactive message send: Forwarding), §10.1.1 (Stateless Replicas
// and Per-Session Coordination)
// diagnosis: a DELETE received by a coordinator that lost the session to a
// peer was applied there, through its stale binding, rather than forwarded
// over CH-HOLDERFORWARD to the REG-COORDLEASE holder; or the adopter's
// terminal write released the slot more than once or not at all; or the
// superseded replica's stale binding was released through the binder, sent
// the pod a Shutdown, or was never evicted. The adopter's post-terminal hook
// releases the slot exactly once and the superseded replica's coordination
// Sweeper evicts its stale binding with no binder call.
func TestSupersededCoordinatorForwardsTheDeleteAndTheAdopterReleasesOnce_spec_10_1_5(t *testing.T) {
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	old, adopter := h.Replicas[0], h.Replicas[1]
	sibling := h.StartSession(adopter)
	id := h.StartSession(old)
	pod := h.Row(id).PodAssignment
	supersede(t, h, old, adopter, id)
	forwards := countForwards(adopter)
	shutdowns := h.Cluster.PodOf(pod).UnaryCalls("Shutdown")
	deleteSession(t, h, old, id)
	h.Settle()
	if n := forwards.Load(); n != 1 {
		t.Fatalf("the adopter's forward listener received %d requests for the DELETE on the superseded replica, want 1", n)
	}
	if st := h.Row(id).State; st != session.StateCancelled {
		t.Fatalf("state = %q after the forwarded DELETE, want cancelled", st)
	}
	// Settle ticks no Sweeper, so the one removal here is the adopter's
	// post-terminal hook releasing the binding it holds as coordinator.
	if n := h.Slots.Removals(pod, id); n != 1 {
		t.Errorf("removals of %s's member after the forwarded DELETE = %d, want 1 by the adopter", id, n)
	}
	if _, ok := adopter.Registry.Get(id); ok {
		t.Error("the adopter kept the terminal session's binding after its terminal write")
	}
	adopterShutdowns := h.Cluster.PodOf(pod).UnaryCalls("Shutdown") - shutdowns
	h.RunToQuiescence()
	if _, ok := old.Registry.Get(id); ok {
		t.Error("the superseded replica kept its stale binding after its Sweeper listed the terminal row")
	}
	if n := h.Slots.Removals(pod, id); n != 1 {
		t.Errorf("removals of %s's member = %d, want 1", id, n)
	}
	if n := h.Cluster.PodOf(pod).UnaryCalls("Shutdown") - shutdowns - adopterShutdowns; n != 0 {
		t.Errorf("the superseded replica's eviction sent %d Shutdowns to %s, want none", n, pod)
	}
	if pod == h.Row(sibling).PodAssignment {
		requireMembers(t, h, pod, sibling)
	}
	h.Check(t)
}

// awaitRecovery runs checker passes until the peer's resume driver has
// written resuming for the session, which the row shows as resuming or, once
// the restore committed, running at a higher recovery generation.
func awaitRecovery(t *testing.T, h *lifecycleharness.Harness, id string, before int64) {
	t.Helper()
	for i := 0; i < recoveryPasses; i++ {
		row := h.Row(id)
		if row.State == session.StateResuming || (row.State == session.StateRunning && row.RecoveryGeneration > before) {
			return
		}
		h.Pass()
	}
	t.Fatalf("the peer's resume driver never wrote resuming; the row is %s", h.Row(id).State)
}

// spec: §10.1.5 (Stale Replica Behavior), §7.3 (Resume driver), §7.1
// (Seal-and-export invariant)
// diagnosis: after a peer took the session over and its pod then failed on
// the peer, a DELETE handled on the old coordinator acted on its stale
// binding: it sent a seal RPC or a release to the earlier pod, which belongs
// to the peer's recovery, or it kept the stale binding. The checker passes
// with no relaxation, so no claim, member, or binding is left over.
func TestOldCoordinatorDeleteAfterPeerResumeEvictsItsStaleBinding_spec_10_1_5(t *testing.T) {
	h := newConcurrentCompose(t, lifecycleharness.Options{Pods: 4})
	old, peer := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(old)
	earlier := h.Row(id).PodAssignment
	supersede(t, h, old, peer, id)
	before := h.Row(id).RecoveryGeneration
	lifecycleharness.Report(t, peer, id, string(session.FailureRuntimeCrash))
	awaitRecovery(t, h, id, before)
	seals := h.Cluster.PodOf(earlier).UnaryCalls("SealSession")
	deleteSession(t, h, old, id)
	h.Settle()
	if got := h.Cluster.PodOf(earlier).UnaryCalls("SealSession"); got != seals {
		t.Errorf("the old coordinator sent %d seal RPCs to the earlier pod, want none", got-seals)
	}
	if _, ok := old.Registry.Get(id); ok {
		t.Error("the old coordinator kept its stale binding on the earlier pod")
	}
	h.Check(t)
}

// postTerminalStores names the session stores the post-terminal reseed
// scenarios run on: the compose Postgres store and the harness's in-memory
// default, each built with the replicas' terminal pipeline budget as its
// post-terminal cap.
var postTerminalStores = []struct {
	name string
	new  func(t *testing.T) *lifecycleharness.Harness
}{
	{"postgres", func(t *testing.T) *lifecycleharness.Harness {
		return newConcurrentCompose(t, lifecycleharness.Options{Pods: 1})
	}},
	{"memstore", newPostTerminalMemHarness},
}

// newPostTerminalMemHarness builds a two-replica harness on a concurrent
// pool of one pod over the in-memory store, whose window cutoff reads the
// harness clock.
func newPostTerminalMemHarness(t *testing.T) *lifecycleharness.Harness {
	t.Helper()
	return lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 1, MaxConcurrentSessions: concurrentSlots, CredentialPool: true,
		ControlPlane: &harnessControlPlane,
	})
}

// loseSet drops the pod's slot set, as a Redis restart that loses its data
// does.
func loseSet(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	if err := h.LoseSlotSet(context.Background(), pod); err != nil {
		t.Fatalf("lose the slot set of %s: %v", pod, err)
	}
}

// requireAdmittedOnPod starts a session on r and requires it to run on pod.
func requireAdmittedOnPod(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, pod string) string {
	t.Helper()
	id, code, body := h.TryStartSession(r)
	if code != http.StatusCreated {
		t.Fatalf("start on %s after the reseed = %d %v, want 201 into the freed slot", r.ID, code, body)
	}
	if got := h.Row(id).PodAssignment; got != pod {
		t.Fatalf("admitted session runs on %q, want %q", got, pod)
	}
	return id
}

// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity, Slot assignment atomicity), §4.6.1 (Warm Pool Controller (Pod
// Lifecycle))
// diagnosis: after replica A ended a session and recorded its pod release,
// the pod's slot set was lost and replica B's reservation reseeded it from
// the session store. The reseed counted the released session (the holder
// predicate ignored the release marker), so the new session was refused a
// slot the release had freed, or the checker found the set or the claim out
// of step with the holder predicate.
func TestReseedAfterARecordedReleaseFreesTheSlot_spec_7_1(t *testing.T) {
	for _, st := range postTerminalStores {
		t.Run(st.name, func(t *testing.T) {
			h := st.new(t)
			a, b := h.Replicas[0], h.Replicas[1]
			pod, ids := h.FillPod(t, a)
			h.EndWithRelease(t, a, pod, ids[0], false)
			loseSet(t, h, pod)
			admitted := requireAdmittedOnPod(t, h, b, pod)
			requireMembers(t, h, pod, append(slices.Clone(ids[1:]), admitted)...)
			h.Check(t)
		})
	}
}

// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity, Slot assignment atomicity), §4.6.1 (Warm Pool Controller (Pod
// Lifecycle))
// diagnosis: replica A ended a session and released its slot, but the
// release-marker write failed, so the session stays in its post-terminal
// window. After the pod's slot set was lost, replica B's reseed omitted the
// session (the seed counted only non-terminal rows) and admitted a new
// session over the slots the terminal pipeline may still use, or the
// checker rejected the reseeded member the holder predicate accepts.
func TestReseedCountsASessionInItsPostTerminalWindow_spec_7_1(t *testing.T) {
	for _, st := range postTerminalStores {
		t.Run(st.name, func(t *testing.T) {
			h := st.new(t)
			a, b := h.Replicas[0], h.Replicas[1]
			pod, ids := h.FillPod(t, a)
			h.EndWithRelease(t, a, pod, ids[0], true)
			loseSet(t, h, pod)
			if id, code, body := h.TryStartSession(b); code == http.StatusCreated {
				t.Fatalf("start %s on %s after the reseed = %d %v, want a refusal at maxConcurrentSessions", id, b.ID, code, body)
			}
			requireMembers(t, h, pod, ids...)
			h.Check(t)
		})
	}
}

// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
// diagnosis: a session whose release marker was never recorded kept
// counting toward its pod's slots after the harness clock passed
// TerminatedAt plus the terminal pipeline budget, so a reseed after a later
// set loss still refused a new session. The cap bounds the post-terminal
// window when no release is recorded. Only the in-memory store reads the
// harness clock for its cutoff, so the case runs there alone.
func TestReseedStopsCountingASessionPastThePostTerminalCap_spec_7_1(t *testing.T) {
	h := newPostTerminalMemHarness(t)
	a, b := h.Replicas[0], h.Replicas[1]
	pod, ids := h.FillPod(t, a)
	h.EndWithRelease(t, a, pod, ids[0], true)
	loseSet(t, h, pod)
	if id, code, body := h.TryStartSession(b); code == http.StatusCreated {
		t.Fatalf("start %s inside the window = %d %v, want a refusal", id, code, body)
	}
	capEnd := h.Row(ids[0]).TerminatedAt.Add(lifecycleharness.PostTerminalCap)
	for !h.Clock.Now().After(capEnd) {
		h.Pass()
	}
	if row := h.Row(ids[0]); !row.PodReleasedAt.IsZero() {
		t.Fatalf("PodReleasedAt of %s = %v, want no marker so only the cap ends the window", ids[0], row.PodReleasedAt)
	}
	loseSet(t, h, pod)
	admitted := requireAdmittedOnPod(t, h, b, pod)
	requireMembers(t, h, pod, append(slices.Clone(ids[1:]), admitted)...)
	h.Check(t)
}

// spec: §7.1 (Post-terminal window), §10.1.1 (Stateless Replicas and
// Per-Session Coordination), §5.2 (Post-recovery rehydration atomicity)
// diagnosis: replica B made the terminal write of a session replica A binds,
// so A's coordination Sweeper released the binding through its terminal
// binding release and made the release-marker write there. A failure means
// A's terminal binding release recorded no marker (or recorded one despite
// the fault), or the reseed after a set loss counted the session against
// the marker: a recorded release must free the slot and a failed marker
// write must keep the session counted in its post-terminal window.
func TestCrossReplicaTerminalWriteRecordsTheReleaseOnTheBindingReplica_spec_7_1(t *testing.T) {
	for _, st := range postTerminalStores {
		for _, failMarker := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/marker-fails-%v", st.name, failMarker), func(t *testing.T) {
				h := st.new(t)
				a, b := h.Replicas[0], h.Replicas[1]
				pod, ids := h.FillPod(t, a)
				h.EndWithRelease(t, b, pod, ids[0], failMarker)
				loseSet(t, h, pod)
				if failMarker {
					if id, code, body := h.TryStartSession(a); code == http.StatusCreated {
						t.Fatalf("start %s on %s after the reseed = %d %v, want a refusal at maxConcurrentSessions", id, a.ID, code, body)
					}
					requireMembers(t, h, pod, ids...)
				} else {
					admitted := requireAdmittedOnPod(t, h, a, pod)
					requireMembers(t, h, pod, append(slices.Clone(ids[1:]), admitted)...)
				}
				h.Check(t)
			})
		}
	}
}
