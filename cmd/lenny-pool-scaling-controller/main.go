// SPDX-License-Identifier: MIT

// Command lenny-pool-scaling-controller runs the §4.6.2
// PoolScalingController against a Kubernetes API server. It treats the
// admin API's Postgres pool definitions as the source of truth and
// reconciles them into the SandboxTemplate and SandboxWarmPool CRD pair
// the WarmPoolController consumes.
//
// The controller runs as its own workload with its own leader-election
// lease (`lenny-pool-scaling-controller`), separate from the
// WarmPoolController's lease, so the two elect leaders independently and
// may run on different replicas (§4.6.2). It is deployed under the
// dedicated ServiceAccount system:serviceaccount:lenny-system:lenny-pool-scaling-controller
// so that its CRD spec writes pass the §4.6.3 pool-config-validator
// rule-set-2 authorization check (manual writes by any other principal
// are rejected; only the PSC SA may write the derived spec fields).
//
// Usage:
//
//	lenny-pool-scaling-controller --leader-elect --agent-namespace lenny-agents
//
// The pool definitions are read from Postgres (--postgres-dsn). Without
// a DSN the controller has no source of truth and exits.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"
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

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	poolstorepg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/metrics"
)

// §4.6.2 leader-election lease parameters, matching the
// WarmPoolController: a 25s worst-case crash-failover window
// (leaseDuration + renewDeadline).
const (
	leaseDuration = 15 * time.Second
	renewDeadline = 10 * time.Second
	retryPeriod   = 2 * time.Second
)

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	return s
}

