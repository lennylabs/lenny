// SPDX-License-Identifier: MIT

package agentpod_test

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
	"github.com/lennylabs/lenny/pkg/controller/agentpod"
)

// reconcileWithClasses runs one AgentPod reconcile with the default
// resource-class registry left to its zero value (so the reconciler uses
// resourceclass.DefaultRegistry).
func reconcileResources(t *testing.T, c client.Client, s *runtime.Scheme) {
	t.Helper()
	r := &agentpod.Reconciler{Client: c, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1", SupervisorImage: "ghcr.io/lennylabs/lenny-supervisor:v1"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNamespace(t), Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func runtimePodContainer(t *testing.T, c client.Client) corev1.Container {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNamespace(t), Name: testName}, &pod); err != nil {
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
// AgentPod.spec.resourceClass empty, so the reconciler resolves the class
// from the pool's PodSet and stamps the resolved limits on the
// container. spec: §5.2, §6.4.
func TestReconcileStampsTemplateResourceClass_spec_6_4_413(t *testing.T) {
	s := newScheme(t)
	pool := &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNamespace(t)},
		Spec:       lennyv1.PodSetSpec{RuntimeRef: testRuntime(t), ResourceClass: "small", MinWarm: 1, MaxWarm: 3},
	}
	c := newClient(t, s, agentPodCR(t, ""), runtimeCR(t), pool)
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("1Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 1Gi (small class)", got.String())
	}
}

// TestReconcileStampsAgentPodResourceClass verifies the §12.6 CreatePod
// path: AgentPod.spec.resourceClass is set directly and takes precedence.
func TestReconcileStampsAgentPodResourceClass(t *testing.T) {
	s := newScheme(t)
	sb := agentPodCR(t, "")
	sb.Spec.ResourceClass = "large"
	c := newClient(t, s, sb, runtimeCR(t))
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 4Gi (large class)", got.String())
	}
}

// TestReconcileDefaultsResourceClassWhenUnspecified verifies the §5.1 deployer-safe default: neither the AgentPod nor any template names a
// class, so the medium default applies.
func TestReconcileDefaultsResourceClassWhenUnspecified_spec_5_1_357(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, agentPodCR(t, ""), runtimeCR(t))
	reconcileResources(t, c, s)

	cont := runtimePodContainer(t, c)
	if got := cont.Resources.Limits.Memory(); got.Cmp(resource.MustParse("2Gi")) != 0 {
		t.Errorf("runtime memory limit = %s, want 2Gi (default medium class)", got.String())
	}
}
