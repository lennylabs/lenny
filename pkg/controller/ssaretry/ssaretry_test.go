// SPDX-License-Identifier: MIT

package ssaretry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ssaConflict builds an HTTP 409 conflict of the kind the API server returns
// when a controller applies a field owned by another SSA field manager.
func ssaConflict() error {
	return apierrors.NewConflict(
		schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"},
		"claude-worker-small",
		errors.New("Apply failed with 1 conflict: conflict with \"lenny-pool-scaling-controller\""),
	)
}

// counterValue reads the current value of the lenny_crd_ssa_conflict_total
// counter for one (crd, controller) label tuple. The counter is a
// package-level CounterVec registered against the controller-runtime
// registry, so tests read it through the delta of a distinct label tuple
// to stay deterministic regardless of ordering.
func counterValue(t *testing.T, crd, controller string) float64 {
	t.Helper()
	return testutil.ToFloat64(crdSSAConflictTotal.WithLabelValues(crd, controller))
}

// captureLogs returns a context carrying a logr sink that appends each log
// record's formatted key-values, and a function that returns the records
// captured so far. It stands in for the controller's structured logger so a
// test can assert the crd_ssa_conflict_stuck emission.
func captureLogs(t *testing.T) (context.Context, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var records []string
	logger := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, args)
	}, funcr.Options{})
	ctx := logf.IntoContext(context.Background(), logger)
	return ctx, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(records))
		copy(out, records)
		return out
	}
}

// stuckRecords returns the captured log records that carry the
// crd_ssa_conflict_stuck message.
func stuckRecords(records []string) []string {
	var out []string
	for _, r := range records {
		if strings.Contains(r, "crd_ssa_conflict_stuck") {
			out = append(out, r)
		}
	}
	return out
}

// TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts pins the §4.6.3
// stuck-conflict signal: an apply that returns a 409 on every attempt drives
// exactly five attempts (the give-up-at-five loop), the helper returns the
// wrapped conflict lastErr rather than nil, lenny_crd_ssa_conflict_total for
// the (crd, controller) tuple increments by exactly one, and one
// crd_ssa_conflict_stuck log event carrying the controller, resource, and name
// is emitted. The non-happy path is the disputed apply that never converges.
//
// spec: §4.6.3 / §16.1 (stuck log + counter after 5 consecutive 409s)
func TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts(t *testing.T) {
	const crd, controller, name, namespace = "SandboxWarmPool", "warm-pool-controller", "claude-worker-small", "lenny-agents"
	ctx, records := captureLogs(t)
	id := ConflictID{Controller: controller, CRD: crd, Namespace: namespace, Name: name}

	before := counterValue(t, crd, controller)
	conflicts := 0
	err := RetryOnConflictSSA(ctx, id, func(int) error {
		conflicts++
		return ssaConflict()
	})

	if !apierrors.IsConflict(err) {
		t.Fatalf("RetryOnConflictSSA = %v, want the wrapped 409 conflict lastErr", err)
	}
	if conflicts != maxAttempts {
		t.Fatalf("apply invoked %d times, want %d (give-up-at-five)", conflicts, maxAttempts)
	}
	if got := counterValue(t, crd, controller) - before; got != 1 {
		t.Fatalf("lenny_crd_ssa_conflict_total delta = %v, want 1 (one increment per stuck episode)", got)
	}
	stuck := stuckRecords(records())
	if len(stuck) != 1 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want exactly 1: %v", len(stuck), stuck)
	}
	rec := stuck[0]
	for field, want := range map[string]string{"controller": controller, "resource": crd, "name": name} {
		if !strings.Contains(rec, want) {
			t.Fatalf("crd_ssa_conflict_stuck log %q missing %s=%q", rec, field, want)
		}
	}
}

