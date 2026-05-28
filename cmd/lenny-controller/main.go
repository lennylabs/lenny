// SPDX-License-Identifier: MIT

// Command lenny-controller runs the Lenny control-plane controllers
// against a Kubernetes API server. It hosts the §4.6.1
// WarmPoolController, which reconciles each SandboxWarmPool toward its
// minWarm/maxWarm target by creating and draining Sandbox resources,
// and the Sandbox-to-Pod reconciler, which materializes each Sandbox
// into a backing Pod and drives the §6.2 warm-path lifecycle.
//
// The binary builds a controller-runtime manager: a shared client
// cache, leader election so only one replica reconciles at a time, a
// metrics endpoint, and health and readiness probes. Leader election
// is off by default and enabled with --leader-elect for a
// multi-replica Deployment; the §4.6.1 lease parameters (15s duration,
// 10s renew deadline, 2s retry period) give a 25s worst-case
// crash-failover window.
//
// Usage:
//
//	lenny-controller --leader-elect --leader-election-namespace lenny-system
//
// The cluster connection is resolved from the in-cluster service
// account when running as a pod, or from KUBECONFIG otherwise.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/cidrdrift"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/ratelimit"
	runtimecontroller "github.com/lennylabs/lenny/pkg/controller/runtime"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/statusdedup"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	sessionstore "github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// buildScheme assembles the runtime scheme the manager uses: the
// Kubernetes built-in types plus the lenny.dev/v1 CRDs the controllers
// reconcile.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	return s
}

