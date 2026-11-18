// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	// saTokenVolumeName, saTokenMountPath, and saTokenFile name the §6.1 projected service-account token: an audience-bound,
	// short-TTL token the agent pod presents to the gateway (§10.3). The
	// mount path is Lenny-namespaced so it does not collide with the
	// kubelet's default kubernetes.io/serviceaccount mount.
	saTokenVolumeName = "lenny-sa-token"
	saTokenMountPath  = "/var/run/secrets/lenny.dev/serviceaccount"
	saTokenFile       = "token"

	// saTokenExpirationSeconds is the §10.3 projected-token TTL
	// (expirationSeconds: 900 / 15 minutes). The kubelet auto-refreshes
	// the token before expiry; the gateway validates the audience claim on
	// every pod→gateway request.
	saTokenExpirationSeconds int64 = 900
)

// injectSATokenVolume adds the §6.1 / §10.3 projected
// service-account token volume to the pod and mounts it read-only into the
// container indices named in mountOn (the container that authenticates to
// the gateway: the adapter in the sidecar model, the runtime in the
// embedded model). The token is audience-bound (Inputs.SATokenAudience,
// the §10.3 deployment-specific audience) with a 900s TTL the kubelet
// auto-refreshes. The injection is a no-op when no audience is configured,
// so the builder never mounts a wrong-audience (cluster-default) token.
func injectSATokenVolume(in Inputs, pod *corev1.Pod, mountOn []int) {
	if in.SATokenAudience == "" {
		return
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: saTokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Audience:          in.SATokenAudience,
						ExpirationSeconds: ptr.To(saTokenExpirationSeconds),
						Path:              saTokenFile,
					},
				}},
			},
		},
	})
	mount := corev1.VolumeMount{Name: saTokenVolumeName, MountPath: saTokenMountPath, ReadOnly: true}
	for _, idx := range mountOn {
		if idx >= 0 && idx < len(pod.Spec.Containers) {
			pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, mount)
		}
	}
}