func main() {
	var (
		metricsAddr   string
		probeAddr     string
		leaderElect   bool
		leaderElectNS string
		postgresDSN   string
		agentNS       string
		syncInterval  time.Duration
		capTier       string
		capPlanning   poolscaling.CapacityPlanning
		safetyFactor  float64
		prometheusURL string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"address the metrics endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"address the health and readiness probes bind to")
	flag.BoolVar(&leaderElect, "leader-elect", false,
		"run leader election so only one replica reconciles at a time")
	flag.StringVar(&leaderElectNS, "leader-election-namespace", "lenny-system",
		"namespace that holds the leader-election Lease")
	flag.StringVar(&postgresDSN, "postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. The §4.6.2 PoolScalingController reads the admin API's pool definitions (the source of truth) from this database and reconciles them into the SandboxTemplate/SandboxWarmPool CRD pair. Required.")
	flag.StringVar(&agentNS, "agent-namespace", os.Getenv("LENNY_AGENT_NAMESPACE"),
		"agent namespace the derived SandboxTemplate/SandboxWarmPool CRDs are materialized in. Required.")
	flag.DurationVar(&syncInterval, "sync-interval", 30*time.Second,
		"§4.6.2 pool-config reconcile cadence. The PSC re-syncs Postgres pool definitions into CRDs on this timer because Kubernetes cannot watch the Postgres source of truth.")
	// §16.5 lines 590-601 — the workload-profile assumptions the tier
	// sizing formulas depend on. The PSC reads them at startup and warns
	// when a Tier 2 / Tier 3 deployment still runs the defaults. The
	// chart renders these from the capacityPlanning.* Helm values.
	defCP := poolscaling.DefaultCapacityPlanning()
	flag.StringVar(&capTier, "capacity-tier", envOr("LENNY_CAPACITY_TIER", "tier1"),
		"§17.8.2 capacity tier (tier1, tier2, tier3) the deployment is sized for. A Tier 2 / Tier 3 deployment running the capacityPlanning.* defaults gets the §16.5 line 601 startup warning.")
	flag.Float64Var(&capPlanning.AvgSessionDurationSeconds, "capacity-avg-session-duration-seconds", envFloatOr("LENNY_CAPACITY_AVG_SESSION_DURATION_SECONDS", defCP.AvgSessionDurationSeconds),
		"§16.5 line 594 capacityPlanning.avgSessionDurationSeconds — average session duration feeding the warm-pool claim rate.")
	flag.Float64Var(&capPlanning.DelegationParticipationRate, "capacity-delegation-participation-rate", envFloatOr("LENNY_CAPACITY_DELEGATION_PARTICIPATION_RATE", defCP.DelegationParticipationRate),
		"§16.5 line 595 capacityPlanning.delegationParticipationRate — fraction of sessions that delegate at least once.")
	flag.Float64Var(&capPlanning.AvgDelegationsPerDelegatingSession, "capacity-avg-delegations-per-delegating-session", envFloatOr("LENNY_CAPACITY_AVG_DELEGATIONS_PER_DELEGATING_SESSION", defCP.AvgDelegationsPerDelegatingSession),
		"§16.5 line 596 capacityPlanning.avgDelegationsPerDelegatingSession — average child delegations per delegating session.")
	flag.Float64Var(&capPlanning.AvgChildSessionSeconds, "capacity-avg-child-session-seconds", envFloatOr("LENNY_CAPACITY_AVG_CHILD_SESSION_SECONDS", defCP.AvgChildSessionSeconds),
		"§16.5 line 597 capacityPlanning.avgChildSessionSeconds — average child-delegation session duration.")
	flag.Float64Var(&capPlanning.AvgWorkspaceSizeMB, "capacity-avg-workspace-size-mb", envFloatOr("LENNY_CAPACITY_AVG_WORKSPACE_SIZE_MB", defCP.AvgWorkspaceSizeMB),
		"§16.5 line 598 capacityPlanning.avgWorkspaceSizeMB — average workspace size feeding checkpoint bandwidth budgets.")
	flag.Float64Var(&capPlanning.SessionIdleFraction, "capacity-session-idle-fraction", envFloatOr("LENNY_CAPACITY_SESSION_IDLE_FRACTION", defCP.SessionIdleFraction),
		"§16.5 line 599 capacityPlanning.sessionIdleFraction — fraction of active sessions idle (no active LLM call).")
	// §17.8.2 line 1008 poolScaling.safetyFactor — the agent-type
	// safety_factor applied to a pool that does not pin one. A non-positive
	// value (the default) resolves the per-tier default from --capacity-tier
	// (1.2 at Tier 3, 1.5 otherwise) so a Tier 3 deployment picks up the
	// override automatically; a positive value pins it for every pool.
	flag.Float64Var(&safetyFactor, "default-safety-factor", envFloatOr("LENNY_POOL_SCALING_SAFETY_FACTOR", 0),
		"§17.8.2 line 1008 poolScaling.safetyFactor — the agent-type safety_factor applied to pools that do not pin one. 0 (default) resolves the per-tier default from --capacity-tier (1.2 at Tier 3, else 1.5).")
	flag.StringVar(&prometheusURL, "prometheus-url", os.Getenv("LENNY_PROMETHEUS_URL"),
		"§6.1 Prometheus HTTP API base URL the SDK-warm circuit breaker reads the rolling demotion rate from (lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total). When unset, the breaker only honors a state already persisted on a pool and never auto-trips (the v1 bootstrap default).")
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	if postgresDSN == "" {
		log.Fatalf("lenny-pool-scaling-controller: --postgres-dsn is required (the §4.6.2 source of truth)")
	}
	if agentNS == "" {
		log.Fatalf("lenny-pool-scaling-controller: --agent-namespace is required")
	}

	// spec: §16.5 line 601 — a Tier 2 / Tier 3 deployment running the
	// capacityPlanning.* workload-profile defaults has not substituted
	// observed values, so the tier sizing formulas rest on first-
	// principles assumptions. Surface the warning at startup so an
	// operator promoting to production reviews the profile.
	if poolscaling.ShouldWarnCapacityPlanningDefaults(capPlanning, capTier) {
		log.Printf("lenny-pool-scaling-controller: %s", poolscaling.CapacityPlanningDefaultsWarning)
	}

	ld, rd, rp := leaseDuration, renewDeadline, retryPeriod
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                        buildScheme(),
		Metrics:                       metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                leaderElect,
		LeaderElectionID:              "lenny-pool-scaling-controller",
		LeaderElectionNamespace:       leaderElectNS,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &ld,
		RenewDeadline:                 &rd,
		RetryPeriod:                   &rp,
	})
	if err != nil {
		log.Fatalf("lenny-pool-scaling-controller: create manager: %v", err)
	}

	pgPool, err := pgxpool.New(context.Background(), postgresDSN)
	if err != nil {
		log.Fatalf("lenny-pool-scaling-controller: postgres: %v", err)
	}
	defer pgPool.Close()

	source := &poolscaling.PoolStoreSource{
		Store:     poolstorepg.New(pgPool),
		Namespace: agentNS,
	}
	// spec: §6.1 lines 48, 50 — when a Prometheus backend is configured the
	// SDK-warm circuit breaker reads the rolling demotion rate from it and
	// auto-trips at 90%; the SDKWarmDemotionRateHigh event fires at the
	// 60% 1-hour threshold. Without --prometheus-url the breaker still
	// honors a state already persisted on a pool's status but never
	// auto-trips (the v1 bootstrap default).
	var demotionSource poolscaling.DemotionRateSource
	// spec: §5.2 line 573 — when a Prometheus backend is configured the
	// controller derives concurrent-stateless pools' demand from
	// rate(lenny_stateless_requests_total[5m]) and
	// max_over_time(lenny_stateless_concurrent_active[5m]). The same
	// source reports Observed=false for every non-stateless pool (which
	// emits no stateless series), so wiring it leaves session / task /
	// workspace-concurrent pools in bootstrap mode while activating
	// demand-driven scaling for stateless pools. F-5.2.29.
	var demandSource poolscaling.DemandSource
	if prometheusURL != "" {
		promClient, perr := metrics.NewPrometheusClient(metrics.PrometheusConfig{BaseURL: prometheusURL})
		if perr != nil {
			log.Fatalf("lenny-pool-scaling-controller: prometheus client: %v", perr)
		}
		demotionSource = &poolscaling.PrometheusDemotionSource{Querier: promClient}
		demandSource = &poolscaling.PrometheusStatelessDemandSource{Querier: promClient}
		log.Printf("lenny-pool-scaling-controller: §6.1 SDK-warm demotion source wired to %s", prometheusURL)
		log.Printf("lenny-pool-scaling-controller: §5.2 line 573 stateless demand source wired to %s", prometheusURL)
	}
	// Without --prometheus-url every pool runs in §4.6.2 bootstrap mode:
	// no DemandSource is wired, so each pool holds at its operator-set
	// warmCount floor until observed-demand metrics are available.
	// spec: §17.8.2 line 1008 — resolve the agent-type safety_factor a pool
	// inherits when it does not pin one. An explicit --default-safety-factor
	// pins it for every pool; 0 selects the per-tier default so a Tier 3
	// deployment automatically applies the 1.2 override.
	effectiveSafetyFactor := safetyFactor
	if effectiveSafetyFactor <= 0 {
		effectiveSafetyFactor = poolscaling.DefaultSafetyFactorForTier(capTier)
	}
	log.Printf("lenny-pool-scaling-controller: §17.8.2 agent-type default safety_factor=%.2f (capacity-tier=%s)", effectiveSafetyFactor, capTier)

	reconciler := &poolscaling.Reconciler{
		Client:              mgr.GetClient(),
		Source:              source,
		Demand:              demandSource,
		Demotion:            demotionSource,
		Events:              mgr.GetEventRecorderFor("lenny-pool-scaling-controller"),
		DefaultSafetyFactor: effectiveSafetyFactor,
	}
	if err := mgr.Add(&poolscaling.Runnable{Reconciler: reconciler, Interval: syncInterval}); err != nil {
		log.Fatalf("lenny-pool-scaling-controller: add reconciler: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("lenny-pool-scaling-controller: add healthz check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("lenny-pool-scaling-controller: add readyz check: %v", err)
	}

	log.Printf("lenny-pool-scaling-controller: starting manager (leader-election=%t)", leaderElect)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("lenny-pool-scaling-controller: manager exited: %v", err)
	}
}

// envOr returns the env var named name, or def when it is unset or
// blank. It supplies the §16.5 capacityPlanning flag defaults from the
// chart-rendered environment.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// envFloatOr returns the env var named name parsed as a float64, or def
// when it is unset, blank, or unparseable. A malformed value falls back
// to the default rather than failing the controller, since the
// workload-profile values are advisory sizing inputs.
func envFloatOr(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