// TestRetryOnConflictSSAFourConflictsThenSucceedEmitsNothing covers the
// near-boundary case: an apply that returns a 409 four times then succeeds on
// the fifth attempt resolves before exhaustion, so the helper returns nil, the
// counter does not increment, and no crd_ssa_conflict_stuck log is emitted. The
// non-happy path is the run that recovers just short of the stuck boundary and
// must stay quiet.
//
// spec: §4.6.3 (increment only after 5 consecutive no-progress 409s)
func TestRetryOnConflictSSAFourConflictsThenSucceedEmitsNothing(t *testing.T) {
	const crd, controller = "SandboxWarmPool", "four-then-succeed-controller"
	ctx, records := captureLogs(t)
	id := ConflictID{Controller: controller, CRD: crd, Namespace: "lenny-agents", Name: "claude-worker-small"}

	before := counterValue(t, crd, controller)
	attempts := 0
	err := RetryOnConflictSSA(ctx, id, func(attempt int) error {
		attempts++
		if attempt < 4 {
			return ssaConflict()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryOnConflictSSA = %v, want nil (resolved on the fifth attempt)", err)
	}
	if attempts != 5 {
		t.Fatalf("apply invoked %d times, want 5 (four conflicts then success)", attempts)
	}
	if got := counterValue(t, crd, controller) - before; got != 0 {
		t.Fatalf("lenny_crd_ssa_conflict_total delta = %v, want 0 (no stuck episode)", got)
	}
	if stuck := stuckRecords(records()); len(stuck) != 0 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want 0: %v", len(stuck), stuck)
	}
}

// TestRetryOnConflictSSAEmptyCRDIsSilent covers the §16.1 counter scope: an
// apply that returns a 409 on every attempt under a ConflictID with an empty
// CRD kind (the host-schedulable Pod-label apply under the sole owning field
// manager) drives five attempts and returns the wrapped conflict lastErr, but
// emits no crd_ssa_conflict_stuck log and no counter increment, because a Pod
// label is not a CRD field owned by another field manager. The non-happy path
// is the non-CRD apply that exhausts the loop and must stay silent.
//
// spec: §16.1 (counter fires only when writing to a CRD field owned by another field manager)
func TestRetryOnConflictSSAEmptyCRDIsSilent(t *testing.T) {
	ctx, records := captureLogs(t)
	// An empty CRD kind carries no counter series; assert on the total series
	// count of the vector so an accidental empty-label increment is caught.
	before := testutil.CollectAndCount(crdSSAConflictTotal)
	id := ConflictID{Controller: "warm-pool-controller", CRD: "", Namespace: "lenny-agents", Name: "claude-worker-small-abc"}

	conflicts := 0
	err := RetryOnConflictSSA(ctx, id, func(int) error {
		conflicts++
		return ssaConflict()
	})

	if !apierrors.IsConflict(err) {
		t.Fatalf("RetryOnConflictSSA = %v, want the wrapped 409 conflict lastErr", err)
	}
	if conflicts != maxAttempts {
		t.Fatalf("apply invoked %d times, want %d", conflicts, maxAttempts)
	}
	if after := testutil.CollectAndCount(crdSSAConflictTotal); after != before {
		t.Fatalf("lenny_crd_ssa_conflict_total series count changed from %d to %d; an empty-CRD apply must not increment", before, after)
	}
	if stuck := stuckRecords(records()); len(stuck) != 0 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want 0 for a non-CRD apply: %v", len(stuck), stuck)
	}
}

