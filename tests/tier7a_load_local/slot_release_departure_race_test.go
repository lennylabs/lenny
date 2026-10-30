// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a departure cases: races whose residue §5.2 (Slot assignment
// atomicity), §4.6.1 (Orphaned SandboxClaim detection), and §4.6.3
// (SandboxClaim: a release that ends the occupancy without reuse) accept,
// and the reseed of a lost slot set racing concurrent reservations while a
// session is in its §7.1 post-terminal window. The harness checker applies
// the §7.1 holder predicate to the pods-and-claims and slot-set invariants.
// Each case runs on the lifecycle harness, drives the racing actors through
// the replicas' session servers and binders, and ends every round with the
// harness invariants, relaxed only for the residue its own departure
// accepts. A case whose departure ends a racing session on the pod-failure
// path also drives that session there: the claim delete starts the pod's
// drain, which ends the pod's CH-ATTACH streams, and the session must take
// the failure funnel's edge rather than stay bound to a retiring pod.
package tier7a_load_local_test

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// departureRounds is the number of rounds each randomized departure case
// runs.
const departureRounds = 6

// departureCap is the concurrent pool's maxConcurrentSessions.
const departureCap = 4

// requireNoViolations fails the round when a harness invariant fails.
func requireNoViolations(t *testing.T, h *lifecycleharness.Harness, round int, when string) {
	t.Helper()
	h.Settle()
	if v := h.Violations(); len(v) != 0 {
		t.Fatalf("round %d, %s: harness invariants: %v", round, when, v)
	}
}

