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
	// AdapterUID and AgentUID are the default non-root UIDs the adapter
	// and runtime containers run as. §13.1 line 7 mandates distinct
	// non-root identities but leaves the specific numbers to the
	// implementation, so these are operator-tunable: a deployer whose
	// runtime base image bakes a different non-root UID overrides them
	// through the Inputs fields below (the controller resolves them from
	// the same Helm value the lenny-pod-security and
	// ephemeral-container-cred-guard webhooks read, so a built pod always
	// passes the webhook UID checks). spec: §13.1 line 7. F-13.1.16.
	AdapterUID int64 = 65532
	AgentUID   int64 = 65533
	// CredReadersGID is the default lenny-cred-readers group — the §13.1
	// credential-file read boundary shared by the adapter and runtime
	// containers. The pod fsGroup is set to it so the kubelet
	// group-owns the credential tmpfs. Operator-tunable via the Inputs
	// field below, in lock-step with the two UIDs. F-13.1.16.
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

	// sdkDemoteTimeoutSeconds mirrors the adapter's DefaultDemoteTimeout
	// (the §6.1 line 67 LENNY_DEMOTE_TIMEOUT_SECONDS default, 5s) the
	// adapter bounds its SIGTERM-time DemoteSDK teardown by.
	sdkDemoteTimeoutSeconds int64 = 5

	// sdkDemoteGraceMarginSeconds is the §6.1 line 67 "+5s" the grace
	// period of a preConnect pod must exceed the demote timeout by, so the
	// kubelet does not SIGKILL the adapter before its bounded DemoteSDK (and
	// the force-terminate fallback) completes.
	sdkDemoteGraceMarginSeconds int64 = 5

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

	// sharedMount is the §6.4 line 409 /workspace/shared path: a separate
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

	// PodNameEnvVar is the Downward API environment variable the adapter
	// reads to learn its own pod name. spec: §4.7, §5.2 — the adapter
	// reports each per-slot cleanup outcome via ReportSessionScrub and
	// the whole-pod scrub outcome via ReportPodScrub, both keyed on the
	// pod identity. ShutdownSlot carries no podId and the base recycle
	// Shutdown carries it only inside RecycleScrub, so the adapter takes
	// its pod identity from this Downward API env and caches it. An
	// absent or misnamed env yields an empty cached podID, which the
	// gateway rejects InvalidArgument, so the name is load-bearing.
	PodNameEnvVar = "POD_NAME"

	// PlatformMCPSocketName is the §9.1/§4.7 abstract Unix socket the
	// adapter's platform MCP server binds (@lenny-platform-mcp). A
	// type:agent runtime discovers it from the adapter manifest's
	// platformMcpServer.socket and dials it to reach the platform tools
	// (lenny/delegate_task, ...). It lives in the same kernel abstract
	// namespace as the runtime socket, reachable across the pod's
	// containers. spec: §9.1 line 8. F-9.1.1.
	PlatformMCPSocketName = "@lenny-platform-mcp"

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
	// GatewayGRPCAddr is the §8.6/§9.1 gateway GatewayControl address
	// (host:port) the adapter dials to forward a type:agent runtime's
	// platform tool calls (lenny/delegate_task, ...). When set, the
	// builder starts the platform MCP server on PlatformMCPSocketName and
	// points the adapter at this address; when empty, the platform MCP
	// server is not started (no gateway link to forward to). Supplied as
	// controller configuration. spec: §9.1 lines 14-31. F-9.1.1.
	GatewayGRPCAddr string
	// IsolationProfile is the §5.3 profile (standard, sandboxed, or
	// microvm) that selects the RuntimeClass.
	IsolationProfile string
	// RuntimeClassNameOverrides remaps the §5.3 isolation profile to a
	// cluster-specific RuntimeClass name. spec: §17.5 line 3 — clusters
	// that ship gVisor as `runsc` or Kata as `kata-qemu` / `kata-fc`
	// set this through the chart's `isolation.runtimeClassNames` Helm
	// values so the controller does not require operators to rename
	// in-cluster RuntimeClass objects to match Lenny's literal defaults.
	// A nil or empty map preserves the §5.3 defaults.
	RuntimeClassNameOverrides map[isolation.Profile]string
	// DeploymentModel is the §4.7 deployment model, from
	// Runtime.spec.deploymentModel. An empty value defaults to the
	// sidecar model.
	DeploymentModel string

	// RequireSoPeercred is the §4.7 nonce-only-mode activation decision the
	// Sandbox reconciler resolves from the runtime's
	// Runtime.spec.requireSoPeercred and the pool's acknowledgment
	// (Sandbox.spec.requireSoPeercred carries it down). An explicitly false
	// value renders --require-so-peercred=false into the sidecar adapter
	// container, putting the adapter in nonce-only mode. A nil or true value
	// renders no flag, so the adapter default of true (require SO_PEERCRED,
	// crash on a failed self-test) governs. The field applies to the sidecar
	// model only: buildEmbedded never renders it, and the RuntimeReconciler
	// rejects an embedded runtime that sets requireSoPeercred: false (§4.1).
	// spec: §4.7.
	RequireSoPeercred *bool
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

	// PreConnect is the §5.1 capabilities.preConnect flag for the pod's
	// runtime. When true the pod is SDK-warm: it may reach `sdk_connecting`,
	// so its terminationGracePeriodSeconds is floored at
	// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` (§6.1 line 67) to give the adapter
	// time to run its bounded DemoteSDK teardown (and the force-terminate
	// fallback) on SIGTERM before the kubelet sends SIGKILL. The floor is the
	// §6.1 safety boundary against abandoning the SDK mid-connection.
	PreConnect bool

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

	// SharedAssetsArg is the §6.4 line 409 inline shared-asset set encoded
	// for the adapter's --shared-assets flag (sharedassets.Encode of the
	// Runtime's sharedAssets). The adapter materializes these into
	// /workspace/shared at warm time, before any slot is assigned. Empty
	// leaves the shared volume mounted but empty — the runtime still cannot
	// write it (read-only mount), so the scrub-space-prevention guarantee
	// holds with or without configured assets. spec: §6.4 line 409 — F-6.4.3.
	SharedAssetsArg string

	// DedicatedDNSClusterIP is the ClusterIP of the lenny-agent-dns
	// Service in lenny-system. spec: §13.2 lines 470-490 (K8S-033) — when
	// non-empty the pod builder sets `dnsPolicy: None` and a `dnsConfig`
	// pointing the pod's resolver at this address, because the
	// agent-namespace NetworkPolicy blocks the kube-system kube-dns the
	// Kubernetes default ClusterFirst policy would otherwise target, so
	// DNS would fail silently. A pool that opts out via the
	// `lenny.dev/dns-policy: cluster-default` label (carried on Labels)
	// keeps the Kubernetes default ClusterFirst behavior even when this is
	// set. An empty value leaves the cluster default in force (dev / no
	// dedicated CoreDNS configured). F-13.2.4.
	DedicatedDNSClusterIP string

	// ReleaseNamespace is the Helm release namespace (lenny-system). It is
	// the first search domain in the dedicated-DNS dnsConfig so in-cluster
	// Service short-names resolve as they would under ClusterFirst.
	ReleaseNamespace string

	// Resources is the §5.2 resource class resolved to container CPU/memory
	// requests and limits. spec: §6.4 line 413 — the memory limit gives the
	// pod a predictable OOM boundary that accounts for the memory-backed
	// tmpfs volumes (/sessions, /tmp, /dev/shm) charging against the pod
	// memory cgroup. The builder stamps it on every Lenny container (adapter
	// and runtime). A nil value leaves the containers without explicit
	// resource requirements (dev / unconfigured), preserving prior behavior.
	Resources *corev1.ResourceRequirements

	// AdapterUID, AgentUID, and CredReadersGID override the non-root
	// identities the adapter container, the runtime container, and the
	// credential-tmpfs fsGroup use. A zero value selects the package
	// default constant of the same name. §13.1 line 7 fixes only
	// "non-root and distinct"; the numbers are operator-tunable for a
	// deployer whose runtime base image bakes a different non-root UID.
	// The controller and the lenny-pod-security /
	// ephemeral-container-cred-guard webhooks MUST resolve these from the
	// same Helm value so a pod the controller builds passes the webhook
	// UID checks; a partial override on one side rejects every agent pod.
	// spec: §13.1 line 7. F-13.1.16.
	AdapterUID     int64
	AgentUID       int64
	CredReadersGID int64
}