// splitNamespaces parses a comma-separated namespace list, trimming
// whitespace and dropping empty entries. An empty or whitespace-only
// input yields a nil slice.
func splitNamespaces(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if ns := strings.TrimSpace(part); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// §4.6.1 leader-election lease parameters. The worst-case crash
// failover window is leaseDuration + renewDeadline = 25s.
const (
	leaseDuration = 15 * time.Second
	renewDeadline = 10 * time.Second
	retryPeriod   = 2 * time.Second
)

func main() {
	var (
		metricsAddr   string
		probeAddr     string
		leaderElect        bool
		leaderElectNS      string
		adapterImage       string
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
		workqueueMaxDepth       int
		devMode                 bool
		certTTL                 time.Duration
		certExpiryThreshold     time.Duration
		saTokenAudience         string
		agentServiceAccount     string
		runtimeClassStandard    string
		runtimeClassSandboxed   string
		runtimeClassMicrovm     string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"address the metrics endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"address the health and readiness probes bind to")
	flag.BoolVar(&leaderElect, "leader-elect", false,
		"run leader election so only one replica reconciles at a time")
	flag.StringVar(&leaderElectNS, "leader-election-namespace", "lenny-system",
		"namespace that holds the leader-election Lease")
	flag.StringVar(&adapterImage, "adapter-image", "",
		"the lenny-adapter sidecar image stamped into agent pods")
	flag.StringVar(&saTokenAudience, "sa-token-audience", os.Getenv("LENNY_SA_TOKEN_AUDIENCE"),
		"§10.3 deployment-specific projected-token audience (global.saTokenAudience, e.g. lenny-gateway-<cluster-name>). When set, every agent pod mounts a §6.1 audience-bound, 900s-TTL projected service-account token; empty leaves the pod without it rather than mounting a cluster-default-audience token.")
	flag.StringVar(&agentServiceAccount, "agent-service-account", os.Getenv("LENNY_AGENT_SERVICE_ACCOUNT"),
		"§10.3 zero-RBAC ServiceAccount bound to agent pods. Empty uses the namespace default SA (which carries no RBAC bindings in agent namespaces).")
	// spec: §17.5 line 3 — operators whose cluster ships the gVisor or
	// Kata RuntimeClass under a non-default name (`runsc`, `kata-qemu`,
	// `kata-fc`) override Lenny's literal defaults here so the chart's
	// `isolation.runtimeClassNames` Helm values reach the controller
	// without forcing a rename of in-cluster RuntimeClass objects.
	flag.StringVar(&runtimeClassStandard, "standard-runtime-class", os.Getenv("LENNY_STANDARD_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 standard profile. Empty uses the default 'runc'.")
	flag.StringVar(&runtimeClassSandboxed, "sandboxed-runtime-class", os.Getenv("LENNY_SANDBOXED_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 sandboxed profile. Empty uses the default 'gvisor'.")
	flag.StringVar(&runtimeClassMicrovm, "microvm-runtime-class", os.Getenv("LENNY_MICROVM_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 microvm profile. Empty uses the default 'kata'.")
	flag.StringVar(&egressCaptureImage, "egress-capture-image", os.Getenv("LENNY_EGRESS_CAPTURE_IMAGE"),
		"the §12.9.8 tier-9 lenny-egress-capture sidecar image. Empty disables capture globally. Non-empty enables injection on every Sandbox whose annotation set carries `lenny.dev/test-egress-capture-upstream`. Production rejects the sidecar via lenny-pod-security; the flag exists for tier-9 §12.9.8 credential-leakage probes.")
	flag.StringVar(&postgresDSN, "postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, the WarmPoolController mirrors Sandbox status to the §4.6.1 agent_pod_state table (the migrations/ schema must already be applied). When empty, mirroring is disabled.")
	flag.StringVar(&agentNSList, "agent-namespaces", os.Getenv("LENNY_AGENT_NAMESPACES"),
		"comma-separated agent namespaces. When set, the §13.2 NET-022 cluster-CIDR drift detector audits the broad-internet egress NetworkPolicies in these namespaces every 5 minutes. When empty, drift detection is disabled.")
	flag.StringVar(&redisURL, "redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When set, controller-emitted pool_state_changed events land on ops:events:stream alongside the gateway-emitted events. When empty, events stay in the controller-local in-memory buffer.")
	flag.StringVar(&redisPassword, "redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password.")
	flag.DurationVar(&initialFillGrace, "initial-fill-grace-period", 120*time.Second,
		"§4.6.1 cold-start fill grace period. The WarmPoolExhausted and WarmPoolLow alerts are suppressed for a pool during this window from pool creation, controller startup, or a minWarm 0→positive re-activation, to avoid false positives while the pool fills toward minWarm.")
	flag.Float64Var(&createQPS, "create-qps", ratelimit.DefaultCreateQPS,
		"§4.6.1 pod-creation rate-limiter bucket QPS. Create calls for new Sandbox pods route through this bucket so scale-up is never starved by status-update traffic.")
	flag.IntVar(&createBurst, "create-burst", ratelimit.DefaultCreateBurst,
		"§4.6.1 pod-creation rate-limiter bucket burst.")
	flag.Float64Var(&statusQPS, "status-qps", ratelimit.DefaultStatusQPS,
		"§4.6.1 status-update rate-limiter bucket QPS. UpdateStatus calls on Sandbox and SandboxWarmPool route through this bucket.")
	flag.IntVar(&statusBurst, "status-burst", ratelimit.DefaultStatusBurst,
		"§4.6.1 status-update rate-limiter bucket burst.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 1,
		"§4.6.1 number of concurrent reconciliation workers per controller. Increase for cold-start fill and cluster-restart recovery; the rate limiter remains the throughput ceiling regardless of worker count.")
	flag.DurationVar(&statusDedupWindow, "status-update-dedup-window", 500*time.Millisecond,
		"§4.6.1 statusUpdateDeduplicationWindow: the minimum interval between consecutive UpdateStatus writes for the same Sandbox. Status changes within the window are coalesced (trailing write wins), reducing etcd write pressure.")
	flag.DurationVar(&claimOrphanTimeout, "claim-orphan-timeout", 5*time.Minute,
		"§4.6.1 SandboxClaim orphan timeout: a SandboxClaim older than this with no active session is reclaimed by the leader's GarbageCollect loop. Requires --postgres-dsn for the active-session lookup.")
	flag.IntVar(&workqueueMaxDepth, "workqueue-max-depth", 500,
		"§4.6.1 controller work-queue max depth. When a controller's reconciliation queue is at this depth, new reconciliation events are dropped and lenny_controller_queue_overflow_total is incremented (requeues are never shed). A non-positive value disables work-shedding. Per-tier recommendations: 500 / 2,000 / 10,000.")
	flag.BoolVar(&devMode, "dev-mode", os.Getenv("LENNY_DEV_MODE") == "true",
		"§5.3 line 677 global.devMode. When true, a Sandbox that omits an isolation profile defaults its pod to `standard` (runc) so a developer can run on a cluster without gVisor installed. Production leaves this false (default `sandboxed`/gVisor).")
	flag.DurationVar(&certTTL, "cert-ttl", 4*time.Hour,
		"§10.3 line 338 agent-pod mTLS certificate lifetime. The §4.6.1 cert-expiry replacement derives an idle pod's certificate expiry as pod-creation-time + this TTL when the pod carries no explicit lenny.dev/cert-not-after annotation.")
	flag.DurationVar(&certExpiryThreshold, "cert-expiry-threshold", 30*time.Minute,
		"§4.6.1 / §10.3 line 342 proactive cert-replacement window. An idle pod whose certificate will expire within this duration is drained and recreated so a claim never lands on a pod with insufficient remaining certificate validity.")
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	// spec: §5.3 line 677 — log the dev-mode isolation warning once at
	// startup so an accidental production dev-mode install is visible.
	if devMode {
		log.Printf("lenny-controller: %s", isolation.DevModeIsolationWarning)
	}

	// spec: §17.5 line 3 — assemble the §5.3 isolation-profile to
	// RuntimeClass-name override map. An unset flag leaves the chart-
	// default literal (runc / gvisor / kata) in place.
	runtimeClassOverrides := map[isolation.Profile]string{}
	if runtimeClassStandard != "" {
		runtimeClassOverrides[isolation.ProfileStandard] = runtimeClassStandard
	}
	if runtimeClassSandboxed != "" {
		runtimeClassOverrides[isolation.ProfileSandboxed] = runtimeClassSandboxed
	}
	if runtimeClassMicrovm != "" {
		runtimeClassOverrides[isolation.ProfileMicrovm] = runtimeClassMicrovm
	}

	// §4.6.1 API server rate limiting: route Create calls for Sandbox
	// pods and UpdateStatus calls for Sandbox/SandboxWarmPool through two
	// dedicated client-side token buckets so pod creation is never starved
	// by status-update traffic; all other requests share the default
	// limiter.
	restCfg := ratelimit.WrapConfig(ctrl.GetConfigOrDie(), ratelimit.Config{
		CreateQPS:   createQPS,
		CreateBurst: createBurst,
		StatusQPS:   statusQPS,
		StatusBurst: statusBurst,
	})

	ld, rd, rp := leaseDuration, renewDeadline, retryPeriod
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                        buildScheme(),
		Metrics:                       metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                leaderElect,
		LeaderElectionID:              "lenny-warm-pool-controller",
		LeaderElectionNamespace:       leaderElectNS,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &ld,
		RenewDeadline:                 &rd,
		RetryPeriod:                   &rp,
	})
	if err != nil {
		log.Fatalf("lenny-controller: create manager: %v", err)
	}

	// The §4.6.1 agent_pod_state mirror is durable under --postgres-dsn:
	// the WarmPoolController writes the Postgres-side copy of Sandbox
	// status that the gateway's fallback claim path reads. When the flag
	// is empty the mirror store is left nil and the controller skips
	// mirroring.
	var mirror *agentpodstatepg.Store
	var sessionLookup warmpool.SessionLookup
	var runtimeRegistry *runtimepg.Store
	if postgresDSN != "" {
		pgPool, err := pgxpool.New(context.Background(), postgresDSN)
		if err != nil {
			log.Fatalf("lenny-controller: postgres: %v", err)
		}
		defer pgPool.Close()
		mirror = agentpodstatepg.New(pgPool)
		// The §4.6.1 orphan-claim GC checks Postgres for an active session
		// backing each candidate claim before reclaiming it.
		sessionLookup = &sessionActiveLookup{store: sessionstorepg.New(pgPool)}
		// §5.1 Runtime CRD mirror: the RuntimeReconciler writes the
		// declarative Runtime resources into the same runtime_definitions
		// registry the gateway reads at session creation. Requires the
		// Postgres registry, so it is wired only under --postgres-dsn.
		runtimeRegistry = runtimepg.New(pgPool)
	}

	// §4.0 pool state manager: the controller emits §16.6
	// pool_state_changed events on every derived PoolPhase transition.
	// §25.5: when --redis-url is set, every emit also lands on the
	// platform-scoped ops:events:stream alongside the gateway-emitted
	// events; lenny-ops reads from that one stream. When Redis is not
	// configured the emitter writes only to the controller-local
	// in-memory ring buffer — the §25.5 per-replica buffer fall-back.
	controllerReplicaID := os.Getenv("HOSTNAME")
	if controllerReplicaID == "" {
		controllerReplicaID = "controller"
	}
	opsEventBuffer := events.NewEventBuffer(0)
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, controllerReplicaID)
	if redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: redisURL, Password: redisPassword})
		if err != nil {
			log.Fatalf("lenny-controller: redis client: %v", err)
		}
		defer func() { _ = redisClient.Close() }()
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/controller/" + controllerReplicaID,
			ReplicaID: controllerReplicaID,
		})
		log.Printf("lenny-controller: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}

	// §4.6.1 bounded, depth-instrumented reconciliation work queue. The
	// same factory is shared by both controllers; each queue is named after
	// its controller, so the lenny_controller_workqueue_depth gauge is
	// labeled per controller.
	queueFactory := controllermetrics.NewQueueFactory(workqueueMaxDepth)

	warmPool := &warmpool.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Events: opsEmitter,
		// §5.3 line 675: validate the pool's RuntimeClass exists before
		// sizing it. The reader-backed checker uses the manager's uncached
		// API reader so the RuntimeClass get needs only the `get` verb the
		// §4.6.3 controller RBAC grants, not a cluster-wide watch.
		RuntimeClasses:            warmpool.NewReaderRuntimeClassChecker(mgr.GetAPIReader()),
		RuntimeClassNameOverrides: runtimeClassOverrides,
		InitialFillGracePeriod:    initialFillGrace,
		MaxConcurrentReconciles:   maxConcurrentReconciles,
		QueueFactory:              queueFactory,
	}
	// A nil *agentpodstatepg.Store assigned to the agentpodstate.Store
	// interface field would be a non-nil interface; only assign when a
	// store was actually constructed so the controller's nil check holds.
	if mirror != nil {
		warmPool.Mirror = mirror
	}
	if err := warmPool.SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up WarmPoolController: %v", err)
	}

	if err := (&sandbox.Reconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		AdapterImage:              adapterImage,
		EgressCaptureImage:        egressCaptureImage,
		DevMode:                   devMode,
		SATokenAudience:           saTokenAudience,
		AgentServiceAccountName:   agentServiceAccount,
		RuntimeClassNameOverrides: runtimeClassOverrides,
		StatusDedup:               statusdedup.New(statusDedupWindow),
		MaxConcurrentReconciles:   maxConcurrentReconciles,
		QueueFactory:              queueFactory,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up Sandbox reconciler: %v", err)
	}

	// §4.6.1 per-pod reconciler: the WarmPoolController is the sole
	// evaluator of per-pod host-node schedulability (the
	// lenny.dev/host-schedulable label the §6.2 task_cleanup precondition
	// reads) and of idle-pod certificate expiry (proactive drain-and-
	// recreate of any idle pod inside the cert-replacement window). It
	// watches managed Pods and Nodes; the Node watch re-labels every pod
	// on a node when the node is cordoned or uncordoned.
	if err := (&warmpool.PodReconciler{
		Client:                  mgr.GetClient(),
		CertTTL:                 certTTL,
		CertExpiryThreshold:     certExpiryThreshold,
		MaxConcurrentReconciles: maxConcurrentReconciles,
		QueueFactory:            queueFactory,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up per-pod reconciler: %v", err)
	}

	// §5.1 RuntimeReconciler: mirror declarative Runtime CRDs into the
	// gateway runtime registry and set each one's Registered condition.
	// The reconciler needs the Postgres registry, so it is registered
	// only when --postgres-dsn is set; an in-memory-only controller posture
	// has no shared registry to mirror into.
	if runtimeRegistry != nil {
		if err := (&runtimecontroller.Reconciler{
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			Store:                   runtimeRegistry,
			DevMode:                 devMode,
			MaxConcurrentReconciles: maxConcurrentReconciles,
			QueueFactory:            queueFactory,
		}).SetupWithManager(mgr); err != nil {
			log.Fatalf("lenny-controller: set up RuntimeReconciler: %v", err)
		}
	}

	// §4.6.1 mirror reconciliation on recovery and orphan-claim GC are
	// leader-elected runnables scoped to the agent namespaces. Both
	// require Postgres: mirror recovery converges the agent_pod_state
	// table, and the GC loop checks the session store before reclaiming a
	// claim. They are registered only when both --postgres-dsn and
	// --agent-namespaces are set.
	agentNamespaces := splitNamespaces(agentNSList)
	if mirror != nil && len(agentNamespaces) > 0 {
		if err := mgr.Add(&warmpool.MirrorReconciler{
			Client:     mgr.GetClient(),
			Mirror:     mirror,
			Namespaces: agentNamespaces,
		}); err != nil {
			log.Fatalf("lenny-controller: set up mirror reconciler: %v", err)
		}
		if err := mgr.Add(&warmpool.ClaimGarbageCollector{
			Client:        mgr.GetClient(),
			Sessions:      sessionLookup,
			Namespaces:    agentNamespaces,
			OrphanTimeout: claimOrphanTimeout,
		}); err != nil {
			log.Fatalf("lenny-controller: set up orphan-claim GC: %v", err)
		}
	}

	// The §13.2 NET-022 cluster-CIDR drift detector is a leader-elected
	// Runnable: every 5 minutes it compares the cluster's actual pod
	// CIDRs against the broad-internet egress NetworkPolicies in the
	// agent namespaces and increments lenny_network_policy_cidr_drift_total
	// on drift. It runs only when --agent-namespaces is set.
	if len(agentNamespaces) > 0 {
		if err := mgr.Add(&cidrdrift.Detector{
			Client:          mgr.GetClient(),
			AgentNamespaces: agentNamespaces,
		}); err != nil {
			log.Fatalf("lenny-controller: set up cluster-CIDR drift detector: %v", err)
		}
	}

	// §4.6.1 controller leader-election monitoring: publish the
	// lenny_controller_leader_lease_renewal_age_seconds gauge so the §16.5
	// ControllerLeaderElectionFailed alert can detect a renewal stall. The
	// lease exists only under leader election, so the monitor is registered
	// only then. The monitor uses the manager's uncached API reader so the
	// Lease read needs only the `get` verb the §4.6.3 RBAC grants.
	if leaderElect {
		if err := mgr.Add(&controllermetrics.LeaseRenewalMonitor{
			Reader:     mgr.GetAPIReader(),
			Namespace:  leaderElectNS,
			Name:       "lenny-warm-pool-controller",
			Controller: "lenny-warm-pool-controller",
		}); err != nil {
			log.Fatalf("lenny-controller: set up leader-lease monitor: %v", err)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add healthz check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add readyz check: %v", err)
	}

	log.Printf("lenny-controller: starting manager (leader-election=%t)", leaderElect)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("lenny-controller: manager exited: %v", err)
	}
}

// sessionActiveLookup adapts the session store to the §4.6.1 orphan-claim
// GC's warmpool.SessionLookup contract: a claim is reclaimable when no
// non-terminal session backs it. A missing session row (the gateway
// crashed before persisting the session) and a terminal session both
// report inactive.
type sessionActiveLookup struct {
	store sessionstore.Store
}

func (l *sessionActiveLookup) SessionActive(ctx context.Context, tenantID, sessionID string) (bool, error) {
	sess, err := l.store.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !apisession.IsTerminal(sess.State), nil
}
