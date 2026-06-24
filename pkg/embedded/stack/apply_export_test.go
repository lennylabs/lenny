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

// ApplyManifestsFromKubeconfigForTest exposes applyManifestsFromKubeconfig
// to the external tier-2 envtest so the §17.4 ApplyManifests entry point
// (which loads a kubeconfig file) runs end to end against a real
// kube-apiserver from an injected manifest set, exercising the same
// kubeconfig-loading path ApplyManifests runs. It is test-only.
// spec: §17.4 (in-cluster control plane).
func ApplyManifestsFromKubeconfigForTest(ctx context.Context, kubeconfigPath string, fsys fs.FS) error {
	return applyManifestsFromKubeconfig(ctx, kubeconfigPath, fsys)
}
