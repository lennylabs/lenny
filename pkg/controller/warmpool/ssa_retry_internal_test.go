// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
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

// TestRetryOnConflictSSAReturnsOnFirstSuccess covers the base case: an apply
// that succeeds on the first attempt is invoked exactly once and returns nil,
// so the retry policy adds no overhead on the common path.
//
// spec: §4.6 line 605-607 (SSA conflict retry policy: retry only on 409).
func TestRetryOnConflictSSAReturnsOnFirstSuccess(t *testing.T) {
	calls := 0
	err := retryOnConflictSSA(context.Background(), func(int) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("retryOnConflictSSA = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("apply invoked %d times, want 1", calls)
	}
}

// TestRetryOnConflictSSADoesNotRetryNonConflict covers the discrimination the
// policy requires: a non-409 error is returned immediately without a re-read
// or a retry, because only a conflict indicates a stale cached resourceVersion
// worth re-reading. Retrying an arbitrary error would mask genuine failures.
//
// spec: §4.6 line 605 ("On any SSA HTTP 409 conflict error, the controller
// must discard its cached copy ... and issue a fresh GET"), line 607 (backoff
// and re-read are keyed on 409 conflicts).
func TestRetryOnConflictSSADoesNotRetryNonConflict(t *testing.T) {
	boom := errors.New("apiserver unavailable")
	calls := 0
	err := retryOnConflictSSA(context.Background(), func(int) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("retryOnConflictSSA = %v, want it to wrap %v", err, boom)
	}
	if calls != 1 {
		t.Fatalf("apply invoked %d times on a non-conflict error, want 1 (no retry)", calls)
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
// spec: §4.6 line 607 ("the controller backs off with jitter (initial 100ms,
// max 2s) and re-reads again").
func TestRetryOnConflictSSARetriesAndBacksOffOnConflict(t *testing.T) {
	var attempts []int
	start := time.Now()
	// Succeed on the third attempt so the loop exercises two conflict-driven
	// backoffs and then returns success.
	err := retryOnConflictSSA(context.Background(), func(attempt int) error {
		attempts = append(attempts, attempt)
		if attempt < 2 {
			return ssaConflict()
		}
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("retryOnConflictSSA = %v, want nil after retries", err)
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
// disputed apply.
//
// spec: §4.6 line 607 (bounded retry with backoff); code-best-practices.md
// (honor context cancellation on any blocking call).
func TestRetryOnConflictSSAHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := retryOnConflictSSA(ctx, func(int) error {
		calls++
		return ssaConflict()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryOnConflictSSA = %v, want context.Canceled", err)
	}
}

// TestUpdateStatusReReadsAndNeverForcesOnSSAConflict covers the two ownership
// invariants of the retry policy at a real call site (updateStatus applies the
// WarmPoolController-owned SandboxWarmPool.status fields via SSA): after a 409
// the controller issues a fresh GET before re-applying, and no apply carries
// Force ownership. Forcing would steal the field from the other manager and
// defeat the isolation the SSA boundary enforces.
//
// spec: §4.6 line 605 ("must discard its cached copy of the resource and issue
// a fresh GET"), line 606 ("Controllers must not use --force-conflicts (SSA
// Force: true)").
func TestUpdateStatusReReadsAndNeverForcesOnSSAConflict(t *testing.T) {
	s := reclaimScheme(t)
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: reclaimNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{MinWarm: 2, MaxWarm: 4},
		Status:     lennyv1.SandboxWarmPoolStatus{WarmCount: 0, ReadyCount: 0},
	}

	var events []string
	firstPatch := true
	forced := false
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(pool).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
					events = append(events, "get")
				}
				return cl.Get(ctx, key, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
					events = append(events, "patch")
					po := &client.SubResourcePatchOptions{}
					for _, o := range opts {
						o.ApplyToSubResourcePatch(po)
					}
					if po.Force != nil && *po.Force {
						forced = true
					}
					if firstPatch {
						firstPatch = false
						return ssaConflict()
					}
					// The fake client cannot execute SSA apply patches, so the
					// interceptor stands in for the apiserver and reports the
					// retry apply as accepted.
					return nil
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).Build()

	r := &Reconciler{Client: c, Scheme: s}
	decision := plan.Plan{WarmCount: 2, ReadyCount: 1}
	if err := r.updateStatus(context.Background(), pool, decision); err != nil {
		t.Fatalf("updateStatus = %v, want nil after one conflict then success", err)
	}

	if forced {
		t.Fatal("an SSA apply carried Force ownership; the policy forbids --force-conflicts in reconciliation")
	}
	// Expect: patch (409) -> get (re-read) -> patch (success).
	if len(events) < 3 {
		t.Fatalf("event sequence = %v, want at least one retry (patch, get, patch)", events)
	}
	if events[0] != "patch" {
		t.Fatalf("first event = %q, want %q", events[0], "patch")
	}
	sawReReadBeforeRetry := false
	for i := 1; i < len(events); i++ {
		if events[i] == "patch" && contains(events[:i], "get") {
			sawReReadBeforeRetry = true
			break
		}
	}
	if !sawReReadBeforeRetry {
		t.Fatalf("event sequence = %v, want a GET (re-read) before the retry patch", events)
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

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts pins the §4.6
// stuck-conflict signal: after five consecutive 409 conflicts on a single
// resource, the controller must emit a crd_ssa_conflict_stuck structured log
// event and increment the lenny_crd_ssa_conflict_total counter, then continue
// with exponential backoff (spec §4.6 line 607).
//
// The current retryOnConflictSSA helper implements neither the counter nor the
// structured log, and it gives up after five attempts rather than continuing.
// The counter lenny_crd_ssa_conflict_total is also not registered anywhere in
// the controller metrics. Closing this behavior additionally requires
// resolving a spec contradiction: §4.6 (this section) increments the counter
// only after five consecutive conflicts, while §16 observability describes the
// same counter as incrementing "on every Server-Side Apply conflict observed."
// This test is kept as the spec-faithful assertion for that behavior and is
// skipped until the increment semantics are settled and the counter is wired.
//
// spec: §4.6 line 607 (crd_ssa_conflict_stuck log + lenny_crd_ssa_conflict_total
// after 5 consecutive 409s).
func TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts(t *testing.T) {
	t.Skip("crd_ssa_conflict_stuck log and lenny_crd_ssa_conflict_total counter are unimplemented, and §4.6 vs §16 disagree on the increment semantics; open TEST-GAPS finding")

	conflicts := 0
	_ = retryOnConflictSSA(context.Background(), func(int) error {
		conflicts++
		return ssaConflict()
	})
	// Intended assertions once the behavior lands:
	//   - conflicts >= 5 (the loop continues past five, per §4.6 line 607),
	//   - lenny_crd_ssa_conflict_total increments for this resource,
	//   - a crd_ssa_conflict_stuck log event is emitted labeled by
	//     controller, resource, and name.
	_ = conflicts
}
