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
