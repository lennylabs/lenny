// SPDX-License-Identifier: MIT

package stack

import (
	"context"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// CollectClusterComponentsFromClientForTest exposes the control-plane
// Deployment-readiness rows to the external tier-2 envtest so the §17.4
// cluster-backed status read runs against a real kube-apiserver from an
// already-built clientset, without writing a kubeconfig file or recording a
// stack. It is test-only. spec: §17.4 (status reads Deployment readiness).
func CollectClusterComponentsFromClientForTest(ctx context.Context, client kubernetes.Interface) []ComponentStatus {
	rows := make([]ComponentStatus, 0, 3)
	for _, c := range []struct{ component, deployment string }{
		{"gateway", gatewayDeploymentName},
		{"controller", controllerDeploymentName},
		{"ops", opsDeploymentName},
	} {
		rows = append(rows, deploymentComponentStatus(ctx, client, c.component, c.deployment))
	}
	return rows
}

// RolloutRestartDeploymentForTest exposes rolloutRestartDeployment to the
// external tier-2 envtest so the §24.19 rollout-restart runs against a real
// kube-apiserver from an already-built clientset. It is test-only. spec:
// §24.19 line 264 (the restart is a Deployment rollout-restart).
func RolloutRestartDeploymentForTest(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	return rolloutRestartDeployment(ctx, client, namespace, name)
}

// EchoPoolReadyFromConfigForTest exposes echoPoolReadyFromConfig to the
// external tier-2 envtest so the §17.4 echo-pool-readiness read runs against a
// real kube-apiserver with the lenny.dev CRDs installed. It is test-only.
// spec: §5.2 (readyCount), §17.4 (pool readiness distinct from gateway-up).
func EchoPoolReadyFromConfigForTest(ctx context.Context, cfg *rest.Config) bool {
	return echoPoolReadyFromConfig(ctx, cfg)
}

// ControlPlaneDeploymentNamesForTest exposes the control-plane Deployment names
// so the envtest can create the matching fixtures and assert against them. It
// is test-only.
func ControlPlaneDeploymentNamesForTest() (gateway, controller, ops string) {
	return gatewayDeploymentName, controllerDeploymentName, opsDeploymentName
}

// RestartedAtAnnotationForTest exposes the rollout-restart annotation key so
// the envtest can assert the rolled Deployment carries it. It is test-only.
func RestartedAtAnnotationForTest() string { return restartedAtAnnotation }

// NewClusterClientForTest exposes the default newClusterClient so the external
// tier-2 envtest exercises the §17.4 cluster-client happy path (load a real
// kubeconfig and build a working clientset) that the unit test cannot reach
// because it injects clusterClientFn. It is test-only. spec: §17.4 (the cluster
// commands reach the in-cluster control plane through the embedded kubeconfig).
func NewClusterClientForTest(kubeconfigPath string) (kubernetes.Interface, error) {
	return newClusterClient(kubeconfigPath)
}

// DeploymentPodSelectorForTest exposes the per-component pod-log selector so the
// envtest can list a component's pods against a real kube-apiserver by the same
// selector lenny logs uses. The lenny-ops Deployment carries the §13.2 app:
// lenny-ops pod-label exception, so a uniform lenny.dev/component selector would
// list zero ops pods; this exposes the resolved selector string for that
// assertion. It is test-only. spec: §17.4, §13.2 (the lenny-ops pod-label
// exception).
func DeploymentPodSelectorForTest(component string) string {
	return deploymentPodSelector(component).String()
}