// TestRetryOnConflictSSAReturnsOnFirstSuccess covers the base case: an apply
// that succeeds on the first attempt is invoked exactly once and returns nil,
// so the retry policy adds no overhead on the common path.
//
// spec: §4.6.3 (SSA conflict retry policy: retry only on 409)
func TestRetryOnConflictSSAReturnsOnFirstSuccess(t *testing.T) {
	id := ConflictID{Controller: "warm-pool-controller", CRD: "SandboxWarmPool", Name: "claude-worker-small"}
	calls := 0
	err := RetryOnConflictSSA(context.Background(), id, func(int) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("RetryOnConflictSSA = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("apply invoked %d times, want 1", calls)
	}
}

// TestRetryOnConflictSSADoesNotRetryNonConflict covers the discrimination the
// policy requires: a non-409 error is returned immediately without a re-read or
// a retry, because only a conflict indicates a stale cached resourceVersion
// worth re-reading. Retrying an arbitrary error would mask genuine failures.
//
// spec: §4.6.3 item 1 (re-read is keyed on 409 conflicts)
func TestRetryOnConflictSSADoesNotRetryNonConflict(t *testing.T) {
	const crd, controller = "SandboxWarmPool", "non-conflict-controller"
	ctx, records := captureLogs(t)
	id := ConflictID{Controller: controller, CRD: crd, Name: "claude-worker-small"}
	before := counterValue(t, crd, controller)
	boom := errors.New("apiserver unavailable")
	calls := 0
	err := RetryOnConflictSSA(ctx, id, func(int) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("RetryOnConflictSSA = %v, want it to wrap %v", err, boom)
	}
	if calls != 1 {
		t.Fatalf("apply invoked %d times on a non-conflict error, want 1 (no retry)", calls)
	}
	if got := counterValue(t, crd, controller) - before; got != 0 {
		t.Fatalf("lenny_crd_ssa_conflict_total delta = %v, want 0 (non-conflict is not a stuck episode)", got)
	}
	if stuck := stuckRecords(records()); len(stuck) != 0 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want 0 on a non-conflict error", len(stuck))
	}
}

// TestRetryOnConflictSSARetriesAndBacksOffOnConflict covers the bounded
// jittered backoff: on a run of 409 conflicts the apply is retried with the
// attempt counter advancing (the signal the call-site closures use to discard
// their cached copy and re-read), and a real backoff delay elapses between
// attempts. The first backoff floor is 100ms, so three attempts (two backoffs)
// take at least 100ms of wall-clock time; a policy that spun without sleeping
// would return near-instantly.
//
// spec: §4.6.3 item 3 (jittered backoff, initial 100ms)
func TestRetryOnConflictSSARetriesAndBacksOffOnConflict(t *testing.T) {
	id := ConflictID{Controller: "warm-pool-controller", CRD: "SandboxWarmPool", Name: "claude-worker-small"}
	var attempts []int
	start := time.Now()
	// Succeed on the third attempt so the loop exercises two conflict-driven
	// backoffs and then returns success.
	err := RetryOnConflictSSA(context.Background(), id, func(attempt int) error {
		attempts = append(attempts, attempt)
		if attempt < 2 {
			return ssaConflict()
		}
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RetryOnConflictSSA = %v, want nil after retries", err)
	}
	if want := []int{0, 1, 2}; !equalInts(attempts, want) {
		t.Fatalf("attempt sequence = %v, want %v (monotonic re-read counter)", attempts, want)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 100ms (backoff must actually sleep between attempts)", elapsed)
	}
}

// TestRetryOnConflictSSAHonorsContextCancellation covers cancellation during
// the backoff: a cancelled context aborts the retry loop with the context
// error rather than blocking, so a shutting-down controller does not hang on a
// disputed apply, and no stuck signal is emitted because the loop did not
// exhaust its attempts.
//
// spec: §4.6.3 (bounded retry with backoff); code-best-practices.md (honor context cancellation)
func TestRetryOnConflictSSAHonorsContextCancellation(t *testing.T) {
	const crd, controller = "SandboxWarmPool", "cancellation-controller"
	base, records := captureLogs(t)
	ctx, cancel := context.WithCancel(base)
	id := ConflictID{Controller: controller, CRD: crd, Name: "claude-worker-small"}
	before := counterValue(t, crd, controller)
	calls := 0
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := RetryOnConflictSSA(ctx, id, func(int) error {
		calls++
		return ssaConflict()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RetryOnConflictSSA = %v, want context.Canceled", err)
	}
	if got := counterValue(t, crd, controller) - before; got != 0 {
		t.Fatalf("lenny_crd_ssa_conflict_total delta = %v, want 0 (cancellation is not a stuck episode)", got)
	}
	if stuck := stuckRecords(records()); len(stuck) != 0 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want 0 on a cancelled context", len(stuck))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
