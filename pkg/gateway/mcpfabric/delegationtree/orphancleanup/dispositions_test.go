// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// dispBase is the instant the seeded trees' first terminal ancestor ended.
var dispBase = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// dispWindow is the cascade timeout the disposition cases configure.
const dispWindow = 30 * time.Minute

// node is a seeded session row: a delegated row sits depth levels below its
// tree root under parent, carries policy as its own cascade policy, and,
// when state is terminal, records terminatedAt.
type node struct {
	id, parent   string
	depth        uint32
	state        session.State
	policy       session.CascadePolicy
	terminatedAt time.Time
}

// seedNodes inserts the nodes in order; a parent precedes its children.
func seedNodes(t *testing.T, store sessionstore.Store, nodes ...node) {
	t.Helper()
	for _, n := range nodes {
		row := sessionstore.Session{
			ID: n.id, TenantID: "acme", State: n.state, ParentSessionID: n.parent,
			DelegationDepth: n.depth, CascadeOnFailure: n.policy,
			CreatedAt: dispBase.Add(-time.Hour), UpdatedAt: dispBase.Add(-time.Hour),
		}
		if session.IsTerminal(n.state) {
			row.TerminatedAt = n.terminatedAt
		}
		seedRow(t, store, row)
	}
}

// stateOf reads one row's state and failure reason.
func stateOf(t *testing.T, store sessionstore.Store, id string) (session.State, string) {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return row.State, row.FailureReason
}

// wantStates asserts each row's state.
func wantStates(t *testing.T, store sessionstore.Store, want map[string]session.State) {
	t.Helper()
	for id, st := range want {
		if got, _ := stateOf(t, store, id); got != st {
			t.Errorf("%s = %q, want %q", id, got, st)
		}
	}
}

// tick runs one pass at now with a metrics sink and returns the pass's ended
// count and the tenant's published active-orphan gauge.
func tick(t *testing.T, store sessionstore.Store, now time.Time) (int, int) {
	t.Helper()
	sink := &fakeMetricsSink{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{CascadeTimeout: dispWindow, Metrics: sink})
	n, err := sw.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	sw.WaitDispatched()
	return n, sink.perTenant["acme"]
}

// spec: §8.10 (Orphan cleanup), §7.2 (Terminal writes from any non-terminal state)
//
// A session under a terminal `cancel_all` parent is cancelled on the pass that
// selects it, long before any cascade window elapses, with no failure reason.
// Before the change the sweep expired it only after the root's window.
func TestOrphanUnderTerminalCancelAllParentIsCancelledOnFirstPass_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "root", state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "parent", parent: "root", depth: 1, state: session.StateFailed, policy: session.CascadeCancelAll, terminatedAt: dispBase},
		node{id: "child", parent: "parent", depth: 2, state: session.StateRunning},
	)
	n, active := tick(t, store, dispBase.Add(time.Second))
	if n != 1 || active != 0 {
		t.Fatalf("pass ended %d with gauge %d, want 1 ended and gauge 0", n, active)
	}
	st, reason := stateOf(t, store, "child")
	if st != session.StateCancelled || reason != "" {
		t.Errorf("child = %q reason %q, want cancelled with no reason", st, reason)
	}
}

