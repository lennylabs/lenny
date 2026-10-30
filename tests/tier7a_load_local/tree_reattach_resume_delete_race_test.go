// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local races for two restore entry points on the lifecycle
// harness. The first races the delegation-tree recovery's ReattachNode,
// which a resumed node's commit tail starts, against the watchdog's resume
// driver on one resume_pending descendant, and asserts that the descendant
// is restored on exactly one claimed pod. The second races POST /resume
// against DELETE and asserts that the session ends cancelled with no pod
// claimed. Each race runs many iterations with a seeded start order and
// jitter, and the invariant checker runs after every iteration.
//
// spec: §8.10 (Delegation Tree Recovery), §7.3 (Resume driver), §7.3 (Retry
// and Resume), §15.1 (REST API), §7.2 (Interactive Session Model)
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// raceIterations is how many iterations each race runs: the value of
// LENNY_RACE_ITERATIONS when set, so a soak run can raise it, or def.
func raceIterations(t *testing.T, def int) int {
	t.Helper()
	s := os.Getenv("LENNY_RACE_ITERATIONS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		t.Fatalf("LENNY_RACE_ITERATIONS = %q, want a positive integer", s)
	}
	return n
}

// treeReattachWait bounds how long ReattachNode can still be polling after
// the descendant left resume_pending and resuming: one tree-recovery poll
// interval (one second) plus slack for the race detector.
const treeReattachWait = 1500 * time.Millisecond

// restoreDeadline bounds the wall-clock wait for a race's restores to leave
// resume_pending and resuming.
const restoreDeadline = 20 * time.Second

// raceChildSeq numbers the delegated children the races commit.
var raceChildSeq atomic.Int64

// raceDelegatedChild commits a child of rootID in created, as the delegation
// service does, and materializes it on r, which then coordinates it.
func raceDelegatedChild(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, rootID string) string {
	t.Helper()
	id := fmt.Sprintf("00000000-0000-4000-a000-%012d", raceChildSeq.Add(1))
	ctx := context.Background()
	if err := h.Store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: lifecycleharness.Tenant, UserID: "alice@acme.com",
		RuntimeRef: lifecycleharness.RuntimeRef, IsolationProfile: isolation.ProfileSandboxed,
		State: session.StateCreated, ParentSessionID: rootID, RootSessionID: rootID, DelegationDepth: 1,
	}); err != nil {
		t.Fatalf("commit the delegated child: %v", err)
	}
	h.Track(id)
	if st, err := r.Server.MaterializeDelegatedChild(ctx, lifecycleharness.Tenant, id); err != nil || st != session.StateRunning {
		t.Fatalf("materialize the delegated child on %s = %q, %v; want running", r.ID, st, err)
	}
	return id
}

// restoring reports whether a state is one a restore has not finished.
func restoring(st session.State) bool {
	return st == session.StateResumePending || st == session.StateResuming
}

// awaitRestored waits on the wall clock, without stepping the harness
// clock or ticking a loop, until no listed session is resume_pending or
// resuming, and fails the test when the deadline passes first.
func awaitRestored(t *testing.T, h *lifecycleharness.Harness, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(restoreDeadline)
	for {
		pending := ""
		for _, id := range ids {
			if st := h.Row(id).State; restoring(st) {
				pending = fmt.Sprintf("%s is %s", id, st)
				break
			}
		}
		if pending == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restores did not finish within %s: %s", restoreDeadline, pending)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitState waits on the wall clock until the session reaches want, and
// reports whether it did before the deadline.
func awaitState(h *lifecycleharness.Harness, id string, want session.State, deadline time.Duration) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if h.Row(id).State == want {
			return true
		}
		time.Sleep(200 * time.Microsecond)
	}
	return false
}

// jitter sleeps a random 0 to max microseconds drawn from rng.
func jitter(rng *rand.Rand, maxMicros int) time.Duration {
	return time.Duration(rng.Intn(maxMicros+1)) * time.Microsecond
}

