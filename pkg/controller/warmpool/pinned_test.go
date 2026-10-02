// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// tenantPod builds the agent Pod backing the same-named Sandbox in the test
// pool, carrying the §5.2 tenant pin when tenant is non-empty.
func tenantPod(name, tenant string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: testNS,
			Labels: map[string]string{warmpool.LabelPool: testPool},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}}},
	}
	if tenant != "" {
		p.Labels["lenny.dev/tenant-id"] = tenant
	}
	return p
}

// pinTestClaim builds claim-<podName> for tenant.
func pinTestClaim(podName, tenant string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + podName, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: podName, TenantID: tenant},
	}
}

// phases maps each pool Sandbox to its phase.
func phases(t *testing.T, c client.Client) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, sb := range poolSandboxes(t, c) {
		out[sb.Name] = sb.Status.Phase
	}
	return out
}

func countPhase(ph map[string]string, want string) int {
	n := 0
	for _, p := range ph {
		if p == want {
			n++
		}
	}
	return n
}

// pinnedReconciler wires a Reconciler whose cached client is c and whose
// uncached reader is apiReader.
func pinnedReconciler(c client.Client, apiReader client.Reader) *warmpool.Reconciler {
	r := &warmpool.Reconciler{Client: c, Scheme: c.Scheme()}
	if apiReader != nil {
		r.APIReader = apiReader
	}
	return r
}

// spec: 5.2 (Pinned idle inventory), 4.6.1 (Warm Pool Controller)
// diagnosis: the planner counts a pinned pod as inventory for every tenant,
// drains a pod whose acquisition is in flight, or writes a pinned pod into
// status.readyCount, so the pool stops provisioning for other tenants.
func TestReconcilePinnedIdleInventory_spec_5_2(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 3),
		idleSandbox("sb-acme"), idleSandbox("sb-globex"),
		tenantPod("sb-acme", "acme"), tenantPod("sb-globex", "globex"))
	reconcileWith(t, pinnedReconciler(c, c))

	ph := phases(t, c)
	if len(ph) != 4 {
		t.Fatalf("have %d sandboxes, want 4 (two pinned plus two created)", len(ph))
	}
	if n := countPhase(map[string]string{"a": ph["sb-acme"], "g": ph["sb-globex"]}, "draining"); n != 1 {
		t.Errorf("draining pinned pods = %d, want exactly 1 (maxWarm 3 minus minWarm 2 keeps one)", n)
	}
	p := getPool(t, c)
	if p.Status.ReadyCount != 0 || p.Status.WarmCount != 2 {
		t.Errorf("status ready/warm = %d/%d, want 0/2", p.Status.ReadyCount, p.Status.WarmCount)
	}
}

// spec: 5.2 (Pinned idle inventory), 4.6.1 (Warm Pool Controller)
// diagnosis: a pod whose acquisition is in flight is drained, a Sandbox
// without a Pod is counted as pinned, a pod pinned `unassigned` is counted as
// claimable inventory, or a claim the cache has not yet seen lets the pod be
// drained.
func TestReconcilePinnedIdleClaimAndLabelConditions_spec_5_2(t *testing.T) {
	t.Run("a claimed labelled pod is inventory", func(t *testing.T) {
		s := newScheme(t)
		c := newClient(t, s, template(), pool(1, 1),
			idleSandbox("sb-held"), tenantPod("sb-held", "acme"), pinTestClaim("sb-held", "acme"))
		reconcileWith(t, pinnedReconciler(c, c))
		if ph := phases(t, c); ph["sb-held"] != "idle" || len(ph) != 1 {
			t.Errorf("phases = %v, want sb-held idle and nothing created", ph)
		}
		if p := getPool(t, c); p.Status.ReadyCount != 1 {
			t.Errorf("readyCount = %d, want 1", p.Status.ReadyCount)
		}
	})
	t.Run("no Pod is unpinned and unassigned is pinned", func(t *testing.T) {
		s := newScheme(t)
		c := newClient(t, s, template(), pool(1, 1),
			idleSandbox("sb-nopod"), idleSandbox("sb-unassigned"), tenantPod("sb-unassigned", "unassigned"))
		reconcileWith(t, pinnedReconciler(c, c))
		ph := phases(t, c)
		if ph["sb-nopod"] != "idle" || ph["sb-unassigned"] != "draining" || len(ph) != 2 {
			t.Errorf("phases = %v, want sb-nopod idle and sb-unassigned draining", ph)
		}
		if p := getPool(t, c); p.Status.ReadyCount != 1 {
			t.Errorf("readyCount = %d, want 1", p.Status.ReadyCount)
		}
	})
	t.Run("a cached NotFound is confirmed through the API reader", func(t *testing.T) {
		s := newScheme(t)
		base := newClient(t, s, template(), pool(1, 1),
			idleSandbox("sb-lag"), tenantPod("sb-lag", "acme"), pinTestClaim("sb-lag", "acme"))
		stale := interceptor.NewClient(base, interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*lennyv1.SandboxClaim); ok {
					return apierrors.NewNotFound(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxclaims"}, key.Name)
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		})
		reconcileWith(t, pinnedReconciler(stale, base))
		if ph := phases(t, base); ph["sb-lag"] != "idle" {
			t.Errorf("sb-lag phase = %q, want idle (its claim exists in the API server)", ph["sb-lag"])
		}
		if p := getPool(t, base); p.Status.ReadyCount != 1 {
			t.Errorf("readyCount = %d, want 1", p.Status.ReadyCount)
		}

		// With no uncached reader the pod counts as unpinned.
		reconcileWith(t, pinnedReconciler(stale, nil))
		if ph := phases(t, base); ph["sb-lag"] != "idle" {
			t.Errorf("sb-lag phase = %q with a nil APIReader, want idle", ph["sb-lag"])
		}
	})
}

