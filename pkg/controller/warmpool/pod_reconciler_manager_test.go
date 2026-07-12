// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestPodReconcileHostSchedulableFollowsCordonUncordon_spec_4_6 exercises
// the §4.6.1 informer-driven half of the host-node schedulability
// contract end to end against a real kube-apiserver: a cordon and a
// subsequent uncordon of the Node hosting a managed pod must, on the
// Node informer alone, trigger re-reconciliation of that pod and flip
// its lenny.dev/host-schedulable label, with no manual Reconcile call.
//
// The spec sentence under test (§4.6.1, "Host-node schedulability
// labeling"): "Node state changes (cordon, uncordon, autoscaler drain)
// surface on the Node informer and trigger re-reconciliation of every
// pod whose spec.nodeName matches the Node; the controller re-labels
// each affected pod within a single reconcile cycle." A Kubernetes
// cordon is a patch of the Node's .spec.unschedulable field (which
// envtest reproduces exactly), and §4.6.1 sets the label to "true" when
// .spec.unschedulable is false and no node.kubernetes.io/unschedulable
// taint is present, or "false" otherwise. The peer test
// TestPodReconcileHostSchedulableLabel_spec_4_6 covers the labeling
// computation via a manual Reconcile; this test covers the SetupWithManager
// Node→Pod watch fan-out and the uncordon return-to-"true" edge that a
// manual Reconcile cannot exercise.
//
// diagnosis: a failure means the WarmPoolController's per-pod reconciler
// does not re-label pods when their host Node is cordoned or uncordoned
// through the Node informer (a dropped Watches(&Node{}) wiring, a broken
// spec.nodeName field index, or a podsOnNode mapper that no longer
// enqueues the affected pods). A stuck "true" after cordon silently
// blocks the §6.2 claimed→sdk_connecting recycle re-warm host-schedulability
// precondition from ever seeing an unschedulable host; a stuck "false"
// after uncordon strands otherwise-reusable pods.
//
// spec: 4.6.1 (Host-node schedulability labeling; Node-informer re-reconcile
// on cordon/uncordon), 6.2 (host-node schedulability precondition consumer)
func TestPodReconcileHostSchedulableFollowsCordonUncordon_spec_4_6(t *testing.T) {
	s := newScheme(t)
	env := envtest.Start(t)

	setup, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := setup.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}); err != nil {
		t.Fatalf("create namespace %s: %v", testNS, err)
	}

	const nodeName = "worker-a"
	if err := setup.Create(ctx, clusterNode(nodeName, false)); err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Cleanup(func() {
		_ = setup.Delete(context.Background(), clusterNode(nodeName, false))
	})
	pod := managedPod("pod-cordon", nodeName, string(state.Idle), nil)
	if err := setup.Create(ctx, pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Run the real controller manager so the Node informer, the
	// spec.nodeName field index, and the Node→Pod watch fan-out that
	// SetupWithManager installs drive every re-reconcile. Listeners bind
	// to "0" (disabled) and leader election is off so the manager needs no
	// ports or lease. SkipNameValidation lets the "warmpool-pod" controller
	// register even if a sibling test in this binary already claimed the
	// process-global controller name.
	mgr, err := manager.New(env.RESTConfig(), manager.Options{
		Scheme:                 s,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Controller:             config.Controller{SkipNameValidation: ptr.To(true)},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	if err := (&warmpool.PodReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}
	mgrCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("manager.Start returned: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Log("manager.Start did not return within 30s of cancel")
		}
	})

	// The initial reconcile, driven purely by the manager's informers,
	// must stamp the label "true" on the schedulable node.
	waitForHostSchedulable(t, setup, pod.Name, "true", "initial reconcile on a schedulable node")

	// Cordon the node (patch .spec.unschedulable=true, exactly what
	// `kubectl cordon` does). The Node informer must fan out to the pod and
	// flip the label to "false" with no manual reconcile.
	setNodeUnschedulable(t, setup, nodeName, true)
	waitForHostSchedulable(t, setup, pod.Name, "false", "cordon must flip the label to false via the Node informer")

	// Uncordon the node. The label must return to "true".
	setNodeUnschedulable(t, setup, nodeName, false)
	waitForHostSchedulable(t, setup, pod.Name, "true", "uncordon must return the label to true via the Node informer")
}

// setNodeUnschedulable patches the node's .spec.unschedulable field,
// mirroring a cordon (true) or uncordon (false).
func setNodeUnschedulable(t *testing.T, c client.Client, name string, unschedulable bool) {
	t.Helper()
	var node corev1.Node
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &node); err != nil {
		t.Fatalf("get node %s: %v", name, err)
	}
	node.Spec.Unschedulable = unschedulable
	if err := c.Update(context.Background(), &node); err != nil {
		t.Fatalf("set node %s unschedulable=%v: %v", name, unschedulable, err)
	}
}

// waitForHostSchedulable polls the pod through the API server until its
// lenny.dev/host-schedulable label reaches want, failing with the given
// reason if it does not within the reconcile budget. The window is
// generous relative to the §4.6.1 "single reconcile cycle (typically
// < 1 s per batch)" target because informer delivery and cache sync add
// nondeterministic latency in envtest.
func waitForHostSchedulable(t *testing.T, c client.Client, podName, want, reason string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var p corev1.Pod
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podName}, &p); err != nil {
			t.Fatalf("get pod %s: %v", podName, err)
		}
		last = p.Labels[warmpool.LabelHostSchedulable]
		if last == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("host-schedulable label=%q, want %q: %s", last, want, reason)
}