// requirePodFailurePath ends the session's CH-ATTACH stream on pod, as the
// drain a deleted claim starts ends the pod's runtime, and requires the
// session to take the pod-failure path: the failure funnel's edge raises the
// row's RetryCount and clears the pod from it, or the session ends failed.
// A session that stayed bound to the retiring pod fails the case.
//
// spec: §7.3 (Resume flow after pod failure), §4.6.1 (occupancy projection
// on claim DELETE)
func requirePodFailurePath(t *testing.T, h *lifecycleharness.Harness, round int, id, pod string) {
	t.Helper()
	r := h.Bound(id)
	if r == nil {
		t.Fatalf("round %d: no replica binds %s on %s", round, id, pod)
	}
	before := h.Row(id).RetryCount
	if code, body := h.Send(r, id, "open-stream"); code != http.StatusOK {
		t.Fatalf("round %d: message to %s = %d %v", round, id, code, body)
	}
	h.Cluster.PodOf(pod).FailAttach()
	deadline := time.Now().Add(10 * time.Second)
	for {
		row := h.Row(id)
		switch {
		case row.State == session.StateFailed:
			return
		case row.RetryCount > before && row.PodAssignment != pod:
			return
		case time.Now().After(deadline):
			t.Fatalf("round %d: %s stayed %s on %s (RetryCount %d) after its pod's drain ended the stream; want the pod-failure path",
				round, id, row.State, row.PodAssignment, row.RetryCount)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settleRecovery runs checker passes until the session is running or
// terminal, so the invariants read no resume in flight.
func settleRecovery(h *lifecycleharness.Harness, id string) {
	for i := 0; i < 20; i++ {
		st := h.Row(id).State
		if st == session.StateRunning || session.IsTerminal(st) {
			break
		}
		h.Pass()
	}
	h.Settle()
}

// endSession ends a session that is still live with a DELETE on any
// surviving replica and waits until no replica binds it, so the next round
// starts with no live claim of the tenant.
func endSession(t *testing.T, h *lifecycleharness.Harness, id string) {
	t.Helper()
	if id == "" || session.IsTerminal(h.Row(id).State) {
		return
	}
	if code, body := h.Do(h.Surviving()[0], http.MethodDelete, "/v1/sessions/"+id, nil); code >= http.StatusBadRequest {
		t.Fatalf("DELETE %s = %d %v", id, code, body)
	}
	for i := 0; i < 10 && h.Bound(id) != nil; i++ {
		h.Pass()
	}
	h.Settle()
}

// claimBound reports whether the pod's per-pod claim exists in `bound`.
func claimBound(t *testing.T, h *lifecycleharness.Harness, pod string) bool {
	t.Helper()
	phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	return exists && phase == string(claimstate.Bound)
}

// classifyJoin records where a racing session ended after the race and,
// when it runs on target and target's claim is gone, drives it onto the
// pod-failure path. A session whose start failed is refused; one that runs
// on target under a bound claim holds the pod's live claim, which it joined
// or, after the delete, acquired fresh; one that runs on another pod took a
// fresh pod.
func classifyJoin(t *testing.T, h *lifecycleharness.Harness, round int, id string, code int, target string, outcomes map[string]int) {
	t.Helper()
	if id == "" || code != http.StatusCreated {
		if id != "" && !session.IsTerminal(h.Row(id).State) {
			t.Fatalf("round %d: the refused start left %s %s", round, id, h.Row(id).State)
		}
		outcomes["refused"]++
		return
	}
	row := h.Row(id)
	switch {
	case row.State != session.StateRunning:
		t.Fatalf("round %d: started session %s is %s, want running", round, id, row.State)
	case row.PodAssignment != target:
		outcomes["took a fresh pod"]++
	case claimBound(t, h, target):
		outcomes["on the pod under a bound claim"]++
	default:
		outcomes["joined a deleted claim (pod-failure path)"]++
		requireNoViolations(t, h, round, "before the drain")
		requirePodFailurePath(t, h, round, id, target)
		settleRecovery(h, id)
	}
}

// spec: §5.2 (Slot assignment atomicity), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse), §7.3 (Resume flow after pod
// failure)
// diagnosis: a session start reserving onto a concurrent pod raced the
// release that emptied the pod's set and deleted its claim, and ended outside
// the accepted departure: the start was refused but left its member or row
// live, a session that joined the deleted claim stayed bound to the retiring
// pod instead of taking the pod-failure path, or a harness invariant failed
// (a member counted twice or not at all, a claim left on an empty pod). The
// accepted outcomes are a refusal, a fresh pod, the pod under a bound claim,
// or a join onto the deleted claim followed by the pod-failure path.
func TestEmptyingReleaseRacesAReservation_spec_5_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 4 * departureRounds, MaxConcurrentSessions: departureCap,
	})
	releaser, joiner := h.Replicas[0], h.Replicas[1]
	outcomes := map[string]int{}
	for round := 0; round < departureRounds; round++ {
		last := h.StartSession(releaser)
		pod := h.Row(last).PodAssignment
		// The emptying release's claim delete is held once the release has
		// removed the last member, and let through after a random delay
		// that spans the joiner's start, so the joiner's Pass 1 reads the
		// claim before or after the delete.
		held, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaser.SetClaimFault(func(_ context.Context, op lifecycleharness.ClaimOp, name string) error {
			if op == lifecycleharness.ClaimDelete && name == podclaim.ClaimName(pod) {
				once.Do(func() {
					close(held)
					<-gate
				})
			}
			return nil
		})
		var joinID string
		var joinCode int
		delay := time.Duration(rng.Intn(40)) * time.Millisecond
		released := make(chan struct{})
		raceJittered(rng,
			func() {
				defer close(released)
				if code, body := h.Do(releaser, http.MethodDelete, "/v1/sessions/"+last, nil); code >= http.StatusBadRequest {
					t.Errorf("round %d: DELETE %s = %d %v", round, last, code, body)
				}
				awaitUnbound(t, releaser, last)
			},
			func() {
				// A join that reserved before the release removed the last
				// member leaves the set non-empty, so no delete is held.
				select {
				case <-held:
					time.Sleep(delay)
					close(gate)
				case <-released:
				}
			},
			func() { joinID, joinCode, _ = h.TryStartSession(joiner) })
		releaser.SetClaimFault(nil)
		h.Settle()
		if h.Slots.Removals(pod, last) != 1 {
			t.Fatalf("round %d: removals of %s's member = %d, want 1", round, last, h.Slots.Removals(pod, last))
		}
		classifyJoin(t, h, round, joinID, joinCode, pod, outcomes)
		requireNoViolations(t, h, round, "after the race")
		endSession(t, h, joinID)
		requireNoViolations(t, h, round, "after the round")
	}
	t.Logf("outcomes: %v", outcomes)
	h.Check(t)
}

// resumeClaimFault fails the first SandboxClaim status write either replica
// makes once armed, which is the bound write of the resume's claimIdle, and
// starts the sibling's session when it fires. It then holds the by-name
// delete claimIdle makes after the failure for a random delay that spans the
// sibling's start, so the sibling's Pass 1 reads the empty-phase claim
// before or after the delete.
type resumeClaimFault struct {
	fired   atomic.Bool
	claim   atomic.Value
	pod     chan string
	started chan struct{}
	delay   time.Duration
	start   func()
}

