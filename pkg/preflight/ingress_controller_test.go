// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func ingressClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("clientgo AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func ingressNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func ingressPod(namespace, name string, labels map[string]string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

// spec: §13.2 line 292 NET-038 — an empty namespace skips the check
// entirely (no ingress integration configured).
func TestIngressControllerCheckSkipsEmpty_spec_13_2_292(t *testing.T) {
	d := IngressControllerCheck{}.Decide()
	if !d.Passed || d.Reason != "" {
		t.Fatalf("empty namespace should skip silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// spec: §13.2 line 292 NET-038 — a configured namespace with a running
// labeled controller pod passes without an advisory.
func TestIngressControllerCheckPasses_spec_13_2_292(t *testing.T) {
	d := IngressControllerCheck{
		Namespace:               "ingress-nginx",
		PodLabelKey:             "app.kubernetes.io/name",
		PodLabelValue:           "ingress-nginx",
		NamespaceExists:         true,
		HasRunningControllerPod: true,
	}.Decide()
	if !d.Passed || d.Reason != "" {
		t.Fatalf("present namespace + running labeled pod should pass clean, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// spec: §13.2 line 292 NET-038 — a missing namespace is a non-blocking
// WARNING (the namespaceSelector matches nothing).
func TestIngressControllerCheckWarnsMissingNamespace_spec_13_2_292(t *testing.T) {
	d := IngressControllerCheck{
		Namespace:       "ingress-nginx",
		PodLabelKey:     "app.kubernetes.io/name",
		PodLabelValue:   "ingress-nginx",
		NamespaceExists: false,
	}.Decide()
	if !d.Passed {
		t.Fatalf("missing-namespace advisory must not abort the install; got Passed=false reason=%q", d.Reason)
	}
	for _, want := range []string{"WARNING", "ingress-nginx", "does not exist", "namespaceSelector"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("missing-namespace reason should contain %q; got %q", want, d.Reason)
		}
	}
}

// spec: §13.2 line 292 NET-038 — a present namespace with no running
// pod matching the controllerPodLabel is a non-blocking WARNING (the
// podSelector matches nothing).
func TestIngressControllerCheckWarnsMissingPod_spec_13_2_292(t *testing.T) {
	d := IngressControllerCheck{
		Namespace:               "ingress-nginx",
		PodLabelKey:             "app.kubernetes.io/name",
		PodLabelValue:           "ingress-nginx",
		NamespaceExists:         true,
		HasRunningControllerPod: false,
	}.Decide()
	if !d.Passed {
		t.Fatalf("missing-pod advisory must not abort the install; got Passed=false reason=%q", d.Reason)
	}
	for _, want := range []string{"WARNING", "app.kubernetes.io/name=ingress-nginx", "podSelector"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("missing-pod reason should contain %q; got %q", want, d.Reason)
		}
	}
}

// spec: §13.2 line 292 NET-038 — a present namespace with an empty pod
// label only checks namespace existence (the pod half is skipped).
func TestIngressControllerCheckSkipsPodHalfWithoutLabel_spec_13_2_292(t *testing.T) {
	d := IngressControllerCheck{
		Namespace:       "ingress-nginx",
		NamespaceExists: true,
	}.Decide()
	if !d.Passed || d.Reason != "" {
		t.Fatalf("present namespace + empty label should pass silently, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// spec: §13.2 line 292 NET-038 — gatherIngressController reads the
// namespace and a running labeled pod from the cluster.
func TestGatherIngressControllerRunningPod_spec_13_2_292(t *testing.T) {
	c := ingressClient(
		t,
		ingressNamespace("ingress-nginx"),
		ingressPod("ingress-nginx", "controller-0",
			map[string]string{"app.kubernetes.io/name": "ingress-nginx"}, corev1.PodRunning),
	)
	nsExists, hasPod, err := gatherIngressController(context.Background(), c, "ingress-nginx", "app.kubernetes.io/name", "ingress-nginx")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !nsExists || !hasPod {
		t.Fatalf("running labeled pod should be discovered, got nsExists=%v hasPod=%v", nsExists, hasPod)
	}
}

// spec: §13.2 line 292 NET-038 — a pod matching the label but not in the
// Running phase (sidecar churn, crash loop) is not a usable controller
// pod, so hasRunningPod is false.
func TestGatherIngressControllerPendingPodNotCounted_spec_13_2_292(t *testing.T) {
	c := ingressClient(
		t,
		ingressNamespace("ingress-nginx"),
		ingressPod("ingress-nginx", "controller-0",
			map[string]string{"app.kubernetes.io/name": "ingress-nginx"}, corev1.PodPending),
	)
	nsExists, hasPod, err := gatherIngressController(context.Background(), c, "ingress-nginx", "app.kubernetes.io/name", "ingress-nginx")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if !nsExists || hasPod {
		t.Fatalf("a non-running labeled pod must not count, got nsExists=%v hasPod=%v", nsExists, hasPod)
	}
}

// spec: §13.2 line 292 NET-038 — a missing namespace returns
// NamespaceExists=false with no error so the advisory fires.
func TestGatherIngressControllerMissingNamespace_spec_13_2_292(t *testing.T) {
	c := ingressClient(t)
	nsExists, hasPod, err := gatherIngressController(context.Background(), c, "ingress-nginx", "app.kubernetes.io/name", "ingress-nginx")
	if err != nil {
		t.Fatalf("missing namespace should not error: %v", err)
	}
	if nsExists || hasPod {
		t.Fatalf("missing namespace should report nsExists=false hasPod=false, got %v/%v", nsExists, hasPod)
	}
}
