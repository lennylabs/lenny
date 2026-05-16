// SPDX-License-Identifier: MIT

// Package podspec builds the Kubernetes Pod that backs a Sandbox. The
// §4.7 WarmPoolController's Sandbox-to-Pod reconciler calls Build to
// translate a resolved Sandbox, SandboxTemplate, and Runtime into a
// corev1.Pod.
//
// The pod follows the §4.7 default sidecar model: an adapter container
// running lenny-adapter and a runtime container running the runtime
// image, sharing the workspace and credential volumes. The §13.1 pod
// security posture is applied: non-root distinct UIDs, all capabilities
// dropped, a read-only root filesystem, the RuntimeDefault seccomp
// profile, and the lenny-cred-readers fsGroup that delivers the
// credential file across the adapter and agent UIDs. The §13.1
// host-sharing flags (shareProcessNamespace, hostPID, hostNetwork,
// hostIPC) are left unset, which is forbidden-by-omission.
package podspec

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	// AdapterUID and AgentUID are the non-root UIDs the adapter and
	// runtime containers run as. §13.1 mandates distinct non-root
	// identities but leaves the specific numbers to the implementation.
	AdapterUID int64 = 65532
	AgentUID   int64 = 65533
	// CredReadersGID is the lenny-cred-readers group — the §13.1
	// credential-file read boundary shared by the adapter and runtime
	// containers. The pod fsGroup is set to it so the kubelet
	// group-owns the credential tmpfs.
	CredReadersGID int64 = 65534

	// CredVolumeName is the name of the pod volume carrying the §4.7
	// credential tmpfs mounted at credentialMount.
	CredVolumeName = "credentials"

	// terminationGraceSeconds is the default pod termination grace
	// period.
	terminationGraceSeconds int64 = 30

	// adapterPort is the adapter's gRPC listener port.
	adapterPort int32 = 8443

	// workspaceMount, credentialMount, and tmpMount are the in-pod
	// mount paths (§6.1, §13.1).
	workspaceMount  = "/workspace"
	credentialMount = "/run/lenny"
	tmpMount        = "/tmp"
)

// Inputs is the resolved configuration Build needs. The reconciler
// resolves the Sandbox, its SandboxTemplate, and its Runtime into
// these fields.
type Inputs struct {
	// Name and Namespace identify the Pod; they match the Sandbox.
	Name      string
	Namespace string
	// Labels are stamped onto the Pod, typically the pool and managed
	// labels the WarmPoolController applies to a Sandbox.
	Labels map[string]string
	// RuntimeImage is the OCI image of the runtime, from Runtime.spec.image.
	RuntimeImage string
	// AdapterImage is the OCI image of the lenny-adapter sidecar,
	// supplied as controller configuration.
	AdapterImage string
	// IsolationProfile is the §5.3 profile (standard, sandboxed, or
	// microvm) that selects the RuntimeClass.
	IsolationProfile string
}

// Build assembles the agent Pod for one Sandbox. It returns an error
// when a required image is missing or the isolation profile is not
// recognized.
func Build(in Inputs) (*corev1.Pod, error) {
	if in.RuntimeImage == "" {
		return nil, errors.New("podspec: runtime image is required")
	}
	if in.AdapterImage == "" {
		return nil, errors.New("podspec: adapter image is required")
	}
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(in.IsolationProfile))
	if !ok {
		return nil, fmt.Errorf("podspec: unknown isolation profile %q", in.IsolationProfile)
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: CredVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
	}
	// The adapter writes the credential file; the runtime only reads it.
	adapterMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount},
		{Name: "tmp", MountPath: tmpMount},
	}
	runtimeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount, ReadOnly: true},
		{Name: "tmp", MountPath: tmpMount},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: in.Namespace,
			Labels:    in.Labels,
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:              &runtimeClass,
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr.To(terminationGraceSeconds),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				FSGroup:        ptr.To(CredReadersGID),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:            "adapter",
					Image:           in.AdapterImage,
					Args:            []string{"--addr=:8443", "--workspace-root=" + workspaceMount + "/current"},
					Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
					VolumeMounts:    adapterMounts,
					SecurityContext: containerSecurityContext(AdapterUID),
				},
				{
					Name:            "runtime",
					Image:           in.RuntimeImage,
					VolumeMounts:    runtimeMounts,
					SecurityContext: containerSecurityContext(AgentUID),
				},
			},
			Volumes: volumes,
		},
	}
	return pod, nil
}

// containerSecurityContext returns the §13.1 per-container security
// context: the given non-root UID, no privilege escalation, a
// read-only root filesystem, and all capabilities dropped.
func containerSecurityContext(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                ptr.To(uid),
		RunAsNonRoot:             ptr.To(true),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}
