// SPDX-License-Identifier: MIT

package deadlock_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
)

// clearanceRig is a live detector over P awaiting C1 and C2, each blocked
// on its own request_input, with a mutable row table the node lookup reads.
type clearanceRig struct {
	det  *deadlock.Detector
	reg  *inputwait.Registry
	m    *fakeMetrics
	mu   sync.Mutex
	rows map[string]session.State
}

func newClearanceRig(t *testing.T) *clearanceRig {
	t.Helper()
	r := &clearanceRig{
		reg:  inputwait.NewRegistry(),
		m:    &fakeMetrics{},
		rows: map[string]session.State{"P": session.StateRunning, "C1": session.StateRunning, "C2": session.StateRunning},
	}
	r.reg.SetClock(func() time.Time { return base })
	tracker := deadlock.NewAwaitTracker()
	t.Cleanup(tracker.Begin("acme", "P", []string{"C1", "C2"}))
	for _, id := range []string{"C1", "C2"} {
		if _, err := r.reg.Register(id, "r-"+id, nil); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
	}
	lookup := func(_ context.Context, _, id string) (deadlock.NodeInfo, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		st, ok := r.rows[id]
		if !ok {
			return deadlock.NodeInfo{}, false
		}
		return deadlock.NodeInfo{State: st, Pool: "pool-" + id}, true
	}
	fail := func(context.Context, string, string) bool { return true }
	r.det = deadlock.NewDetector(deadlock.NewManager(120*time.Second, r.m), tracker, r.reg, lookup, fail)
	return r
}

// end leaves id's wait and settles its row in state.
func (r *clearanceRig) end(id string, state session.State, answered bool) {
	if answered {
		_ = r.reg.Resolve(id, "r-"+id, "yes")
	} else {
		r.reg.Cancel(id, "r-"+id)
	}
	r.mu.Lock()
	r.rows[id] = state
	r.mu.Unlock()
}

// TestDetectorClassifiesClearance_spec_16_1 pins the §16.1 resolution a
// subtree that clears before its timeout records. An answered wait records
// `client_input`; a cancelled blocked child records `child_cancel`, and
// takes precedence over an answer seen in the same sweep; children that end
// on their own, a wait that leaves the registry unanswered, and rows that
// are gone record nothing, because §16.1 lists no value for them. Every
// recorded resolution carries the root's pool and the latency from the
// first detection, and no case records `resolved`.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestDetectorClassifiesClearance_spec_16_1(t *testing.T) {
	cases := []struct {
		name  string
		clear func(r *clearanceRig)
		want  []string
	}{
		{name: "one wait answered", want: []string{deadlock.ResolutionClientInput}, clear: func(r *clearanceRig) {
			r.end("C1", session.StateRunning, true)
		}},
		{name: "one child cancelled, the other completed", want: []string{deadlock.ResolutionChildCancel}, clear: func(r *clearanceRig) {
			r.end("C1", session.StateCancelled, false)
			r.end("C2", session.StateCompleted, false)
		}},
		{name: "cancel and answer in one sweep", want: []string{deadlock.ResolutionChildCancel}, clear: func(r *clearanceRig) {
			r.end("C1", session.StateRunning, true)
			r.end("C2", session.StateCancelled, false)
		}},
		{name: "children completed on their own", clear: func(r *clearanceRig) {
			r.end("C1", session.StateCompleted, false)
			r.end("C2", session.StateCompleted, false)
		}},
		{name: "children failed", clear: func(r *clearanceRig) {
			r.end("C1", session.StateFailed, false)
			r.end("C2", session.StateFailed, false)
		}},
		{name: "wait left unanswered", clear: func(r *clearanceRig) {
			r.end("C1", session.StateRunning, false)
		}},
		{name: "child rows gone", clear: func(r *clearanceRig) {
			for _, id := range []string{"C1", "C2"} {
				r.reg.Cancel(id, "r-"+id)
				r.mu.Lock()
				delete(r.rows, id)
				r.mu.Unlock()
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newClearanceRig(t)
			r.det.RunOnce(context.Background(), base)
			if r.m.detected != 1 || !reflect.DeepEqual(r.m.detectedPools, []string{"pool-P"}) {
				t.Fatalf("detections = %d pools %v, want one under pool-P", r.m.detected, r.m.detectedPools)
			}
			tc.clear(r)
			r.det.RunOnce(context.Background(), base.Add(45*time.Second))
			if _, ok := r.det.Manager().Event("P"); ok {
				t.Fatal("Event(P) still active, want the subtree cleared")
			}
			if !reflect.DeepEqual(r.m.resolutions, tc.want) {
				t.Fatalf("resolutions = %v, want %v", r.m.resolutions, tc.want)
			}
			if len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(r.m.resolvedPools, []string{"pool-P"}) || r.m.durations[0] != 45 {
				t.Errorf("resolution pools %v duration %v, want [pool-P] 45", r.m.resolvedPools, r.m.durations)
			}
		})
	}
}

// TestDetectorAnswerWhileStillDeadlockedIsNotAttributed_spec_16_1 pins the
// per-sweep baseline: an answer to one of two waits that leaves the subtree
// deadlocked is absorbed by the next sweep, so when the subtree later clears
// because the remaining child completes on its own, no `client_input` is
// recorded for the earlier answer.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestDetectorAnswerWhileStillDeadlockedIsNotAttributed_spec_16_1(t *testing.T) {
	r := newClearanceRig(t)
	ctx := context.Background()
	r.det.RunOnce(ctx, base)
	// C1 is answered and completes; C2 still blocks P, so P stays deadlocked.
	r.end("C1", session.StateCompleted, true)
	r.det.RunOnce(ctx, base.Add(10*time.Second))
	if _, ok := r.det.Manager().Event("P"); !ok {
		t.Fatal("Event(P) absent, want the subtree still deadlocked on C2")
	}
	r.end("C2", session.StateCompleted, false)
	r.det.RunOnce(ctx, base.Add(20*time.Second))
	if len(r.m.resolutions) != 0 {
		t.Errorf("resolutions = %v, want none for a clearing no listed cause explains", r.m.resolutions)
	}
}
