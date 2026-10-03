// SPDX-License-Identifier: MIT

package podspec

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// buildEmbedded assembles the §4.7 embedded-model pod: a single
// container whose first-party runtime image links the adapter and
// serves the gRPC contract. There is no separate adapter container, so
// the runtime container both writes and reads the credential and
// manifest volumes.
func buildEmbedded(in Inputs, runtimeClass string) (*corev1.Pod, error) {
	volumes := podVolumes()
	// The embedded runtime is the adapter — one process — so it writes
	// the credential file and the manifest itself; the mounts are
	// read-write.
	runtimeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount},
		{Name: "tmp", MountPath: tmpMount},
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
		// spec: §6.4 — the embedded runtime is the adapter, so it
		// populates /workspace/shared itself and mounts it read-write. The
		// kernel-level EROFS boundary is a sidecar-model property (the agent
		// and adapter are separate containers there); in the embedded model
		// the single process is trusted not to write the shared tree after
		// the warm-time populate, the same tradeoff the credential mount
		// makes.
		{Name: sharedVolumeName, MountPath: sharedMount},
	}

	embeddedArgs := []string{
		fmt.Sprintf("--addr=:%d", adapterPort),
		// spec: §6.4 — every session on every pod owns a per-slot tree
		// under the workspace base: `<base>/slots/{sessionId}/current/` is
		// its cwd and `<base>/slots/{sessionId}/staging/` its upload
		// staging area. The layout is uniform across pool concurrencies,
		// so the builder renders the base the trees nest under and the
		// adapter resolves each session's root from it. The pod carries no
		// pod-global `current` directory.
		"--workspace-base=" + workspaceMount,
		// §4.7: the embedded runtime is the adapter, so it stages
		// uploads the same way the sidecar adapter does.
		"--staging-dir=" + stagingPath,
	}
	embeddedArgs = append(embeddedArgs, sharedAssetsArgs(in)...)
	embeddedArgs = append(embeddedArgs, platformMCPArgs(in)...)
	embeddedArgs = append(embeddedArgs, checkpointProbeArgs(in)...)

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "runtime",
			Image: in.RuntimeImage,
			Args:  embeddedArgs,
			// spec: §4.7, §5.2 — the embedded runtime is the adapter and the
			// pod's gateway-facing process, so it emits ReportSessionScrub and
			// ReportPodScrub keyed on the pod identity it reads from this
			// Downward API POD_NAME env. Omitting it here leaves an embedded
			// recycling pool without pod identity and silently disables the
			// scrub-report chain, the same load-bearing env as the sidecar
			// adapter container.
			Env:             []corev1.EnvVar{podNameEnv()},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    runtimeMounts,
			SecurityContext: containerSecurityContext(in.agentUID()),
			// spec: §4.6.1 — the embedded first-party runtime links the
			// adapter and accepts the same CLI, so its preStop drain runs
			// the same checkpoint-before-termination path.
			Lifecycle: preStopDrainHook(in),
		},
	}
	pod.Spec.Volumes = volumes
	// spec: §6.4 — stamp the resolved §5.2 resource class on the
	// single runtime container before the test-only egress sidecar.
	applyResources(in, pod)
	// spec: §10.3 — the embedded runtime is the pod's gateway-facing
	// process, so the projected token mounts on the runtime container.
	injectSATokenVolume(in, pod, []int{0})
	// spec: §13.2 — the embedded runtime is the adapter and uploads
	// checkpoints, so the CA trust bundle mounts on the runtime container.
	injectObjectStoreCAVolume(in, pod, []int{0})
	injectEgressCaptureSidecar(in, pod, []int{0})
	return pod, nil
}
