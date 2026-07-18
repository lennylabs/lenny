// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// buildProcessSetup configures the §25.4 process-wide state: the
// remediation-lock TTL policy, the replica identity, the structured JSON
// logger, the SIGTERM/SIGINT-bounded root context, and the §16.3 tracing
// provider. The trace-provider shutdown is recorded on the accumulator so
// runOps can defer it. spec: §25.4, §16.3.
func (w *opsWiring) buildProcessSetup() {
	// spec: §25.4 ops.locks.{minTTLSeconds,defaultTTLSeconds,maxTTLSeconds}.
	// Configure the deployment-wide remediation-lock TTL policy once, before
	// the lock Service (and its tier stores) is built, so every tier clamps
	// requested TTLs identically. A zero value keeps the built-in bound. F-25.4.9.
	coordination.SetTTLBounds(*w.f.locksMinTTL, *w.f.locksDefaultTTL, *w.f.locksMaxTTL)

	// Replica identity: the pod name (the Helm chart sets POD_NAME from
	// the downward API), falling back to the hostname.
	w.replicaID = envOr("POD_NAME", "")
	if w.replicaID == "" {
		w.replicaID, _ = os.Hostname()
	}
	if w.replicaID == "" {
		w.replicaID = "lenny-ops"
	}

	// §25.4 lines 2499-2526: install the structured JSON logger. Every
	// log line carries ts / level / msg / component=lenny-ops; lines
	// emitted from a request context also carry operation_id, agent_name,
	// and trace_id pulled from the §25.2 X-Lenny-Operation-ID,
	// X-Lenny-Agent-Name, and traceparent headers (stamped by the
	// opsserver correlation middleware).
	configureStructuredLogging()

	// Root context cancelled on SIGTERM/SIGINT; it bounds the background
	// loops and the leader-election goroutine.
	w.ctx, w.stop = signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	// spec: §16.3 line 359 — install the process-wide TracerProvider and
	// W3C propagator so lenny-ops spans reach the OTLP Collector instead of
	// the no-op provider. With no OTEL endpoint a stdout exporter is used.
	// F-16.3.2.
	traceShutdown, err := tracing.InitProvider(w.ctx, tracing.ProviderConfig{
		ServiceName:  "lenny-ops",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	if err != nil {
		log.Fatalf("lenny-ops: tracing init: %v", err)
	}
	w.traceShutdown = traceShutdown
}

// buildDependencies constructs the §25.4 backing dependencies: the optional
// Postgres pool and Redis client, the §12.6 store router and §11.7 platform
// audit recorder, the Kubernetes clients, the dependency probes, the §25.7
// runbook index, and the §25.4 leader elector. Each degrades gracefully when
// its dependency is absent, the single-process dev path. spec: §25.4.
func (w *opsWiring) buildDependencies() {
	// Postgres: optional. §25.4 has lenny-ops degrade gracefully when
	// Postgres is unavailable, so a missing DSN is not fatal.
	if *w.f.postgresDSN != "" {
		pool, err := pgxpool.New(w.ctx, *w.f.postgresDSN)
		if err != nil {
			log.Fatalf("lenny-ops: postgres: %v", err)
		}
		w.pgPool = pool
	}

	// Redis: optional. The §25.5 event stream falls back to the gateway
	// buffer when Redis is absent. Direct mode (--redis-url) and
	// Sentinel mode (--redis-sentinel-addrs) are mutually exclusive.
	if *w.f.redisURL != "" && *w.f.redisSentinelAddrs != "" {
		log.Fatalf("lenny-ops: --redis-url and --redis-sentinel-addrs are mutually exclusive")
	}
	if *w.f.redisURL != "" || *w.f.redisSentinelAddrs != "" {
		var rcfg redisconn.Config
		switch {
		case *w.f.redisURL != "":
			rcfg = redisconn.Config{URL: *w.f.redisURL, Password: *w.f.redisPassword, AllowInsecure: *w.f.redisAllowInsecure}
		default:
			rcfg = redisconn.Config{
				SentinelAddrs:    splitAndTrim(*w.f.redisSentinelAddrs),
				MasterName:       *w.f.redisSentinelMaster,
				Password:         *w.f.redisPassword,
				SentinelPassword: *w.f.redisSentinelPassword,
				TLS:              *w.f.redisTLS,
				AllowInsecure:    *w.f.redisAllowInsecure,
			}
		}
		client, err := redisconn.NewClient(rcfg)
		if err != nil {
			log.Fatalf("lenny-ops: redis client: %v", err)
		}
		w.redisClient = client
	}

	// §12.6 StoreRouter + §11.7 durable platform-audit recorder. lenny-ops
	// accesses platform Postgres/Redis through the single-shard router so
	// the §12.3 R-03 audit-write path routes via AuditShard(); the recorder
	// commits every ops_event.* audit event (remediation-lock lifecycle,
	// escalation flush, self-health transitions, identity discovery,
	// operations-inventory queries, plus the §25.6/§25.10/§25.11/§25.8
	// diagnostics/drift/backup/upgrade events) to the platform §11.7 hash
	// chain. Without Postgres both degrade gracefully: the router is nil and
	// the recorder logs each event so single-process dev stays observable.
	// F-25.4.14, F-25.4.22.
	w.storeRouter = buildStoreRouter(w.pgPool, w.redisClient)
	w.auditRecorder = buildPlatformAuditRecorder(w.storeRouter)

	// Kubernetes API: the §25.4 required dependency for diagnostics,
	// upgrade orchestration, backup Jobs, and leader election. When no
	// cluster connection is available lenny-ops still serves the HTTP
	// surface (the K8s probe reports unreachable) and skips leader
	// election — a single-process degraded mode for local development.
	var clientset *kubernetes.Clientset
	var dynClient dynamic.Interface
	var apiextClient apiextensionsclientset.Interface
	if cfg, err := ctrlconfig.GetConfig(); err != nil {
		log.Printf("lenny-ops: no Kubernetes config (%v); running without leader election", err)
	} else if cs, err := kubernetes.NewForConfig(cfg); err != nil {
		log.Printf("lenny-ops: build Kubernetes clientset: %v; running without leader election", err)
	} else {
		clientset = cs
		// The §25.8 cert-manager probe reads the cert-manager Certificate
		// CRs through a dynamic client; a build failure leaves the probe
		// unconfigured (reports healthy) rather than failing startup.
		if dc, err := dynamic.NewForConfig(cfg); err != nil {
			log.Printf("lenny-ops: build Kubernetes dynamic client: %v; cert-manager probe disabled", err)
		} else {
			dynClient = dc
		}
		// The §25.8 version-aggregation CRD source reads the
		// `lenny.dev/schema-version` annotation on the installed CRDs
		// through the apiextensions clientset; a build failure leaves the
		// CRD-version component out of the report rather than failing
		// startup.
		if ac, err := apiextensionsclientset.NewForConfig(cfg); err != nil {
			log.Printf("lenny-ops: build apiextensions clientset: %v; CRD-version source disabled", err)
		} else {
			apiextClient = ac
		}
	}
	w.clientset = clientset
	w.dynClient = dynClient
	w.apiextClient = apiextClient

	// The §25.4 dependency probes feed the readiness signal and the
	// §25.6 connectivity diagnostic.
	w.gatewayHTTP = &http.Client{Timeout: 5 * time.Second}
	w.probes = map[string]probe.Func{
		opsservice.ProbePostgres: opsservice.PostgresProbe(w.pgPool),
		opsservice.ProbeRedis:    opsservice.RedisProbe(w.redisClient),
		// spec: §25.2 line 169 — lenny-ops connects to MinIO and Prometheus;
		// the §25.6 connectivity report names both. Each probe is registered
		// unconditionally and reports "not configured" when its endpoint is
		// empty, so the report is honest about object-storage / metrics
		// availability rather than silently omitting the dependency. F-25.2.10.
		opsservice.ProbeMinIO:      opsservice.MinIOProbe(w.gatewayHTTP, *w.f.backupMinIOEndpoint),
		opsservice.ProbePrometheus: opsservice.PrometheusProbe(w.gatewayHTTP, *w.f.prometheusURL),
	}
	if w.clientset != nil {
		w.probes[opsservice.ProbeK8sAPI] = opsservice.K8sAPIProbe(w.clientset.Discovery())
	}
	if *w.f.gatewayURL != "" {
		// spec: §25.6 lines 2905-2906 — the §25.6 connectivity check
		// "probes the gateway admin API itself (GET
		// /v1/admin/health/summary)", the gateway's aggregated §25.3
		// dependency-health endpoint, rather than the liveness-only
		// /healthz.
		w.probes[opsservice.ProbeGateway] = opsservice.GatewayProbe(w.gatewayHTTP, *w.f.gatewayURL+"/v1/admin/health/summary")
	}

	// The §25.7 runbook index, read from docs/runbooks/.
	if src, err := opsserver.LoadRunbookDir(*w.f.runbookDir); err != nil {
		log.Printf("lenny-ops: runbook index unavailable: %v", err)
	} else {
		w.runbookSource = src
		log.Printf("lenny-ops: indexed %d runbooks from %s", len(src.Runbooks()), *w.f.runbookDir)
	}

	// The §25.4 leader elector. Without a clientset, a noop elector
	// keeps lenny-ops a follower so the leader-only loops never start.
	var elector opsservice.Elector = noopElector{}
	if w.clientset != nil {
		le, err := opsservice.NewLeaseElector(*w.f.leaderElectNS, w.replicaID,
			w.clientset.CoreV1(), w.clientset.CoordinationV1(),
			// spec: §25.4 ops.leaderElection.{leaseDurationSeconds,renewDeadlineSeconds,
			// retryPeriodSeconds}. Zero fields keep the built-in 15s/10s/2s. F-25.4.9.
			opsservice.LeaseTimings{
				LeaseDuration: time.Duration(*w.f.leaderLeaseDuration) * time.Second,
				RenewDeadline: time.Duration(*w.f.leaderRenewDeadline) * time.Second,
				RetryPeriod:   time.Duration(*w.f.leaderRetryPeriod) * time.Second,
			})
		if err != nil {
			log.Fatalf("lenny-ops: build leader elector: %v", err)
		}
		elector = le
	}
	w.elector = elector
}
