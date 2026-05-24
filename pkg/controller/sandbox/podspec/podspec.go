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

	// defaultTerminationGraceSeconds is the default pod termination grace
	// period. spec: §4.6.1 "Disruption protection for agent pods" — the
	// pod's terminationGracePeriodSeconds is set high enough (default:
	// 120s) to give the preStop checkpoint time to complete and be
	// persisted to object storage. A 30s default would SIGKILL the
	// adapter mid-checkpoint on a node drain.
	defaultTerminationGraceSeconds int64 = 120

	// preStopDrainMarginSeconds is the slice of the grace period the
	// preStop drain leaves for the kubelet to reap the container after
	// the adapter exits. The preStop command's own timeout is the grace
	// period minus this margin so the kubelet never SIGKILLs the adapter
	// while its preStop drain is still in flight.
	preStopDrainMarginSeconds int64 = 10

	// adapterPort is the adapter's gRPC listener port (§13.2: the
	// gateway reaches the adapter on TCP 50051).
	adapterPort int32 = 50051

	// workspaceMount, credentialMount, and tmpMount are the in-pod
	// mount paths (§6.1, §13.1).
	workspaceMount  = "/workspace"
	credentialMount = "/run/lenny"
	tmpMount        = "/tmp"

	// stagingPath is the §4.7 PrepareWorkspace staging directory. It sits
	// under the shared workspace emptyDir so the adapter can promote
	// staged content into /workspace/current without crossing a volume
	// boundary.
	stagingPath = workspaceMount + "/.staging"

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
	// EgressCapture is the §12.9.8 tier-9 egress-capture sidecar
	// configuration. Non-nil injects an additional container running
	// lenny-egress-capture into the pod, plus a shared emptyDir mounted
	// on the runtime container so the §12.9.8 leakage probe can read
	// the JSONL capture file. The sidecar is TEST-ONLY: the
	// lenny-pod-security admission webhook rejects pods carrying it in
	// production.
	EgressCapture *EgressCapture

	// MaxTerminationGraceSeconds is the §4.6.1 / §5.2
	// SandboxTemplate.spec.maxTerminationGracePeriodSeconds hard
	// ceiling. When set and below the default, it clamps the pod's
	// terminationGracePeriodSeconds so a pod never advertises a grace
	// period the deployer has declared exceeds the cluster's node-drain
	// timeout. A nil value leaves the default in force.
	MaxTerminationGraceSeconds *int64
}

// EgressCapture configures the §12.9.8 egress-capture sidecar an
// agent pod runs alongside its runtime. The sidecar listens on
// ListenPort, forwards every accepted TCP connection to Upstream,
// and writes one JSONL row per connection to a path under the shared
// capture volume; the §12.9.8 leakage probe (tier-9) reads the file
// via `kubectl exec` to assert no credential material appears in
// egress.
type EgressCapture struct {
	// Image is the OCI image of the egress-capture container.
	// Production rejects this image via the lenny-pod-security
	// admission webhook.
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
				// §4.7: PrepareWorkspace stages uploads here before
				// FinalizeWorkspace promotes them into /workspace/current.
				"--staging-dir=" + stagingPath,
				// §4.7/§13: enforce the SO_PEERCRED MCP peer check against
				// the runtime container's runAsUser.
				fmt.Sprintf("--runtime-uid=%d", AgentUID),
				// §4.7 sidecar transport: the adapter binds the abstract
				// runtime socket the runtime container dials.
				"--runtime-socket=" + RuntimeSocketName,
			},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    adapterMounts,
			SecurityContext: containerSecurityContext(AdapterUID),
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
			SecurityContext: containerSecurityContext(AgentUID),
		},
	}
	pod.Spec.Volumes = volumes
	injectEgressCaptureSidecar(in, pod, []int{1})
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
				// §4.7: the embedded runtime is the adapter, so it stages
				// uploads the same way the sidecar adapter does.
				"--staging-dir=" + stagingPath,
			},
			Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: adapterPort}},
			VolumeMounts:    runtimeMounts,
			SecurityContext: containerSecurityContext(AgentUID),
			// spec: §4.6.1 — the embedded first-party runtime links the
			// adapter and accepts the same CLI, so its preStop drain runs
			// the same checkpoint-before-termination path.
			Lifecycle: preStopDrainHook(in),
		},
	}
	pod.Spec.Volumes = volumes
	injectEgressCaptureSidecar(in, pod, []int{0})
	return pod, nil
}

const (
	// EgressCaptureContainerName is the name of the §12.9.8 egress
	// capture sidecar container injected into agent pods that opt in.
	EgressCaptureContainerName = "egress-capture"
	// EgressCaptureVolumeName is the shared emptyDir between the
	// sidecar (writer) and the runtime container (reader) that holds
	// the JSONL capture file.
	EgressCaptureVolumeName = "egress-capture"
	// EgressCaptureMountPath is the in-pod path the capture volume is
	// mounted on in both the sidecar and the runtime containers. The
	// JSONL file lives at egress.jsonl within it by default.
	EgressCaptureMountPath = "/run/lenny-capture"
	// defaultEgressCaptureListenPort is the TCP port the sidecar
	// listens on by default. The runtime container dials it instead
	// of the real upstream.
	defaultEgressCaptureListenPort int32 = 8443
)

// defaultEgressCapturePath is the default in-pod path for the
// JSONL capture file. The §12.9.8 leakage probe reads it via
// `kubectl exec` against the runtime container.
var defaultEgressCapturePath = EgressCaptureMountPath + "/egress.jsonl"

// injectEgressCaptureSidecar appends the §12.9.8 egress-capture
// container to pod.Spec.Containers, mounts the capture volume on
// each container index named in mountOn (typically the runtime
// container so the §12.9.8 probe can read it), and appends the
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
		VolumeMounts:    []corev1.VolumeMount{captureMount},
		SecurityContext: containerSecurityContext(AgentUID),
	})
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
			TerminationGracePeriodSeconds: ptr.To(terminationGrace(in)),
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

// terminationGrace returns the pod's terminationGracePeriodSeconds. It
// is the §4.6.1 default (120s) unless the SandboxTemplate declares a
// lower maxTerminationGracePeriodSeconds ceiling, in which case the
// grace period is clamped down to that ceiling. spec: §4.6.1
// "Disruption protection for agent pods".
func terminationGrace(in Inputs) int64 {
	grace := defaultTerminationGraceSeconds
	if in.MaxTerminationGraceSeconds != nil && *in.MaxTerminationGraceSeconds > 0 &&
		*in.MaxTerminationGraceSeconds < grace {
		grace = *in.MaxTerminationGraceSeconds
	}
	return grace
}

// preStopDrainHook returns the §4.6.1 preStop lifecycle hook for the
// adapter container. The hook invokes the adapter binary's `prestop`
// drain subcommand, which signals the running adapter to drain and
// blocks until it exits or the bounded timeout elapses. Front-loading
// the drain into the preStop window keeps an in-flight gateway
// Checkpoint RPC from being SIGKILLed at the grace deadline. The
// command timeout is the grace period minus a reaping margin so the
// kubelet never cuts the drain short. spec: §4.6.1.
func preStopDrainHook(in Inputs) *corev1.Lifecycle {
	timeout := terminationGrace(in) - preStopDrainMarginSeconds
	if timeout < 1 {
		timeout = 1
	}
	return &corev1.Lifecycle{
		PreStop: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{
					"lenny-adapter", "prestop",
					fmt.Sprintf("--timeout=%ds", timeout),
				},
			},
		},
	}
}
