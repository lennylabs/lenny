// SPDX-License-Identifier: MIT

// Command lenny-ops runs the §25 operability service: a Deployment
// separate from the gateway that hosts the operability endpoints
// reading durable state (Postgres, Redis, the Kubernetes API,
// Prometheus). §25 makes lenny-ops mandatory in every Lenny
// installation; it is reachable only from outside the cluster via an
// Ingress, never from internal cluster workloads.
//
// lenny-ops runs as a Deployment with one or more replicas. The §25.4
// service body has two parts: the HTTP surface (pkg/ops/opsserver),
// which every replica serves, and the leader-elected background loops
// (pkg/ops/opsservice) — the cron evaluator, the webhook delivery
// worker, the scheduled-backup runner, and the reconciliation
// goroutines — which only the replica holding the lenny-ops-leader
// Lease runs. Every replica also runs its own §25.4 self-monitor.
//
// Usage:
//
//	lenny-ops --addr :8090 --leader-election-namespace lenny-system \
//	  --postgres-dsn $LENNY_POSTGRES_DSN --redis-url $LENNY_REDIS_URL
//
// The cluster connection is resolved from the in-cluster service
// account when running as a pod, or from KUBECONFIG otherwise. When no
// cluster connection is available the binary still serves the HTTP
// surface in degraded mode and skips leader election.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

