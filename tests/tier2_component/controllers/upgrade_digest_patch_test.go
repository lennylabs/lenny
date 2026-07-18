// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/common/registry"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestUpgradeOpsRollPatchesDeploymentWithDigestWhenRequireDigest exercises
// the §25.8 OpsRoll Deployment mutation: when platform.registry.requireDigest
// is true, the strategic merge patch the old lenny-ops applies to its own
// Deployment must carry the resolved sha256: digest reference rather than a
// mutable tag.
//
// diagnosis: once the OpsRoll self-patch call site exists, a failure here
// means the patch carried the tag form of the resolved image reference
// instead of the digest form, defeating the requireDigest guarantee that a
// registry mutation cannot silently change which bits an upgrade rolls out.
//
// spec: §25.8 line 3506 ("Old lenny-ops patches its own Deployment's image
// tag via K8s API to the resolved ops image reference. The patch is a
// strategic merge patch using the digest form when
// platform.registry.requireDigest: true").
func TestUpgradeOpsRollPatchesDeploymentWithDigestWhenRequireDigest(t *testing.T) {
	t.Skip("pkg/ops/upgradeservice has no OpsRoll Deployment-patch call site: " +
		"grep for StrategicMergePatchType across pkg/ops/upgradeservice and " +
		"cmd/lenny-ops finds none. The orchestrator is documented and built " +
		"as operator-paced (it records phase transitions and audit/events; " +
		"the actual kubectl/helm mutation is left to the operator or a " +
		"Kubernetes seam between calls), so there is no product entry point " +
		"this test can drive. Closing this gap needs a human decision on " +
		"whether to add an in-process self-patch or keep the operator-paced " +
		"model as the accepted reading of the spec sentence cited above.")

	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	cl, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	const ns = "lenny-system"
	const deploymentName = "lenny-ops"

	if err := cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	oldRef := "ghcr.io/lennylabs/lenny-ops:1.4.3"
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: deploymentName, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": deploymentName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": deploymentName}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: deploymentName, Image: oldRef}},
				},
			},
		},
	}
	if err := cl.Create(ctx, dep); err != nil {
		t.Fatalf("create lenny-ops Deployment: %v", err)
	}

	// The digest-form target reference the OpsRoll patch must carry: the
	// registry ImageResolver resolves "ops" to a digest reference when
	// Config.RequireDigest is true (pkg/common/registry.Resolver.Resolve).
	resolver := registry.New(registry.Config{
		URL:           "ghcr.io/lennylabs",
		RequireDigest: true,
		Overrides: map[string]string{
			"ops": "ghcr.io/lennylabs/lenny-ops@sha256:" +
				"5d6e7f5a6b7c5d6e7f5a6b7c5d6e7f5a6b7c5d6e7f5a6b7c5d6e7f5a6b7c5d6e",
		},
	})
	targetRef, err := resolver.Resolve("ops")
	if err != nil {
		t.Fatalf("resolve target ops image: %v", err)
	}

	// Once the OpsRoll self-patch call site exists (pkg/ops/upgradeservice or
	// cmd/lenny-ops), invoke it here against dep with requireDigest true, then
	// re-fetch the Deployment and assert its container image equals
	// targetRef (the sha256: digest form), per the spec sentence cited above.
	_ = targetRef
}