// runShuffled starts every op on its own goroutine behind one rendezvous,
// in an order and with per-op delays taken from rng, and waits for all of
// them.
func runShuffled(rng *rand.Rand, maxMicros int, ops ...func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, i := range rng.Perm(len(ops)) {
		op, delay := ops[i], jitter(rng, maxMicros)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(delay)
			op()
		}()
	}
	close(start)
	wg.Wait()
}

// toAwaitingClientActionRace spends the session's retry budget, one restored
// crash at a time, until the next crash parks it in awaiting_client_action.
func toAwaitingClientActionRace(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
	t.Helper()
	for i := 0; i <= lifecycleharness.MaxRetries; i++ {
		lifecycleReport(t, r, id, string(session.FailureRuntimeCrash))
		h.AwaitState(id, session.StateRunning, 10)
	}
	if st := h.Row(id).State; st != session.StateAwaitingClientAction {
		t.Fatalf("after the retry budget %s is %q, want awaiting_client_action", id, st)
	}
}

// boundClaimsOn returns the bound claims that name one of pods.
func boundClaimsOn(t *testing.T, h *lifecycleharness.Harness, pods []string) []string {
	t.Helper()
	claims, err := h.Cluster.BoundClaims(context.Background())
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}
	want := map[string]bool{}
	for _, p := range pods {
		want[p] = true
	}
	var out []string
	for _, c := range claims {
		if want[c] {
			out = append(out, c)
		}
	}
	return out
}