// fault is the ClaimFault both replicas carry.
func (f *resumeClaimFault) fault(_ context.Context, op lifecycleharness.ClaimOp, name string) error {
	switch {
	case op == lifecycleharness.ClaimStatus && f.fired.CompareAndSwap(false, true):
		f.claim.Store(name)
		f.pod <- strings.TrimPrefix(name, "claim-")
		go func() {
			defer close(f.started)
			f.start()
		}()
		return errors.New("injected bound status failure")
	case op == lifecycleharness.ClaimDelete && f.claim.Load() == name:
		time.Sleep(f.delay)
	}
	return nil
}

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity), §7.3 (Resume flow after pod failure)
// diagnosis: on a concurrent pool, a Binder.Resume whose claimIdle bound
// status write failed deleted its fresh claim by name while a same-tenant
// sibling's start joined that claim, and the sibling ended outside the
// accepted departure: it stayed bound to the pod the delete retired instead
// of taking the pod-failure path, a refused start left its member or row
// live, or a harness invariant failed. A sibling that joined the claim
// before the delete must reach the pod-failure path, which fails closed.
func TestResumeClaimDeleteRacesASlotJoin_spec_5_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 6 * departureRounds, MaxConcurrentSessions: departureCap,
	})
	resumer, sibling := h.Replicas[0], h.Replicas[1]
	outcomes := map[string]int{}
	for round := 0; round < departureRounds; round++ {
		failed := h.StartSession(resumer)
		failedPod := h.Row(failed).PodAssignment
		var joinID string
		var joinCode int
		f := &resumeClaimFault{
			pod: make(chan string, 1), started: make(chan struct{}),
			delay: time.Duration(rng.Intn(40)) * time.Millisecond,
			start: func() { joinID, joinCode, _ = h.TryStartSession(sibling) },
		}
		for _, r := range h.Replicas {
			r.SetClaimFault(f.fault)
		}
		// The failed session's pod loss runs its funnel, which releases its
		// slot, and its resume driver claims an idle pod through
		// Binder.Resume, whose bound write the fault fails.
		requirePodFailurePath(t, h, round, failed, failedPod)
		var resumePod string
		for i := 0; resumePod == "" && i < 20; i++ {
			select {
			case resumePod = <-f.pod:
			case <-time.After(100 * time.Millisecond):
				h.Pass()
			}
		}
		if resumePod == "" {
			t.Fatalf("round %d: no resume claim of %s reached its bound status write", round, failed)
		}
		<-f.started
		for _, r := range h.Replicas {
			r.SetClaimFault(nil)
		}
		settleRecovery(h, failed)
		classifyJoin(t, h, round, joinID, joinCode, resumePod, outcomes)
		requireNoViolations(t, h, round, "after the race")
		endSession(t, h, joinID)
		endSession(t, h, failed)
		requireNoViolations(t, h, round, "after the round")
	}
	t.Logf("outcomes: %v", outcomes)
	h.Check(t)
}

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity), §4.6.3 (SandboxClaim: a release that ends the occupancy
// without reuse)
// diagnosis: the accepted departure for a CREATE loser whose withdrawal
// empties the set after the winner's last release changed. The winner's
// release ran no disposition, because the loser's member was still counted,
// and the loser's withdrawal is a raw keyed release with no disposition, so
// the winner's claim stays bound on an empty set for the orphan GC. The
// harness must flag exactly that claim without the empty-set relaxation and
// accept the scenario with it alone. A claim deleted here means a withdrawal
// disposes of a claim it does not own; any other violation is a residue the
// departure does not accept.
func TestCreateLoserWithdrawalAfterTheWinnersReleaseLeavesTheClaimForGC_spec_4_6_1(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 1, MaxConcurrentSessions: departureCap})
	winnerRep, loserRep := h.Replicas[0], h.Replicas[1]
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	loserRep.SetClaimFault(func(_ context.Context, op lifecycleharness.ClaimOp, _ string) error {
		if op == lifecycleharness.ClaimCreate {
			once.Do(func() {
				close(blocked)
				<-release
			})
		}
		return nil
	})
	type started struct {
		id   string
		code int
	}
	loserDone := make(chan started, 1)
	go func() {
		id, code, _ := h.TryStartSession(loserRep)
		loserDone <- started{id, code}
	}()
	<-blocked
	winner := h.StartSession(winnerRep)
	pod := h.Row(winner).PodAssignment
	if code, body := h.Do(winnerRep, http.MethodDelete, "/v1/sessions/"+winner, nil); code >= http.StatusBadRequest {
		t.Fatalf("DELETE the winner = %d %v", code, body)
	}
	awaitUnbound(t, winnerRep, winner)
	close(release)
	loser := <-loserDone
	loserRep.SetClaimFault(nil)
	if loser.code == http.StatusCreated {
		t.Fatalf("the CREATE loser's start answered %d, want a refusal (no other pod is idle)", loser.code)
	}
	if loser.id != "" && !session.IsTerminal(h.Row(loser.id).State) {
		t.Fatalf("the refused loser's session is %s, want terminal", h.Row(loser.id).State)
	}
	h.Settle()
	members, err := h.SlotMembers(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 || !claimBound(t, h, pod) {
		t.Fatalf("slot set %v, claim bound %v; want an empty set and the winner's claim bound", members, claimBound(t, h, pod))
	}
	requireOnlyEmptySetClaimViolations(t, h, pod)
	h.RelaxEmptySetClaims = true
	h.Check(t)
}

