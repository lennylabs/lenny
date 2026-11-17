// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// CredVolumeName is the name of the pod volume carrying the §4.7
	// credential tmpfs mounted at credentialMount.
	CredVolumeName = "credentials"

	// workspaceMount, credentialMount, and tmpMount are the in-pod
	// mount paths (§6.1, §13.1).
	workspaceMount  = "/workspace"
	credentialMount = "/run/lenny"
	tmpMount        = "/tmp"

	// sessionsMount and artifactsMount are the §6.4 in-pod
	// paths for session files and artifacts. /sessions holds conversation
	// logs and runtime state; /artifacts holds logs, outputs, and
	// checkpoints. §13.1 lists both among the agent's writable
	// paths, so without these volumes a runtime write lands on the
	// read-only root filesystem and fails with EROFS.
	sessionsMount  = "/sessions"
	artifactsMount = "/artifacts"
	// sessionsVolumeName and artifactsVolumeName back the two mounts.
	sessionsVolumeName  = "sessions"
	artifactsVolumeName = "artifacts"

	// sharedMount is the §6.4 /workspace/shared path: a separate
	// emptyDir holding read-only assets shared across a concurrent-workspace
	// pod's slots. It is mounted on every pod (empty when the Runtime
	// declares no sharedAssets) so the runtime cannot use the path as
	// writable scratch space. The runtime container's mount is read-only, so
	// an agent write returns EROFS.
	sharedMount = workspaceMount + "/shared"
	// sharedVolumeName backs sharedMount. It is a separate volume from
	// `workspace` so the read-only volumeMount enforces immutability at the
	// kernel level rather than relying on file modes inside the shared
	// workspace emptyDir.
	sharedVolumeName = "shared"

	// tmpfsSizeLimit is the §6.4 recommended cap for the
	// memory-backed /sessions and /tmp tmpfs volumes (256Mi each). The cap
	// gives a predictable OOM boundary instead of silent memory pressure:
	// tmpfs usage charges against the pod memory limit, so an uncapped
	// tmpfs lets a runaway runtime grow until the kernel OOM-kills a
	// container.
	tmpfsSizeLimit = "256Mi"

	// dshmMount is the in-pod /dev/shm path. spec: §6.4
	// "/dev/shm is limited to 64MB." A memory-backed emptyDir with an
	// explicit SizeLimit gives the cap a Lenny-controlled value rather
	// than relying on the OCI runtime default.
	dshmMount = "/dev/shm"
	// dshmVolumeName is the pod volume backing dshmMount.
	dshmVolumeName = "dshm"
	// dshmSizeLimit is the §6.4 64MB /dev/shm ceiling.
	dshmSizeLimit = "64Mi"

	// stagingPath is the §4.7 PrepareWorkspace staging directory. It sits
	// under the shared workspace emptyDir so the adapter can promote
	// staged content into the session's slot cwd without crossing a
	// volume boundary.
	stagingPath = workspaceMount + "/.staging"
)

// podVolumes returns the §6.1 / §6.4 / §13.1 pod volumes: the
// disk-backed workspace and artifacts emptyDirs, the memory-backed
// credential, tmp, and sessions tmpfs volumes, and the §6.4 size-capped
// /dev/shm volume. spec: §6.4
// (data-at-rest medium and tmpfs size caps).
func podVolumes() []corev1.Volume {
	dshmLimit := resource.MustParse(dshmSizeLimit)
	tmpfsLimit := resource.MustParse(tmpfsSizeLimit)
	return []corev1.Volume{
		// spec: §6.4 — /workspace and /artifacts are disk-backed
		// emptyDirs (no Memory medium): logs, outputs, and checkpoints can
		// exceed the pod memory budget.
		{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: artifactsVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		// spec: §6.4 — /workspace/shared is a separate emptyDir so
		// the read-only volumeMount on the runtime container enforces
		// immutability at the kernel level. It is disk-backed: shared assets
		// can exceed the pod memory budget, the same as the workspace volume.
		{Name: sharedVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: CredVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
		// spec: §6.4 — /tmp and /sessions are
		// memory-backed (tmpfs) so their contents are guaranteed gone when
		// the pod terminates, each capped at the recommended 256Mi to bound
		// memory pressure.
		{Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &tmpfsLimit},
		}},
		{Name: sessionsVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &tmpfsLimit},
		}},
		// spec: §6.4 A
		// memory-backed emptyDir with an explicit SizeLimit enforces the
		// cap under Lenny's control rather than the OCI runtime default,
		// which varies across container runtimes.
		{Name: dshmVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: &dshmLimit,
			},
		}},
	}
}
