// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

func metricsRegistry(t *testing.T, seed ...client.Object) (*CRDPodRegistry, *Metrics) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(seed...).
		WithStatusSubresource(&lennyv1.Sandbox{}).
		Build()
	r, err := New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	r.SetMetrics(m)
	return r, m
}

func sb(name, pool, phase string) *lennyv1.Sandbox {
	s := &lennyv1.Sandbox{}
	s.Namespace = "lenny-agents"
	s.Name = name
	s.Labels = map[string]string{PoolLabel: pool}
	s.Spec.PoolRef = pool
	s.Status.Phase = phase
	return s
}

// spec: §12.6 line 478 — every PodRegistry method records its duration
// under lenny_pod_registry_operation_duration_seconds{operation, pool}.
func TestRegistryMetrics_OperationDuration_spec_12_6_478(t *testing.T) {
	r, m := metricsRegistry(t, sb("alpha", "echo-pool", "idle"))
	ctx := context.Background()

	if _, err := r.GetPod(ctx, "alpha"); err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if _, err := r.ListPodsByPool(ctx, "echo-pool", PodFilter{}); err != nil {
		t.Fatalf("ListPodsByPool: %v", err)
	}
	if _, err := r.CountByState(ctx, "echo-pool"); err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if _, err := r.CreatePod(ctx, "echo-pool", PodSpec{ExecutionMode: "session"}); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	// Each distinct (operation, pool) combination is one histogram series:
	// get / list / count / create against echo-pool = 4.
	if got := testutil.CollectAndCount(m.duration); got != 4 {
		t.Errorf("duration histogram series = %d, want 4 (get, list, count, create)", got)
	}
}

// spec: §12.6 line 478 — a failed operation increments
// lenny_pod_registry_error_total{operation, pool}; a successful one does
// not.
func TestRegistryMetrics_ErrorCounter_spec_12_6_478(t *testing.T) {
	r, m := metricsRegistry(t, sb("alpha", "echo-pool", "idle"))
	ctx := context.Background()

	// A missing pod is a get error. The pool is unknown on a miss, so it
	// is recorded under the empty pool label.
	if _, err := r.GetPod(ctx, "does-not-exist"); err == nil {
		t.Fatalf("GetPod on a missing pod returned no error")
	}
	if got := testutil.ToFloat64(m.errors.WithLabelValues(opGet, "")); got != 1 {
		t.Errorf("get error_total = %v, want 1", got)
	}

	// A claim against an empty pool exhausts; ErrPoolExhausted is the
	// recorded error for the claim operation.
	if _, err := r.ClaimPod(ctx, ClaimOpts{PoolID: "empty-pool", SessionID: "s1"}); err == nil {
		t.Fatalf("ClaimPod against an empty pool returned no error")
	}
	if got := testutil.ToFloat64(m.errors.WithLabelValues(opClaim, "empty-pool")); got != 1 {
		t.Errorf("claim error_total = %v, want 1", got)
	}

	// A successful get records no error.
	if _, err := r.GetPod(ctx, "alpha"); err != nil {
		t.Fatalf("GetPod(alpha): %v", err)
	}
	if got := testutil.ToFloat64(m.errors.WithLabelValues(opGet, "echo-pool")); got != 0 {
		t.Errorf("successful get must not increment error_total, got %v", got)
	}
}

// A registry with no metrics attached records nothing and does not panic.
func TestRegistryMetrics_NilSafe(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(sb("alpha", "echo-pool", "idle")).
		WithStatusSubresource(&lennyv1.Sandbox{}).Build()
	r, err := New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.GetPod(context.Background(), "alpha"); err != nil {
		t.Fatalf("GetPod without metrics: %v", err)
	}
}