// spec: 5.2 (Pinned idle inventory), 4.6.1 (Warm Pool Controller)
// diagnosis: a failed Pod or claim read is read as no claim, so the
// controller drains a pod whose acquisition may be in flight or writes a
// status computed from a partial view.
func TestReconcilePinnedIdleReadFailuresFailTheReconcile_spec_5_2(t *testing.T) {
	boom := apierrors.NewInternalError(errors.New("read boom"))
	cases := []struct {
		name   string
		cached interceptor.Funcs
		reader func(client.WithWatch) client.Reader
	}{
		{
			name: "cached claim Get fails",
			cached: interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*lennyv1.SandboxClaim); ok {
					return boom
				}
				return cl.Get(ctx, key, obj, opts...)
			}},
		},
		{
			name: "Pod List fails",
			cached: interceptor.Funcs{List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); ok {
					return boom
				}
				return cl.List(ctx, list, opts...)
			}},
		},
		{
			name: "uncached claim Get fails",
			reader: func(base client.WithWatch) client.Reader {
				return interceptor.NewClient(base, interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*lennyv1.SandboxClaim); ok {
						return boom
					}
					return cl.Get(ctx, key, obj, opts...)
				}})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScheme(t)
			base := newClient(t, s, template(), pool(2, 3),
				idleSandbox("sb-acme"), idleSandbox("sb-globex"),
				tenantPod("sb-acme", "acme"), tenantPod("sb-globex", "globex"))
			var reader client.Reader = base
			if tc.reader != nil {
				reader = tc.reader(base)
			}
			r := pinnedReconciler(interceptor.NewClient(base, tc.cached), reader)
			if _, err := r.Reconcile(context.Background(), reconcileRequest()); err == nil || !errors.Is(err, boom) {
				t.Fatalf("Reconcile = %v, want the read error", err)
			}
			ph := phases(t, base)
			if len(ph) != 2 || countPhase(ph, "draining") != 0 {
				t.Errorf("phases = %v, want the two seeded pods untouched", ph)
			}
			if p := getPool(t, base); p.Status.ReadyCount != 0 || p.Status.WarmCount != 0 {
				t.Errorf("status ready/warm = %d/%d, want no status write", p.Status.ReadyCount, p.Status.WarmCount)
			}
		})
	}
}

// spec: 5.2 (Pinned idle inventory), 4.6.1 (Warm Pool Controller)
// diagnosis: a service-mode pool reads the tenant labels the stateless
// router stamps on serving pods and drains them.
func TestReconcileServicePoolReadsNoPin_spec_5_2(t *testing.T) {
	s := newScheme(t)
	tm := template()
	tm.Spec.ExecutionMode = "service"
	c := newClient(t, s, tm, pool(2, 3),
		idleSandbox("sb-a"), idleSandbox("sb-b"),
		tenantPod("sb-a", "acme"), tenantPod("sb-b", "globex"))
	reconcileWith(t, pinnedReconciler(c, c))
	ph := phases(t, c)
	if len(ph) != 2 || countPhase(ph, "idle") != 2 {
		t.Errorf("phases = %v, want both service pods idle and nothing created", ph)
	}
}

// metricValue reads a pool-labelled sample of the named metric from the
// controller-runtime registry.
func metricValue(t *testing.T, name, pool string) float64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if labelValue(m, "pool") != pool {
				continue
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// spec: 5.2 (Pinned idle inventory), 16.1 (lenny_warmpool_idle_pods)
// diagnosis: pinned idle pods are reported as claimable idle inventory, or
// their idle cost disappears from lenny_warmpool_idle_pod_minutes.
func TestReconcilePinnedIdleMetrics_spec_5_2(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(1, 3),
		idleSandbox("sb-p1"), idleSandbox("sb-p2"),
		tenantPod("sb-p1", "acme"), tenantPod("sb-p2", "globex"))
	r := pinnedReconciler(c, c)
	reconcileWith(t, r)
	before := metricValue(t, "lenny_warmpool_idle_pod_minutes", testPool)
	time.Sleep(50 * time.Millisecond)
	reconcileWith(t, r)
	if got := metricValue(t, "lenny_warmpool_idle_pods", testPool); got != 0 {
		t.Errorf("lenny_warmpool_idle_pods = %v, want 0 with only pinned idle pods", got)
	}
	if after := metricValue(t, "lenny_warmpool_idle_pod_minutes", testPool); after <= before {
		t.Errorf("lenny_warmpool_idle_pod_minutes did not increase (%v → %v)", before, after)
	}
}
