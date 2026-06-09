// SPDX-License-Identifier: MIT

package prestop

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// recordingBarrier records the Dispatch call and whether the context it
// received carried a deadline within the ACK budget.
type recordingBarrier struct {
	mu          sync.Mutex
	called      bool
	hadDeadline bool
	deadline    time.Time
	err         error
}

func (b *recordingBarrier) Dispatch(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.called = true
	if d, ok := ctx.Deadline(); ok {
		b.hadDeadline = true
		b.deadline = d
	}
	return b.err
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