// spec: §8.10 (Orphan cleanup)
//
// A delegated session whose parent no longer exists is expired on the pass
// that selects it, whether the parent link was cleared (the foreign key's ON
// DELETE SET NULL) or names a row that is gone. Before the change a cleared
// link took the row out of the sweep entirely.
func TestOrphanWithAbsentParentIsExpiredOnFirstPass_spec_8_10(t *testing.T) {
	cases := []struct {
		name   string
		absent func(t *testing.T, store *memstore.Store)
	}{
		{name: "cleared parent link", absent: func(t *testing.T, store *memstore.Store) {
			if _, err := store.Update(context.Background(), "acme", "child", func(r *sessionstore.Session) error {
				r.ParentSessionID = ""
				return nil
			}); err != nil {
				t.Fatalf("clear link: %v", err)
			}
		}},
		{name: "dangling parent id", absent: func(t *testing.T, store *memstore.Store) {
			if err := store.Delete(context.Background(), "acme", "parent"); err != nil {
				t.Fatalf("delete parent: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seedNodes(
				t, store,
				node{id: "parent", state: session.StateRunning, policy: session.CascadeDetach},
				node{id: "child", parent: "parent", depth: 1, state: session.StateRunning},
			)
			tc.absent(t, store)
			if n, active := tick(t, store, dispBase); n != 1 || active != 0 {
				t.Fatalf("pass ended %d with gauge %d, want 1 and 0", n, active)
			}
			st, reason := stateOf(t, store, "child")
			if st != session.StateExpired || reason != string(session.FailureExpiredDeadline) {
				t.Errorf("child = %q reason %q, want expired with %q", st, reason, session.FailureExpiredDeadline)
			}
		})
	}
}

// spec: §8.10 (Orphan cleanup)
//
// Under a terminal `await_completion` or `detach` parent the session is not
// ended at exactly cascadeTimeout after the anchor and is expired one
// nanosecond later.
func TestOrphanUnderTerminalNonCancelParentExpiresAfterWindow_spec_8_10(t *testing.T) {
	for _, policy := range []session.CascadePolicy{session.CascadeAwaitCompletion, session.CascadeDetach} {
		for _, tc := range []struct {
			at   time.Duration
			want session.State
		}{{dispWindow, session.StateRunning}, {dispWindow + time.Nanosecond, session.StateExpired}} {
			t.Run(fmt.Sprintf("%s at %s", policy, tc.at), func(t *testing.T) {
				store := memstore.New()
				seedNodes(
					t, store,
					node{id: "parent", state: session.StateCompleted, policy: policy, terminatedAt: dispBase},
					node{id: "child", parent: "parent", depth: 1, state: session.StateRunning},
				)
				tick(t, store, dispBase.Add(tc.at))
				wantStates(t, store, map[string]session.State{"child": tc.want})
			})
		}
	}
}

// spec: §8.10 (Orphan cleanup)
//
// The window counts from the earliest terminal transition among the
// session's ancestors. A mid-tree ancestor that ended before the root anchors
// the window, so its live child is expired one window after the mid-tree
// ancestor's end even though the root ended later. A root-anchored window
// leaves the child running at that instant.
func TestOrphanWindowAnchorsOnEarliestTerminalAncestor_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "root", state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "mid", parent: "root", depth: 1, state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "leaf", parent: "mid", depth: 2, state: session.StateRunning},
	)
	setState(t, store, "mid", session.StateCompleted, dispBase)
	setState(t, store, "root", session.StateCompleted, dispBase.Add(20*time.Minute))
	tick(t, store, dispBase.Add(dispWindow))
	wantStates(t, store, map[string]session.State{"leaf": session.StateRunning})
	tick(t, store, dispBase.Add(dispWindow+time.Nanosecond))
	wantStates(t, store, map[string]session.State{"leaf": session.StateExpired})
}

// spec: §8.10 (Orphan cleanup)
//
// A terminal parent above a depth-3 `detach` chain ends every descendant at
// one cascadeTimeout after the parent's TerminatedAt, in the same pass. A
// direct-parent anchor would give each generation its own window and end the
// depth-3 descendant only after three windows.
func TestOrphanDetachChainEndsAtOneWindow_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "root", state: session.StateCancelled, policy: session.CascadeDetach, terminatedAt: dispBase},
		node{id: "c1", parent: "root", depth: 1, state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "c2", parent: "c1", depth: 2, state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "c3", parent: "c2", depth: 3, state: session.StateRunning, policy: session.CascadeDetach},
	)
	if n, active := tick(t, store, dispBase.Add(dispWindow)); n != 0 || active != 3 {
		t.Fatalf("pass at the window's end ended %d with gauge %d, want 0 and 3", n, active)
	}
	if n, active := tick(t, store, dispBase.Add(dispWindow+time.Second)); n != 3 || active != 0 {
		t.Fatalf("pass past the window ended %d with gauge %d, want 3 and 0", n, active)
	}
	wantStates(t, store, map[string]session.State{
		"c1": session.StateExpired, "c2": session.StateExpired, "c3": session.StateExpired,
	})
}