// adapterUID, agentUID, and credReadersGID resolve the pod's non-root
// identities: the per-deployment override when set, else the package
// default constant. A zero override is treated as unset because UID 0 is
// root, which §13.1 line 7 forbids, so it can never be a legitimate
// value. spec: §13.1 line 7. F-13.1.16.
func (in Inputs) adapterUID() int64 {
	if in.AdapterUID != 0 {
		return in.AdapterUID
	}
	return AdapterUID
}

func (in Inputs) agentUID() int64 {
	if in.AgentUID != 0 {
		return in.AgentUID
	}
	return AgentUID
}

func (in Inputs) credReadersGID() int64 {
	if in.CredReadersGID != 0 {
		return in.CredReadersGID
	}
	return CredReadersGID
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
	runtimeClass, ok := isolation.ResolveRuntimeClassName(isolation.Profile(in.IsolationProfile), in.RuntimeClassNameOverrides)
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
		// spec: §6.4 line 409 — the adapter populates /workspace/shared at
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
		// spec: §6.4 line 409 — read-only on the runtime container: any write
		// the agent process attempts under /workspace/shared returns EROFS.
		{Name: sharedVolumeName, MountPath: sharedMount, ReadOnly: true},
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "adapter",
			Image: in.AdapterImage,
			Args: append([]string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				// spec: §6.4 line 407 — session-mode pods (maxConcurrentSessions=1)
				// use the single `/workspace/current` cwd. The concurrent-workspace
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
				fmt.Sprintf("--runtime-uid=%d", in.agentUID()),
				// §4.7 sidecar transport: the adapter binds the abstract
				// runtime socket the runtime container dials.
				"--runtime-socket=" + RuntimeSocketName,
			}, append(append(sharedAssetsArgs(in), platformMCPArgs(in)...), nonceOnlyArgs(in)...)...),
			// spec: §4.7, §5.2 — the adapter reports each per-slot cleanup
			// outcome (ReportSessionScrub) and the whole-pod scrub outcome
			// (ReportPodScrub) keyed on the pod identity. It reads that
			// identity from this Downward API POD_NAME env and caches it,
			// since ShutdownSlot carries no podId and the base recycle
			// Shutdown carries it only inside RecycleScrub.
			Env: []corev1.EnvVar{{
				Name: PodNameEnvVar,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
				},
			}},
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
	// spec: §6.4 line 413 — stamp the resolved §5.2 resource class on both
	// Lenny containers before the test-only egress sidecar is injected.
	applyResources(in, pod)
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
		// spec: §6.4 line 409 — the embedded runtime is the adapter, so it
		// populates /workspace/shared itself and mounts it read-write. The
		// kernel-level EROFS boundary is a sidecar-model property (the agent
		// and adapter are separate containers there); in the embedded model
		// the single process is trusted not to write the shared tree after
		// the warm-time populate, the same tradeoff the credential mount
		// makes.
		{Name: sharedVolumeName, MountPath: sharedMount},
	}

	pod := basePod(in, runtimeClass)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "runtime",
			Image: in.RuntimeImage,
			Args: append([]string{
				fmt.Sprintf("--addr=:%d", adapterPort),
				// spec: §6.4 line 407 — session-mode pods (maxConcurrentSessions=1)
				// use the single `/workspace/current` cwd. The concurrent-workspace
				// per-slot tree (`/workspace/slots/{slotId}/current/`) is unbuilt
				// in v1 (tracked under F-6.4.2); when it lands, the builder will
				// select the workspace root from the resolved Runtime layout
				// instead of hard-coding it here.
				"--workspace-root=" + workspaceMount + "/current",
				// §4.7: the embedded runtime is the adapter, so it stages
				// uploads the same way the sidecar adapter does.
				"--staging-dir=" + stagingPath,
			}, append(sharedAssetsArgs(in), platformMCPArgs(in)...)...),
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
	// spec: §6.4 line 413 — stamp the resolved §5.2 resource class on the
	// single runtime container before the test-only egress sidecar.
	applyResources(in, pod)
	// spec: §10.3 — the embedded runtime is the pod's gateway-facing
	// process, so the projected token mounts on the runtime container.
	injectSATokenVolume(in, pod, []int{0})
	injectEgressCaptureSidecar(in, pod, []int{0})
	return pod, nil
}

