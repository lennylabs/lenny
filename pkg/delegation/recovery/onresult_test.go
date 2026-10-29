// SPDX-License-Identifier: MIT

package recovery_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/delegation/recovery"
)

// traversalEvent is one start or OnResult call, recorded on one sequence.
type traversalEvent struct {
	kind  string // "start" or "result"
	node  recovery.Node
	res   recovery.NodeResult
	depth int
}

// traversalRecorder records start and OnResult calls on one sequence.
type traversalRecorder struct{ events []traversalEvent }

func (r *traversalRecorder) start(n recovery.Node) {
	r.events = append(r.events, traversalEvent{kind: "start", node: n, depth: n.Depth})
}

func (r *traversalRecorder) onResult(res recovery.NodeResult) {
	r.events = append(r.events, traversalEvent{kind: "result", node: res.Node, res: res, depth: res.Node.Depth})
}

func (r *traversalRecorder) results() []recovery.NodeResult {
	var out []recovery.NodeResult
	for _, e := range r.events {
		if e.kind == "result" {
			out = append(out, e.res)
		}
	}
	return out
}

// assertLevelsSettleBeforeNextStart checks the §8.10 per-level ordering on
// a recorded sequence: OnResult is called once per node, in the order of
// Recover's returned results, and every OnResult call for a level precedes
// the first start call of any shallower level.
func assertLevelsSettleBeforeNextStart(t *testing.T, rec *traversalRecorder, nodes []recovery.Node, returned []recovery.NodeResult) {
	t.Helper()
	got := rec.results()
	if !reflect.DeepEqual(got, returned) {
		t.Fatalf("OnResult sequence %+v differs from the returned results %+v", got, returned)
	}
	seen := map[string]int{}
	for _, res := range got {
		seen[res.Node.SessionID]++
	}
	for _, n := range nodes {
		if seen[n.SessionID] != 1 {
			t.Errorf("node %s reported to OnResult %d times, want once", n.SessionID, seen[n.SessionID])
		}
	}
	for i, e := range rec.events {
		if e.kind != "start" {
			continue
		}
		for _, later := range rec.events[i+1:] {
			if later.kind == "result" && later.depth > e.depth {
				t.Fatalf("depth-%d node %s was reported after depth-%d node %s started; a level must settle before the next one starts",
					later.depth, later.node.SessionID, e.depth, e.node.SessionID)
			}
		}
	}
}

// TestRecoverOnResultSettlesEachLevelBeforeNextStart_spec_8_10 asserts that
// OnResult is called once per node in traversal order, and that every call
// for a level precedes the next level's first start, for recovered nodes and
// for nodes whose restore failed.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverOnResultSettlesEachLevelBeforeNextStart_spec_8_10(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "root-child", Depth: 1},
		{SessionID: "mid-a", Depth: 2},
		{SessionID: "mid-b", Depth: 2},
		{SessionID: "leaf-a", Depth: 3},
		{SessionID: "leaf-b", Depth: 3},
	}
	rec := &traversalRecorder{}
	returned := recovery.Recover(context.Background(), nodes, rec.start, func(_ context.Context, n recovery.Node) error {
		if n.SessionID == "leaf-b" || n.SessionID == "mid-a" {
			return errors.New("restore failed")
		}
		return nil
	}, recovery.Config{OnResult: rec.onResult})
	assertLevelsSettleBeforeNextStart(t, rec, nodes, returned)
	want := []string{"leaf-a", "leaf-b", "mid-a", "mid-b", "root-child"}
	var order []string
	for _, res := range rec.results() {
		order = append(order, res.Node.SessionID)
	}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("OnResult order = %v, want %v (bottom-up, by session id within a level)", order, want)
	}
}

// TestRecoverOnResultReportsNodeFailedWithoutStart_spec_8_10 asserts that a
// node whose level budget elapsed before it was driven is reported to
// OnResult as failed, at the point it is failed, with no start call.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverOnResultReportsNodeFailedWithoutStart_spec_8_10(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{{SessionID: "a", Depth: 1}, {SessionID: "b", Depth: 1}}
	rec := &traversalRecorder{}
	returned := recovery.Recover(context.Background(), nodes, func(n recovery.Node) {
		rec.start(n)
		clock = clock.Add(2 * time.Minute) // driving a uses up the level budget
	}, recoverAll, recovery.Config{
		LevelTimeout: time.Minute,
		TreeTimeout:  time.Hour,
		Now:          func() time.Time { return clock },
		OnResult:     rec.onResult,
	})
	assertLevelsSettleBeforeNextStart(t, rec, nodes, returned)
	if len(rec.events) < 2 || rec.events[1].kind != "result" || rec.events[1].node.SessionID != "b" {
		t.Fatalf("events = %+v, want b reported right after a's start, before a's wait returns", rec.events)
	}
	if b := rec.events[1].res; b.Outcome != recovery.OutcomeFailed || b.Reason != recovery.ReasonLevelTimeout {
		t.Errorf("b = %+v, want failed with the level reason", b)
	}
	for _, e := range rec.events {
		if e.kind == "start" && e.node.SessionID == "b" {
			t.Error("b was started past its level budget")
		}
	}
}