// spec: §8.10 (Delegation Tree Recovery), §7.3 (Resume driver)
// diagnosis: the delegation-tree recovery that a resumed root starts and the
// watchdog's resume driver both restored one resume_pending descendant, so
// the descendant claimed a second pod (the in-flight attempt record or the
// primary re-read in DriveResume no longer admits one restore per row), or
// ReattachNode reported the restored descendant as failed, so the traversal
// failed a node that the watchdog driver restored. The iteration, placement,
// and seed are in the log; LENNY_LIFECYCLE_SEED replays the start orders.
func TestTreeReattachRacingWatchdogDriverClaimsOnePod_spec_8_10(t *testing.T) {
	seed := lifecycleSeed(t)
	t.Logf("seed %d (replay with LENNY_LIFECYCLE_SEED=%d)", seed, seed)
	runs := raceIterations(t, 8)
	placements := []struct {
		name string
		// child is the index of the replica that coordinates the
		// descendant; the root is always coordinated by replica 0, which
		// runs the tree recovery.
		child int
	}{
		{name: "descendant on the recovering replica", child: 0},
		{name: "descendant on a peer replica", child: 1},
	}
	for pi, pl := range placements {
		t.Run(pl.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed + int64(pi)))
			// Each iteration claims four pods: the root's and the
			// descendant's first pods and one restore pod for each.
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 4*runs + 8, CredentialPool: true})
			r1 := h.Replicas[0]
			coord, peer := h.Replicas[pl.child], h.Replicas[1-pl.child]
			reattached := 0
			for run := 0; run < runs; run++ {
				t.Run("iteration "+strconv.Itoa(run), func(t *testing.T) {
					root := h.StartSession(r1)
					child := raceDelegatedChild(t, h, coord, root)
					lifecycleReport(t, coord, child, string(session.FailureRuntimeCrash))
					lifecycleReport(t, r1, root, string(session.FailureRuntimeCrash))
					for _, id := range []string{root, child} {
						if st := h.Row(id).State; st != session.StateResumePending {
							t.Fatalf("pre-state of %s = %q, want resume_pending", id, st)
						}
					}
					rootPending := h.Row(root)
					claimsBefore := h.Claims.Len()
					outcomesBefore := len(h.TreeMetrics.Outcomes())

					// mode 0 ticks the watchdogs with the root's driver;
					// mode 1 ticks them once the root committed, when its
					// commit tail is starting the tree recovery; mode 2
					// ticks them later, once the tree recovery has listed
					// the still resume_pending descendant and ReattachNode
					// is polling. The first iterations take each mode once,
					// so every run reaches ReattachNode at least once.
					mode := run % 3
					if run >= 3 {
						mode = rng.Intn(3)
					}
					ctx := context.Background()
					tick := func(r *lifecycleharness.Replica) func() {
						return func() {
							if mode > 0 && !awaitState(h, root, session.StateRunning, restoreDeadline) {
								return
							}
							if mode == 2 {
								time.Sleep(50*time.Millisecond + jitter(rng2(seed, run), 20000))
							}
							_, _ = r.Watchdog.Tick(ctx, h.Clock.Now())
						}
					}
					runShuffled(rng, 2000,
						func() { r1.Server.DriveResume(ctx, rootPending) },
						tick(coord),
						tick(peer))
					awaitRestored(t, h, root, child)
					time.Sleep(treeReattachWait)
					h.Settle()

					rootRow, childRow := h.Row(root), h.Row(child)
					if rootRow.State != session.StateRunning || childRow.State != session.StateRunning {
						t.Fatalf("after the race the root is %q and the descendant %q, want both running (mode %d)",
							rootRow.State, childRow.State, mode)
					}
					claimed := h.Claims.Since(claimsBefore)
					if len(claimed) != 2 {
						t.Errorf("the race created %d claims %v, want 2: one restore pod each for the root and the descendant (mode %d)",
							len(claimed), claimed, mode)
					}
					want := map[string]bool{rootRow.PodAssignment: true, childRow.PodAssignment: true}
					for _, pod := range claimed {
						if !want[pod] {
							t.Errorf("the race claimed %s, which neither the root (%s) nor the descendant (%s) runs on (mode %d)",
								pod, rootRow.PodAssignment, childRow.PodAssignment, mode)
						}
					}
					if rootRow.PodAssignment == childRow.PodAssignment {
						t.Errorf("the root and the descendant both run on %s", rootRow.PodAssignment)
					}
					if b := h.Bound(child); b != coord {
						t.Errorf("the descendant is bound on %v, want its coordinator %s", b, coord.ID)
					}
					outcomes := h.TreeMetrics.Outcomes()[outcomesBefore:]
					for _, o := range outcomes {
						if o != "full_success" {
							t.Errorf("tree recovery outcome = %s, want full_success: ReattachNode failed a descendant the driver restored", o)
						}
					}
					if len(outcomes) > 0 {
						reattached++
					}
					h.Check(t)
				})
			}
			t.Logf("tree recovery selected the descendant in %d of %d iterations", reattached, runs)
			if reattached == 0 {
				t.Errorf("no iteration reached ReattachNode on the descendant, so the race was not exercised")
			}
		})
	}
}

// rng2 derives a per-iteration source for a delay drawn on a goroutine, so
// the shared rng is never read concurrently.
func rng2(seed int64, run int) *rand.Rand {
	return rand.New(rand.NewSource(seed*31 + int64(run)))
}

