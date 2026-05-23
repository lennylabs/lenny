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
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/events"
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
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	ld, rd, rp := leaseDuration, renewDeadline, retryPeriod
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
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
	if postgresDSN != "" {
		pool, err := pgxpool.New(context.Background(), postgresDSN)
		if err != nil {
			log.Fatalf("lenny-controller: postgres: %v", err)
		}
		defer pool.Close()
		mirror = agentpodstatepg.New(pool)
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

	warmPool := &warmpool.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Events: opsEmitter,
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
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		AdapterImage:       adapterImage,
		EgressCaptureImage: egressCaptureImage,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up Sandbox reconciler: %v", err)
	}

	// The §13.2 NET-022 cluster-CIDR drift detector is a leader-elected
	// Runnable: every 5 minutes it compares the cluster's actual pod
	// CIDRs against the broad-internet egress NetworkPolicies in the
	// agent namespaces and increments lenny_network_policy_cidr_drift_total
	// on drift. It runs only when --agent-namespaces is set.
	if agentNamespaces := splitNamespaces(agentNSList); len(agentNamespaces) > 0 {
		if err := mgr.Add(&cidrdrift.Detector{
			Client:          mgr.GetClient(),
			AgentNamespaces: agentNamespaces,
		}); err != nil {
			log.Fatalf("lenny-controller: set up cluster-CIDR drift detector: %v", err)
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
