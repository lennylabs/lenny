// SPDX-License-Identifier: MIT

package podspec

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	// objectStoreCAVolumeName, objectStoreCAMountPath, and objectStoreCAFile
	// name the §13.2 object-store CA trust bundle projected into the pod's
	// gateway-facing container. A self-managed object store (MinIO) presenting
	// a server certificate signed by a non-public CA requires the adapter to
	// trust that CA before it can complete the TLS handshake during checkpoint
	// upload. The controller resolves a per-agent-namespace ConfigMap holding
	// the CA in the `ca.crt` key; the builder mounts it read-only at the mount
	// path and points the adapter's --objectstore-ca-bundle at the mounted
	// file. A cloud-managed endpoint chains to a public CA and needs no bundle,
	// so the volume, the mount, and the flag are all omitted when no ConfigMap
	// is configured. spec: §13.2.
	objectStoreCAVolumeName = "objectstore-ca"
	objectStoreCAMountPath  = "/etc/lenny/objectstore-ca"
	objectStoreCAFile       = "ca.crt"
	// objectStoreCABundlePath is the in-pod path the adapter reads the CA
	// trust bundle from (the mounted ConfigMap's ca.crt key).
	objectStoreCABundlePath = objectStoreCAMountPath + "/" + objectStoreCAFile
)

// checkpointProbeArgs returns the §4.4 / §13.2 adapter flags for the
// checkpoint upload path. --workspace-size-limit-bytes carries the §4.4 per-pod workspace-size limit so the adapter runs its
// pre-checkpoint size probe; it is omitted when the pool declares no limit,
// leaving the probe disabled. --objectstore-ca-bundle points the adapter at
// the mounted §13.2 object-store CA trust bundle so it can complete the TLS
// handshake to a self-managed object store; it is omitted when no CA
// ConfigMap is configured (a cloud-managed endpoint chaining to a public CA).
// spec: §4.4, §13.2.
func checkpointProbeArgs(in Inputs) []string {
	var args []string
	if in.WorkspaceSizeLimitBytes != nil {
		args = append(args, fmt.Sprintf("--workspace-size-limit-bytes=%d", *in.WorkspaceSizeLimitBytes))
	}
	if in.ObjectStoreCAConfigMap != "" {
		args = append(args, "--objectstore-ca-bundle="+objectStoreCABundlePath)
	}
	return args
}

// injectObjectStoreCAVolume projects the §13.2 per-agent-namespace
// object-store CA ConfigMap read-only into the pod and mounts it on the
// container indices named in mountOn (the pod's gateway-facing container: the
// adapter in the sidecar model, the runtime in the embedded model). The
// adapter reads the CA from the mounted ca.crt file (objectStoreCABundlePath)
// so it trusts a self-managed object store's non-public CA during checkpoint
// upload. The injection is a no-op when no ConfigMap is configured, so a
// cloud-managed endpoint chaining to a public CA carries no volume, mount, or
// flag. spec: §13.2.
func injectObjectStoreCAVolume(in Inputs, pod *corev1.Pod, mountOn []int) {
	if in.ObjectStoreCAConfigMap == "" {
		return
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: objectStoreCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: in.ObjectStoreCAConfigMap},
			},
		},
	})
	mount := corev1.VolumeMount{Name: objectStoreCAVolumeName, MountPath: objectStoreCAMountPath, ReadOnly: true}
	for _, idx := range mountOn {
		if idx >= 0 && idx < len(pod.Spec.Containers) {
			pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, mount)
		}
	}
}
