// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// containerSecurityContext returns the §13.1 per-container security
// context: the given non-root UID, no privilege escalation, a
// read-only root filesystem, all capabilities dropped, and the default
// masked /proc mount.
func containerSecurityContext(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser: ptr.To(uid),
		// spec: §13.1 (User row; Container identity)
		// The primary GID equals the UID. Every agent-pod container sets
		// runAsUser and runAsGroup explicitly at container level so the
		// identity lenny-pod-security reads at admission is the identity
		// that runs; leaving runAsGroup unset lets the image choose the
		// primary GID, which can be 0 or the lenny-cred-readers GID. basePod
		// deliberately sets no pod-level runAsUser or runAsGroup, because a
		// pod-level value does not satisfy the container-level rule.
		RunAsGroup:               ptr.To(uid),
		RunAsNonRoot:             ptr.To(true),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		// spec: §6.4
		// DefaultProcMount keeps the kubelet's standard masked-/proc set
		// (the /proc/kcore, /proc/sys, and similar paths are masked or
		// read-only) rather than the Unmasked variant. Combined with the
		// read-only root filesystem above, /sys is mounted read-only.
		// Setting it explicitly states the §6.4 invariant in the pod spec
		// instead of relying on the container runtime default.
		ProcMount: ptr.To(corev1.DefaultProcMount),
	}
}