// spec: §8.10 (Orphan cleanup)
//
// A session whose terminal ancestors all carry a zero TerminatedAt opens no
// window: it is counted as an active orphan and is not ended.
func TestOrphanWithZeroTerminatedAtAncestorsIsCountedNotEnded_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "root", state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "mid", parent: "root", depth: 1, state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "leaf", parent: "mid", depth: 2, state: session.StateRunning},
	)
	setState(t, store, "root", session.StateCompleted, time.Time{})
	setState(t, store, "mid", session.StateFailed, time.Time{})
	if n, active := tick(t, store, dispBase.Add(100*dispWindow)); n != 0 || active != 1 {
		t.Fatalf("pass ended %d with gauge %d, want 0 and 1", n, active)
	}
	wantStates(t, store, map[string]session.State{"leaf": session.StateRunning})
}

// spec: §8.10 (Orphan cleanup), §12.6 (StoreRouter)
//
// A derived or replayed root (delegation depth 0) is never selected, with no
// parent or with a parent link to a terminal source, and the child of a live
// parent under a live root is not selected either.
func TestOrphanSelectionSkipsRootsAndLiveTrees_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "source", state: session.StateCompleted, policy: session.CascadeDetach, terminatedAt: dispBase},
		node{id: "derived", state: session.StateRunning},
		node{id: "legacy_derived", parent: "source", state: session.StateRunning},
		node{id: "live_root", state: session.StateRunning},
		node{id: "live_parent", parent: "live_root", depth: 1, state: session.StateRunning},
		node{id: "live_child", parent: "live_parent", depth: 2, state: session.StateRunning},
	)
	if n, active := tick(t, store, dispBase.Add(100*dispWindow)); n != 0 || active != 0 {
		t.Fatalf("pass ended %d with gauge %d, want 0 and 0", n, active)
	}
	wantStates(t, store, map[string]session.State{
		"derived": session.StateRunning, "legacy_derived": session.StateRunning, "live_child": session.StateRunning,
	})
}

// spec: §8.10 (Orphan cleanup)
//
// A live `detach` parent whose own parent no longer exists is expired on the
// pass, and its child stays counted and running on that pass: the child's
// window opens at its parent's end.
func TestOrphanAbsentAncestorAboveLiveDetachParent_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "gone", state: session.StateRunning},
		node{id: "parent", parent: "gone", depth: 1, state: session.StateRunning, policy: session.CascadeDetach},
		node{id: "child", parent: "parent", depth: 2, state: session.StateRunning},
	)
	if err := store.Delete(context.Background(), "acme", "gone"); err != nil {
		t.Fatalf("delete ancestor: %v", err)
	}
	if n, active := tick(t, store, dispBase); n != 1 || active != 1 {
		t.Fatalf("pass ended %d with gauge %d, want 1 and 1", n, active)
	}
	wantStates(t, store, map[string]session.State{"parent": session.StateExpired, "child": session.StateRunning})
}

// spec: §8.10 (Orphan cleanup)
//
// A session under a live parent is counted and not ended while the parent is
// live. The pass that ends a `cancel_all` parent cancels the child on the
// same pass, because the sweep visits the parent first.
func TestOrphanUnderLiveParentWaitsThenCancelsWithParent_spec_8_10(t *testing.T) {
	store := memstore.New()
	seedNodes(
		t, store,
		node{id: "root", state: session.StateCompleted, policy: session.CascadeDetach, terminatedAt: dispBase},
		node{id: "parent", parent: "root", depth: 1, state: session.StateRunning, policy: session.CascadeCancelAll},
		node{id: "child", parent: "parent", depth: 2, state: session.StateRunning},
	)
	if n, active := tick(t, store, dispBase.Add(time.Minute)); n != 0 || active != 2 {
		t.Fatalf("pass inside the window ended %d with gauge %d, want 0 and 2", n, active)
	}
	wantStates(t, store, map[string]session.State{"parent": session.StateRunning, "child": session.StateRunning})
	if n, active := tick(t, store, dispBase.Add(dispWindow+time.Minute)); n != 2 || active != 0 {
		t.Fatalf("pass past the window ended %d with gauge %d, want 2 and 0", n, active)
	}
	wantStates(t, store, map[string]session.State{"parent": session.StateExpired, "child": session.StateCancelled})
}