// applyResources stamps the §5.2 resource class (resolved to CPU/memory
// requests and limits) onto every Lenny container in the pod. It runs
// before injectEgressCaptureSidecar so the test-only egress sidecar is not
// given the agent's resource budget. spec: §6.4 line 413 — the memory limit
// bounds the pod's combined agent-process-plus-tmpfs footprint with a
// predictable OOM boundary. A nil Resources leaves containers unconstrained.
func applyResources(in Inputs, pod *corev1.Pod) {
	if in.Resources == nil {
		return
	}
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Resources = *in.Resources.DeepCopy()
	}
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
		SecurityContext: containerSecurityContext(in.agentUID()),
	})
}

// sharedAssetsArgs returns the §6.4 line 409 adapter flags for the
// /workspace/shared read-only asset tree. The --shared-assets-dir flag
// is always set so the adapter ensures the directory at warm time (it is
// mounted read-write on the adapter container, read-only on the runtime
// container). --shared-assets carries the inline asset set only when the
// Runtime declares any; an empty set leaves the directory mounted but
// empty. spec: §6.4 line 409 — F-6.4.3.
func sharedAssetsArgs(in Inputs) []string {
	args := []string{"--shared-assets-dir=" + sharedMount}
	if in.SharedAssetsArg != "" {
		args = append(args, "--shared-assets="+in.SharedAssetsArg)
	}
	return args
}

