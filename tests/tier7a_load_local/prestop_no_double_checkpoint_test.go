// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §10.1 line 169 quiesce-and-hold
// drain: across a whole preStop drain no coordinated session is
// checkpointed twice. Under quiesce-and-hold the barrier drives a full
// gateway-side Checkpoint for every session it acks, so the post-barrier
// per-session loop must skip every barrier-acked session and cover only
// the barrier-unreachable ones (the §10.1 lines 169-172 partial-capture
// cases). The invariant is exercised under the race detector with many
// concurrent sessions so the barrier fan-out and the loop race on the
// shared checkpoint counter.
//
// spec: §10.1 lines 163-172 (CheckpointBarrier protocol, quiesce-and-hold,
// BarrierAck-timeout partial-capture), §4.4 line 259 (eviction trigger).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
)

// drainCheckpointer is the single in-process checkpointer both the barrier
// driver and the post-barrier loop share in production. It counts, per
// session, the total drives, the successful drives, and every trigger it
// was invoked with. A session whose pod is unreachable errors on every
// drive (both the barrier-side concurrent drive and the loop's retry),
// mirroring that one unreachable pod fails both paths.
type drainCheckpointer struct {
	mu          sync.Mutex
	drives      map[string]int
	successes   map[string]int
	unreachable map[string]bool
	sawNonEvict bool
}

func newDrainCheckpointer(unreachable map[string]bool) *drainCheckpointer {
	return &drainCheckpointer{
		drives:      map[string]int{},
		successes:   map[string]int{},
		unreachable: unreachable,
	}
}

func (c *drainCheckpointer) CheckpointWithTrigger(_ context.Context, _, sessionID string, trigger checkpoint.Trigger) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drives[sessionID]++
	if trigger != checkpoint.TriggerEviction {
		c.sawNonEvict = true
	}
	if c.unreachable[sessionID] {
		return errors.New("pod unreachable")
	}
	c.successes[sessionID]++
	return nil
}

// drainDispatcher acks every reachable session and errors every
// unreachable one, so the unreachable sessions fall through to the loop.
type drainDispatcher struct {
	unreachable map[string]bool
}

func (d drainDispatcher) Send(_ context.Context, t barrier.Target, _ string) (barrier.Ack, error) {
	if d.unreachable[t.SessionID] {
		return barrier.Ack{}, errors.New("deadline exceeded")
	}
	return barrier.Ack{CheckpointRef: "ck-" + t.SessionID}, nil
}

// drainLister returns the fixed barrier-target set.
type drainLister struct {
	targets []barrier.Target
}

func (l drainLister) Targets(context.Context) ([]barrier.Target, string, error) {
	return l.targets, barrier.SourcePostgres, nil
}

// drainBarrierDispatch adapts a *barrier.Coordinator to the
// prestop.BarrierDispatcher, surfacing the Outcome set so the hook can
// skip barrier-acked sessions.
type drainBarrierDispatch struct{ c *barrier.Coordinator }

func (d drainBarrierDispatch) Dispatch(ctx context.Context) ([]barrier.Outcome, error) {
	sum, err := d.c.Dispatch(ctx)
	return sum.Outcomes, err
}

// diagnosis: a failure means the preStop drain checkpoints a barrier-acked
// session twice — the post-barrier per-session loop no longer skips the
// sessions the quiesce-and-hold barrier already captured — so a single
// drain opens two completed manifest rows (and duplicate catalog chunk
// rows) for one (session, coordination_generation), which neither the
// resume cleanup nor the §12.5 backstop reclaims. It also fails if the
// drain stamps a non-eviction trigger on either drain path.
// spec: §10.1 lines 163-172 (quiesce-and-hold, no-double-checkpoint),
// §4.4 line 259 (eviction trigger).
func TestDrainCheckpointsEachSessionOnce(t *testing.T) {
	const reachable = 15
	const unreachable = 5
	targets := make([]barrier.Target, 0, reachable+unreachable)
	sessions := make([]prestop.SessionInfo, 0, reachable+unreachable)
	unreach := map[string]bool{}
	acked := map[string]bool{}
	for i := 0; i < reachable+unreachable; i++ {
		sid := fmt.Sprintf("s%02d", i)
		targets = append(targets, barrier.Target{TenantID: "acme", SessionID: sid, CoordinationGeneration: 1})
		sessions = append(sessions, prestop.SessionInfo{TenantID: "acme", SessionID: sid})
		if i >= reachable {
			unreach[sid] = true
		} else {
			acked[sid] = true
		}
	}

	cp := newDrainCheckpointer(unreach)
	coord := barrier.New(
		drainLister{targets: targets},
		drainDispatcher{unreachable: unreach},
		sessioncheckpointmeta.NewMemoryStore(nil),
		nil,
		cp,
	)
	hook := &prestop.Hook{
		Sessions:          &staticEnumerator{sessions: sessions},
		Checkpoint:        prestop.CheckpointFnFor(cp),
		Barrier:           drainBarrierDispatch{c: coord},
		BarrierAckTimeout: 90 * time.Second,
		GracePeriod:       240 * time.Second,
	}

	rec := httptest.NewRecorder()
	hook.ServeHTTP(rec, httptest.NewRequest("POST", "/internal/prestop", nil))

	var summary prestop.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()

	if cp.sawNonEvict {
		t.Error("a drain checkpoint used a trigger other than eviction")
	}
	// No session is checkpointed successfully more than once — the
	// no-double-checkpoint invariant.
	for sid, n := range cp.successes {
		if n > 1 {
			t.Errorf("session %s checkpointed successfully %d times, want <= 1", sid, n)
		}
	}
	// Each reachable session is captured exactly once, by the barrier, and
	// never re-driven by the loop: its total drive count is 1.
	for sid := range acked {
		if cp.drives[sid] != 1 {
			t.Errorf("barrier-acked session %s driven %d times across the drain, want exactly 1 (barrier only, loop skips it)", sid, cp.drives[sid])
		}
		if cp.successes[sid] != 1 {
			t.Errorf("barrier-acked session %s succeeded %d times, want 1", sid, cp.successes[sid])
		}
	}
	// The loop covers only the barrier-unreachable sessions.
	if summary.BarrierCheckpointedSessions != reachable {
		t.Errorf("BarrierCheckpointedSessions = %d, want %d", summary.BarrierCheckpointedSessions, reachable)
	}
	if summary.AttemptedSessions != unreachable {
		t.Errorf("AttemptedSessions (post-barrier loop) = %d, want %d (only barrier-unreachable sessions)", summary.AttemptedSessions, unreachable)
	}
}

// staticEnumerator returns a fixed session snapshot.
type staticEnumerator struct {
	sessions []prestop.SessionInfo
}

func (e *staticEnumerator) Snapshot(context.Context) ([]prestop.SessionInfo, error) {
	return e.sessions, nil
}