// requireOnlyEmptySetClaimViolations requires the unrelaxed checker to flag
// the bound claim on the empty pod, in the pods-and-claims and the empty-pod
// invariants, and nothing else.
func requireOnlyEmptySetClaimViolations(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	v := h.Violations()
	want := map[string]bool{"pods and claims": false, "empty pods keep no bound claim": false}
	for _, line := range v {
		name, msg, _ := strings.Cut(line, ": ")
		if _, ok := want[name]; !ok || !strings.Contains(msg, "claim on "+pod+" is bound") {
			t.Fatalf("unrelaxed violation %q is not the accepted residue on %s", line, pod)
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("the unrelaxed checker did not flag the bound claim on %s in %q: %v", pod, name, v)
		}
	}
}

// recycleRebindOrder is where the writer's held by-name delete lands
// relative to the peer's recycle and the successor's rebind.
type recycleRebindOrder int

const (
	deleteBeforeRecycle recycleRebindOrder = iota
	deleteAfterRecycle
	deleteAfterRebind
)

func (o recycleRebindOrder) String() string {
	return [...]string{"delete before the recycle", "delete after the recycle", "delete after the rebind"}[o]
}

// The by-name delete comes from the writer's full_revoke. A DELETE of a
// starting session is forwarded to the replica holding its coordination
// lease, which is the peer that binds it, so a DELETE never reaches the
// by-name reclaim on a replica without the binding. The full_revoke write
// runs on the replica that serves it, and its pre-running reclaim deletes
// the claim by name there.
//
// spec: §5.2 (Recycle lifecycle), §4.6.3 (SandboxClaim: a release that ends
// the occupancy without reuse), §4.6.1 (within-hold rebind), §7.3 (Resume
// flow after pod failure), §11.4 (User Invalidation), §29.3 (Interactive
// message send) Forwarding, §7.2 (Session state machine: ready → starting)
// diagnosis: on an exclusive recycling pool, a session's /start bound on a
// peer and wrote starting, the writer's full_revoke cancelled the session and
// deleted the claim by name, the peer's terminal binding release recycled the
// pod, and a successor rebound the reserved pod before the writer's delete
// landed; the successor then stayed bound to a pod whose claim was gone
// instead of taking its own pod-failure path, the writer's delete did not end
// the claim, or a harness invariant failed. A revoke that made no by-name
// delete means starting is missing from the full_revoke pre-running states.
func TestRecycleRebindRacesAByNameDelete_spec_5_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	const perOrder = 2
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 4 * 3 * perOrder, Recycling: true, KeepRuntime: true,
	})
	writer, peer := h.Replicas[0], h.Replicas[1]
	outcomes := map[string]int{}
	for round := 0; round < 3*perOrder; round++ {
		order := recycleRebindOrder(round % 3)
		successor := recycleRebindRound(t, h, rng, round, order, writer, peer)
		pod := ""
		if successor != "" {
			pod = h.Row(successor).PodAssignment
		}
		switch {
		case successor == "":
			outcomes[order.String()+": no successor"]++
		case order == deleteAfterRebind:
			outcomes[order.String()+": successor on the pod-failure path"]++
			requireNoViolations(t, h, round, "before the drain")
			requirePodFailurePath(t, h, round, successor, pod)
			settleRecovery(h, successor)
		default:
			outcomes[order.String()+": successor on a fresh pod"]++
		}
		requireNoViolations(t, h, round, "after the race")
		endSession(t, h, successor)
		requireNoViolations(t, h, round, "after the round")
	}
	t.Logf("outcomes: %v", outcomes)
	h.Check(t)
}

