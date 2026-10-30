// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// The identities a sandboxed probe pod runs its containers at: the chart
// defaults for the adapter and agent UIDs, and the stage UID the pod
// builder gives lenny-supervisor-stage. spec: §13.1 (Container identity;
// Runtime supervisor volume).
const (
	sandboxedAdapterUID = podspec.AdapterUID
	sandboxedAgentUID   = podspec.AgentUID
)

// SupervisorRunPath is where a supervised runtime container runs the
// supervisor from: the read-only mount of the supervisor volume.
// spec: §13.1 (Runtime supervisor volume).
const SupervisorRunPath = "/lenny-supervisor/lenny-supervisor"

// SupervisorHarnessExe is the supervisor's C harness the probe image
// carries, statically linked (cmd/runtimes/supervise-probe/Dockerfile).
const SupervisorHarnessExe = "/usr/local/bin/lenny-supervisor-harness"

// SyncFaultCase is the harness case that installs the supervisor's signal
// handling, faults synchronously, and checks that the kernel delivered
// SIGSEGV through the block and ended the process. spec: §4.7.10 (Runtime
// supervisor, Signals).
const SyncFaultCase = "signals_synchronous_fault_ends_process_through_block"

// SandboxedPodSpec is what a sandboxed probe pod is built from.
type SandboxedPodSpec struct {
	Name             string
	Namespace        string
	RuntimeClassName string
	SupervisorImage  string
	ProbeImage       string
	// PingInterval and PingTimeout are the peer's CH-SUPERVISE ping bounds.
	PingInterval, PingTimeout string
}

// SandboxedProbePod builds a supervised probe pod for a RuntimeClass the
// platform's pools do not select, such as one per gVisor platform. It
// mirrors the pod builder's supervised layout: lenny-supervisor-stage
// copies the supervisor into an emptyDir at the stage UID, and the runtime
// container runs the supervisor from a read-only mount as its first process
// at the agent UID, with the probe as its command. In place of the adapter,
// the adapter container runs the probe's supervise-peer at the adapter UID,
// which accepts the supervisor's connection and pings it through the
// adapter's own CH-SUPERVISE client, and exits non-zero when the link
// fails. The supervisor launches no runtime process, because the peer sends
// no launch. The pod runs in a test namespace outside the lenny-pod-security
// webhook's scope, carries TestLabel, tolerates the dedicated-node isolation
// taint, and uses the RuntimeDefault seccomp profile, which a gVisor handler
// run with --oci-seccomp enforces. spec: §4.7.10 (Runtime supervisor);
// §13.1 (Runtime supervisor volume); §28.5.3 (CH-SUPERVISE).
func SandboxedProbePod(s SandboxedPodSpec) *corev1.Pod {
	pod := sandboxedPodBase(s)
	pod.Spec.Volumes = []corev1.Volume{{
		Name:         podspec.SupervisorVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}}
	pod.Spec.InitContainers = []corev1.Container{{
		Name:            podspec.SupervisorStageContainerName,
		Image:           s.SupervisorImage,
		Args:            []string{"stage", "/stage"},
		VolumeMounts:    []corev1.VolumeMount{{Name: podspec.SupervisorVolumeName, MountPath: "/stage"}},
		SecurityContext: sandboxedContainerContext(podspec.SupervisorStageUID),
	}}
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "adapter",
			Image: s.ProbeImage,
			Command: []string{
				ProbeExe, superviseprobe.SubSupervisePeer,
				"--agent-uid", strconv.FormatInt(sandboxedAgentUID, 10),
				"--ping-interval", s.PingInterval, "--ping-timeout", s.PingTimeout,
			},
			SecurityContext: sandboxedContainerContext(sandboxedAdapterUID),
		},
		{
			Name:  "runtime",
			Image: s.ProbeImage,
			Command: []string{
				SupervisorRunPath, "run", "--socket", podspec.SuperviseSocketName,
				"--adapter-uid", strconv.FormatInt(sandboxedAdapterUID, 10), "--", ProbeExe,
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name: podspec.SupervisorVolumeName, MountPath: "/lenny-supervisor", ReadOnly: true,
			}},
			SecurityContext: sandboxedContainerContext(sandboxedAgentUID),
		},
	}
	return pod
}

// SyncFaultPod builds a one-container pod that runs the harness's
// synchronous-fault case under s.RuntimeClassName and ends Succeeded when
// the case passes.
func SyncFaultPod(s SandboxedPodSpec) *corev1.Pod {
	pod := sandboxedPodBase(s)
	pod.Spec.Containers = []corev1.Container{{
		Name:            "sync-fault",
		Image:           s.ProbeImage,
		Command:         []string{SupervisorHarnessExe, "--run", SyncFaultCase},
		SecurityContext: sandboxedContainerContext(sandboxedAgentUID),
	}}
	return pod
}

// sandboxedPodBase is the metadata and pod-level settings both sandboxed
// pods share.
func sandboxedPodBase(s SandboxedPodSpec) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels:    map[string]string{TestLabel: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:             ptr.To(s.RuntimeClassName),
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: ptr.To(false),
			EnableServiceLinks:           ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Tolerations: []corev1.Toleration{{Key: "lenny.dev/isolation", Operator: corev1.TolerationOpExists}},
		},
	}
}

// sandboxedContainerContext is the §13.1 container baseline at uid.
func sandboxedContainerContext(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                ptr.To(uid),
		RunAsGroup:               ptr.To(uid),
		RunAsNonRoot:             ptr.To(true),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}
