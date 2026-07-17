// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

const (
	objectStoreCAVolume  = "objectstore-ca"
	objectStoreCAMount   = "/etc/lenny/objectstore-ca"
	objectStoreCABundle  = "--objectstore-ca-bundle=/etc/lenny/objectstore-ca/ca.crt"
	workspaceSizeLimitFl = "--workspace-size-limit-bytes"
)

// containerArgs returns the named container's args, failing the test when
// the container is absent.
func containerArgs(t *testing.T, pod *corev1.Pod, name string) []string {
	t.Helper()
	return container(t, pod, name).Args
}

// TestBuildRendersWorkspaceSizeLimit_spec_4_4 verifies the §4.4 line 254
// workspace-size hard limit is rendered as --workspace-size-limit-bytes on
// both the sidecar adapter and the embedded runtime container. Without the
// flag the adapter's pre-checkpoint size probe stays disabled, so the §10.1
// storage reservation and the permanent-error checkpoint arm are inert.
//
// spec: 4.4 (workspace-size probe)
func TestBuildRendersWorkspaceSizeLimit_spec_4_4(t *testing.T) {
	for _, tc := range []struct {
		model     string
		container string
	}{
		{model: "", container: "adapter"},
		{model: "embedded", container: "runtime"},
	} {
		in := inputs()
		in.DeploymentModel = tc.model
		in.WorkspaceSizeLimitBytes = ptr.To(int64(536870912))
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("model=%q Build: %v", tc.model, err)
		}
		args := containerArgs(t, pod, tc.container)
		if !hasArg(args, "--workspace-size-limit-bytes=536870912") {
			t.Errorf("model=%q: %s container args = %v, want --workspace-size-limit-bytes=536870912", tc.model, tc.container, args)
		}
	}
}

// TestBuildOmitsWorkspaceSizeLimitWhenUnset_spec_4_4 verifies the flag is
// absent when the pool declares no limit, which keeps the probe disabled
// exactly where no limit is configured.
//
// spec: 4.4 (workspace-size probe)
func TestBuildOmitsWorkspaceSizeLimitWhenUnset_spec_4_4(t *testing.T) {
	for _, tc := range []struct {
		model     string
		container string
	}{
		{model: "", container: "adapter"},
		{model: "embedded", container: "runtime"},
	} {
		in := inputs() // WorkspaceSizeLimitBytes nil
		in.DeploymentModel = tc.model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("model=%q Build: %v", tc.model, err)
		}
		if args := containerArgs(t, pod, tc.container); hasArgPrefix(args, workspaceSizeLimitFl) {
			t.Errorf("model=%q: %s container args = %v, want no --workspace-size-limit-bytes flag", tc.model, tc.container, args)
		}
	}
}

// TestBuildProjectsObjectStoreCABundle_spec_13_2 verifies the §13.2
// object-store CA trust bundle: when the reconciler carries a ConfigMap name
// into Inputs, the builder projects the ConfigMap read-only onto the pod's
// gateway-facing container and points --objectstore-ca-bundle at the mounted
// ca.crt so the adapter can complete the TLS handshake to a self-managed
// object store. Verified on both the sidecar adapter and the embedded runtime.
//
// spec: 13.2 (object-store TLS)
func TestBuildProjectsObjectStoreCABundle_spec_13_2(t *testing.T) {
	for _, tc := range []struct {
		model     string
		container string
	}{
		{model: "", container: "adapter"},
		{model: "embedded", container: "runtime"},
	} {
		in := inputs()
		in.DeploymentModel = tc.model
		in.ObjectStoreCAConfigMap = "lenny-objectstore-ca"
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("model=%q Build: %v", tc.model, err)
		}

		v := volume(t, pod, objectStoreCAVolume)
		if v.ConfigMap == nil || v.ConfigMap.Name != "lenny-objectstore-ca" {
			t.Fatalf("model=%q: %q volume is not a ConfigMap named lenny-objectstore-ca: %+v", tc.model, objectStoreCAVolume, v)
		}

		m, ok := hasMount(container(t, pod, tc.container), objectStoreCAVolume)
		if !ok {
			t.Fatalf("model=%q: %s container does not mount the object-store CA bundle", tc.model, tc.container)
		}
		if !m.ReadOnly || m.MountPath != objectStoreCAMount {
			t.Errorf("model=%q: CA mount = %+v, want read-only at %s", tc.model, m, objectStoreCAMount)
		}

		if args := containerArgs(t, pod, tc.container); !hasArg(args, objectStoreCABundle) {
			t.Errorf("model=%q: %s container args = %v, want %s", tc.model, tc.container, args, objectStoreCABundle)
		}
	}
}

// TestBuildOmitsObjectStoreCABundleWhenUnset_spec_13_2 verifies the failing
// arm: a cloud-managed endpoint chaining to a public CA (or an unconfigured
// deployment) carries no ConfigMap name, so the builder renders no volume, no
// mount, and no --objectstore-ca-bundle flag. A pod that mounted a
// nonexistent ConfigMap would sit in ContainerCreating and never start.
//
// spec: 13.2 (object-store TLS)
func TestBuildOmitsObjectStoreCABundleWhenUnset_spec_13_2(t *testing.T) {
	for _, tc := range []struct {
		model     string
		container string
	}{
		{model: "", container: "adapter"},
		{model: "embedded", container: "runtime"},
	} {
		in := inputs() // ObjectStoreCAConfigMap empty
		in.DeploymentModel = tc.model
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("model=%q Build: %v", tc.model, err)
		}
		if hasVolume(pod, objectStoreCAVolume) {
			t.Errorf("model=%q: pod carries the object-store CA volume with no ConfigMap configured", tc.model)
		}
		if _, ok := hasMount(container(t, pod, tc.container), objectStoreCAVolume); ok {
			t.Errorf("model=%q: %s container mounts the CA bundle with no ConfigMap configured", tc.model, tc.container)
		}
		if args := containerArgs(t, pod, tc.container); hasArgPrefix(args, "--objectstore-ca-bundle") {
			t.Errorf("model=%q: %s container args = %v, want no --objectstore-ca-bundle flag", tc.model, tc.container, args)
		}
	}
}