// recycleRebindRound runs one round and returns the successor session, or
// "" when the order leaves no reserved pod to rebind and the successor was
// not needed. The writer's by-name delete of the starting session's claim,
// made by its full_revoke, is held until the round's order point, after a
// random jitter.
func recycleRebindRound(t *testing.T, h *lifecycleharness.Harness, rng *rand.Rand, round int, order recycleRebindOrder,
	writer, peer *lifecycleharness.Replica,
) string {
	t.Helper()
	revoked := h.CreateSession(writer)
	pod := h.Row(revoked).PodAssignment
	held, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	writer.SetClaimFault(func(_ context.Context, op lifecycleharness.ClaimOp, name string) error {
		if op == lifecycleharness.ClaimDelete && name == podclaim.ClaimName(pod) {
			once.Do(func() {
				close(held)
				<-gate
			})
		}
		return nil
	})
	defer writer.SetClaimFault(nil)
	hs := startHeldBeforeCommit(t, h, peer, revoked)
	deleted := make(chan struct{})
	go func() {
		defer close(deleted)
		fullRevoke(t, h, writer)
	}()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		hs.release()
		t.Fatalf("round %d: the writer's full_revoke of the starting session made no by-name claim delete", round)
	}
	letDelete := func() {
		time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
		close(gate)
		<-deleted
		h.Settle()
	}
	if order == deleteBeforeRecycle {
		letDelete()
	}
	hs.release()
	if code := <-hs.answer; code != http.StatusConflict {
		t.Fatalf("round %d: the held start answered %d after the terminal write, want 409", round, code)
	}
	// The peer's binding of the cancelled session is released by its
	// coordination Sweeper's terminal binding release.
	for i := 0; i < 10; i++ {
		if _, ok := peer.Registry.Get(revoked); !ok {
			break
		}
		h.Pass()
	}
	if _, ok := peer.Registry.Get(revoked); ok {
		t.Fatalf("round %d: %s still binds the cancelled session %s", round, peer.ID, revoked)
	}
	if order == deleteBeforeRecycle {
		requireClaimGone(t, h, round, pod)
		return ""
	}
	awaitClaimPhase(t, h, round, pod, claimstate.Recycling)
	if order == deleteAfterRecycle {
		letDelete()
		requireClaimGone(t, h, round, pod)
		return ""
	}
	completeScrub(t, h, round, pod)
	successor := h.StartSession(peer)
	if got := h.Row(successor).PodAssignment; got != pod {
		letDelete()
		t.Fatalf("round %d: the successor landed on %s, want the reserved pod %s", round, got, pod)
	}
	letDelete()
	requireClaimGone(t, h, round, pod)
	return successor
}

// completeScrub stands in for the scrub report the harness does not deliver
// and the occupancy projection it does not run: the recycled claim moves to
// `reserved` with a hold, and the pod's Sandbox to `reserved`, so a
// same-tenant acquisition rebinds it. The rebind reads the agent Pod for its
// runtime-process lifetime and skips a candidate whose Pod is absent, so the
// Pod the WarmPoolController would have created is seeded first, pinned to
// the harness tenant and labelled with the pool's `keep` lifetime.
// spec: §4.6.1 (Reserved hold; within-hold rebind), §5.2 (Runtime-process
// lifetime; Tenant pinning)
func completeScrub(t *testing.T, h *lifecycleharness.Harness, round int, pod string) {
	t.Helper()
	ctx := context.Background()
	if err := h.Cluster.SeedAgentPod(ctx, h.Cluster.PodOf(pod), map[string]string{
		podclaim.LabelRuntimeProcess: string(runtimestore.RuntimeProcessKeep),
		podclaim.LabelTenant:         lifecycleharness.Tenant,
	}); err != nil {
		t.Fatalf("round %d: %v", round, err)
	}
	if _, err := podclaim.WriteReservedStatus(ctx, h.Cluster.Client, lifecycleharness.Namespace, podclaim.ClaimName(pod), 10*time.Minute, time.Now); err != nil {
		t.Fatalf("round %d: reserve the recycled claim of %s: %v", round, pod, err)
	}
	if err := h.Cluster.ProjectPhase(ctx, h.Cluster.PodOf(pod), "reserved"); err != nil {
		t.Fatalf("round %d: %v", round, err)
	}
}

