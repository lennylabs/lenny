// SPDX-License-Identifier: MIT

// Package podspec builds the Kubernetes Pod that backs a Sandbox. The
// §4.7 WarmPoolController's Sandbox-to-Pod reconciler calls Build to
// translate a resolved Sandbox, SandboxTemplate, and Runtime into a
// corev1.Pod.
//
// §4.7 defines two agent-pod deployment models, selected by the
// Runtime's deploymentModel field:
//
//   - sidecar (the default): an adapter container running lenny-adapter
//     and a runtime container running the runtime image. The adapter
//     binds an abstract Unix socket; the runtime container dials it,
//     discovering the socket name from the LENNY_ADAPTER_SOCKET
//     environment variable. The two containers share the pod network
//     namespace, so the abstract socket carries the §15.4.1 JSONL
//     protocol with no shared filesystem path. The manifest emptyDir
//     (/run/lenny) is mounted read-only into the runtime container and
//     read-write into the adapter container; the workspace emptyDir
//     (/workspace) is separate.
//   - embedded: a single container running a first-party runtime image
//     that links the adapter as a library and serves the gRPC contract
//     to the gateway itself. There is no separate adapter container.
//
// The §13.1 pod security posture is applied in both models: non-root
// distinct UIDs, all capabilities dropped, a read-only root filesystem,
// the RuntimeDefault seccomp profile, and the lenny-cred-readers fsGroup
// that delivers the credential file across the adapter and agent UIDs.
// The §13.1 host-sharing flags (shareProcessNamespace, hostPID,
// hostNetwork, hostIPC) are left unset, which is forbidden-by-omission.
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

	// adapterPort is the adapter's gRPC listener port (§13.2: the
	// gateway reaches the adapter on TCP 50051).
	adapterPort int32 = 50051

	// workspaceMount, credentialMount, and tmpMount are the in-pod
	// mount paths (§6.1, §13.1).
	workspaceMount  = "/workspace"
	credentialMount = "/run/lenny"
	tmpMount        = "/tmp"

	// RuntimeSocketName is the §4.7 sidecar-model abstract Unix socket
	// the adapter binds and the runtime container dials. A Linux
	// abstract address begins with "@" and occupies the kernel
	// abstract namespace — no filesystem path, reachable across the
	// pod's containers because they share the network namespace.
	RuntimeSocketName = "@lenny-runtime"

	// RuntimeSocketEnvVar is the environment variable the runtime
	// container reads to discover the adapter's runtime socket. It
	// matches runtimekit.SocketEnvVar.
	RuntimeSocketEnvVar = "LENNY_ADAPTER_SOCKET"
)

// DeploymentModel is the §4.7 agent-pod deployment model. It mirrors
// the Runtime CRD's deploymentModel enum.
type DeploymentModel string

const (
	// DeploymentSidecar is the §4.7 default: a separate adapter sidecar
	// container bridging the runtime over an abstract Unix socket.
	DeploymentSidecar DeploymentModel = "sidecar"
	// DeploymentEmbedded is the §4.7 alternative: a single container
	// whose first-party runtime image links the adapter and serves the
	// gRPC contract directly.
	DeploymentEmbedded DeploymentModel = "embedded"
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
	// supplied as controller configuration. It is required for the
	// sidecar model and unused for the embedded model.
	AdapterImage string
	// IsolationProfile is the §5.3 profile (standard, sandboxed, or
	// microvm) that selects the RuntimeClass.
	IsolationProfile string
	// DeploymentModel is the §4.7 deployment model, from
	// Runtime.spec.deploymentModel. An empty value defaults to the
	// sidecar model.
	DeploymentModel string
}

// Build assembles the agent Pod for one Sandbox. It dispatches on the
// §4.7 deployment model: the sidecar model produces a two-container pod
// (adapter plus runtime) and the embedded model produces a
// single-container pod. It returns an error when a required image is
// missing, the isolation profile is not recognized, or the deployment
// model is unknown.
func Build(in Inputs) (*corev1.Pod, error) {
	if in.RuntimeImage == "" {
		return nil, errors.New("podspec: runtime image is required")
	}
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(in.IsolationProfile))
	if !ok {
		return nil, fmt.Errorf("podspec: unknown isolation profile %q", in.IsolationProfile)
	}

	model := DeploymentModel(in.DeploymentModel)
	if model == "" {
		model = DeploymentSidecar
	}

	switch model {
	case DeploymentSidecar:
		return buildSidecar(in, runtimeClass)
	case DeploymentEmbedded:
		return buildEmbedded(in, runtimeClass)
	default:
		return nil, fmt.Errorf("podspec: unknown deployment model %q", in.DeploymentModel)
	}
}

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
	}
	runtimeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount, ReadOnly: true},
		{Name: "tmp", MountPath: tmpMount},
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "adapter",
			Image: in.AdapterImage,
			Args: []string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				"--workspace-root=" + workspaceMount + "/current",
				// §4.7 sidecar transport: the adapter binds the abstract
				// runtime socket the runtime container dials.
				"--runtime-socket=" + RuntimeSocketName,
			},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    adapterMounts,
			SecurityContext: containerSecurityContext(AdapterUID),
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
			SecurityContext: containerSecurityContext(AgentUID),
		},
	}
	pod.Spec.Volumes = volumes
	return pod, nil
}

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
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "runtime",
			Image: in.RuntimeImage,
			Args: []string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				"--workspace-root=" + workspaceMount + "/current",
			},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    runtimeMounts,
			SecurityContext: containerSecurityContext(AgentUID),
		},
	}
	pod.Spec.Volumes = volumes
	return pod, nil
}

// podVolumes returns the §6.1 / §13.1 pod volumes: the workspace
// emptyDir, the memory-backed credential tmpfs, and the memory-backed
// tmp volume.
func podVolumes() []corev1.Volume {
	return []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: CredVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
	}
}

// basePod returns the Pod skeleton common to both deployment models:
// the §13.1 pod security context and the §5.3 RuntimeClass, with the
// container list and volume list left for the caller to fill.
func basePod(in Inputs, runtimeClass string) *corev1.Pod {
	return &corev1.Pod{
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
		},
	}
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
