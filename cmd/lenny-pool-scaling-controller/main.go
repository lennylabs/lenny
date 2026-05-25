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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	poolstorepg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
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
	// v1 runs every pool in §4.6.2 bootstrap mode: no DemandSource is
	// wired, so each pool holds at its operator-set warmCount floor until
	// a deployer supplies observed-demand metrics. The SDK-warm circuit
	// breaker honors any state already persisted on a pool's status even
	// without a DemotionRateSource.
	reconciler := &poolscaling.Reconciler{
		Client: mgr.GetClient(),
		Source: source,
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
