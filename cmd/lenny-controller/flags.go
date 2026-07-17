// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/lennylabs/lenny/pkg/controller/ratelimit"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// controllerFlags holds the parsed lenny-controller command-line flags and
// environment-derived defaults the composition root threads into each build
// step. parseFlags populates it once; every build step reads its inputs from
// the embedded *controllerFlags rather than re-reading the process environment,
// mirroring the gatewayFlags / opsFlags pattern the gateway and lenny-ops
// composition roots use (proposal 0020 §4 Part A R1 / R4 / R8).
type controllerFlags struct {
	metricsAddr        string
	probeAddr          string
	leaderElect        bool
	leaderElectNS      string
	adapterImage       string
	gatewayGRPCAddr    string
	egressCaptureImage string
	postgresDSN        string
	agentNSList        string
	redisURL           string
	redisPassword      string
	initialFillGrace   time.Duration

	createQPS               float64
	createBurst             int
	statusQPS               float64
	statusBurst             int
	maxConcurrentReconciles int
	statusDedupWindow       time.Duration
	claimOrphanTimeout      time.Duration
	reservedHoldGrace       time.Duration
	workqueueMaxDepth       int
	devMode                 bool
	certTTL                 time.Duration
	certExpiryThreshold     time.Duration
	certIssuanceGrace       time.Duration
	requireCertIssuance     bool
	saTokenAudience         string
	agentServiceAccount     string
	dedicatedDNSClusterIP   string
	objectStoreCAConfigMap  string
	runtimeClassStandard    string
	runtimeClassSandboxed   string
	runtimeClassMicrovm     string
	resourceClassOverrides  repeatableFlag
	adapterUID              int64
	agentUID                int64
	credReadersGID          int64

	zapOpts zap.Options
}

// parseFlags registers every lenny-controller flag against the default flag
// set, parses os.Args once, and returns the populated controllerFlags. The
// flag definitions are grouped into per-domain register helpers so the parse
// step reads as a sequence of domain registrations rather than one flat block,
// mirroring the lenny-ops parseFlags decomposition (proposal 0020 §4 Part A R4 /
// R8). The §16.4 controller logger and the §16.3 tracer are installed by the
// composition root after the parse, not here.
func parseFlags() *controllerFlags {
	f := &controllerFlags{}
	registerServerFlags(f)
	registerPodIdentityFlags(f)
	registerRateLimitFlags(f)
	registerLifecycleFlags(f)
	registerCertFlags(f)

	// The zap development flags are bound on the shared command line so the
	// controller-runtime logger honors --zap-* overrides; they are read after
	// flag.Parse to construct the logger in buildManagerSetup.
	f.zapOpts = zap.Options{Development: false}
	f.zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()
	return f
}

// registerServerFlags binds the §4.6.1 metrics/probe bind addresses, the
// leader-election toggle and namespace, and the §9.1/§8.6 gateway link and
// §10.3 pod-identity addresses.
func registerServerFlags(f *controllerFlags) {
	flag.StringVar(&f.metricsAddr, "metrics-bind-address", ":8080",
		"address the metrics endpoint binds to")
	flag.StringVar(&f.probeAddr, "health-probe-bind-address", ":8081",
		"address the health and readiness probes bind to")
	flag.BoolVar(&f.leaderElect, "leader-elect", false,
		"run leader election so only one replica reconciles at a time")
	flag.StringVar(&f.leaderElectNS, "leader-election-namespace", "lenny-system",
		"namespace that holds the leader-election Lease")
	flag.StringVar(&f.adapterImage, "adapter-image", "",
		"the lenny-adapter sidecar image stamped into agent pods")
	flag.StringVar(&f.gatewayGRPCAddr, "gateway-grpc-addr", os.Getenv("LENNY_GATEWAY_GRPC_ADDR"),
		"§9.1/§8.6 gateway GatewayControl address (host:port) stamped onto agent-pod adapters so a type:agent runtime's platform tool calls (lenny/delegate_task, ...) reach the gateway platform tool surface. Empty leaves the platform MCP server unstarted.")
	flag.StringVar(&f.saTokenAudience, "sa-token-audience", os.Getenv("LENNY_SA_TOKEN_AUDIENCE"),
		"§10.3 deployment-specific projected-token audience (global.saTokenAudience, e.g. lenny-gateway-<cluster-name>). When set, every agent pod mounts a §6.1 audience-bound, 900s-TTL projected service-account token; empty leaves the pod without it rather than mounting a cluster-default-audience token.")
	flag.StringVar(&f.agentServiceAccount, "agent-service-account", os.Getenv("LENNY_AGENT_SERVICE_ACCOUNT"),
		"§10.3 zero-RBAC ServiceAccount bound to agent pods. Empty uses the namespace default SA (which carries no RBAC bindings in agent namespaces).")
	flag.StringVar(&f.dedicatedDNSClusterIP, "dedicated-dns-cluster-ip", os.Getenv("LENNY_DEDICATED_DNS_CLUSTER_IP"),
		"§13.2 (K8S-033) ClusterIP of the lenny-agent-dns Service (chart coredns.clusterIP). When set, every agent pod is stamped with dnsPolicy:None and a dnsConfig targeting this address so DNS resolves through the dedicated CoreDNS instance; pools that opt out via dnsPolicy:cluster-default keep the Kubernetes default ClusterFirst behavior. Empty leaves the cluster default in force.")
	flag.StringVar(&f.objectStoreCAConfigMap, "objectstore-ca-configmap", os.Getenv("LENNY_OBJECTSTORE_CA_CONFIGMAP"),
		"§13.2 name of the per-agent-namespace ConfigMap holding the object-store CA trust bundle (key ca.crt). When set, every agent pod projects the ConfigMap read-only and points the adapter's --objectstore-ca-bundle at it so the adapter trusts a self-managed object store's non-public CA during checkpoint upload. Empty (cloud-managed endpoint chaining to a public CA, or unconfigured) leaves the pod without the bundle.")
}

