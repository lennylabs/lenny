// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// releasePathRounds is the number of rounds with a live sibling on the pod,
// after the round that empties it.
const releasePathRounds = 6

// spec: §5.2 (Slot assignment atomicity; Recycle lifecycle), §6.2 (leaked
// slot semantics), §4.6.3 (SandboxClaim: a release that ends the occupancy
// without reuse)
// diagnosis: the three release paths of one concurrent-workspace slot raced
// and removed the member twice, lost a leaked release's leaked: member, left
// the session's plain member counted, ran the occupancy-zero disposition on a
// removal that did not empty the set or twice on one that did (a recycle
// patch followed by a delete), or left the replica binding the ended
// session. The paths are the post-terminal hook's slot-aware reclaim
// (Binder.ReclaimClaimed), the binding replica's terminal binding release
// (Server.ReleaseTerminalBinding, which sends the session's Shutdown and
// recycles on a recycling pool), and a failed bind's reservation release
// (Binder.ReleaseSlotReservation, which never recycles).
func TestSlotReleasePathsRaceRemovesTheMemberOnce_spec_5_2(t *testing.T) {
	for _, recycling := range []bool{false, true} {
		t.Run(fmt.Sprintf("recycling=%v", recycling), func(t *testing.T) {
			seed := time.Now().UnixNano()
			t.Logf("seed %d", seed)
			rng := rand.New(rand.NewSource(seed))
			h := lifecycleharness.New(t, lifecycleharness.Options{
				Replicas: 2, Pods: 2, MaxConcurrentSessions: 12, Recycling: recycling,
			})
			race := &releasePathRace{h: h, rng: rng, recycling: recycling, binder: h.Replicas[0], peer: h.Replicas[1]}
			race.round(t, 0, "")
			sibling := h.StartSession(race.peer)
			for round := 1; round <= releasePathRounds; round++ {
				race.round(t, round, sibling)
			}
			h.Check(t)
		})
	}
}

// releasePathRace races the release paths of one session's slot on the
// lifecycle harness's concurrent pool.
type releasePathRace struct {
	h         *lifecycleharness.Harness
	rng       *rand.Rand
	recycling bool
	// binder binds the racing session and runs its terminal binding
	// release; peer runs the hook's reclaim and the bind-failure release.
	binder, peer *lifecycleharness.Replica
}

// round starts a session on the binder, ends its row without a hook, races
// the three release paths, and asserts the outcome. With no sibling the
// session is the pod's only member, so the winning removal empties the set
// and runs the occupancy-zero disposition.
func (rp *releasePathRace) round(t *testing.T, round int, sibling string) {
	t.Helper()
	h := rp.h
	id := h.StartSession(rp.binder)
	pod := h.Row(id).PodAssignment
	if sibling != "" && h.Row(sibling).PodAssignment != pod {
		t.Fatalf("round %d: the session is on %s, want the sibling's pod %s", round, pod, h.Row(sibling).PodAssignment)
	}
	shutdowns := h.Cluster.PodOf(pod).UnaryCalls("Shutdown")
	row := endWithoutHook(t, h, id)
	leaked := sibling != "" && rp.rng.Intn(2) == 0
	ctx := context.Background()
	raceJittered(rp.rng,
		func() {
			if err := rp.peer.Binder.ReclaimClaimed(ctx, pod, id, true); err != nil {
				t.Errorf("round %d: hook reclaim: %v", round, err)
			}
		},
		func() {
			rp.binder.Server.ReleaseTerminalBinding(row)
			awaitUnbound(t, rp.binder, id)
		},
		func() {
			if err := rp.peer.Binder.ReleaseSlotReservation(ctx, pod, id, leaked); err != nil {
				t.Errorf("round %d: bind-failure release: %v", round, err)
			}
		})
	h.Settle()
	rp.check(t, round, pod, id, sibling, leaked, h.Cluster.PodOf(pod).UnaryCalls("Shutdown")-shutdowns)
}

// check asserts one round's outcome and the harness invariants.
func (rp *releasePathRace) check(t *testing.T, round int, pod, id, sibling string, leaked bool, shutdowns int) {
	t.Helper()
	h := rp.h
	members, err := h.SlotMembers(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, m := range members {
		set[m] = true
	}
	switch {
	case set[id]:
		t.Fatalf("round %d: the session's plain member is still counted: %v", round, members)
	case leaked && !set[slotcounter.LeakedMember(id)]:
		t.Fatalf("round %d: the leaked bind-failure release left no leaked member: %v", round, members)
	case h.Slots.Removals(pod, id) != 1:
		t.Fatalf("round %d: removals of the session's member = %d, want 1", round, h.Slots.Removals(pod, id))
	}
	phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if sibling != "" {
		if !set[sibling] || phase != string(claimstate.Bound) || shutdowns != 1 {
			t.Fatalf("round %d: sibling counted %v, claim %q, Shutdowns %d; want the sibling counted, the claim bound, and the session's one Shutdown", round, set[sibling], phase, shutdowns)
		}
	} else {
		rp.checkEmptyingDisposition(t, phase, exists, shutdowns)
	}
	if v := h.Violations(); len(v) != 0 {
		t.Fatalf("round %d: harness invariants: %v", round, v)
	}
}

// checkEmptyingDisposition asserts the one disposition of the release that
// emptied the set. The terminal binding release recycles on a recycling pool
// and then sends the whole-pod recycle Shutdown after the session's own; the
// hook's reclaim and the bind-failure release never recycle and delete the
// claim. A recycle patch followed by a delete, or a delete that also sent the
// recycle Shutdown, is a second disposition.
func (rp *releasePathRace) checkEmptyingDisposition(t *testing.T, phase string, exists bool, shutdowns int) {
	t.Helper()
	switch {
	case !exists && shutdowns == 1:
	case rp.recycling && exists && phase == string(claimstate.Recycling) && shutdowns == 2:
	default:
		t.Fatalf("emptying round: claim %q (exists %v) with %d Shutdowns; want a deleted claim with the session's Shutdown, or on a recycling pool a recycling claim with the session's and the recycle Shutdown", phase, exists, shutdowns)
	}
}

// endWithoutHook writes the session's row completed directly in the store,
// so no post-terminal hook runs and the race below supplies every release.
func endWithoutHook(t *testing.T, h *lifecycleharness.Harness, id string) sessionstore.Session {
	t.Helper()
	row, err := h.Inner.Update(context.Background(), lifecycleharness.Tenant, id, func(r *sessionstore.Session) error {
		from := r.State
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, from, h.Clock.Now())
		return nil
	})
	if err != nil {
		t.Fatalf("end %s: %v", id, err)
	}
	return row
}

// awaitUnbound waits until the replica no longer binds the session, which
// the asynchronous terminal binding release does once it has released.
func awaitUnbound(t *testing.T, r *lifecycleharness.Replica, id string) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.Registry.Get(id); !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("%s still binds %s after its terminal binding release", r.ID, id)
}
