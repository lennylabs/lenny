// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// TestNewClusterClientRejectsBadKubeconfig covers the production cluster-client
// seam's fail-closed path: a kubeconfig path that does not parse surfaces a
// load error rather than building a client against nothing, so the
// cluster-backed commands fail closed when the recorded kubeconfig is broken.
//
// spec: §17.4 (the cluster commands reach the control plane through the
// embedded kubeconfig).
func TestNewClusterClientRejectsBadKubeconfig(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(bad, []byte("not: a: valid: kubeconfig"), 0o600); err != nil {
		t.Fatalf("write bad kubeconfig: %v", err)
	}
	if _, err := newClusterClient(bad); err == nil {
		t.Error("newClusterClient on a malformed kubeconfig = nil, want a load error")
	}
}

// TestEchoPoolReadyFalseOnBadKubeconfig covers the echo-pool-readiness seam's
// fail-closed path: an unloadable kubeconfig reports the pool not ready rather
// than panicking, so lenny status reports "warming" when the cluster cannot be
// reached.
//
// spec: §5.2 (readyCount), §17.4 (pool readiness).
func TestEchoPoolReadyFalseOnBadKubeconfig(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(bad, []byte("not: a: valid: kubeconfig"), 0o600); err != nil {
		t.Fatalf("write bad kubeconfig: %v", err)
	}
	if echoPoolReady(context.Background(), bad) {
		t.Error("echoPoolReady on a malformed kubeconfig = true, want false")
	}
}

// deploymentPodTemplate builds a pod template carrying the control-plane
// component label so a fake-client Deployment and the pods that match its
// selector share the label the status and logs paths select on.
func deploymentPodTemplate(component string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{componentLabel: component}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: component, Image: "ghcr.io/lennylabs/lenny-" + component + ":dev"}},
		},
	}
}

// readyDeployment builds a Deployment whose status reports one ready replica
// caught up to its generation, so deploymentReady reports it healthy.
func readyDeployment(name, component string) *appsv1.Deployment {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: controlPlaneNamespace, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{componentLabel: component}},
			Template: deploymentPodTemplate(component),
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1},
	}
	return dep
}

// notReadyDeployment builds a Deployment with the desired replica but zero
// ready replicas, so deploymentReady reports it down (still rolling out).
func notReadyDeployment(name, component string) *appsv1.Deployment {
	dep := readyDeployment(name, component)
	dep.Status.ReadyReplicas = 0
	return dep
}

// controlPlanePod builds a control-plane pod carrying the component label, so
// the logs path lists it under the matching Deployment's selector.
func controlPlanePod(name, component string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controlPlaneNamespace,
			Labels:    map[string]string{componentLabel: component},
		},
	}
}

// agentPod builds an agent pod in the agent namespace carrying the §6.2
// runtime-name label, so the logs path lists it under runtime-<name>.
func agentPod(name, runtimeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: agentNamespace,
			Labels:    map[string]string{state.LabelRuntime: runtimeName},
		},
	}
}