// registerPodIdentityFlags binds the §17.5 RuntimeClass-name overrides, the
// §5.2/§6.4 resource-class overrides, and the §13.1 non-root pod UID/GID
// defaults the chart wires through env.
func registerPodIdentityFlags(f *controllerFlags) {
	// spec: §17.5 line 3 — operators whose cluster ships the gVisor or
	// Kata RuntimeClass under a non-default name (`runsc`, `kata-qemu`,
	// `kata-fc`) override Lenny's literal defaults here so the chart's
	// `isolation.runtimeClassNames` Helm values reach the controller
	// without forcing a rename of in-cluster RuntimeClass objects.
	flag.StringVar(&f.runtimeClassStandard, "standard-runtime-class", os.Getenv("LENNY_STANDARD_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 standard profile. Empty uses the default 'runc'.")
	flag.StringVar(&f.runtimeClassSandboxed, "sandboxed-runtime-class", os.Getenv("LENNY_SANDBOXED_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 sandboxed profile. Empty uses the default 'gvisor'.")
	flag.Var(&f.resourceClassOverrides, "resource-class",
		"override a §5.2 resource class as `name=requests.cpu:250m,requests.memory:512Mi,limits.cpu:1,limits.memory:1Gi` (repeatable; defaults small/medium/large)")
	flag.StringVar(&f.runtimeClassMicrovm, "microvm-runtime-class", os.Getenv("LENNY_MICROVM_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 microvm profile. Empty uses the default 'kata'.")
	// spec: §13.1 line 7 — the non-root pod UIDs are operator-tunable
	// (default 65532/65533/65534). The lenny-webhook binary MUST be given
	// the same values (the chart wires both from security.podUIDs) or a
	// built pod fails the lenny-pod-security UID checks. F-13.1.16.
	flag.Int64Var(&f.adapterUID, "adapter-uid", envInt64("LENNY_ADAPTER_UID", podspec.AdapterUID),
		"§13.1 non-root UID for the lenny-adapter container. Must match the lenny-webhook --adapter-uid and the runtime image's baked UID.")
	flag.Int64Var(&f.agentUID, "agent-uid", envInt64("LENNY_AGENT_UID", podspec.AgentUID),
		"§13.1 non-root UID for the runtime container. Must match the lenny-webhook --agent-uid and the runtime image's baked UID.")
	flag.Int64Var(&f.credReadersGID, "cred-readers-gid", envInt64("LENNY_CRED_READERS_GID", podspec.CredReadersGID),
		"§13.1 lenny-cred-readers GID used as the pod fsGroup for the credential tmpfs. Must match the lenny-webhook --cred-readers-gid.")
	flag.StringVar(&f.egressCaptureImage, "egress-capture-image", os.Getenv("LENNY_EGRESS_CAPTURE_IMAGE"),
		"the §12.9.8 tier-9 lenny-egress-capture sidecar image. Empty disables capture globally. Non-empty enables injection on every Sandbox whose annotation set carries `lenny.dev/test-egress-capture-upstream`. Production rejects the sidecar via lenny-pod-security; the flag exists for tier-9 §12.9.8 credential-leakage probes.")
}

// registerRateLimitFlags binds the §4.6.1 Postgres/Redis backends, the agent
// namespaces, and the create/status rate-limiter and reconciliation-concurrency
// knobs.
func registerRateLimitFlags(f *controllerFlags) {
	flag.StringVar(&f.postgresDSN, "postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, the WarmPoolController mirrors Sandbox status to the §4.6.1 agent_pod_state table (the migrations/ schema must already be applied). When empty, mirroring is disabled.")
	flag.StringVar(&f.agentNSList, "agent-namespaces", os.Getenv("LENNY_AGENT_NAMESPACES"),
		"comma-separated agent namespaces. When set, the §13.2 NET-022 cluster-CIDR drift detector audits the broad-internet egress NetworkPolicies in these namespaces every 5 minutes. When empty, drift detection is disabled.")
	flag.StringVar(&f.redisURL, "redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When set, controller-emitted pool_state_changed events land on ops:events:stream alongside the gateway-emitted events. When empty, events stay in the controller-local in-memory buffer.")
	flag.StringVar(&f.redisPassword, "redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password.")
	flag.Float64Var(&f.createQPS, "create-qps", ratelimit.DefaultCreateQPS,
		"§4.6.1 pod-creation rate-limiter bucket QPS. Create calls for new Sandbox pods route through this bucket so scale-up is never starved by status-update traffic.")
	flag.IntVar(&f.createBurst, "create-burst", ratelimit.DefaultCreateBurst,
		"§4.6.1 pod-creation rate-limiter bucket burst.")
	flag.Float64Var(&f.statusQPS, "status-qps", ratelimit.DefaultStatusQPS,
		"§4.6.1 status-update rate-limiter bucket QPS. UpdateStatus calls on Sandbox and SandboxWarmPool route through this bucket.")
	flag.IntVar(&f.statusBurst, "status-burst", ratelimit.DefaultStatusBurst,
		"§4.6.1 status-update rate-limiter bucket burst.")
	flag.IntVar(&f.maxConcurrentReconciles, "max-concurrent-reconciles", 1,
		"§4.6.1 number of concurrent reconciliation workers per controller. Increase for cold-start fill and cluster-restart recovery; the rate limiter remains the throughput ceiling regardless of worker count.")
}

// registerLifecycleFlags binds the §4.6.1 fill grace, status-dedup, claim-GC,
// work-queue depth, and §5.3 dev-mode reconciliation-lifecycle knobs.
func registerLifecycleFlags(f *controllerFlags) {
	flag.DurationVar(&f.initialFillGrace, "initial-fill-grace-period", 120*time.Second,
		"§4.6.1 cold-start fill grace period. The WarmPoolExhausted and WarmPoolLow alerts are suppressed for a pool during this window from pool creation, controller startup, or a minWarm 0→positive re-activation, to avoid false positives while the pool fills toward minWarm.")
	flag.DurationVar(&f.statusDedupWindow, "status-update-dedup-window", 500*time.Millisecond,
		"§4.6.1 statusUpdateDeduplicationWindow: the minimum interval between consecutive UpdateStatus writes for the same Sandbox. Status changes within the window are coalesced (trailing write wins), reducing etcd write pressure.")
	flag.DurationVar(&f.claimOrphanTimeout, "claim-orphan-timeout", 5*time.Minute,
		"§4.6.1 SandboxClaim orphan timeout: a live (bound/recycling) or empty-status SandboxClaim whose orphan key is older than this with no active session is reclaimed by the leader's GarbageCollect loop. Requires --postgres-dsn for the active-session lookup.")
	flag.DurationVar(&f.reservedHoldGrace, "reserved-hold-grace", 60*time.Second,
		"§4.6.1 reserved-claim grace period: a reserved SandboxClaim is reclaimed by the leader's GarbageCollect loop once holdExpiresAt plus this grace has passed, so the GC does not race the gateway's own hold-expiry DELETE.")
	flag.IntVar(&f.workqueueMaxDepth, "workqueue-max-depth", 500,
		"§4.6.1 controller work-queue max depth. When a controller's reconciliation queue is at this depth, new reconciliation events are dropped and lenny_controller_queue_overflow_total is incremented (requeues are never shed). A non-positive value disables work-shedding. Per-tier recommendations: 500 / 2,000 / 10,000.")
	flag.BoolVar(&f.devMode, "dev-mode", os.Getenv("LENNY_DEV_MODE") == "true",
		"§5.3 line 677 global.devMode. When true, a Sandbox that omits an isolation profile defaults its pod to `standard` (runc) so a developer can run on a cluster without gVisor installed. Production leaves this false (default `sandboxed`/gVisor).")
}

// registerCertFlags binds the §4.6.1 / §10.3 idle-pod certificate-expiry
// replacement window knobs.
func registerCertFlags(f *controllerFlags) {
	flag.DurationVar(&f.certTTL, "cert-ttl", 4*time.Hour,
		"§10.3 line 338 agent-pod mTLS certificate lifetime. The §4.6.1 cert-expiry replacement derives an idle pod's certificate expiry as pod-creation-time + this TTL when the pod carries no explicit lenny.dev/cert-not-after annotation.")
	flag.DurationVar(&f.certExpiryThreshold, "cert-expiry-threshold", 30*time.Minute,
		"§4.6.1 / §10.3 line 342 proactive cert-replacement window. An idle pod whose certificate will expire within this duration is drained and recreated so a claim never lands on a pod with insufficient remaining certificate validity.")
	flag.DurationVar(&f.certIssuanceGrace, "cert-issuance-grace", 60*time.Second,
		"§10.3 line 342 cert-issuance grace. A pre-idle pod that has not presented a valid lenny.dev/cert-not-after annotation within this window of its creation is treated as a cert-issuance failure and replaced. Active only with --require-cert-issuance.")
	flag.BoolVar(&f.requireCertIssuance, "require-cert-issuance", false,
		"§10.3 line 342 cert-issuance enforcement. When true, a pre-idle pod without a valid certificate after --cert-issuance-grace is drained for replacement. Default false because the check keys on the lenny.dev/cert-not-after annotation a per-pod cert producer stamps; enable it only when that producer is wired, otherwise every pre-idle pod is replaced.")
}
