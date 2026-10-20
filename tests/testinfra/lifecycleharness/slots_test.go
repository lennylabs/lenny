// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// loggedCounter returns a slot counter whose commands feed a fresh SlotLog,
// with the pod's set seeded so no rehydration runs.
func loggedCounter(t *testing.T) (*slotcounter.Counter, *SlotLog, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	log := NewSlotLog()
	rc.AddHook(log)
	if err := mr.Set("lenny:pod:{p1}:rehydrated", "1"); err != nil {
		t.Fatal(err)
	}
	return slotcounter.New(rc), log, mr
}

// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics)
//
// The checker's single-removal invariant reads the slot log, so the log must
// count each removal a release or a reap made inside one reservation, and
// must not count a duplicate release that removed nothing.
func TestSlotLogCountsRemovalsPerReservation_spec_5_2(t *testing.T) {
	ctx := context.Background()
	c, log, mr := loggedCounter(t)
	for _, id := range []string{"s1", "s2", "s3"} {
		if _, _, err := c.Reserve(ctx, "p1", id, 4); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := c.Release(ctx, "p1", "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.Release(ctx, "p1", "s1", false); err != nil {
		t.Fatal(err)
	}
	if got := log.Removals("p1", "s1"); got != 1 {
		t.Errorf("removals of s1 after a release and a duplicate = %d, want 1", got)
	}
	if _, _, _, err := c.Release(ctx, "p1", "s2", true); err != nil {
		t.Fatal(err)
	}
	if got := log.Removals("p1", "s2"); got != 1 {
		t.Errorf("removals of s2 after a leaked release = %d, want 1", got)
	}
	if removed, err := c.Reap(ctx, "p1", "s3"); err != nil || !removed {
		t.Fatalf("Reap = %v, %v", removed, err)
	}
	if got := log.Removals("p1", "s3"); got != 1 {
		t.Errorf("removals of s3 after a reap = %d, want 1", got)
	}
	if over := log.OverRemoved(); len(over) != 0 {
		t.Errorf("OverRemoved = %v, want none", over)
	}

	// A member a seed re-adds without a reservation, as a rehydration
	// after a restart does, and then removes again is a double removal.
	if _, err := mr.SAdd("lenny:pod:{p1}:active_slots", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.Release(ctx, "p1", "s1", false); err != nil {
		t.Fatal(err)
	}
	if over := log.OverRemoved(); !reflect.DeepEqual(over, []string{"p1/s1"}) {
		t.Errorf("OverRemoved = %v, want [p1/s1]", over)
	}

	// A new reservation opens a new episode.
	if _, _, err := c.Reserve(ctx, "p1", "s1", 4); err != nil {
		t.Fatal(err)
	}
	if got := log.Removals("p1", "s1"); got != 0 {
		t.Errorf("removals of s1 after a fresh reservation = %d, want 0", got)
	}
}

// spec: §5.2 (Slot-holder predicate), §6.2 (leaked slot semantics)
//
// The slot-set invariant accepts exactly the non-terminal sessions naming
// the pod plus leaked: members, and the stale-member relaxation accepts a
// terminal session's member and nothing else.
func TestSlotSetViolations_spec_5_2(t *testing.T) {
	rows := map[string]sessionRow{
		"live":   {state: session.StateRunning, pod: "p1"},
		"ended":  {state: session.StateCompleted, pod: "p1"},
		"absent": {state: session.StateRunning, pod: "p1"},
		"other":  {state: session.StateRunning, pod: "p2"},
	}
	cases := []struct {
		name    string
		relax   bool
		members []string
		want    []string
	}{
		{"exact", false, []string{"live", "absent", "leaked:gone"}, nil},
		{"missing live member", false, []string{"live"}, []string{"lacks absent"}},
		{"terminal member", false, []string{"live", "absent", "ended"}, []string{"holds ended, whose session is completed"}},
		{"terminal member relaxed", true, []string{"live", "absent", "ended"}, nil},
		{"member of another pod", true, []string{"live", "absent", "other"}, []string{"holds other, whose running row names pod \"p2\""}},
		{"untracked member", true, []string{"live", "absent", "stranger"}, []string{"which no tracked session is"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Harness{RelaxStaleSlotMembers: tc.relax}
			got := h.slotSetViolations("p1", tc.members, rows)
			if len(got) != len(tc.want) {
				t.Fatalf("violations = %v, want %d matching %v", got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("violation %q does not contain %q", got[i], w)
				}
			}
		})
	}
}

// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §4.6.1 (Orphaned SandboxClaim detection)
//
// On a concurrent-workspace pool a leaked: member, or a counted member whose
// release no replica performed, keeps the shared claim bound after the pod's
// last session ends. The pods-and-claims invariant accepts that claim without
// RelaxOrphanClaims and leaves the pod to the empty-pod slot invariant, while
// a bound claim on an unoccupied pod that no row names is still a violation
// unless RelaxEmptySetClaims accepts it on a concurrent pool.
func TestPodsClaimsAcceptsAnOccupiedConcurrentPod_spec_5_2(t *testing.T) {
	held := map[string]bool{"p-live": true}
	cases := []struct {
		name       string
		relax      bool
		emptyRelax bool
		concurrent bool
		bound      map[string]bool
		occupied   map[string]bool
		want       []string
	}{
		{"exclusive pool", false, false, false, nil, nil, []string{"p-leaked", "p-empty"}},
		{"occupied pod with no row", false, false, true, nil, map[string]bool{"p-leaked": true}, []string{"p-empty"}},
		{"unoccupied pods with no row", false, false, true, nil, map[string]bool{}, []string{"p-leaked", "p-empty"}},
		// The crash relaxation accepts an unbound pod and keeps flagging a
		// pod a surviving replica still binds.
		{"crash relaxation", true, false, false, map[string]bool{"p-leaked": true}, nil, []string{"p-leaked"}},
		// The empty-set relaxation accepts a bound claim on a concurrent
		// pod whose set is empty, as the empty-pod slot invariant does, and
		// changes nothing on an exclusive pool.
		{"empty-set relaxation", false, true, true, nil, map[string]bool{}, nil},
		{"empty-set relaxation on an exclusive pool", false, true, false, nil, nil, []string{"p-leaked", "p-empty"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Harness{RelaxOrphanClaims: tc.relax, RelaxEmptySetClaims: tc.emptyRelax}
			if tc.concurrent {
				h.maxConcurrent = 4
			}
			got := h.podsClaimsViolations([]string{"p-live", "p-leaked", "p-empty"}, held, tc.bound, tc.occupied)
			if len(got) != len(tc.want) {
				t.Fatalf("violations = %v, want %d naming %v", got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], "claim on "+w+" ") {
					t.Errorf("violation %q does not name %s", got[i], w)
				}
			}
		})
	}
}

// spec: §5.2 (sessionPolicy block, gateway-enforced subset; Recycle
// lifecycle)
//
// The harness registers its concurrent pool with the gateway pool registry,
// which validates the session policy, so a recycling concurrent pool must
// carry the reuse limit §5.2 requires, and both pool kinds must register.
func TestConcurrentPoolStoreRegistersBothPoolKinds_spec_5_2(t *testing.T) {
	for _, recycling := range []bool{false, true} {
		pools, err := poolPolicyStore(4, recycling)
		if err != nil {
			t.Fatalf("recycling=%v: %v", recycling, err)
		}
		p, err := pools.Get(context.Background(), Pool)
		if err != nil {
			t.Fatalf("recycling=%v: read the pool: %v", recycling, err)
		}
		if got := p.SessionPolicy.MaxConcurrentSessions; got != 4 {
			t.Errorf("recycling=%v: maxConcurrentSessions = %d, want 4", recycling, got)
		}
		if got := p.SessionPolicy.Recycle != nil && p.SessionPolicy.Recycle.Enabled; got != recycling {
			t.Errorf("recycling=%v: recycle enabled = %v", recycling, got)
		}
	}
}

// spec: §5.2 (Deployer acknowledgment (runtime process kept across
// sessions)), §4.6.1 (within-hold rebind)
//
// The exclusive recycling pool a KeepRuntime harness registers keeps its
// runtime across sessions, so an acquisition rebinds a reserved pod rather
// than ending its hold, and stays an exclusive pool.
func TestPoolPolicyStoreRegistersAnExclusivePoolThatKeepsItsRuntime_spec_5_2(t *testing.T) {
	pools, err := poolPolicyStore(1, true)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	p, err := pools.Get(context.Background(), Pool)
	if err != nil {
		t.Fatalf("read the pool: %v", err)
	}
	if !poolstore.KeepsRuntimeAcrossSessions(p.SessionPolicy) {
		t.Error("the exclusive recycling pool does not keep its runtime across sessions")
	}
	if h := (&Harness{maxConcurrent: 1}); h.Concurrent() {
		t.Error("a pool with one slot per pod reads as a concurrent-workspace pool")
	}
}
