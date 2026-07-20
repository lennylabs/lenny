// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr/funcr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
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

// stuckCounterValue reads lenny_crd_ssa_conflict_total for one (crd,
// controller) label tuple off the controller-runtime registry the retry
// helper registers against. Reading through the registry rather than the
// helper's unexported collector keeps this test at the call site, where the
// ConflictID labels are decided.
func stuckCounterValue(t *testing.T, crd, controller string) float64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather controller-runtime registry: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "lenny_crd_ssa_conflict_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["crd"] == crd && labels["controller"] == controller {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// TestUpdateStatusEmitsStuckSignalWithOwningCRDIdentity pins the identity the
// SandboxWarmPool status call site hands the shared retry helper. An apply
// that conflicts on every attempt must drive the helper to exhaustion and
// produce the §4.6.3 stuck signal labeled with the owning CRD kind
// (SandboxWarmPool) and the field manager the apply actually uses
// (lenny-warm-pool-controller), plus a crd_ssa_conflict_stuck log carrying the
// pool's namespace and name.
//
// The non-happy path is the ownership dispute the controller cannot resolve.
// Before this call site was routed through the shared helper it retried
// silently and emitted neither the counter nor the log, so the alert had no
// series to fire on; a call site that passed the wrong CRD kind or field
// manager would mislabel the series and break the same alert.
//
// spec: §4.6.3 (SSA conflict retry policy), §16.1 (lenny_crd_ssa_conflict_total)
func TestUpdateStatusEmitsStuckSignalWithOwningCRDIdentity(t *testing.T) {
	const wantCRD = string(ownership.SandboxWarmPool)
	const wantController = string(ownership.WarmPoolController)

	s := reclaimScheme(t)
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: reclaimNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{MinWarm: 2, MaxWarm: 4},
		Status:     lennyv1.SandboxWarmPoolStatus{WarmCount: 0, ReadyCount: 0},
	}

	patches := 0
	c := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(pool).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
					patches++
					return ssaConflict()
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).Build()

	var mu sync.Mutex
	var records []string
	logger := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, args)
	}, funcr.Options{})
	ctx := logf.IntoContext(context.Background(), logger)

	before := stuckCounterValue(t, wantCRD, wantController)

	r := &Reconciler{Client: c, Scheme: s}
	err := r.updateStatus(ctx, pool, plan.Plan{WarmCount: 2, ReadyCount: 1})
	if !apierrors.IsConflict(err) {
		t.Fatalf("updateStatus = %v, want the 409 conflict returned so controller-runtime requeues", err)
	}
	if patches != 5 {
		t.Fatalf("status applies = %d, want 5 (the bounded retry exhausts before requeue)", patches)
	}

	if got := stuckCounterValue(t, wantCRD, wantController) - before; got != 1 {
		t.Fatalf("lenny_crd_ssa_conflict_total{crd=%q,controller=%q} delta = %v, want 1", wantCRD, wantController, got)
	}

	mu.Lock()
	captured := append([]string(nil), records...)
	mu.Unlock()
	var stuck []string
	for _, rec := range captured {
		if strings.Contains(rec, "crd_ssa_conflict_stuck") {
			stuck = append(stuck, rec)
		}
	}
	if len(stuck) != 1 {
		t.Fatalf("crd_ssa_conflict_stuck log events = %d, want exactly 1: %v", len(stuck), stuck)
	}
	// Assert the rendered key/value pair rather than the bare value. The
	// alert and the runbook key off these exact field names, so a helper
	// that logged the CRD under some other key, or dropped the namespace
	// while its value happened to appear elsewhere in the record, must fail
	// here.
	for field, want := range map[string]string{
		"controller": wantController,
		"resource":   wantCRD,
		"name":       pool.Name,
		"namespace":  pool.Namespace,
	} {
		pair := fmt.Sprintf("%q=%q", field, want)
		if !strings.Contains(stuck[0], pair) {
			t.Fatalf("crd_ssa_conflict_stuck log %q missing %s", stuck[0], pair)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
