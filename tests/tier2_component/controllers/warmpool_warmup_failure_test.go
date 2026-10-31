// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// warmupFailures reads lenny_warmpool_warmup_failure_total for the test
// pool, the runc RuntimeClass the standard isolation profile resolves to,
// and errorType from the controller-runtime registry the WarmPoolController
// serves. The counter is process-wide and every test in this package uses
// the same pool name, so callers compare a delta.
func warmupFailures(t *testing.T, errorType string) float64 {
	t.Helper()
	mfs, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather controller metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "lenny_warmpool_warmup_failure_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["pool"] == taPool && labels["runtime_class"] == "runc" && labels["error_type"] == errorType {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// setPodWaitingOnImagePull writes a Pending pod status whose runtime
// container waits on an image pull failure, standing in for the kubelet.
func (e *terminatedAdapterEnv) setPodWaitingOnImagePull(name string) {
	e.t.Helper()
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, &p); err != nil {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	p.Status = corev1.PodStatus{
		Phase: corev1.PodPending,
		ContainerStatuses: []corev1.ContainerStatus{
			{Name: podspec.AdapterContainerName, Image: "adapter", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: podspec.RuntimeContainerName, Image: "runtime", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
		},
	}
	if err := e.c.Status().Update(e.ctx, &p); err != nil {
		e.t.Fatalf("update pod %s status: %v", name, err)
	}
}

// setPodEvicted writes the status a kubelet node-pressure eviction leaves.
func (e *terminatedAdapterEnv) setPodEvicted(name string) {
	e.t.Helper()
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, &p); err != nil {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	p.Status = corev1.PodStatus{
		Phase:   corev1.PodFailed,
		Reason:  "Evicted",
		Message: "The node was low on resource: memory.",
	}
	if err := e.c.Status().Update(e.ctx, &p); err != nil {
		e.t.Fatalf("update pod %s status: %v", name, err)
	}
}

// spec: 16.1 (Metrics), 6.2 (Pod State Machine)
// diagnosis: A warming pod whose runtime container waits on an image pull
// failure was not counted as error_type="image_pull_error" with the pool
// and runtime_class labels, or a level-triggered reconcile counted the same
// stuck pod more than once. Check the Sandbox reconciler's warm-up failure
// meter and classifyWarmupPod.
func TestWarmingPodImagePullFailureCountedOnce(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()
	before := warmupFailures(t, "image_pull_error")

	e.setPodWaitingOnImagePull(name)
	for i := 0; i < 3; i++ {
		e.reconcileSandbox(name)
	}
	if got := warmupFailures(t, "image_pull_error") - before; got != 1 {
		t.Fatalf("image_pull_error delta = %v after three reconciles of one stuck pod, want 1", got)
	}
	if got := e.phase(name); got != state.Warming && got != "" {
		t.Fatalf("pod stuck on an image pull: phase = %q, want warming", got)
	}
}

// spec: 16.1 (Metrics), 6.2 (Pod State Machine, warming → failed)
// diagnosis: A warming pod failed by a kubelet node-pressure eviction was
// not counted as error_type="node_pressure", or the Sandbox did not take
// the warming → failed edge. Check classifyWarmupPod and the observation
// order in Reconcile (the meter reads the phase before the reconcile
// moves it).
func TestWarmingPodEvictionCountedAsNodePressure(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()
	before := warmupFailures(t, "node_pressure")

	e.setPodEvicted(name)
	e.reconcileSandbox(name)
	if got := e.phase(name); got != state.Failed {
		t.Fatalf("evicted warming pod: phase = %q, want failed", got)
	}
	e.reconcileSandbox(name) // failed → draining: past warm-up, no count
	if got := warmupFailures(t, "node_pressure") - before; got != 1 {
		t.Fatalf("node_pressure delta = %v, want 1", got)
	}
}

// spec: 16.1 (Metrics), 16.5 (Alerting Rules and SLOs, PoolScaleoutBlockedByQuota), 17.2 (Namespace Layout, ResourceQuota)
// diagnosis: A pod creation the namespace ResourceQuota rejected was not
// counted as error_type="resource_quota_exceeded", so
// PoolScaleoutBlockedByQuota cannot fire, or each retried creation was
// counted again. Check classifyCreateError against the API server's quota
// rejection and the createPod error path.
func TestWarmPodCreationRejectedByQuotaCounted(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	// A pods=0 quota rejects every pod creation in the namespace. The
	// quota admission plugin reads status.hard, which the
	// kube-controller-manager quota controller would write; envtest runs
	// no controller manager, so the status is written here.
	q := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "agents", Namespace: e.ns},
		Spec:       corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("0")}},
	}
	mustCreate(t, e.ctx, e.c, q)
	q.Status = corev1.ResourceQuotaStatus{
		Hard: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("0")},
		Used: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("0")},
	}
	if err := e.c.Status().Update(e.ctx, q); err != nil {
		t.Fatalf("write quota status: %v", err)
	}

	e.reconcilePool()
	sbs := e.sandboxes()
	if len(sbs) != 1 {
		t.Fatalf("pool fill created %d sandboxes, want 1", len(sbs))
	}
	name := sbs[0].Name
	before := warmupFailures(t, "resource_quota_exceeded")
	for i := 0; i < 3; i++ {
		_, err := e.sb.Reconcile(e.ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: e.ns, Name: name}})
		if err == nil {
			t.Fatalf("reconcile %d: pod creation under a pods=0 quota succeeded", i)
		}
	}
	if e.podExists(name) {
		t.Fatalf("a pod was created under a pods=0 quota")
	}
	if got := warmupFailures(t, "resource_quota_exceeded") - before; got != 1 {
		t.Fatalf("resource_quota_exceeded delta = %v after three rejected creations of one Sandbox, want 1", got)
	}
}
