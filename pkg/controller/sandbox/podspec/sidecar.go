// SPDX-License-Identifier: MIT

package podspec

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// buildSidecar assembles the §4.7 sidecar-model pod: an adapter
// container and a runtime container bridged over an abstract Unix
// socket. shareProcessNamespace is left unset (false) so the §13.1
// process-isolation boundary holds.
func buildSidecar(in Inputs, runtimeClass string) (*corev1.Pod, error) {
	if in.AdapterImage == "" {
		return nil, errors.New("podspec: adapter image is required for the sidecar deployment model")
	}

	volumes := podVolumes()
	// The adapter writes the credential file and the manifest; the
	// runtime only reads them — the §4.7 manifest emptyDir is mounted
	// read-only into the runtime container.
	adapterMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount},
		{Name: "tmp", MountPath: tmpMount},
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
		// spec: §6.4 — the adapter populates /workspace/shared at
		// warm time, so its mount is read-write. The runtime container's is
		// read-only, which is where the EROFS write boundary is enforced.
		{Name: sharedVolumeName, MountPath: sharedMount},
	}
	runtimeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount, ReadOnly: true},
		{Name: "tmp", MountPath: tmpMount},
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
		// spec: §6.4 — read-only on the runtime container: any write
		// the agent process attempts under /workspace/shared returns EROFS.
		{Name: sharedVolumeName, MountPath: sharedMount, ReadOnly: true},
	}

	adapterArgs := []string{
		fmt.Sprintf("--addr=:%d", adapterPort),
		// spec: §6.4 — every session on every pod owns a per-slot tree
		// under the workspace base: `<base>/slots/{sessionId}/current/` is
		// its cwd and `<base>/slots/{sessionId}/staging/` its upload
		// staging area. The layout is uniform across pool concurrencies,
		// so the builder renders the base the trees nest under and the
		// adapter resolves each session's root from it. The pod carries no
		// pod-global `current` directory.
		"--workspace-base=" + workspaceMount,
		// §4.7: PrepareWorkspace stages uploads here before
		// FinalizeWorkspace promotes them into the session's slot cwd.
		"--staging-dir=" + stagingPath,
		// §4.7/§13: enforce the SO_PEERCRED MCP peer check against
		// the runtime container's runAsUser.
		fmt.Sprintf("--runtime-uid=%d", in.agentUID()),
		// §4.7 sidecar transport: the adapter binds the abstract
		// runtime socket the runtime container dials.
		"--runtime-socket=" + RuntimeSocketName,
	}
	adapterArgs = append(adapterArgs, sharedAssetsArgs(in)...)
	adapterArgs = append(adapterArgs, platformMCPArgs(in)...)
	adapterArgs = append(adapterArgs, nonceOnlyArgs(in)...)
	adapterArgs = append(adapterArgs, checkpointProbeArgs(in)...)

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "adapter",
			Image: in.AdapterImage,
			Args:  adapterArgs,
			// spec: §4.7, §5.2 — the adapter reports the outcome of a cleanup a
			// `Shutdown` performs to reclaim a slot that reached `running`
			// (ReportSessionScrub) and the whole-pod scrub outcome (ReportPodScrub)
			// keyed on the pod identity. It reads that identity from this Downward
			// API POD_NAME env and caches it, since a session Shutdown carries no
			// podId and the recycle Shutdown carries it only inside RecycleScrub.
			Env:             []corev1.EnvVar{podNameEnv()},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    adapterMounts,
			SecurityContext: containerSecurityContext(in.adapterUID()),
			// spec: §4.6.1 "Disruption protection for agent pods" — the
			// preStop hook triggers a checkpoint before termination. It
			// runs the adapter's drain so an in-flight gateway Checkpoint
			// RPC completes within the grace period rather than being cut
			// short by the kubelet SIGTERM/SIGKILL clock.
			Lifecycle: preStopDrainHook(in),
		},
		{
			Name:  "runtime",
			Image: in.RuntimeImage,
			// §4.7 sidecar transport: the runtime discovers the adapter's
			// abstract socket from this variable and dials it instead of
			// reading stdin, which is not attached in a pod container.
			Env: []corev1.EnvVar{
				{Name: RuntimeSocketEnvVar, Value: RuntimeSocketName},
			},
			VolumeMounts:    runtimeMounts,
			SecurityContext: containerSecurityContext(in.agentUID()),
		},
	}
	pod.Spec.Volumes = volumes
	// spec: §6.4 — stamp the resolved §5.2 resource class on both
	// Lenny containers before the test-only egress sidecar is injected.
	applyResources(in, pod)
	// spec: §10.3 — the adapter is the pod's gateway-facing process, so the
	// projected token mounts on the adapter container.
	injectSATokenVolume(in, pod, []int{0})
	// spec: §13.2 — the adapter uploads checkpoints to the object store, so
	// the CA trust bundle mounts on the adapter container.
	injectObjectStoreCAVolume(in, pod, []int{0})
	injectEgressCaptureSidecar(in, pod, []int{1})
	return pod, nil
}