// spec: §8.10 (Orphan cleanup), §10.1.1 (Primary:)
//
// A store error on one orphan's Update does not stop the pass, and the gauge
// equals the selected sessions minus the applied ends: the failed write and
// the session whose window is open stay counted.
func TestOrphanGaugeIsSelectedMinusAppliedEnds_spec_8_10(t *testing.T) {
	mem := memstore.New()
	seedNodes(
		t, mem,
		node{id: "cancel_parent", state: session.StateFailed, policy: session.CascadeCancelAll, terminatedAt: dispBase},
		node{id: "a_bad", parent: "cancel_parent", depth: 1, state: session.StateRunning},
		node{id: "b_good", parent: "cancel_parent", depth: 1, state: session.StateRunning},
		node{id: "detach_parent", state: session.StateFailed, policy: session.CascadeDetach, terminatedAt: dispBase},
		node{id: "c_waiting", parent: "detach_parent", depth: 1, state: session.StateRunning},
	)
	store := &faultStore{Store: mem, updateErr: map[string]error{"a_bad": errInjectedWrite}}
	sink := &fakeMetricsSink{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{CascadeTimeout: dispWindow, Metrics: sink})
	n, err := sw.Tick(context.Background(), dispBase.Add(time.Minute))
	if !errors.Is(err, errInjectedWrite) {
		t.Fatalf("Tick error = %v, want the injected write failure", err)
	}
	if n != 1 || sink.perTenant["acme"] != 2 || sink.active != 2 {
		t.Fatalf("pass ended %d with gauges %d/%d, want 1 ended and 3 selected minus 1 = 2",
			n, sink.perTenant["acme"], sink.active)
	}
	wantStates(t, mem, map[string]session.State{
		"a_bad": session.StateRunning, "b_good": session.StateCancelled, "c_waiting": session.StateRunning,
	})
}

// spec: §8.10 (Orphan cleanup)
//
// The predicate selects by delegation depth and terminal or absent
// ancestors, reports the direct parent, and anchors on the earliest non-zero
// TerminatedAt. A cyclic chain stops the walk without selecting the row on
// that ground alone.
func TestOrphanedPredicate_spec_8_10(t *testing.T) {
	early, late := dispBase, dispBase.Add(time.Hour)
	rows := []sessionstore.Session{
		{ID: "root", State: session.StateCompleted, TerminatedAt: late},
		{ID: "mid", ParentSessionID: "root", DelegationDepth: 1, State: session.StateFailed, TerminatedAt: early},
		{ID: "leaf", ParentSessionID: "mid", DelegationDepth: 2, State: session.StateRunning},
		{ID: "ended_leaf", ParentSessionID: "mid", DelegationDepth: 2, State: session.StateCancelled},
		{ID: "live_root", State: session.StateRunning},
		{ID: "live_kid", ParentSessionID: "live_root", DelegationDepth: 1, State: session.StateRunning},
		{ID: "broken_mid", ParentSessionID: "missing", DelegationDepth: 1, State: session.StateRunning},
		{ID: "broken_kid", ParentSessionID: "broken_mid", DelegationDepth: 2, State: session.StateRunning},
		{ID: "cyc_a", ParentSessionID: "cyc_b", DelegationDepth: 1, State: session.StateRunning},
		{ID: "cyc_b", ParentSessionID: "cyc_a", DelegationDepth: 1, State: session.StateRunning},
		{ID: "cyc_kid", ParentSessionID: "cyc_a", DelegationDepth: 2, State: session.StateRunning},
		{ID: "zero_root", State: session.StateFailed},
		{ID: "zero_kid", ParentSessionID: "zero_root", DelegationDepth: 1, State: session.StateRunning},
	}
	byID := orphancleanup.IndexByID(rows)
	cases := []struct {
		id      string
		ok      bool
		present bool
		parent  string
		anchor  time.Time
	}{
		{id: "leaf", ok: true, present: true, parent: "mid", anchor: early},
		{id: "ended_leaf"},
		{id: "root"},
		{id: "live_kid", present: true, parent: "live_root"},
		{id: "broken_mid", ok: true},
		{id: "broken_kid", ok: true, present: true, parent: "broken_mid"},
		{id: "cyc_kid", present: true, parent: "cyc_a"},
		{id: "zero_kid", ok: true, present: true, parent: "zero_root"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			o, ok := orphancleanup.Orphaned(byID[tc.id], byID)
			if ok != tc.ok {
				t.Fatalf("Orphaned = %v, want %v", ok, tc.ok)
			}
			if !ok && tc.parent == "" {
				return
			}
			if o.ParentPresent != tc.present || o.Parent.ID != tc.parent || !o.Anchor.Equal(tc.anchor) {
				t.Errorf("Orphan = present %v parent %q anchor %v, want %v %q %v",
					o.ParentPresent, o.Parent.ID, o.Anchor, tc.present, tc.parent, tc.anchor)
			}
		})
	}
}