func main() {
	addr := flag.String("addr", ":8090", "address the lenny-ops HTTP server binds to")
	postgresDSN := flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, lenny-ops uses it for audit, backup, "+
			"and upgrade state; when empty those features degrade. Override via LENNY_POSTGRES_DSN.")
	redisURL := flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When empty the "+
			"event stream falls back to the gateway buffer. Mutually exclusive with "+
			"--redis-sentinel-addrs. Override via LENNY_REDIS_URL.")
	redisSentinelAddrs := flag.String("redis-sentinel-addrs", os.Getenv("LENNY_REDIS_SENTINEL_ADDRS"),
		"Comma-separated list of §12.8 Redis Sentinel host:port pairs. When set with "+
			"--redis-sentinel-master, lenny-ops discovers the master via Sentinel and follows "+
			"automatic failover. Mutually exclusive with --redis-url.")
	redisSentinelMaster := flag.String("redis-sentinel-master", os.Getenv("LENNY_REDIS_SENTINEL_MASTER"),
		"§12.8 Redis Sentinel monitored master name (e.g., lenny-master). Required when "+
			"--redis-sentinel-addrs is set.")
	redisPassword := flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password applied to both direct and Sentinel modes. §12.4 requires AUTH; an empty password fails startup unless --redis-allow-insecure is set. Override via LENNY_REDIS_PASSWORD.")
	redisSentinelPassword := flag.String("redis-sentinel-password", os.Getenv("LENNY_REDIS_SENTINEL_PASSWORD"),
		"AUTH password for the sentinels themselves. Optional; sentinels typically run unauthenticated.")
	redisTLS := flag.Bool("redis-tls", envBool("LENNY_REDIS_TLS", false),
		"§12.4 request TLS on the Sentinel path. The direct-URL path derives TLS from the rediss:// scheme instead. TLS is mandatory unless --redis-allow-insecure is set. Override via LENNY_REDIS_TLS.")
	redisAllowInsecure := flag.Bool("redis-allow-insecure", envBool("LENNY_REDIS_ALLOW_INSECURE", false),
		"§12.4 opt out of the mandatory Redis AUTH-and-TLS startup invariant. Defaults off; set only for a dev or local Redis. Override via LENNY_REDIS_ALLOW_INSECURE.")
	gatewayURL := flag.String("gateway-url", os.Getenv("LENNY_GATEWAY_URL"),
		"§25.4 gateway admin API base URL (the internal ClusterIP Service). Used for the "+
			"connectivity probe and gateway-backed diagnostics. Override via LENNY_GATEWAY_URL.")
	leaderElectNS := flag.String("leader-election-namespace", envOr("LENNY_LEADER_ELECTION_NAMESPACE", "lenny-system"),
		"namespace that holds the §25.4 lenny-ops-leader Lease")
	runbookDir := flag.String("runbook-dir", envOr("LENNY_RUNBOOK_DIR", "docs/runbooks"),
		"directory of §25.7 operational-runbook markdown files the runbook index serves")
	selfHealthInterval := flag.Duration("self-health-interval", 10*time.Second,
		"§25.4 ops.selfHealth.checkIntervalSeconds — how often the self-monitor runs")
	memoryLimitBytes := flag.Int64("memory-limit-bytes", envInt64("LENNY_MEMORY_LIMIT_BYTES", 0),
		"§25.4 container memory limit in bytes for the memory_pressure self-health check; "+
			"0 disables the check. Override via LENNY_MEMORY_LIMIT_BYTES.")
	production := flag.Bool("production", envBool("LENNY_PRODUCTION", false),
		"§25.11: when set, a full backup requires confirm:true. Override via LENNY_PRODUCTION.")
	releaseChannelKeyPath := flag.String("release-channel-key-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_KEY_FILE"),
		"path to the PEM file carrying the §25.8 release-channel Ed25519 signing key (PKCS8). "+
			"When empty the release-channel publisher is not registered and GET /v1/latest returns 404. "+
			"Override via LENNY_RELEASE_CHANNEL_KEY_FILE.")
	releaseChannelKeyID := flag.String("release-channel-key-id",
		envOr("LENNY_RELEASE_CHANNEL_KEY_ID", ""),
		"identifier of the §25.8 release-channel signing key (appears in the "+
			"X-Lenny-Release-Signature envelope). Required when --release-channel-key-file is set.")
	releaseChannelPrevKeyPath := flag.String("release-channel-previous-key-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_FILE"),
		"path to the PEM file carrying the §25.8 release-channel previous public key. "+
			"When set, signatures emitted under the previous key remain valid during the "+
			"rotation overlap window. Override via LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_FILE.")
	releaseChannelPrevKeyID := flag.String("release-channel-previous-key-id",
		envOr("LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_ID", ""),
		"identifier of the §25.8 previous release-channel key during the rotation overlap window.")
	releaseChannelManifestPath := flag.String("release-channel-manifest-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_MANIFEST_FILE"),
		"path to the §25.8 release-channel manifest JSON the publisher serves. When set "+
			"the publisher loads this file at startup and serves it on GET /v1/latest. "+
			"Override via LENNY_RELEASE_CHANNEL_MANIFEST_FILE.")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "graceful shutdown timeout")
	// §4.4 line 232 / §11.7 pgaudit sink consumer wiring.
	pgauditLogFile := flag.String("pgaudit-log-file", os.Getenv("LENNY_PGAUDIT_LOG_FILE"),
		"§4.4 / §11.7 pgaudit log file path. When set, lenny-ops tails the file, "+
			"translates each pgaudit record to OCSF, and delivers it to the configured "+
			"pgaudit sink. Override via LENNY_PGAUDIT_LOG_FILE.")
	pgauditTenantID := flag.String("pgaudit-tenant-id", envOr("LENNY_PGAUDIT_TENANT_ID", "platform"),
		"Tenant stamped on every pgaudit-sourced OCSF record (defaults to 'platform' for the "+
			"regulated-Postgres-instance case). Override via LENNY_PGAUDIT_TENANT_ID.")
	flag.Parse()

	// Replica identity: the pod name (the Helm chart sets POD_NAME from
	// the downward API), falling back to the hostname.
	replicaID := envOr("POD_NAME", "")
	if replicaID == "" {
		replicaID, _ = os.Hostname()
	}
	if replicaID == "" {
		replicaID = "lenny-ops"
	}

	// Root context cancelled on SIGTERM/SIGINT; it bounds the background
	// loops and the leader-election goroutine.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Postgres: optional. §25.4 has lenny-ops degrade gracefully when
	// Postgres is unavailable, so a missing DSN is not fatal.
	var pgPool *pgxpool.Pool
	if *postgresDSN != "" {
		pool, err := pgxpool.New(ctx, *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-ops: postgres: %v", err)
		}
		defer pool.Close()
		pgPool = pool
	}

	// Redis: optional. The §25.5 event stream falls back to the gateway
	// buffer when Redis is absent. Direct mode (--redis-url) and
	// Sentinel mode (--redis-sentinel-addrs) are mutually exclusive.
	var redisClient redis.UniversalClient
	if *redisURL != "" && *redisSentinelAddrs != "" {
		log.Fatalf("lenny-ops: --redis-url and --redis-sentinel-addrs are mutually exclusive")
	}
	if *redisURL != "" || *redisSentinelAddrs != "" {
		var rcfg redisconn.Config
		switch {
		case *redisURL != "":
			rcfg = redisconn.Config{URL: *redisURL, Password: *redisPassword, AllowInsecure: *redisAllowInsecure}
		default:
			rcfg = redisconn.Config{
				SentinelAddrs:    splitAndTrim(*redisSentinelAddrs),
				MasterName:       *redisSentinelMaster,
				Password:         *redisPassword,
				SentinelPassword: *redisSentinelPassword,
				TLS:              *redisTLS,
				AllowInsecure:    *redisAllowInsecure,
			}
		}
		client, err := redisconn.NewClient(rcfg)
		if err != nil {
			log.Fatalf("lenny-ops: redis client: %v", err)
		}
		defer func() { _ = client.Close() }()
		redisClient = client
	}

	// Kubernetes API: the §25.4 required dependency for diagnostics,
	// upgrade orchestration, backup Jobs, and leader election. When no
	// cluster connection is available lenny-ops still serves the HTTP
	// surface (the K8s probe reports unreachable) and skips leader
	// election — a single-process degraded mode for local development.
	var clientset *kubernetes.Clientset
	if cfg, err := ctrlconfig.GetConfig(); err != nil {
		log.Printf("lenny-ops: no Kubernetes config (%v); running without leader election", err)
	} else if cs, err := kubernetes.NewForConfig(cfg); err != nil {
		log.Printf("lenny-ops: build Kubernetes clientset: %v; running without leader election", err)
	} else {
		clientset = cs
	}

	// The §25.4 dependency probes feed the readiness signal and the
	// §25.6 connectivity diagnostic.
	gatewayHTTP := &http.Client{Timeout: 5 * time.Second}
	probes := map[string]probe.Func{
		opsservice.ProbePostgres: opsservice.PostgresProbe(pgPool),
		opsservice.ProbeRedis:    opsservice.RedisProbe(redisClient),
	}
	if clientset != nil {
		probes[opsservice.ProbeK8sAPI] = opsservice.K8sAPIProbe(clientset.Discovery())
	}
	if *gatewayURL != "" {
		probes[opsservice.ProbeGateway] = opsservice.GatewayProbe(gatewayHTTP, *gatewayURL+"/healthz")
	}

	// The §25.7 runbook index, read from docs/runbooks/.
	var runbookSource opsserver.RunbookSource
	if src, err := opsserver.LoadRunbookDir(*runbookDir); err != nil {
		log.Printf("lenny-ops: runbook index unavailable: %v", err)
	} else {
		runbookSource = src
		log.Printf("lenny-ops: indexed %d runbooks from %s", len(src.Runbooks()), *runbookDir)
	}

	// The §25.4 leader elector. Without a clientset, a noop elector
	// keeps lenny-ops a follower so the leader-only loops never start.
	var elector opsservice.Elector = noopElector{}
	if clientset != nil {
		le, err := opsservice.NewLeaseElector(*leaderElectNS, replicaID,
			clientset.CoreV1(), clientset.CoordinationV1())
		if err != nil {
			log.Fatalf("lenny-ops: build leader elector: %v", err)
		}
		elector = le
	}

	// The §25.5 webhook delivery worker. Its event and subscription
	// sources are wired from Redis and Postgres as those subsystems are
	// built; until then it runs against empty sources and delivers
	// nothing, which is the correct cold-start behavior.
	webhook := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events:        emptyEventSource{},
		Subscriptions: emptySubscriptionSource{},
		HTTPTimeout:   10 * time.Second,
	})

	// The §25.4 self-health checks every replica runs.
	var disc discovery.DiscoveryInterface
	if clientset != nil {
		disc = clientset.Discovery()
	}
	selfChecks := map[string]opsservice.SelfCheck{
		opsservice.CheckPostgresPool:   opsservice.PostgresPoolCheck(pgPool),
		opsservice.CheckRedisLag:       opsservice.RedisLagCheck(redisClient, nil),
		opsservice.CheckWebhookBacklog: opsservice.WebhookBacklogCheck(webhook.Backlog),
		opsservice.CheckK8sAPI:         opsservice.K8sAPICheck(disc),
		opsservice.CheckMemoryPressure: opsservice.MemoryPressureCheck(*memoryLimitBytes),
	}

	// The §25.11 BackupService. lenny-ops orchestrates backup/restore
	// Kubernetes Jobs through it. The Postgres-backed ops_backups store
	// and the Kubernetes Job launcher are wired as those seams land; the
	// in-memory store and launcher keep the §25.11 endpoints serving in
	// a single-process degraded mode so an agent can exercise them.
	backupSvc, backupJobs := buildBackupService(*production)

	// The §25.4 remediation-lock service. v1 runs the in-memory Tier 3
	// store; the Postgres and Redis tiers and the outage-epoch
	// reconciliation are wired as those seams land. The HTTP layer
	// applies the §25.4 scope-based authorization control before the
	// store.
	lockStore := coordination.NewMemStore()

	// The §25.4 escalation service, the §25.10 configuration-drift
	// service, and the §25.6 DiagnosticService. Each runs against an
	// in-memory or unconfigured backing store in this single-process
	// degraded mode so the §25 endpoints serve and an agent can exercise
	// them; the durable backing stores are documented seams.
	escalationSvc := buildEscalationService()
	driftSvc := buildDriftService()
	diagnosticSvc := buildDiagnosticService()

	// The §25.8 release-channel manifest publisher. Loaded from the
	// operator-supplied key + manifest paths. When no key is configured
	// the publisher is nil and GET /v1/latest is unmapped; lenny-ops
	// will not silently serve unsigned responses on the canonical
	// release-channel path.
	releaseChannelPub := buildReleaseChannelPublisher(
		*releaseChannelKeyPath, *releaseChannelKeyID,
		*releaseChannelPrevKeyPath, *releaseChannelPrevKeyID,
		*releaseChannelManifestPath,
	)

	// The §25.4 service body: leader election plus the background loops.
	// The §25.11 scheduled-backup cron jobs register here; the upgrade
	// and escalation loops are wired as those subsystems are built.
	svc, err := opsservice.New(opsservice.Config{
		ReplicaID:          replicaID,
		Elector:            elector,
		Webhook:            webhook,
		CronJobs:           backupJobs,
		SelfHealthChecks:   selfChecks,
		SelfHealthInterval: *selfHealthInterval,
		OnSelfHealthChange: func(prev, next opsservice.SelfHealthReport) {
			// §25.4: emit ops_health_status_changed. Event emission is
			// wired with the §25.5 event stream; until then the
			// transition is logged so operators see it.
			log.Printf("lenny-ops: self-health %s -> %s (replica %s)",
				prev.StatusText, next.StatusText, replicaID)
		},
	})
	if err != nil {
		log.Fatalf("lenny-ops: build service: %v", err)
	}

	// The §25.5 operational-event stream service. lenny-ops emits the
	// events it originates (ops_health_status_changed, escalation_*,
	// remediation_lock_*, drift_detected, platform_upgrade_*,
	// operation_progressed) into this service; subsystems take it as
	// the §4.0 EventEmitter dependency. When Redis is wired the events
	// also land on the platform-scoped ops:events:stream alongside the
	// gateway-emitted events; until then the opsstream.Service in-memory
	// buffer is the only delivery surface (per §25.5 cold-start).
	eventStream := opsstream.New(opsstream.Options{})

	// §4.0 EventEmitter for lenny-ops subsystems. With Redis configured
	// every emit also writes to the §25.5 platform-scoped Redis stream;
	// without Redis the local opsstream.Service is the only destination.
	var opsEmitter events.EventEmitter = eventStream
	if redisClient != nil {
		opsEmitter = newRedisFanOutEmitter(redisClient, eventStream, replicaID)
		log.Printf("lenny-ops: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}

	// §4.0 / §25.13: the in-process alert tracker. lenny-ops has no
	// PromQL backend wired in this commit, so the evaluator runs with
	// NoopExprEvaluator and no rule fires — the §25.5 alert events come
	// from Prometheus's `/api/v1/alerts` aggregation rather than this
	// evaluator until a real backend lands. The wiring is unconditional
	// so a future commit that supplies a real ExprEvaluator only swaps
	// the backend.
	alertEvaluator := evaluator.NewWithEmitter(
		rules.Catalog(),
		evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{
			Emitter: opsEmitter,
			Source:  "//lenny.dev/ops/" + replicaID,
		},
	)
	go alertEvaluator.Run(ctx)

	// The §25.4 HTTP surface. Every replica serves it, leader or not. It
	// hosts the §25.6 diagnostics, the §25.7 runbook index, the §25.4
	// self-health, remediation-lock, and escalation endpoints, the
	// §25.10 drift endpoints, the §25.11 backup endpoints, and the
	// §25.12 MCP management server.
	srv := &http.Server{
		Addr: *addr,
		Handler: opsserver.New(opsserver.Options{
			Probes:         probes,
			Runbooks:       runbookSource,
			SelfHealth:     svc.Monitor(),
			Leader:         svc,
			Backups:        backupSvc,
			Diagnostics:    diagnosticSvc,
			Drift:          driftSvc,
			Locks:          lockStore,
			Escalations:    escalationSvc,
			EventStream:    eventStream,
			ReleaseChannel: releaseChannelPub,
			Production:     *production,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// §4.4 line 232: when --pgaudit-log-file is set, start the pgaudit
	// shipper. The shipper tails the file, parses each AUDIT line,
	// translates to OCSF, and delivers to the NoOp sink (deployers
	// override the sink by editing pkg/audit/pgaudit/main wiring to
	// point at a real downstream). The metrics surface bumps the
	// catalog-declared lenny_pgaudit_grant_events_total counter so the
	// §16.5 PgAuditSinkDeliveryFailed alert has a signal to fire on.
	var pgauditShipper *pgaudit.Shipper
	if *pgauditLogFile != "" {
		pgauditShipper = pgaudit.New(pgaudit.Config{
			LogFile:  *pgauditLogFile,
			TenantID: *pgauditTenantID,
			Sink:     pgaudit.NoOpSink(),
			// Metrics could be wired via a Prometheus registerer once
			// lenny-ops exposes its own /metrics; for now the shipper
			// runs without per-class metric emission. The dedicated
			// PromMetrics adapter (pkg/audit/pgaudit/prommetrics.go)
			// is the integration seam.
		})
		if err := pgauditShipper.Start(ctx); err != nil {
			log.Printf("lenny-ops: pgaudit shipper start failed (continuing without it): %v", err)
			pgauditShipper = nil
		} else {
			log.Printf("lenny-ops: §4.4 pgaudit shipper tailing %s (tenant=%s)",
				*pgauditLogFile, *pgauditTenantID)
		}
	}

	// The background loops run on their own goroutine; the leader-only
	// loops start when this replica wins the lease.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.Run(ctx)
	}()

	// On shutdown signal, stop the HTTP server within the grace window.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("lenny-ops: replica %s serving the operability API on %s (loops: %v)",
		replicaID, *addr, svc.LoopNames())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("lenny-ops: serve: %v", err)
	}

	// The HTTP server has stopped; wait for the background loops to
	// drain (StopLeaderLoops blocks until the singleton loops return).
	stop()
	if pgauditShipper != nil {
		pgauditShipper.Stop()
	}
	wg.Wait()
	log.Printf("lenny-ops: replica %s stopped", replicaID)
}

// envOr returns the environment variable name when set, else fallback.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// envInt64 parses the named environment variable as an int64, falling
// back when it is unset or malformed.
func envInt64(name string, fallback int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

// envBool parses the named environment variable as a bool, falling back
// when it is unset or malformed.
func envBool(name string, fallback bool) bool {
	if v := os.Getenv(name); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// splitAndTrim splits a comma-separated string and drops empty entries
// after trimming whitespace. Used to parse --redis-sentinel-addrs.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