// awaitClaimPhase waits until the pod's claim reaches phase.
func awaitClaimPhase(t *testing.T, h *lifecycleharness.Harness, round int, pod string, phase claimstate.State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
		if err != nil {
			t.Fatal(err)
		}
		if exists && got == string(phase) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("round %d: claim of %s = %q (exists %v), want %s", round, pod, got, exists, phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireClaimGone fails the round unless the writer's by-name delete ended
// the pod's claim.
func requireClaimGone(t *testing.T, h *lifecycleharness.Harness, round int, pod string) {
	t.Helper()
	if phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod); err != nil || exists {
		t.Fatalf("round %d: claim of %s = %q (exists %v, err %v) after the writer's by-name delete, want it gone", round, pod, phase, exists, err)
	}
}

// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity, Slot assignment atomicity), §4.6.1 (Warm Pool Controller (Pod
// Lifecycle))
// diagnosis: a session on a full concurrent pod ended and released its slot,
// with its release-marker write either recorded or fault-injected, the pod's
// slot set was lost, and concurrent starts on both replicas raced the
// reseed. With the marker recorded exactly one start must take the freed
// slot; with the marker write failed the session is in its post-terminal
// window, the reseed must count it, and every start must be refused. More
// admissions mean the reseed ignored the holder predicate or two reseeds
// raced past the rehydration lock; fewer mean the reseed counted a released
// session; a harness violation means the set or a claim fell out of step
// with the holder predicate.
func TestPostTerminalReseedRacesConcurrentReservations_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, MaxConcurrentSessions: departureCap})
	const racers = 3
	outcomes := map[string]int{}
	for round := 0; round < departureRounds; round++ {
		failMarker := rng.Intn(2) == 1
		h.Cluster.AddIdlePod(t)
		pod, ids := h.FillPod(t, h.Replicas[rng.Intn(2)])
		ended := ids[rng.Intn(len(ids))]
		h.EndWithRelease(t, h.Replicas[rng.Intn(2)], pod, ended, failMarker)
		if err := h.LoseSlotSet(context.Background(), pod); err != nil {
			t.Fatalf("round %d: lose the slot set of %s: %v", round, pod, err)
		}
		codes := make([]int, racers)
		started := make([]string, racers)
		actors := make([]func(), racers)
		for i := range actors {
			r := h.Replicas[rng.Intn(2)]
			actors[i] = func() { started[i], codes[i], _ = h.TryStartSession(r) }
		}
		raceJittered(rng, actors...)
		h.Settle()
		admitted := 0
		for i, code := range codes {
			if code != http.StatusCreated {
				continue
			}
			admitted++
			if got := h.Row(started[i]).PodAssignment; got != pod {
				t.Fatalf("round %d: admitted session %s runs on %q, want the freed slot on %q", round, started[i], got, pod)
			}
		}
		want := 1
		if failMarker {
			want = 0
		}
		if admitted != want {
			t.Fatalf("round %d (marker write failed %v): %d starts admitted, want %d", round, failMarker, admitted, want)
		}
		members, err := h.SlotMembers(context.Background(), pod)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) != departureCap {
			t.Fatalf("round %d: slot set of %s = %v, want %d members", round, pod, members, departureCap)
		}
		if counted := slices.Contains(members, ended); counted != failMarker {
			t.Fatalf("round %d: slot set of %s = %v; ended session %s counted %v, want %v", round, pod, members, ended, counted, failMarker)
		}
		requireNoViolations(t, h, round, "after the race")
		if failMarker {
			outcomes["window session counted, starts refused"]++
		} else {
			outcomes["released session omitted, one start admitted"]++
		}
	}
	t.Logf("outcomes: %v", outcomes)
	h.Check(t)
}