// writeCounter counts, per session, the Updates that moved a row from a
// non-terminal to a terminal state.
type writeCounter struct {
	sessionstore.Store
	mu     sync.Mutex
	writes map[string]int
}

func (c *writeCounter) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	var ended bool
	row, err := c.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		was := session.IsTerminal(r.State)
		if err := mut(r); err != nil {
			return err
		}
		ended = !was && session.IsTerminal(r.State)
		return nil
	})
	if err == nil && ended {
		c.mu.Lock()
		c.writes[id]++
		c.mu.Unlock()
	}
	return row, err
}

// spec: §8.10 (Orphan cleanup), §7.2 (Terminal writes from any non-terminal state)
//
// Two sweepers over one store, as on two gateway replicas, make one terminal
// write and run one terminal hook per orphan, across the cancel and the
// expiry dispositions, in a randomized start order.
func TestTwoOrphanSweepersEndEachOrphanOnce_spec_8_10(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < 20; round++ {
		mem := memstore.New()
		nodes := []node{
			{id: "cancel_root", state: session.StateFailed, policy: session.CascadeCancelAll, terminatedAt: dispBase},
			{id: "detach_root", state: session.StateFailed, policy: session.CascadeDetach, terminatedAt: dispBase},
		}
		var kids []string
		for i := 0; i < 20; i++ {
			parent := []string{"cancel_root", "detach_root"}[rng.Intn(2)]
			id := fmt.Sprintf("kid%02d", i)
			kids = append(kids, id)
			nodes = append(nodes, node{id: id, parent: parent, depth: 1, state: session.StateRunning, policy: session.CascadeDetach})
		}
		seedNodes(t, mem, nodes...)
		store := &writeCounter{Store: mem, writes: map[string]int{}}
		hook := &fakeTerminalHook{}
		sweepers := []*orphancleanup.Sweeper{
			orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{CascadeTimeout: dispWindow, Terminal: hook}),
			orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{CascadeTimeout: dispWindow, Terminal: hook}),
		}
		rng.Shuffle(len(sweepers), func(i, j int) { sweepers[i], sweepers[j] = sweepers[j], sweepers[i] })
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, sw := range sweepers {
			wg.Add(1)
			go func(sw *orphancleanup.Sweeper) {
				defer wg.Done()
				<-start
				if _, err := sw.Tick(context.Background(), dispBase.Add(2*dispWindow)); err != nil {
					t.Errorf("Tick: %v", err)
				}
			}(sw)
		}
		close(start)
		wg.Wait()
		for _, sw := range sweepers {
			sw.WaitDispatched()
		}
		hooks := map[string]int{}
		hook.mu.Lock()
		for _, c := range hook.calls {
			hooks[c.ID]++
		}
		hook.mu.Unlock()
		for _, id := range kids {
			if st, _ := stateOf(t, mem, id); !session.IsTerminal(st) {
				t.Errorf("round %d: %s is %s after both passes", round, id, st)
			}
			store.mu.Lock()
			w := store.writes[id]
			store.mu.Unlock()
			if w != 1 || hooks[id] != 1 {
				t.Errorf("round %d: %s has %d terminal writes and %d hooks, want 1 and 1", round, id, w, hooks[id])
			}
		}
	}
}
