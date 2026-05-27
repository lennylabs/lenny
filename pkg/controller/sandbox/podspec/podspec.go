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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/admission/t4_node_isolation"
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

	// sessionsMount and artifactsMount are the §6.4 lines 380-381 in-pod
	// paths for session files and artifacts. /sessions holds conversation
	// logs and runtime state; /artifacts holds logs, outputs, and
	// checkpoints. §13.1 line 10 lists both among the agent's writable
	// paths, so without these volumes a runtime write lands on the
	// read-only root filesystem and fails with EROFS.
	sessionsMount  = "/sessions"
	artifactsMount = "/artifacts"
	// sessionsVolumeName and artifactsVolumeName back the two mounts.
	sessionsVolumeName  = "sessions"
	artifactsVolumeName = "artifacts"

	// tmpfsSizeLimit is the §6.4 line 413 recommended cap for the
	// memory-backed /sessions and /tmp tmpfs volumes (256Mi each). The cap
	// gives a predictable OOM boundary instead of silent memory pressure:
	// tmpfs usage charges against the pod memory limit, so an uncapped
	// tmpfs lets a runaway runtime grow until the kernel OOM-kills a
	// container.
	tmpfsSizeLimit = "256Mi"

	// dshmMount is the in-pod /dev/shm path. spec: §6.4 line 420
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

	// ReadinessGateSandboxReady is the §6.1 line 18 pod readiness gate
	// ("Marked 'idle and claimable' via readiness gate"). The pod spec
	// declares the gate so the kubelet holds Pod.Ready False — and the pod
	// un-claimable — until the WarmPoolController asserts the pod is warm.
	// Container readiness alone does not make a pod claimable; the
	// controller flips this gate to True (see the Sandbox-to-Pod
	// reconciler) once it has observed the containers ready, which is the
	// Lenny-controlled claimability handoff to the gateway.
	ReadinessGateSandboxReady corev1.PodConditionType = "lenny.dev/sandbox-ready"

	// saTokenVolumeName, saTokenMountPath, and saTokenFile name the §6.1
	// line 14 projected service-account token: an audience-bound,
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

	// TerminationGraceSeconds is the §5.2
	// SandboxTemplate.spec.terminationGracePeriodSeconds deployer
	// override. spec: §5.2 line 516 — for concurrent-workspace pools the
	// deployer sets this to cover the per-slot checkpoint budget
	// (`maxConcurrent × max_tiered_checkpoint_cap +
	// checkpointBarrierAckTimeoutSeconds + 30`); §6.4 line 67 requires it
	// to be at least `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s`. When set, it
	// replaces the §4.6.1 120s default as the base grace period; the
	// MaxTerminationGraceSeconds ceiling still clamps it down. A nil
	// value leaves the default in force.
	TerminationGraceSeconds *int64

	// TopologySpreadConstraints are the §5.2 lines 631-636 spread
	// constraints resolved for the pool (the PoolScalingController's zone
	// and node defaults, or the deployer's per-pool override), carried
	// down through Sandbox.spec. The builder stamps them onto the pod so
	// the scheduler distributes the pool's pods across zones and nodes.
	TopologySpreadConstraints []corev1.TopologySpreadConstraint

	// SATokenAudience is the §10.3 deployment-specific audience
	// (global.saTokenAudience, formatted as lenny-gateway-<cluster-name>)
	// for the §6.1 line 14 projected service-account token. When non-empty,
	// the builder mounts an audience-bound, 900s-TTL projected token the
	// agent pod presents to the gateway and disables the kubelet's default
	// (cluster-audience) token automount. An empty value (test or
	// unconfigured) leaves the pod without the projected token rather than
	// mounting a wrong-audience one.
	SATokenAudience string

	// ServiceAccountName is the agent pod's ServiceAccount. spec: §10.3 —
	// the SA bound to agent pods has zero RBAC bindings (no Kubernetes API
	// access); the projected token is one defense-in-depth layer alongside
	// mTLS and NetworkPolicy. An empty value uses the namespace default SA
	// (which carries no RBAC bindings in agent namespaces).
	ServiceAccountName string

	// WorkspaceTier is the §12.9 / §5.2 data-classification tier resolved from
	// the Sandbox's Runtime (`Runtime.spec.workspaceTier`). When equal to
	// `T4`, the pod builder stamps the `lenny.dev/workspace-tier: t4` label
	// the lenny-t4-node-isolation admission webhook keys on, adds the T4
	// `nodeSelector`, and adds the T4 NoSchedule toleration so the pod can
	// land on a §6.4 dedicated T4 node pool and is rejected from any other
	// node. Any other value (including the empty default `T3`) leaves the
	// pod with no T4 injection.
	WorkspaceTier string
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
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
	}
	runtimeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: workspaceMount},
		{Name: CredVolumeName, MountPath: credentialMount, ReadOnly: true},
		{Name: "tmp", MountPath: tmpMount},
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "adapter",
			Image: in.AdapterImage,
			Args: []string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				// spec: §6.4 line 407 — session-mode and task-mode pods use the
				// single `/workspace/current` cwd. The concurrent-workspace
				// per-slot tree (`/workspace/slots/{slotId}/current/`) is unbuilt
				// in v1 (tracked under F-6.4.2); when it lands, the builder will
				// select the workspace root from the resolved Runtime layout
				// instead of hard-coding it here.
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
	// spec: §10.3 — the adapter is the pod's gateway-facing process, so the
	// projected token mounts on the adapter container.
	injectSATokenVolume(in, pod, []int{0})
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
		{Name: sessionsVolumeName, MountPath: sessionsMount},
		{Name: artifactsVolumeName, MountPath: artifactsMount},
		{Name: dshmVolumeName, MountPath: dshmMount},
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "runtime",
			Image: in.RuntimeImage,
			Args: []string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				// spec: §6.4 line 407 — session-mode and task-mode pods use the
				// single `/workspace/current` cwd. The concurrent-workspace
				// per-slot tree (`/workspace/slots/{slotId}/current/`) is unbuilt
				// in v1 (tracked under F-6.4.2); when it lands, the builder will
				// select the workspace root from the resolved Runtime layout
				// instead of hard-coding it here.
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
	// spec: §10.3 — the embedded runtime is the pod's gateway-facing
	// process, so the projected token mounts on the runtime container.
	injectSATokenVolume(in, pod, []int{0})
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
	//
	// spec: §6.4 / §13.1 — distinct from the credentialMount (/run/lenny):
	// `/run/lenny-capture` is a sibling path and not a subdirectory of
	// `/run/lenny`, so the credential tmpfs and the capture emptyDir cannot
	// collide. Any future §6.4 in-pod path under the `/run/lenny-*` prefix
	// must remain a sibling — never a subpath of an existing mount — so the
	// mounts stay independent.
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
		VolumeMounts:    []corev1.VolumeMount{captureMount, {Name: dshmVolumeName, MountPath: dshmMount}},
		SecurityContext: containerSecurityContext(AgentUID),
	})
}

