// SPDX-License-Identifier: MIT

package stack

import (
	"context"

	"k8s.io/client-go/rest"
)

// EnsureAgentNamespaceFromConfigForTest exposes ensureAgentNamespaceFromConfig
// to the external tier-2 envtest in package stack_test so the §4.6.2
// namespace-create path runs against a real kube-apiserver. It is test-only.
func EnsureAgentNamespaceFromConfigForTest(ctx context.Context, cfg *rest.Config, namespace string) error {
	return ensureAgentNamespaceFromConfig(ctx, cfg, namespace)
}

// ApplyEchoRuntimeCRFromConfigForTest exposes applyEchoRuntimeCRFromConfig to
// the external tier-2 envtest in package stack_test so the §4.7/§5.1 echo
// Runtime CR apply runs against a real kube-apiserver with the lenny.dev CRDs
// installed. It is test-only.
func ApplyEchoRuntimeCRFromConfigForTest(ctx context.Context, cfg *rest.Config, image string) error {
	return applyEchoRuntimeCRFromConfig(ctx, cfg, image)
}

// The kubeconfig-loading placement wrappers are exposed so the tier-2 envtest
// drives the §4.7 activation sequence end to end from a written kubeconfig (the
// path Up takes with the launcher's host-rewritten kubeconfig), exercising the
// BuildConfigFromFlags leg the already-tested ...FromConfig variants skip. They
// are test-only. spec: §4.6.2, §5.3, §4.7, §17.4.

// EnsureAgentNamespaceForTest exposes ensureAgentNamespace.
func EnsureAgentNamespaceForTest(ctx context.Context, kubeconfigPath, namespace string) error {
	return ensureAgentNamespace(ctx, kubeconfigPath, namespace)
}

// EnsureRuntimeClassForTest exposes ensureRuntimeClass.
func EnsureRuntimeClassForTest(ctx context.Context, kubeconfigPath, name, handler string) error {
	return ensureRuntimeClass(ctx, kubeconfigPath, name, handler)
}

// ApplyEchoRuntimeCRForTest exposes applyEchoRuntimeCR.
func ApplyEchoRuntimeCRForTest(ctx context.Context, kubeconfigPath, image string) error {
	return applyEchoRuntimeCR(ctx, kubeconfigPath, image)
}

// ApplyEchoPoolForTest exposes applyEchoPool.
func ApplyEchoPoolForTest(ctx context.Context, kubeconfigPath, namespace string) error {
	return applyEchoPool(ctx, kubeconfigPath, namespace)
}