// platformMCPArgs returns the §9.1 platform MCP server args for the
// adapter container: when the controller is configured with the gateway
// GatewayControl address, the adapter binds the platform MCP socket and
// forwards a type:agent runtime's platform tool calls to that gateway.
// When no gateway address is configured the platform MCP server is not
// started (there is no gateway link to forward to). spec: §9.1 lines
// 8-31. F-9.1.1.
func platformMCPArgs(in Inputs) []string {
	if in.GatewayGRPCAddr == "" {
		return nil
	}
	return []string{
		"--mcp-socket=" + PlatformMCPSocketName,
		"--gateway-grpc-addr=" + in.GatewayGRPCAddr,
	}
}

// nonceOnlyArgs returns the §4.7 nonce-only-mode adapter flag. It renders
// --require-so-peercred=false only when the Sandbox reconciler resolved the
// activation decision to an explicit false (a deploymentModel: sidecar
// runtime carrying requireSoPeercred: false in an acknowledged pool, §4.7).
// A nil or true value renders no flag: the adapter's own default of true
// (require SO_PEERCRED, crash on a failed self-test) then governs, so the
// builder fails closed on the security boundary. spec: §4.7.
func nonceOnlyArgs(in Inputs) []string {
	if in.RequireSoPeercred != nil && !*in.RequireSoPeercred {
		return []string{"--require-so-peercred=false"}
	}
	return nil
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
		// spec: §6.4 line 409 — /workspace/shared is a separate emptyDir so
		// the read-only volumeMount on the runtime container enforces
		// immutability at the kernel level. It is disk-backed: shared assets
		// can exceed the pod memory budget, the same as the workspace volume.
		{Name: sharedVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
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
				RunAsNonRoot: ptr.To(true),
				FSGroup:      ptr.To(in.credReadersGID()),
				// spec: §13.1 line 25 — both the adapter UID and the agent
				// UID are declared in the pod's
				// spec.securityContext.supplementalGroups list, making
				// lenny-cred-readers a shared group of which both
				// containers are members. The fsGroup above sets the
				// credential tmpfs group ownership; this explicit
				// supplementalGroups declaration is the membership the §13.1
				// cross-UID delivery path requires, rather than relying on
				// the kubelet's implicit fsGroup-to-supplementary-group
				// propagation side-effect.
				SupplementalGroups: []int64{in.credReadersGID()},
				SeccompProfile:     &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
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
	// spec: §17.2 lines 97-101 — Kata (microvm) pods MUST run on dedicated
	// node pools enforced by hard scheduling constraints, not merely
	// taints/tolerations. The RuntimeClass scheduling.nodeSelector
	// (control 1, rendered by the chart) constrains the pod at admission,
	// but the controller injects a requiredDuringSchedulingIgnoredDuring-
	// Execution node affinity (control 2) so scheduling fails rather than
	// falling back to an unsuitable node, plus the dedicated-node taint
	// toleration (control 3) so the pod can land on Kata-tainted hardware.
	if isolation.Profile(in.IsolationProfile) == isolation.ProfileMicrovm {
		applyKataNodeIsolation(pod)
	}
	applyDedicatedDNS(in, pod)
	return pod
}

// DNSPolicyOptOutLabel is the §13.2 pod label whose presence (value
// `cluster-default`) signals the pool opted out of the dedicated CoreDNS
// instance. The pod builder leaves such a pod at the Kubernetes default
// ClusterFirst behavior so it resolves through kube-system CoreDNS.
const (
	DNSPolicyOptOutLabel    = "lenny.dev/dns-policy"
	DNSPolicyClusterDefault = "cluster-default"
)

// applyDedicatedDNS points the agent pod's resolver at the dedicated
// lenny-system CoreDNS instance. spec: §13.2 lines 470-490 (K8S-033) —
// the agent-namespace NetworkPolicy routes DNS only to the dedicated
// instance and blocks kube-system kube-dns, so a pod left at the
// Kubernetes default ClusterFirst policy (resolver pointed at
// kube-system kube-dns) would have every name lookup blackholed. Setting
// `dnsPolicy: None` plus an explicit `dnsConfig` makes the pod query the
// dedicated instance instead. The search domains and ndots mirror the
// standard ClusterFirst behavior so in-cluster Service short-names still
// resolve.
//
// A pool that opts out via the `lenny.dev/dns-policy: cluster-default`
// label keeps the Kubernetes default behavior (resolving through
// kube-system CoreDNS); the builder makes no change in that case. The
// builder also makes no change when no dedicated ClusterIP is configured
// (dev or a deployment without the dedicated instance).
func applyDedicatedDNS(in Inputs, pod *corev1.Pod) {
	if in.DedicatedDNSClusterIP == "" {
		return
	}
	if in.Labels[DNSPolicyOptOutLabel] == DNSPolicyClusterDefault {
		return
	}
	pod.Spec.DNSPolicy = corev1.DNSNone
	searches := []string{"svc.cluster.local", "cluster.local"}
	if in.ReleaseNamespace != "" {
		searches = append([]string{in.ReleaseNamespace + ".svc.cluster.local"}, searches...)
	}
	pod.Spec.DNSConfig = &corev1.PodDNSConfig{
		Nameservers: []string{in.DedicatedDNSClusterIP},
		Searches:    searches,
		Options:     []corev1.PodDNSConfigOption{{Name: "ndots", Value: ptr.To("5")}},
	}
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

const (
	// KataNodePoolLabelKey and KataNodePoolValue are the §17.2 line 99
	// dedicated-node-pool label (`lenny.dev/node-pool: kata`). The chart
	// stamps it on the Kata RuntimeClass's scheduling.nodeSelector
	// (control 1) and on the Kata node pool's nodes; the controller
	// injects the matching hard node affinity (control 2). The literal
	// must stay in sync with the `lenny.dev/node-pool` value the chart's
	// microvm RuntimeClass nodeSelector renders.
	KataNodePoolLabelKey = "lenny.dev/node-pool"
	KataNodePoolValue    = "kata"

	// KataIsolationTaintKey and KataIsolationTaintValue are the §17.2
	// line 101 dedicated-node taint (`lenny.dev/isolation=kata:NoSchedule`).
	// Kata node pools carry the taint so only pods with the matching
	// toleration (control 3, injected below) can land on Kata-dedicated
	// hardware, keeping non-Kata workloads off it even if the node-pool
	// label drifts.
	KataIsolationTaintKey   = "lenny.dev/isolation"
	KataIsolationTaintValue = "kata"
)

// applyKataNodeIsolation stamps the §17.2 lines 99-101 hard node
// affinity and dedicated-node toleration onto a Kata (microvm) pod.
// spec: §17.2 lines 97-101.
//
// Control 2 is a requiredDuringSchedulingIgnoredDuringExecution node
// affinity matching `lenny.dev/node-pool: kata`: as a defense-in-depth
// measure beyond the RuntimeClass nodeSelector (control 1), it makes
// scheduling fail rather than fall back to an unsuitable node. The
// requirement is ANDed into every existing node-selector term (or a
// single term is created when none exist) so it constrains every
// scheduling option, and the merge is idempotent across re-reconciles.
//
// Control 3 is the toleration for the `lenny.dev/isolation=kata:NoSchedule`
// taint the Kata node pool carries, appended only when an equivalent
// entry is not already present.
func applyKataNodeIsolation(pod *corev1.Pod) {
	req := corev1.NodeSelectorRequirement{
		Key:      KataNodePoolLabelKey,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{KataNodePoolValue},
	}
	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &corev1.Affinity{}
	}
	if pod.Spec.Affinity.NodeAffinity == nil {
		pod.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	na := pod.Spec.Affinity.NodeAffinity
	if na.RequiredDuringSchedulingIgnoredDuringExecution == nil ||
		len(na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		na.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: []corev1.NodeSelectorRequirement{req}},
			},
		}
	} else {
		terms := na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		for i := range terms {
			if !hasNodeSelectorRequirement(terms[i].MatchExpressions, req) {
				terms[i].MatchExpressions = append(terms[i].MatchExpressions, req)
			}
		}
	}

	want := corev1.Toleration{
		Key:      KataIsolationTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    KataIsolationTaintValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	for _, tol := range pod.Spec.Tolerations {
		if tol.Key == want.Key && tol.Value == want.Value && tol.Effect == want.Effect {
			return
		}
	}
	pod.Spec.Tolerations = append(pod.Spec.Tolerations, want)
}