// podVolumes returns the §6.1 / §6.4 / §13.1 pod volumes: the
// disk-backed workspace and artifacts emptyDirs, the memory-backed
// credential, tmp, and sessions tmpfs volumes, and the §6.4 size-capped
// /dev/shm volume. spec: §6.4 lines 376-383 (filesystem layout), 411-414
// (data-at-rest medium and tmpfs size caps).
func podVolumes() []corev1.Volume {
	dshmLimit := resource.MustParse(dshmSizeLimit)
	tmpfsLimit := resource.MustParse(tmpfsSizeLimit)
	return []corev1.Volume{
		// spec: §6.4 line 414 — /workspace and /artifacts are disk-backed
		// emptyDirs (no Memory medium): logs, outputs, and checkpoints can
		// exceed the pod memory budget.
		{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: artifactsVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: CredVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		}},
		// spec: §6.4 lines 413, 380, 382 — /tmp and /sessions are
		// memory-backed (tmpfs) so their contents are guaranteed gone when
		// the pod terminates, each capped at the recommended 256Mi to bound
		// memory pressure.
		{Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &tmpfsLimit},
		}},
		{Name: sessionsVolumeName, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &tmpfsLimit},
		}},
		// spec: §6.4 line 420 "/dev/shm is limited to 64MB." A
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