// spec: §7.3 (Retry and Resume), §15.1 (REST API), §7.2 (Interactive Session
// Model), §7.3 (Resume driver)
// diagnosis: POST /resume racing DELETE on an awaiting_client_action or a
// podless suspended session left the session in a state other than
// cancelled, answered something other than 200 or a precondition refusal,
// or claimed a pod: either POST /resume claimed a pod itself, which the
// store-only write must never do, or a resume driver restored a session the
// DELETE had cancelled. With the watchdog driver also racing, a pod it
// claimed before the DELETE was not released. The iteration, pre-state,
// and seed are in the log; LENNY_LIFECYCLE_SEED replays the start orders.
func TestResumeRacingDeleteEndsCancelledWithNoPodClaimed_spec_7_3(t *testing.T) {
	seed := lifecycleSeed(t)
	t.Logf("seed %d (replay with LENNY_LIFECYCLE_SEED=%d)", seed, seed)
	runs := raceIterations(t, 8)
	preStates := []struct {
		name string
		// pods is how many pods reaching the pre-state claims.
		pods  int
		reach func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string
	}{
		{
			name: "awaiting_client_action",
			pods: lifecycleharness.MaxRetries + 1,
			reach: func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
				id := h.StartSession(r)
				toAwaitingClientActionRace(t, h, r, id)
				return id
			},
		},
		{
			name: "podless suspended",
			pods: 1,
			reach: func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
				id := h.StartSession(r)
				h.SuspendIdle(r, id)
				if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment != "" {
					t.Fatalf("pre-state %q on %q, want podless suspended", row.State, row.PodAssignment)
				}
				return id
			},
		},
	}
	for si, ps := range preStates {
		t.Run(ps.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed + int64(si)))
			// Each iteration claims the pre-state's pods, and the driver
			// variant claims at most one restore pod more.
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: (ps.pods+1)*runs + 8, CredentialPool: true})
			var resumedFirst, driven int
			for run := 0; run < runs; run++ {
				t.Run("iteration "+strconv.Itoa(run), func(t *testing.T) {
					id := ps.reach(t, h, h.Replicas[0])
					claimsBefore := h.Claims.Len()
					// The driver variant also ticks every replica's
					// watchdog, the resume driver, so a resume_pending row
					// the POST /resume wrote can be claimed before the
					// DELETE lands.
					withDriver := rng.Intn(2) == 1
					rResume := h.Replicas[rng.Intn(len(h.Replicas))]
					rDelete := h.Replicas[rng.Intn(len(h.Replicas))]
					var resumeCode, deleteCode int
					var resumeBody map[string]any
					ops := []func(){
						func() {
							resumeCode, resumeBody = h.Do(rResume, http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
						},
						func() { deleteCode, _ = h.Do(rDelete, http.MethodDelete, "/v1/sessions/"+id, nil) },
					}
					if withDriver {
						driven++
						for _, r := range h.Replicas {
							ops = append(ops, func() { _, _ = r.Watchdog.Tick(context.Background(), h.Clock.Now()) })
						}
					}
					// A wide jitter lets the driver's claim land between
					// the two writes; a narrow one races the writes
					// themselves.
					spread := 2000
					if rng.Intn(2) == 1 {
						spread = 20000
					}
					runShuffled(rng, spread, ops...)
					h.Settle()

					if deleteCode != http.StatusOK {
						t.Errorf("DELETE answered %d, want 200: every non-terminal pre-state admits it", deleteCode)
					}
					switch resumeCode {
					case http.StatusOK:
						resumedFirst++
						if st, _ := resumeBody["state"].(string); st != string(session.StateResumePending) {
							t.Errorf("POST /resume answered 200 with state %q, want resume_pending", st)
						}
					case http.StatusConflict:
						// The DELETE landed first, and the precondition
						// refuses a resume of a cancelled session.
					default:
						t.Errorf("POST /resume answered %d %v, want 200 or 409", resumeCode, resumeBody)
					}
					// Run the checker's passes, which tick every replica's
					// watchdog and Sweeper, so a driver that would restore
					// the cancelled session gets every chance to claim.
					h.RunToQuiescence()
					row := h.Row(id)
					if row.State != session.StateCancelled {
						t.Fatalf("after the race the session is %q, want cancelled (driver %v)", row.State, withDriver)
					}
					claimed := h.Claims.Since(claimsBefore)
					if !withDriver && len(claimed) != 0 {
						t.Errorf("the race claimed %v, want no pod: POST /resume is a store write and the cancelled session is never restored", claimed)
					}
					if withDriver && len(claimed) > 1 {
						t.Errorf("the race claimed %v, want at most one restore pod", claimed)
					}
					if left := boundClaimsOn(t, h, claimed); len(left) != 0 {
						t.Errorf("claims %v the race created are still bound after the session was cancelled", left)
					}
					if b := h.Bound(id); b != nil {
						t.Errorf("replica %s still binds the cancelled session", b.ID)
					}
					h.Check(t)
				})
			}
			t.Logf("POST /resume landed first in %d of %d iterations; the watchdog driver raced in %d", resumedFirst, runs, driven)
		})
	}
}