// hasNodeSelectorRequirement reports whether reqs already contains an
// equivalent (key, operator, values) requirement, so applyKataNodeIsolation
// stays idempotent across re-reconciles.
func hasNodeSelectorRequirement(reqs []corev1.NodeSelectorRequirement, want corev1.NodeSelectorRequirement) bool {
	for _, r := range reqs {
		if r.Key == want.Key && r.Operator == want.Operator && stringsEqual(r.Values, want.Values) {
			return true
		}
	}
	return false
}

// stringsEqual reports whether two string slices are element-wise equal.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	// spec: §6.1 line 67 — a preConnect (SDK-warm) pod may receive SIGTERM
	// while in `sdk_connecting`. Its grace period must be at least
	// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` so the adapter can run its bounded
	// DemoteSDK teardown (and the force-terminate fallback) before the
	// kubelet sends SIGKILL. This safety floor takes precedence over a lower
	// §5.2 maxTerminationGracePeriodSeconds ceiling because abandoning the
	// SDK mid-connection leaks credentials; the default 120s grace already
	// satisfies it. The floor assumes the default demote timeout — a deployer
	// who raises LENNY_DEMOTE_TIMEOUT_SECONDS must size the pool grace to
	// match, which the 120s default does for any reasonable timeout.
	if in.PreConnect {
		if floor := sdkDemoteTimeoutSeconds + sdkDemoteGraceMarginSeconds; grace < floor {
			grace = floor
		}
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