// basePod returns the Pod skeleton common to both deployment models:
// the §13.1 pod security context and the §5.3 RuntimeClass, with the
// container list and volume list left for the caller to fill.
func basePod(in Inputs, runtimeClass string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: in.Namespace,
			Labels:    in.Labels,
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:              &runtimeClass,
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr.To(terminationGrace(in)),
			// spec: §5.2 lines 631-636 — stamp the resolved topology spread
			// constraints so the scheduler distributes the pool's pods.
			TopologySpreadConstraints: in.TopologySpreadConstraints,
			// spec: §6.1 line 18 — the readiness gate lets the
			// WarmPoolController gate claimability: Pod.Ready stays False
			// (and the warming → idle transition, which keys off Pod.Ready,
			// does not fire) until the controller flips this gate to True.
			ReadinessGates: []corev1.PodReadinessGate{{ConditionType: ReadinessGateSandboxReady}},
			// spec: §10.3 — the agent pod presents an audience-bound
			// projected token, not the kubelet's default cluster-audience
			// one; disable the automount so only the §6.1 line 14 projected
			// token (injected below when an audience is configured) is
			// present.
			AutomountServiceAccountToken: ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				FSGroup:        ptr.To(CredReadersGID),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
		},
	}
	// spec: §10.3 — the agent pod's ServiceAccount has zero RBAC bindings;
	// an empty name uses the namespace default SA (no bindings in agent
	// namespaces).
	if in.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = in.ServiceAccountName
	}
	// spec: §6.4 lines 416-419 — when the resolved Runtime is at
	// `workspaceTier: T4`, the sandbox reconciler stamps the
	// `lenny.dev/workspace-tier: t4` pod label that the
	// lenny-t4-node-isolation admission webhook keys on, pins the pod to
	// the T4 node pool via nodeSelector, and tolerates the T4 NoSchedule
	// taint. With all three present, the webhook admits the pod onto a
	// dedicated T4 node; without them, the webhook (failurePolicy: Fail)
	// rejects the pod with the §6.4 STR-003 message.
	//
	// The Runtime CRD's `workspaceTier` enum uses the uppercase `T4` form
	// (§12.9 line 1025) — the lowercase `t4` is the pod label / node label
	// value, not the tier-name comparison key.
	if in.WorkspaceTier == WorkspaceTierT4 {
		applyT4NodeIsolation(pod)
	}
	return pod
}

// WorkspaceTierT4 is the §12.9 / §5.2 Restricted-tier value the Runtime
// CRD's `workspaceTier` enum carries. The pod builder injects the §6.4
// dedicated-node label/selector/toleration whenever Inputs.WorkspaceTier
// equals this constant.
const WorkspaceTierT4 = "T4"

// applyT4NodeIsolation stamps the §6.4 T4 dedicated-node selector,
// toleration, and pod label onto pod. spec: §6.4 lines 416-419.
//
// The injection is idempotent and additive: it preserves any
// deployer-supplied nodeSelector entries (so a pool that pins a more
// specific node label still applies its own constraints), only adding
// the T4 label/value entry the webhook predicate matches on. The
// toleration is appended only when an equivalent entry is not already
// present so re-reconciliations do not accumulate duplicates.
func applyT4NodeIsolation(pod *corev1.Pod) {
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[t4_node_isolation.WorkspaceTierLabel] = t4_node_isolation.WorkspaceTierT4
	if pod.Spec.NodeSelector == nil {
		pod.Spec.NodeSelector = map[string]string{}
	}
	pod.Spec.NodeSelector[t4_node_isolation.NodeLabelKey] = t4_node_isolation.NodeLabelValue
	want := corev1.Toleration{
		Key:      t4_node_isolation.NodeTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    t4_node_isolation.NodeTaintValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	for _, tol := range pod.Spec.Tolerations {
		if tol.Key == want.Key && tol.Value == want.Value && tol.Effect == want.Effect {
			return
		}
	}
	pod.Spec.Tolerations = append(pod.Spec.Tolerations, want)
}

// injectSATokenVolume adds the §6.1 line 14 / §10.3 projected
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

// containerSecurityContext returns the §13.1 per-container security
// context: the given non-root UID, no privilege escalation, a
// read-only root filesystem, all capabilities dropped, and the default
// masked /proc mount.
func containerSecurityContext(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                ptr.To(uid),
		RunAsNonRoot:             ptr.To(true),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		// spec: §6.4 line 420 "procfs and sysfs are masked/read-only."
		// DefaultProcMount keeps the kubelet's standard masked-/proc set
		// (the /proc/kcore, /proc/sys, and similar paths are masked or
		// read-only) rather than the Unmasked variant. Combined with the
		// read-only root filesystem above, /sys is mounted read-only.
		// Setting it explicitly states the §6.4 invariant in the pod spec
		// instead of relying on the container runtime default.
		ProcMount: ptr.To(corev1.DefaultProcMount),
	}
}

// terminationGrace returns the pod's terminationGracePeriodSeconds. It
// is the §4.6.1 default (120s) unless the SandboxTemplate declares a
// lower maxTerminationGracePeriodSeconds ceiling, in which case the
// grace period is clamped down to that ceiling. spec: §4.6.1
// "Disruption protection for agent pods".
func terminationGrace(in Inputs) int64 {
	grace := defaultTerminationGraceSeconds
	// spec: §5.2 line 516 — the deployer-set base grace period (sized to
	// the pool's per-slot checkpoint budget) replaces the 120s default.
	if in.TerminationGraceSeconds != nil && *in.TerminationGraceSeconds > 0 {
		grace = *in.TerminationGraceSeconds
	}
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