// TestRecoverOnResultTreeDeadlineFailsEveryRemainingNode_spec_8_10 asserts
// that once the tree deadline passes, every remaining node of every
// shallower level is reported to OnResult as failed with the tree reason
// and none is started.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverOnResultTreeDeadlineFailsEveryRemainingNode_spec_8_10(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{
		{SessionID: "leaf", Depth: 3},
		{SessionID: "mid-a", Depth: 2},
		{SessionID: "mid-b", Depth: 2},
		{SessionID: "top", Depth: 1},
	}
	rec := &traversalRecorder{}
	returned := recovery.Recover(context.Background(), nodes, rec.start, func(context.Context, recovery.Node) error {
		clock = clock.Add(11 * time.Minute) // past the 10-minute tree budget
		return nil
	}, recovery.Config{
		LevelTimeout: 30 * time.Minute,
		TreeTimeout:  10 * time.Minute,
		Now:          func() time.Time { return clock },
		OnResult:     rec.onResult,
	})
	assertLevelsSettleBeforeNextStart(t, rec, nodes, returned)
	for _, res := range rec.results() {
		if res.Node.SessionID == "leaf" {
			if res.Outcome != recovery.OutcomeRecovered {
				t.Errorf("leaf = %+v, want recovered", res)
			}
			continue
		}
		if res.Outcome != recovery.OutcomeFailed || res.Reason != recovery.ReasonTreeTimeout {
			t.Errorf("%s = %+v, want failed with the tree reason", res.Node.SessionID, res)
		}
	}
	for _, e := range rec.events {
		if e.kind == "start" && e.node.SessionID != "leaf" {
			t.Errorf("%s started past the tree deadline", e.node.SessionID)
		}
	}
}

// TestRecoverNilOnResultReturnsSameResults_spec_8_10 asserts that a nil
// OnResult is safe and leaves the returned results unchanged.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverNilOnResultReturnsSameResults_spec_8_10(t *testing.T) {
	nodes := []recovery.Node{{SessionID: "a", Depth: 2}, {SessionID: "b", Depth: 1}}
	fail := func(_ context.Context, n recovery.Node) error {
		if n.SessionID == "a" {
			return errors.New("restore failed")
		}
		return nil
	}
	withNil := recovery.Recover(context.Background(), nodes, nil, fail, recovery.Config{})
	rec := &traversalRecorder{}
	withCallback := recovery.Recover(context.Background(), nodes, nil, fail, recovery.Config{OnResult: rec.onResult})
	if !reflect.DeepEqual(withNil, withCallback) {
		t.Errorf("nil OnResult results %+v differ from %+v", withNil, withCallback)
	}
}

// TestRecoverOnResultRandomizedTrees_spec_8_10 runs the traversal over
// randomized trees, restore outcomes, and clock jumps, and checks the
// per-level ordering invariant on every run. The seed is logged.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverOnResultRandomizedTrees_spec_8_10(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for run := 0; run < 200; run++ {
		var nodes []recovery.Node
		for i, n := 0, 1+rng.Intn(12); i < n; i++ {
			nodes = append(nodes, recovery.Node{SessionID: fmt.Sprintf("n%02d", i), Depth: 1 + rng.Intn(4)})
		}
		clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		rec := &traversalRecorder{}
		returned := recovery.Recover(context.Background(), nodes, rec.start, func(context.Context, recovery.Node) error {
			clock = clock.Add(time.Duration(rng.Intn(90)) * time.Second)
			if rng.Intn(3) == 0 {
				return errors.New("restore failed")
			}
			return nil
		}, recovery.Config{
			LevelTimeout: time.Minute,
			TreeTimeout:  4 * time.Minute,
			Now:          func() time.Time { return clock },
			OnResult:     rec.onResult,
		})
		assertLevelsSettleBeforeNextStart(t, rec, nodes, returned)
		if t.Failed() {
			t.Fatalf("run %d failed (seed %d)", run, seed)
		}
	}
}
