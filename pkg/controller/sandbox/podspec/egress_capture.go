// SPDX-License-Identifier: MIT

package podspec

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// EgressCapture configures the egress-capture sidecar an
// agent pod runs alongside its runtime. The sidecar listens on
// ListenPort, forwards every accepted TCP connection to Upstream,
// and writes one JSONL row per connection to a path under the shared
// capture volume; the leakage probe (tier-9) reads the file
// via `kubectl exec` to assert no credential material appears in
// egress.
type EgressCapture struct {
	// Image is the OCI image of the egress-capture container. The
	// controller takes it from --egress-capture-image, which is empty
	// unless a test overlay sets it; no admission webhook rejects the
	// image itself.
	Image string
	// Upstream is the host:port the sidecar forwards every accepted
	// connection to (e.g. `api.openai.com:443`). Required.
	Upstream string
	// ListenPort is the local TCP port the runtime container dials.
	// Defaults to 8443 when zero.
	ListenPort int32
	// CapturePath is the in-pod path the sidecar writes the JSONL
	// capture file to. Defaults to /run/lenny-capture/egress.jsonl
	// when empty. The capture lives on a shared emptyDir mounted into
	// both the sidecar and the runtime container (the probe reads via
	// the runtime mount with `kubectl exec`).
	CapturePath string
}

const (
	// EgressCaptureContainerName is the name of the egress
	// capture sidecar container injected into agent pods that opt in.
	EgressCaptureContainerName = "egress-capture"
	// EgressCaptureVolumeName is the shared emptyDir between the
	// sidecar (writer) and the runtime container (reader) that holds
	// the JSONL capture file.
	EgressCaptureVolumeName = "egress-capture"
	// EgressCaptureMountPath is the in-pod path the capture volume is
	// mounted on in both the sidecar and the runtime containers. The
	// JSONL file lives at egress.jsonl within it by default.
	//
	// spec: §6.4 / §13.1 — distinct from the credentialMount (/run/lenny):
	// `/run/lenny-capture` is a sibling path and not a subdirectory of
	// `/run/lenny`, so the credential tmpfs and the capture emptyDir cannot
	// collide. Any future §6.4 in-pod path under the `/run/lenny-*` prefix
	// must remain a sibling — never a subpath of an existing mount — so the
	// mounts stay independent.
	EgressCaptureMountPath = "/run/lenny-capture"
	// defaultEgressCaptureListenPort is the TCP port the sidecar
	// listens on by default. The runtime container dials it instead
	// of the real upstream.
	defaultEgressCaptureListenPort int32 = 8443
	// egressCaptureUID is the UID and primary GID of the test-only
	// egress-capture container, which the builder injects only when the
	// controller's egress-capture image is set. It lies outside the default
	// adapter and agent UIDs, the default lenny-cred-readers GID, and 0.
	egressCaptureUID int64 = 65531
)

// defaultEgressCapturePath is the default in-pod path for the
// JSONL capture file. The leakage probe reads it via
// `kubectl exec` against the runtime container.
var defaultEgressCapturePath = EgressCaptureMountPath + "/egress.jsonl"

// injectEgressCaptureSidecar appends the egress-capture
// container to pod.Spec.Containers, mounts the capture volume on
// each container index named in mountOn (typically the runtime
// container so the probe can read it), and appends the
// emptyDir volume to pod.Spec.Volumes. The injection is a no-op when
// in.EgressCapture is nil.
func injectEgressCaptureSidecar(in Inputs, pod *corev1.Pod, mountOn []int) {
	if in.EgressCapture == nil {
		return
	}
	listenPort := in.EgressCapture.ListenPort
	if listenPort == 0 {
		listenPort = defaultEgressCaptureListenPort
	}
	capturePath := in.EgressCapture.CapturePath
	if capturePath == "" {
		capturePath = defaultEgressCapturePath
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: EgressCaptureVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		},
	})
	captureMount := corev1.VolumeMount{Name: EgressCaptureVolumeName, MountPath: EgressCaptureMountPath}
	for _, idx := range mountOn {
		if idx >= 0 && idx < len(pod.Spec.Containers) {
			pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, corev1.VolumeMount{
				Name: EgressCaptureVolumeName, MountPath: EgressCaptureMountPath, ReadOnly: true,
			})
		}
	}
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name:  EgressCaptureContainerName,
		Image: in.EgressCapture.Image,
		Args: []string{
			fmt.Sprintf("--listen=:%d", listenPort),
			fmt.Sprintf("--upstream=%s", in.EgressCapture.Upstream),
			fmt.Sprintf("--capture=%s", capturePath),
		},
		Ports: []corev1.ContainerPort{{
			Name:          "capture",
			ContainerPort: listenPort,
		}},
		VolumeMounts: []corev1.VolumeMount{captureMount, {Name: dshmVolumeName, MountPath: dshmMount}},
		// spec: §13.1 (Container identity)
		// The agent UID is reserved to the runtime container, so the
		// capture container runs at its own non-reserved identity. The
		// runtime still reads the capture file through the
		// lenny-cred-readers group the fsGroup-managed emptyDir assigns.
		SecurityContext: containerSecurityContext(egressCaptureUID),
	})
}
