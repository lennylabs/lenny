// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"io/fs"

	"k8s.io/client-go/rest"
)

// ApplyManifestsFromConfigForTest exposes applyManifestsFromConfig to the
// external tier-2 envtest in package stack_test so the §17.4 dynamic-apply
// path runs against a real kube-apiserver from an injected manifest set,
// without writing a kubeconfig file or applying the full embedded render
// (which references images and CRDs envtest does not carry). It is
// test-only. spec: §17.4 (in-cluster control plane).
func ApplyManifestsFromConfigForTest(ctx context.Context, cfg *rest.Config, fsys fs.FS) error {
	return applyManifestsFromConfig(ctx, cfg, fsys)
}

// ApplyManifestsFromKubeconfigForTest exposes the kubeconfig-loading apply
// path to the external tier-2 envtest so the §17.4 ApplyManifests entry
// point (which loads a kubeconfig file) runs end to end against a real
// kube-apiserver from an injected manifest set, exercising the same
// kubeconfig-loading path ApplyManifests runs. It applies the full set in
// one pass (the all phase), matching ApplyManifests. It is test-only.
// spec: §17.4 (in-cluster control plane).
func ApplyManifestsFromKubeconfigForTest(ctx context.Context, kubeconfigPath string, fsys fs.FS) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, fsys, applyPhaseAll)
}

// ApplyNonDeploymentsFromKubeconfigForTest and ApplyDeploymentsFromKubeconfigForTest
// expose the two fenced bring-up phases through the kubeconfig-loading path
// applyNonImageManifests and applyDeploymentManifests run, with an injected
// manifest set so the envtest exercises the §17.4 two-phase apply (non-image
// objects first, Deployments after the import lands) against a real
// kube-apiserver without the full embedded render. They are test-only.
// spec: §17.4 (apply the Deployments after the image import lands).
func ApplyNonDeploymentsFromKubeconfigForTest(ctx context.Context, kubeconfigPath string, fsys fs.FS) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, fsys, applyPhaseNonDeployments)
}

func ApplyDeploymentsFromKubeconfigForTest(ctx context.Context, kubeconfigPath string, fsys fs.FS) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, fsys, applyPhaseDeployments)
}
