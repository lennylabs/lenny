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
//     namespace, so the abstract socket carries the §28.5.3 JSONL
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

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	// AdapterUID and AgentUID are the default non-root UIDs the adapter
	// and runtime containers run as. §13.1 mandates distinct
	// non-root identities but leaves the specific numbers to the
	// implementation, so these are operator-tunable: a deployer whose
	// runtime base image bakes a different non-root UID overrides them
	// through the Inputs fields below (the controller resolves them from
	// the same Helm value the lenny-pod-security and
	// ephemeral-container-cred-guard webhooks read, so a built pod always
	// passes the webhook UID checks). spec: §13.1. F-13.1.16.
	AdapterUID int64 = 65532
	AgentUID   int64 = 65533
	// CredReadersGID is the default lenny-cred-readers group — the §13.1
	// credential-file read boundary shared by the adapter and runtime
	// containers. The pod fsGroup is set to it so the kubelet
	// group-owns the credential tmpfs. Operator-tunable via the Inputs
	// field below, in lock-step with the two UIDs. F-13.1.16.
	CredReadersGID int64 = 65534

	// adapterPort is the adapter's gRPC listener port (§13.2: the
	// gateway reaches the adapter on TCP 50051).
	adapterPort int32 = 50051
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
	// controller configuration. spec: §9.1. F-9.1.1.
	GatewayGRPCAddr string
	// IsolationProfile is the §5.3 profile (standard, sandboxed, or
	// microvm) that selects the RuntimeClass.
	IsolationProfile string
	// RuntimeClassNameOverrides remaps the §5.3 isolation profile to a
	// cluster-specific RuntimeClass name. spec: §17.5 — clusters
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
	// EgressCapture is the TESTING.md §12.9.8 tier-9 egress-capture sidecar
	// configuration. Non-nil injects an additional container running
	// lenny-egress-capture into the pod, plus a shared emptyDir mounted
	// on the runtime container so the TESTING.md §12.9.8 leakage probe can read
	// the JSONL capture file. The sidecar is TEST-ONLY. The controller
	// injects it only when its --egress-capture-image flag is set (the
	// chart default controller.egressCaptureImage is empty) and the
	// Sandbox carries the opt-in annotation. The lenny-pod-security
	// webhook does not reject it: the container runs at its own
	// non-reserved identity (egressCaptureUID) and mounts no credential
	// path, so it passes the §13.1 checks like any other container.
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
	// override. spec: §5.2 — for concurrent-workspace pools the
	// deployer sets this to cover the per-slot checkpoint budget
	// (`maxConcurrent × max_tiered_checkpoint_cap +
	// checkpointBarrierAckTimeoutSeconds + 30`); §6.4 requires it
	// to be at least `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s`. When set, it
	// replaces the §4.6.1 120s default as the base grace period; the
	// MaxTerminationGraceSeconds ceiling still clamps it down. A nil
	// value leaves the default in force.
	TerminationGraceSeconds *int64

	// PreConnect is the §5.1 capabilities.preConnect flag for the pod's
	// runtime. When true the pod is SDK-warm: it may reach `sdk_connecting`,
	// so its terminationGracePeriodSeconds is floored at
	// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` (§6.1) to give the adapter
	// time to run its bounded DemoteSDK teardown (and the force-terminate
	// fallback) on SIGTERM before the kubelet sends SIGKILL. The floor is the
	// §6.1 safety boundary against abandoning the SDK mid-connection.
	PreConnect bool

	// TopologySpreadConstraints are the §5.2 spread
	// constraints resolved for the pool (the PoolScalingController's zone
	// and node defaults, or the deployer's per-pool override), carried
	// down through Sandbox.spec. The builder stamps them onto the pod so
	// the scheduler distributes the pool's pods across zones and nodes.
	TopologySpreadConstraints []corev1.TopologySpreadConstraint

	// SATokenAudience is the §10.3 deployment-specific audience
	// (global.saTokenAudience, formatted as lenny-gateway-<cluster-name>)
	// for the §6.1 projected service-account token. When non-empty,
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

	// SharedAssetsArg is the §6.4 inline shared-asset set encoded
	// for the adapter's --shared-assets flag (sharedassets.Encode of the
	// Runtime's sharedAssets). The adapter materializes these into
	// /workspace/shared at warm time, before any slot is assigned. Empty
	// leaves the shared volume mounted but empty — the runtime still cannot
	// write it (read-only mount), so the scrub-space-prevention guarantee
	// holds with or without configured assets. spec: §6.4 — F-6.4.3.
	SharedAssetsArg string

	// WorkspaceSizeLimitBytes is the §4.4 per-pod workspace-size
	// hard limit resolved from the pool's SandboxTemplate. When set, the
	// builder renders --workspace-size-limit-bytes onto the adapter so the
	// adapter runs its pre-checkpoint workspace-size probe (stat the workspace
	// tree, abort the checkpoint without quiescing the runtime when the
	// measured size exceeds the limit). A nil value renders no flag, which
	// keeps the probe disabled exactly where the pool declares no limit; the
	// size probe, the §10.1 storage reservation it feeds, and the
	// permanent-error checkpoint arm are all inert without it. spec: §4.4.
	WorkspaceSizeLimitBytes *int64

	// ObjectStoreCAConfigMap is the name of the §13.2 per-agent-namespace
	// ConfigMap holding the object-store CA trust bundle (key `ca.crt`). When
	// non-empty, the builder projects the ConfigMap read-only into the pod's
	// gateway-facing container and points the adapter's --objectstore-ca-bundle
	// at the mounted file so the adapter trusts a self-managed object store's
	// non-public CA during checkpoint upload. The controller resolves the name
	// from its --objectstore-ca-configmap flag. An empty value (a cloud-managed
	// endpoint chaining to a public CA, or an unconfigured deployment) omits
	// the volume, the mount, and the flag. spec: §13.2.
	ObjectStoreCAConfigMap string

	// DedicatedDNSClusterIP is the ClusterIP of the lenny-agent-dns
	// Service in lenny-system. spec: §13.2 — when
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
	// requests and limits. spec: §6.4 — the memory limit gives the
	// pod a predictable OOM boundary that accounts for the memory-backed
	// tmpfs volumes (/sessions, /tmp, /dev/shm) charging against the pod
	// memory cgroup. The builder stamps it on every Lenny container (adapter
	// and runtime). A nil value leaves the containers without explicit
	// resource requirements (dev / unconfigured), preserving prior behavior.
	Resources *corev1.ResourceRequirements

	// AdapterUID, AgentUID, and CredReadersGID override the non-root
	// identities the adapter container, the runtime container, and the
	// credential-tmpfs fsGroup use. A zero value selects the package
	// default constant of the same name. §13.1 fixes only
	// "non-root and distinct"; the numbers are operator-tunable for a
	// deployer whose runtime base image bakes a different non-root UID.
	// The controller and the lenny-pod-security /
	// ephemeral-container-cred-guard webhooks MUST resolve these from the
	// same Helm value so a pod the controller builds passes the webhook
	// UID checks; a partial override on one side rejects every agent pod.
	// spec: §13.1. F-13.1.16.
	AdapterUID     int64
	AgentUID       int64
	CredReadersGID int64
}

// adapterUID, agentUID, and credReadersGID resolve the pod's non-root
// identities: the per-deployment override when set, else the package
// default constant. A zero override is treated as unset because UID 0 is
// root, which §13.1 forbids, so it can never be a legitimate
// value. spec: §13.1. F-13.1.16.
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
