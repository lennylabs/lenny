// SPDX-License-Identifier: MIT

package prestop

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
)

// recordingBarrier records the Dispatch call and whether the context it
// received carried a deadline within the ACK budget. outcomes is the
// per-session Outcome set the hook consumes to skip barrier-acked
// sessions in its post-barrier loop.
type recordingBarrier struct {
	mu          sync.Mutex
	called      bool
	hadDeadline bool
	deadline    time.Time
	err         error
	outcomes    []barrier.Outcome
}

func (b *recordingBarrier) Dispatch(ctx context.Context) ([]barrier.Outcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.called = true
	if d, ok := ctx.Deadline(); ok {
		b.hadDeadline = true
		b.deadline = d
	}
	return b.outcomes, b.err
}

// spec: §10.1 line 165 / line 167 — the preStop hook fires the
// CheckpointBarrier at Stage 1 under a deadline bounded by
// checkpointBarrierAckTimeoutSeconds.
func TestHookFiresBarrierBoundedByAckTimeout_spec_10_1_167(t *testing.T) {
	bar := &recordingBarrier{}
	hook := &Hook{
		Sessions:          &fakeEnumerator{},
		Checkpoint:        func(context.Context, string, string, time.Duration) error { return nil },
		Barrier:           bar,
		BarrierAckTimeout: 90 * time.Second,
		GracePeriod:       240 * time.Second,
	}
	start := time.Now()
	hook.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/internal/prestop", nil))

	if !bar.called {
		t.Fatal("barrier was not dispatched at Stage 1")
	}
	if !bar.hadDeadline {
		t.Fatal("barrier Dispatch context carried no deadline; the ACK budget is unbounded")
	}
	// The deadline must be ~90s out (the ACK budget), well under the 240s
	// grace period.
	budget := bar.deadline.Sub(start)
	if budget < 80*time.Second || budget > 100*time.Second {
		t.Fatalf("barrier deadline budget = %s, want ~90s", budget)
	}
}

// spec: §11.3 line 210 — a zero BarrierAckTimeout selects the 90s default.
func TestHookBarrierDefaultAckTimeout_spec_11_3_210(t *testing.T) {
	bar := &recordingBarrier{}
	hook := &Hook{
		Sessions:    &fakeEnumerator{},
		Checkpoint:  func(context.Context, string, string, time.Duration) error { return nil },
		Barrier:     bar,
		GracePeriod: 240 * time.Second,
	}
	start := time.Now()
	hook.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/internal/prestop", nil))
	if !bar.hadDeadline {
		t.Fatal("no deadline on default ACK budget")
	}
	budget := bar.deadline.Sub(start)
	if budget < 80*time.Second || budget > 100*time.Second {
		t.Fatalf("default barrier budget = %s, want ~90s (DefaultBarrierAckTimeoutSeconds)", budget)
	}
}

// spec: §10.1 line 165 — a barrier dispatch error is best-effort: it does
// not abort the eviction-checkpoint drain.
func TestHookBarrierErrorDoesNotAbortDrain_spec_10_1_165(t *testing.T) {
	bar := &recordingBarrier{err: errors.New("postgres down during drain")}
	var checkpointed int
	hook := &Hook{
		Sessions: &fakeEnumerator{sessions: []SessionInfo{{TenantID: "acme", SessionID: "s1"}}},
		Checkpoint: func(context.Context, string, string, time.Duration) error {
			checkpointed++
			return nil
		},
		Barrier:     bar,
		GracePeriod: 240 * time.Second,
	}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))
	if !bar.called {
		t.Fatal("barrier not dispatched")
	}
	if checkpointed != 1 {
		t.Fatalf("eviction checkpoint ran %d times after barrier error, want 1", checkpointed)
	}
}

// spec: §10.1 line 169 — the post-barrier per-session loop skips every
// session the barrier already checkpointed under quiesce-and-hold, so no
// session is checkpointed twice on a drain. With three coordinated
// sessions where two ack the barrier and one does not, the loop
// checkpoints only the barrier-unreachable session; the two acked
// sessions are skipped and counted separately.
//
// This is the no-double-checkpoint regression: against the pre-fix code
// (BarrierDispatcher.Dispatch returned only an error, so fireBarrier had
// no acked set and the loop re-ran every coordinated session) all three
// sessions would be checkpointed, re-running the two the barrier already
// captured.
func TestHookSkipsBarrierAckedSessions_spec_10_1_169(t *testing.T) {
	bar := &recordingBarrier{outcomes: []barrier.Outcome{
		{Target: barrier.Target{SessionID: "s1"}, Acked: true},
		{Target: barrier.Target{SessionID: "s2"}, Acked: true},
		// s3 timed out — a §10.1 lines 169-172 partial-capture case that
		// the loop must still cover.
		{Target: barrier.Target{SessionID: "s3"}, Acked: false, Err: errors.New("deadline")},
	}}
	var mu sync.Mutex
	checkpointed := map[string]int{}
	hook := &Hook{
		Sessions: &fakeEnumerator{sessions: []SessionInfo{
			{TenantID: "acme", SessionID: "s1"},
			{TenantID: "acme", SessionID: "s2"},
			{TenantID: "acme", SessionID: "s3"},
		}},
		Checkpoint: func(_ context.Context, _, sessionID string, _ time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			checkpointed[sessionID]++
			return nil
		},
		Barrier:     bar,
		GracePeriod: 240 * time.Second,
	}
	summary := hook.run(context.Background())

	if checkpointed["s1"] != 0 || checkpointed["s2"] != 0 {
		t.Errorf("barrier-acked sessions re-checkpointed by the loop: s1=%d s2=%d, want 0",
			checkpointed["s1"], checkpointed["s2"])
	}
	if checkpointed["s3"] != 1 {
		t.Errorf("barrier-unreachable s3 checkpointed %d times by the loop, want 1", checkpointed["s3"])
	}
	if summary.BarrierCheckpointedSessions != 2 {
		t.Errorf("BarrierCheckpointedSessions = %d, want 2", summary.BarrierCheckpointedSessions)
	}
	if summary.AttemptedSessions != 1 {
		t.Errorf("AttemptedSessions = %d, want 1 (only the barrier-unreachable session)", summary.AttemptedSessions)
	}
	if summary.CompletedSessions != 1 {
		t.Errorf("CompletedSessions = %d, want 1", summary.CompletedSessions)
	}
}

// spec: §10.1 line 165 — a nil Barrier is skipped without affecting the
// drain (dev-mode / single-replica posture).
func TestHookNilBarrierIsNoop(t *testing.T) {
	hook := &Hook{
		Sessions:    &fakeEnumerator{},
		Checkpoint:  func(context.Context, string, string, time.Duration) error { return nil },
		GracePeriod: 240 * time.Second,
	}
	// Must not panic.
	hook.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/internal/prestop", nil))
}
