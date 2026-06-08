// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
)

// reconcileWithClasses runs one Sandbox reconcile with the default
// resource-class registry left to its zero value (so the reconciler uses
// resourceclass.DefaultRegistry).
func reconcileResources(t *testing.T, c client.Client, s *runtime.Scheme) {
	t.Helper()
	r := &sandbox.Reconciler{Client: c, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func runtimePodContainer(t *testing.T, c client.Client) corev1.Container {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, cont := range pod.Spec.Containers {
		if cont.Name == "runtime" {
			return cont
		}
	}
	t.Fatal("pod has no runtime container")
	return corev1.Container{}
}

// TestReconcileStampsTemplateResourceClass verifies the warm-pod path:
// Sandbox.spec.resourceClass empty, so the reconciler resolves the class
// from the pool's SandboxTemplate and stamps the resolved limits on the
// container. spec: §5.2, §6.4 line 413.
func TestReconcileStampsTemplateResourceClass_spec_6_4_413(t *testing.T) {
	s := newScheme(t)
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-template", Namespace: testNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "claude-code", ResourceClass: "small"},
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-template", MinWarm: 1, MaxWarm: 3},
	}
	c := newClient(t, s, sandboxCR(""), runtimeCR(), tmpl, pool)
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("1Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 1Gi (small class)", got.String())
	}
}

// TestReconcileStampsSandboxResourceClass verifies the §12.6 CreatePod
// path: Sandbox.spec.resourceClass is set directly and takes precedence.
func TestReconcileStampsSandboxResourceClass_spec_12_6(t *testing.T) {
	s := newScheme(t)
	sb := sandboxCR("")
	sb.Spec.ResourceClass = "large"
	c := newClient(t, s, sb, runtimeCR())
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 4Gi (large class)", got.String())
	}
}

// TestReconcileDefaultsResourceClassWhenUnspecified verifies the §5.1 line
// 357 deployer-safe default: neither the Sandbox nor any template names a
// class, so the medium default applies.
func TestReconcileDefaultsResourceClassWhenUnspecified_spec_5_1_357(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR(""), runtimeCR())
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("2Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 2Gi (default medium class)", got.String())
	}
}
