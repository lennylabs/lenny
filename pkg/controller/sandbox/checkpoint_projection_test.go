// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
)

// hasArgPrefixVal reports whether any arg begins with the given prefix.
func hasArgPrefixVal(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// checkpointFixture seeds a Sandbox referencing a pool whose SandboxTemplate
// carries the supplied workspaceSizeLimitBytes, returning the booted envtest
// client.
func checkpointFixture(t *testing.T, sizeLimit *int64) client.Client {
	t.Helper()
	s := newScheme(t)
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-template", Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:              "claude-code",
			WorkspaceSizeLimitBytes: sizeLimit,
		},
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-template", MinWarm: 1, MaxWarm: 3},
	}
	return newClient(t, s, sandboxCR(""), runtimeCR(), tmpl, pool)
}

// reconcileWithCA runs one Sandbox reconcile with the reconciler carrying the
// supplied object-store CA ConfigMap name.
func reconcileWithCA(t *testing.T, c client.Client, s *runtime.Scheme, caConfigMap string) {
	t.Helper()
	r := &sandbox.Reconciler{
		Client:                 c,
		Scheme:                 s,
		AdapterImage:           "ghcr.io/lennylabs/lenny-adapter:v1",
		ObjectStoreCAConfigMap: caConfigMap,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func adapterContainer(t *testing.T, c client.Client) corev1.Container {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, ctr := range pod.Spec.Containers {
		if ctr.Name == "adapter" {
			return ctr
		}
	}
	t.Fatalf("pod has no adapter container; containers=%v", pod.Spec.Containers)
	return corev1.Container{}
}

// TestReconcileProjectsWorkspaceLimitAndCABundle_spec_4_4 verifies the
// reconciler resolves the pool's SandboxTemplate workspaceSizeLimitBytes and
// carries the controller's object-store CA ConfigMap onto the created pod: the
// adapter container gets --workspace-size-limit-bytes and --objectstore-ca-bundle
// and the CA ConfigMap is projected read-only. Without this projection the
// adapter's pre-checkpoint size probe and the TLS handshake to the object
// store are both inert.
//
// diagnosis: a failure means the pool's workspace-size limit or the
// deployer-supplied object-store CA never reaches the agent pod, so the
// checkpoint size probe stays disabled and the adapter cannot complete the TLS
// handshake to a self-managed object store during checkpoint upload.
//
// spec: 4.4 (workspace-size probe), 13.2 (object-store TLS)
func TestReconcileProjectsWorkspaceLimitAndCABundle_spec_4_4(t *testing.T) {
	s := newScheme(t)
	c := checkpointFixture(t, ptr.To(int64(314572800)))
	reconcileWithCA(t, c, s, "lenny-objectstore-ca")

	adapter := adapterContainer(t, c)
	if !hasArg(adapter.Args, "--workspace-size-limit-bytes=314572800") {
		t.Errorf("adapter args = %v, want --workspace-size-limit-bytes=314572800", adapter.Args)
	}
	if !hasArg(adapter.Args, "--objectstore-ca-bundle=/etc/lenny/objectstore-ca/ca.crt") {
		t.Errorf("adapter args = %v, want --objectstore-ca-bundle=/etc/lenny/objectstore-ca/ca.crt", adapter.Args)
	}
	var mounted bool
	for _, m := range adapter.VolumeMounts {
		if m.Name == "objectstore-ca" {
			mounted = true
			if !m.ReadOnly || m.MountPath != "/etc/lenny/objectstore-ca" {
				t.Errorf("CA mount = %+v, want read-only at /etc/lenny/objectstore-ca", m)
			}
		}
	}
	if !mounted {
		t.Errorf("adapter container does not mount the object-store CA bundle; mounts=%v", adapter.VolumeMounts)
	}
}

// TestReconcileOmitsCheckpointProjectionWhenUnset_spec_13_2 verifies the
// failing arm through the reconciler: a pool that declares no
// workspaceSizeLimitBytes and a controller with no object-store CA ConfigMap
// produce a pod with neither flag and no CA volume, so a cloud-managed object
// store chaining to a public CA is not saddled with a nonexistent ConfigMap
// that would strand the pod in ContainerCreating.
//
// diagnosis: a failure means the reconciler renders the object-store CA
// projection or the workspace-size flag when the deployer configured neither,
// which either strands agent pods on a missing ConfigMap or enables a size
// probe the pool never asked for.
//
// spec: 4.4 (workspace-size probe), 13.2 (object-store TLS)
func TestReconcileOmitsCheckpointProjectionWhenUnset_spec_13_2(t *testing.T) {
	s := newScheme(t)
	c := checkpointFixture(t, nil)
	reconcileWithCA(t, c, s, "")

	adapter := adapterContainer(t, c)
	if hasArgPrefixVal(adapter.Args, "--workspace-size-limit-bytes") {
		t.Errorf("adapter args = %v, want no --workspace-size-limit-bytes flag", adapter.Args)
	}
	if hasArgPrefixVal(adapter.Args, "--objectstore-ca-bundle") {
		t.Errorf("adapter args = %v, want no --objectstore-ca-bundle flag", adapter.Args)
	}
	for _, m := range adapter.VolumeMounts {
		if m.Name == "objectstore-ca" {
			t.Errorf("adapter mounts the object-store CA bundle with no ConfigMap configured")
		}
	}
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == "objectstore-ca" {
			t.Errorf("pod carries the object-store CA volume with no ConfigMap configured")
		}
	}
}
