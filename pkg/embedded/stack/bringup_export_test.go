// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"time"

	"k8s.io/client-go/rest"
)

// CreateDevBearerSecretFromConfigForTest exposes createDevBearerSecretFromConfig
// to the external tier-2 envtest in package stack_test so the §10.2/§17.4
// dev bearer-trust Secret create and update-in-place path runs against a real
// kube-apiserver without writing a kubeconfig file. It is test-only.
func CreateDevBearerSecretFromConfigForTest(ctx context.Context, cfg *rest.Config, keyFilePath string) error {
	return createDevBearerSecretFromConfig(ctx, cfg, keyFilePath)
}

// WaitDeploymentReadyForTest exposes waitDeploymentReady to the external
// tier-2 envtest so the §17.4 gateway-readiness wait runs against a real
// kube-apiserver. It is test-only.
func WaitDeploymentReadyForTest(ctx context.Context, cfg *rest.Config, namespace, name string, timeout, interval time.Duration) error {
	return waitDeploymentReady(ctx, cfg, namespace, name, timeout, interval)
}

// DevBearerTrustSecretNameForTest, DevBearerTrustSecretKeyForTest, and
// ControlPlaneNamespaceForTest expose the fixed dev bearer-trust Secret name,
// its data key, and the control-plane namespace so the envtest can locate the
// created Secret. They are test-only.
func DevBearerTrustSecretNameForTest() string { return devBearerTrustSecretName }
func DevBearerTrustSecretKeyForTest() string  { return devBearerTrustSecretKey }
func ControlPlaneNamespaceForTest() string    { return controlPlaneNamespace }
