// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func managedPod(name, nodeName, stateLabel string, annotations map[string]string) *corev1.Pod {
	labels := map[string]string{
		warmpool.LabelManaged: "true",
		warmpool.LabelPool:    testPool,
	}
	if stateLabel != "" {
		labels[state.LabelState] = stateLabel
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   testNS,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "agent", Image: "example/agent:latest"}},
		},
	}
}

func clusterNode(name string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
	}
}

func reconcilePod(t *testing.T, r *warmpool.PodReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: name},
	})
	if err != nil {
		t.Fatalf("PodReconciler.Reconcile(%s): %v", name, err)
	}
	return res
}

func getCorePod(t *testing.T, c client.Client, name string) corev1.Pod {
	t.Helper()
	var p corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &p); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	return p
}

func getSandbox(t *testing.T, c client.Client, name string) lennyv1.Sandbox {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &sb); err != nil {
		t.Fatalf("get sandbox %s: %v", name, err)
	}
	return sb
}

// TestPodReconcileHostSchedulableLabel_spec_4_6 verifies the §4.6.1
// host-node schedulability label tracks the node's cordon state: it is
// "true" on a schedulable node and flips to "false" when the node is
// cordoned, within a single reconcile.
func TestPodReconcileHostSchedulableLabel_spec_4_6(t *testing.T) {
	s := newScheme(t)
	nd := clusterNode("node-a", false)
	pod := managedPod("pod-a", "node-a", string(state.Idle), nil)
	c := newClient(t, s, nd, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-a")

	if got := getCorePod(t, c, "pod-a").Labels[warmpool.LabelHostSchedulable]; got != "true" {
		t.Fatalf("host-schedulable label=%q after reconcile on a schedulable node, want %q", got, "true")
	}

	// Cordon the node and re-reconcile: the label must flip to "false".
	var live corev1.Node
	if err := c.Get(testContext(), client.ObjectKey{Name: "node-a"}, &live); err != nil {
		t.Fatalf("get node: %v", err)
	}
	live.Spec.Unschedulable = true
	if err := c.Update(testContext(), &live); err != nil {
		t.Fatalf("cordon node: %v", err)
	}
	reconcilePod(t, r, "pod-a")
	if got := getCorePod(t, c, "pod-a").Labels[warmpool.LabelHostSchedulable]; got != "false" {
		t.Fatalf("host-schedulable label=%q after cordon, want %q", got, "false")
	}
}

// TestPodReconcileUnscheduledPodNoLabel_spec_4_6 verifies that a pod with
// no spec.nodeName (still Pending at the scheduler) is not labeled, per
// §4.6.1 ("pods whose spec.nodeName is not yet set ... are not eligible").
func TestPodReconcileUnscheduledPodNoLabel_spec_4_6(t *testing.T) {
	s := newScheme(t)
	pod := managedPod("pod-pending", "", string(state.Warming), nil)
	c := newClient(t, s, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-pending")

	if _, ok := getCorePod(t, c, "pod-pending").Labels[warmpool.LabelHostSchedulable]; ok {
		t.Fatal("host-schedulable label set on an unscheduled pod")
	}
}

// TestPodReconcileForeignPodIgnored verifies the reconciler ignores a pod
// that is not controller-managed even if it is somehow enqueued.
func TestPodReconcileForeignPodIgnored(t *testing.T) {
	s := newScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: testNS},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "x", Image: "example/x:latest"}},
		},
	}
	c := newClient(t, s, clusterNode("node-a", false), pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "foreign")

	if _, ok := getCorePod(t, c, "foreign").Labels[warmpool.LabelHostSchedulable]; ok {
		t.Fatal("host-schedulable label set on a non-managed pod")
	}
}

// TestPodReconcileCertExpiryDrainsIdlePod_spec_4_6 verifies that an idle
// pod whose certificate is inside the §4.6.1 replacement window has its
// backing Sandbox transitioned to draining (the pool reconciler then
// recreates a fresh pod).
func TestPodReconcileCertExpiryDrainsIdlePod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := idleSandbox("pod-expiring")
	// Cert expires in 10 minutes — inside the 30-minute window.
	pod := managedPod("pod-expiring", "", string(state.Idle), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(10 * time.Minute).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	res := reconcilePod(t, r, "pod-expiring")

	if got := getSandbox(t, c, "pod-expiring").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q after cert-expiry reconcile, want %q", got, state.Draining)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter=%v after draining, want 0", res.RequeueAfter)
	}
}

// TestPodReconcileCertExpiryKeepsFreshPod_spec_4_6 verifies that an idle
// pod whose certificate is comfortably outside the window is left idle
// and re-queued for a later re-check.
func TestPodReconcileCertExpiryKeepsFreshPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := idleSandbox("pod-fresh")
	pod := managedPod("pod-fresh", "", string(state.Idle), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(2 * time.Hour).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	res := reconcilePod(t, r, "pod-fresh")

	if got := getSandbox(t, c, "pod-fresh").Status.Phase; got != string(state.Idle) {
		t.Fatalf("sandbox phase=%q for a fresh-cert idle pod, want %q", got, state.Idle)
	}
	// 2h expiry minus the 30m threshold leaves ~90m before the next check.
	if res.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter=%v for a fresh-cert idle pod, want > 0", res.RequeueAfter)
	}
}

// TestPodReconcileCertExpirySkipsClaimedPod_spec_4_6 verifies that a
// claimed (non-idle) pod is never drained for cert expiry — an active
// session must not be disrupted, and idle is the only legal source state
// for the idle→draining edge.
func TestPodReconcileCertExpirySkipsClaimedPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-claimed",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: string(state.Claimed)},
	}
	pod := managedPod("pod-claimed", "", string(state.Claimed), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(5 * time.Minute).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	reconcilePod(t, r, "pod-claimed")

	if got := getSandbox(t, c, "pod-claimed").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("claimed pod drained for cert expiry: phase=%q, want %q", got, state.Claimed)
	}
}

// TestPodsOnNodeEnqueuesManagedPodsOnNode_spec_4_6 verifies the §4.6.1
// Node→Pod fan-out: a Node event enqueues exactly the managed pods
// scheduled onto that node, and ignores pods on other nodes and
// non-managed pods on the same node.
func TestPodsOnNodeEnqueuesManagedPodsOnNode_spec_4_6(t *testing.T) {
	s := newScheme(t)
	onA1 := managedPod("on-a-1", "node-a", string(state.Idle), nil)
	onA2 := managedPod("on-a-2", "node-a", string(state.Idle), nil)
	onB := managedPod("on-b", "node-b", string(state.Idle), nil)
	foreignOnA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign-on-a", Namespace: testNS},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "x", Image: "example/x:latest"}},
		},
	}
	c := newClient(t, s, clusterNode("node-a", false), clusterNode("node-b", false), onA1, onA2, onB, foreignOnA)

	r := &warmpool.PodReconciler{Client: c}
	reqs := r.PodsOnNodeForTest(testContext(), clusterNode("node-a", false))

	got := map[string]bool{}
	for _, rq := range reqs {
		got[rq.Name] = true
	}
	if len(got) != 2 || !got["on-a-1"] || !got["on-a-2"] {
		t.Fatalf("podsOnNode(node-a) enqueued %v, want exactly on-a-1 and on-a-2", got)
	}
}
