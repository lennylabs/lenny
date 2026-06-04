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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	opsLogging "github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	opsmetrics "github.com/lennylabs/lenny/pkg/ops/metrics"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// buildVersion is the compiled-in lenny-ops binary version, overridden
// at build time via "-X main.buildVersion=...". The §25.8 upgrade-check
// compares it against the release channel's advertised version to decide
// whether a newer release is available (§25.8 "lenny-ops binary metadata
// — local, compiled-in via ldflags").
var buildVersion = "dev"

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
	agentNamespace := flag.String("agent-namespace", envOr("LENNY_AGENT_NAMESPACE", "lenny-system"),
		"namespace agent pods run in; the §25.6 session diagnosis reads pod failure "+
			"signals and stamps the relatedLogs reference against it")
	runbookDir := flag.String("runbook-dir", envOr("LENNY_RUNBOOK_DIR", "docs/runbooks"),
		"directory of §25.7 operational-runbook markdown files the runbook index serves")
	// §25.6 doctor auto-remediation guardrails (admin.doctor.* / global.maintenanceMode).
	doctorFixTimeout := flag.Int("doctor-fix-timeout-seconds", 120,
		"§25.6 admin.doctor.fixTimeoutSeconds: per-remediation timeout for `doctor --fix`")
	doctorAllowedFixes := flag.String("doctor-allowed-fixes", os.Getenv("LENNY_DOCTOR_ALLOWED_FIXES"),
		"§25.6 admin.doctor.allowedFixes: comma-separated allowlist of fixable findings; empty means the full set")
	maintenanceMode := flag.Bool("maintenance-mode", envOr("LENNY_MAINTENANCE_MODE", "false") == "true",
		"§25.6 global.maintenanceMode: when true, `doctor --fix` skips every remediation")
	backupImage := flag.String("backup-image", os.Getenv("LENNY_BACKUP_IMAGE"),
		"§25.11 lenny-backup image ({platform.registry.url}/lenny-backup:{version}) the "+
			"Kubernetes JobLauncher runs. Empty (with no cluster) keeps the in-process fake "+
			"launcher; set it to orchestrate real backup/restore Jobs. Override via LENNY_BACKUP_IMAGE.")
	backupMinIOEndpoint := flag.String("backup-minio-endpoint", os.Getenv("LENNY_BACKUP_MINIO_ENDPOINT"),
		"§25.11 MinIO endpoint (host:port) the backup Job uploads archives to")
	backupMinIOBucket := flag.String("backup-minio-bucket", envOr("LENNY_BACKUP_MINIO_BUCKET", "lenny-backups"),
		"§25.11 MinIO backup bucket")
	backupKMSKeyID := flag.String("backup-kms-key-id", os.Getenv("LENNY_BACKUP_KMS_KEY_ID"),
		"§12.9 SSE-KMS key for the backup upload; empty selects SSE-S3")
	backupReportDSNSecret := flag.String("backup-report-dsn-secret", os.Getenv("LENNY_BACKUP_REPORT_DSN_SECRET"),
		"name of the Secret whose report-dsn key holds the lenny-ops DSN the backup Job uses for "+
			"the §25.11 step-8 ops_backups update; empty leaves it unset")
	backupRegions := flag.String("backups-regions", os.Getenv("LENNY_OPS_BACKUPS_REGIONS"),
		"§12.8 backups.regions per-region backup endpoint map as JSON "+
			"({\"eu\":{\"minioEndpoint\":...,\"kmsKeyId\":...,\"accessCredentialSecret\":...}}). "+
			"Empty keeps the single-region global dump. Override via LENNY_OPS_BACKUPS_REGIONS.")
	backupShardRegions := flag.String("backups-shard-regions", os.Getenv("LENNY_OPS_BACKUP_SHARD_REGIONS"),
		"§12.8 shard→region map as JSON ([{\"shardId\":...,\"region\":...}]) used to dispatch "+
			"one pg_dump per region. Required when backups-regions is set. Override via "+
			"LENNY_OPS_BACKUP_SHARD_REGIONS.")
	selfHealthInterval := flag.Duration("self-health-interval", 10*time.Second,
		"§25.4 ops.selfHealth.checkIntervalSeconds — how often the self-monitor runs")
	eventsStreamMaxLen := flag.Int64("events-stream-max-len", envInt64("LENNY_OPS_EVENTS_STREAM_MAX_LEN", events.DefaultStreamMaxLen),
		"§25.5 ops.events.streamMaxLen — MAXLEN of the platform-scoped ops:events:stream Redis stream. Tier 1 default 10,000; tier presets raise it (50,000 at Tier 2, 100,000 at Tier 3). Override via LENNY_OPS_EVENTS_STREAM_MAX_LEN. F-17.8.1.")
	webhookTrackingMode := flag.String("webhook-tracking-mode", envOr("LENNY_OPS_WEBHOOK_TRACKING_MODE", string(webhookdelivery.TrackingFull)),
		"§25.5 ops.webhooks.deliveryTrackingMode — full, failures-only, or metric-only. Override via LENNY_OPS_WEBHOOK_TRACKING_MODE.")
	webhookRetentionDays := flag.Int("webhook-delivery-retention-days", envInt("LENNY_OPS_WEBHOOK_DELIVERY_RETENTION_DAYS", 7),
		"§25.5 ops.webhooks.deliveryRetentionDays — lifetime of a delivery row. Tier defaults 1/7/30 days. Override via LENNY_OPS_WEBHOOK_DELIVERY_RETENTION_DAYS.")
	webhookFailuresRetentionDays := flag.Int("webhook-failures-retention-days", envInt("LENNY_OPS_WEBHOOK_FAILURES_ONLY_RETENTION_DAYS", 0),
		"§25.5 ops.webhooks.failuresOnlyRetentionDays — lifetime of failed delivery rows when longer than the default; 0 uses deliveryRetentionDays. Override via LENNY_OPS_WEBHOOK_FAILURES_ONLY_RETENTION_DAYS.")
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
	// spec: §25.16 Production "Prometheus (BYO)" block (lines 5124-5132).
	// When set, lenny-ops uses the supplied HTTP API endpoint as the
	// §25.13 ExprEvaluator backend and the §25.4 cross-replica health
	// aggregator. When empty (the §25.16 Minimal default) lenny-ops
	// degrades to the per-replica fan-out fallback the spec permits at
	// Tier 1. F-25.16.4.
	prometheusURL := flag.String("prometheus-url", os.Getenv("LENNY_PROMETHEUS_URL"),
		"§25.16 BYO Prometheus HTTP API base URL (e.g. http://prometheus.monitoring.svc:9090). "+
			"When empty the §25.4 cross-replica health aggregator falls back to per-replica fan-out. "+
			"Override via LENNY_PROMETHEUS_URL.")
	// spec: §25.9 line 3700. ops.audit.diagnosticsRatePerMinute caps the
	// distinct diagnostic audit events a service account may emit per
	// minute before excess is dropped; repeated calls for one resource
	// coalesce into a single event. Default 60. F-25.9.15.
	diagnosticsAuditRate := flag.Int("diagnostics-audit-rate-per-minute",
		envInt("LENNY_OPS_DIAGNOSTICS_AUDIT_RATE_PER_MINUTE", auditrate.DefaultRatePerMinute),
		"§25.9 ops.audit.diagnosticsRatePerMinute — per-service-account cap on distinct "+
			"diagnostic audit events per minute. Default 60. Override via "+
			"LENNY_OPS_DIAGNOSTICS_AUDIT_RATE_PER_MINUTE.")
	// spec: §25.10 line 3809. ops.drift.snapshotStaleWarningDays sets the
	// threshold at which a stored desired-state snapshot is flagged stale
	// in the GET /v1/admin/drift report. Default 7 days; 0 disables the
	// warning entirely. F-25.10.9.
	driftSnapshotStaleWarningDays := flag.Int("drift-snapshot-stale-warning-days",
		envInt("LENNY_DRIFT_SNAPSHOT_STALE_WARNING_DAYS", driftservice.DefaultStaleWarningDays),
		"§25.10 ops.drift.snapshotStaleWarningDays — threshold (in days) for the "+
			"bootstrap_seed_snapshot staleness warning on GET /v1/admin/drift. Default 7; "+
			"0 disables the warning. Override via LENNY_DRIFT_SNAPSHOT_STALE_WARNING_DAYS.")
	// spec: §25.10 line 3824. ops.drift.runningStateCacheTTLSeconds caps
	// how long the §25.10 running-state cache holds the gateway-aggregated
	// running state. ?fresh=true on the drift report bypasses the cache.
	// Default 60s; 0 disables caching entirely. F-25.10.7.
	driftRunningStateCacheTTLSeconds := flag.Int("drift-running-state-cache-ttl-seconds",
		envInt("LENNY_DRIFT_RUNNING_STATE_CACHE_TTL_SECONDS",
			int(driftservice.DefaultRunningStateCacheTTL/time.Second)),
		"§25.10 ops.drift.runningStateCacheTTLSeconds — TTL (in seconds) for the §25.10 "+
			"line 3822 running-state cache that backs GET /v1/admin/drift. Default 60; "+
			"0 disables caching (every report reads fresh). "+
			"Override via LENNY_DRIFT_RUNNING_STATE_CACHE_TTL_SECONDS.")
	// spec: §25.4 line 2396. ops.escalation.requireDurable rejects an
	// escalation create with ESCALATION_NO_DURABLE_STORE when neither
	// Postgres nor Redis accepts it, instead of buffering in memory — the
	// conservative posture for deployers who prefer an explicit failure
	// over a silent durability gap during a storage outage. Default false.
	// F-25.4.7.
	escalationRequireDurable := flag.Bool("escalation-require-durable",
		envBool("LENNY_OPS_ESCALATION_REQUIRE_DURABLE", false),
		"§25.4 ops.escalation.requireDurable — reject an escalation create with "+
			"ESCALATION_NO_DURABLE_STORE when both Postgres and Redis are unavailable "+
			"instead of buffering in memory. Override via LENNY_OPS_ESCALATION_REQUIRE_DURABLE.")
	// spec: §25.4 line 2414. ops.escalation.reconciliationWritesPerSecond
	// paces the leader-only flush that promotes buffered escalations to a
	// recovered durable tier, so a large recovery does not spike Postgres.
	// Default 20. F-25.4.7.
	escalationReconcileWPS := flag.Int("escalation-reconciliation-writes-per-second",
		envInt("LENNY_OPS_ESCALATION_RECONCILIATION_WRITES_PER_SECOND", 20),
		"§25.4 ops.escalation.reconciliationWritesPerSecond — flush rate cap for the "+
			"leader-only escalation reconciliation loop. Default 20. "+
			"Override via LENNY_OPS_ESCALATION_RECONCILIATION_WRITES_PER_SECOND.")
	// spec: §25.4 lines 1562-1564 + §17 security.oidc.issuerUrl (line 916).
	// lenny-ops validates bearer JWTs with the same OIDC issuer the gateway
	// admin API trusts and requires platform-admin or tenant-admin on every
	// endpoint. The v1 verify key is the shared HMAC signing key (the same
	// --bearer-trust-hmac-key-file mechanism the §17.4 embedded gateway
	// uses); --oidc-issuer-url pins the expected iss claim. F-25.4.1,
	// F-25.4.20.
	oidcIssuerURL := flag.String("oidc-issuer-url", os.Getenv("LENNY_OIDC_ISSUER_URL"),
		"§25.4/§17 security.oidc.issuerUrl: the OIDC issuer whose tokens lenny-ops trusts. "+
			"When set, a bearer whose iss claim differs is rejected. Override via LENNY_OIDC_ISSUER_URL.")
	bearerTrustHMACKeyFile := flag.String("bearer-trust-hmac-key-file", os.Getenv("LENNY_BEARER_TRUST_HMAC_KEY_FILE"),
		"§25.4 line 1562: path to the shared HMAC signing key (the gateway Token Service / "+
			"embedded OIDC key file) lenny-ops verifies bearer JWTs against. Required when "+
			"--production is set; when empty in dev the operability surface is unauthenticated. "+
			"Override via LENNY_BEARER_TRUST_HMAC_KEY_FILE.")
	authMultiTenant := flag.Bool("auth-multi-tenant", envBool("LENNY_AUTH_MULTI_TENANT", false),
		"§10.2: when true, lenny-ops extracts the tenant identifier from the JWT tenant claim "+
			"(so tenant-admin scoping resolves to the bearer's tenant); when false every caller "+
			"resolves to the built-in default tenant. Override via LENNY_AUTH_MULTI_TENANT.")
	rateLimitRPS := flag.Float64("rate-limit-rps", envFloat("LENNY_OPS_RATE_LIMIT_RPS", opsserver.DefaultRateLimitRPS),
		"§25.4 line 2001 ops.rateLimiting.requestsPerSecond: per-service-account token-bucket "+
			"refill rate. Override via LENNY_OPS_RATE_LIMIT_RPS.")
	rateLimitBurst := flag.Int("rate-limit-burst", envInt("LENNY_OPS_RATE_LIMIT_BURST", opsserver.DefaultRateLimitBurst),
		"§25.4 line 2001 ops.rateLimiting.burst: per-service-account token-bucket depth. "+
			"Override via LENNY_OPS_RATE_LIMIT_BURST.")
	// spec: §25.4 lines 2206-2212 ops.locks.memoryTier. Governs whether the
	// in-memory (Tier-3) remediation-lock store grants an acquisition when
	// both Postgres and Redis are down. "single-replica-only" (default)
	// rejects acquisitions in a multi-replica deployment (detected via the
	// lenny-ops Service Endpoints); "always" grants with a replica-local
	// warning; "never" disables Tier 3. F-25.4.26.
	locksMemoryTier := flag.String("locks-memory-tier",
		envOr("LENNY_OPS_LOCKS_MEMORY_TIER", string(coordination.MemoryTierSingleReplicaOnly)),
		"§25.4 ops.locks.memoryTier: in-memory lock-tier policy — "+
			"single-replica-only | always | never. Override via LENNY_OPS_LOCKS_MEMORY_TIER.")
	opsServiceName := flag.String("ops-service-name", envOr("LENNY_OPS_SERVICE_NAME", "lenny-ops"),
		"§25.4 line 2208: name of the lenny-ops Service whose Endpoints the single-replica-only "+
			"lock policy reads to count ready replicas. Override via LENNY_OPS_SERVICE_NAME.")
	// spec: §25.4 ops.locks.{minTTLSeconds,defaultTTLSeconds,maxTTLSeconds} —
	// the deployment-wide remediation-lock TTL policy. A request omitting
	// ttlSeconds takes the default; one below the floor is raised, one above
	// the ceiling is clamped. Every tier (Postgres, Redis, in-memory) clamps
	// identically through coordination.SetTTLBounds. F-25.4.9.
	locksMinTTL := flag.Int("locks-min-ttl-seconds", envInt("LENNY_OPS_LOCKS_MIN_TTL_SECONDS", 0),
		"§25.4 ops.locks.minTTLSeconds: floor a requested remediation-lock TTL is raised to. 0 = no floor.")
	locksDefaultTTL := flag.Int("locks-default-ttl-seconds", envInt("LENNY_OPS_LOCKS_DEFAULT_TTL_SECONDS", 0),
		"§25.4 ops.locks.defaultTTLSeconds: TTL applied when a lock request omits ttlSeconds. 0 = built-in 300s.")
	locksMaxTTL := flag.Int("locks-max-ttl-seconds", envInt("LENNY_OPS_LOCKS_MAX_TTL_SECONDS", 0),
		"§25.4 ops.locks.maxTTLSeconds: ceiling a requested remediation-lock TTL is clamped to. 0 = built-in 3600s.")
	// spec: §25.4 ops.idempotency.{keyTTLSeconds,longRunningKeyTTLSeconds} —
	// the lifetime of a stored idempotency record. The standard class covers
	// single-request mutations (24h default); the long-running class covers
	// multi-phase operations such as upgrade and restore (7d default). F-25.4.9.
	idempotencyKeyTTL := flag.Int("idempotency-key-ttl-seconds", envInt("LENNY_OPS_IDEMPOTENCY_KEY_TTL_SECONDS", 0),
		"§25.4 ops.idempotency.keyTTLSeconds: standard idempotency-key lifetime. 0 = built-in 24h.")
	idempotencyLongRunningTTL := flag.Int("idempotency-long-running-key-ttl-seconds", envInt("LENNY_OPS_IDEMPOTENCY_LONG_RUNNING_KEY_TTL_SECONDS", 0),
		"§25.4 ops.idempotency.longRunningKeyTTLSeconds: long-running idempotency-key lifetime. 0 = built-in 7d.")
	// spec: §25.4 ops.leaderElection.{leaseDurationSeconds,renewDeadlineSeconds,
	// retryPeriodSeconds} — the client-go leader-election lease timings for the
	// lenny-ops-leader Lease. F-25.4.9.
	leaderLeaseDuration := flag.Int("leader-lease-duration-seconds", envInt("LENNY_OPS_LEADER_LEASE_DURATION_SECONDS", 0),
		"§25.4 ops.leaderElection.leaseDurationSeconds: leader-election lease duration. 0 = built-in 15s.")
	leaderRenewDeadline := flag.Int("leader-renew-deadline-seconds", envInt("LENNY_OPS_LEADER_RENEW_DEADLINE_SECONDS", 0),
		"§25.4 ops.leaderElection.renewDeadlineSeconds: leader renew deadline. 0 = built-in 10s.")
	leaderRetryPeriod := flag.Int("leader-retry-period-seconds", envInt("LENNY_OPS_LEADER_RETRY_PERIOD_SECONDS", 0),
		"§25.4 ops.leaderElection.retryPeriodSeconds: leader-election retry period. 0 = built-in 2s.")
	// spec: §25.5 lines 2735-2745 ops.webhooks.{allowHTTP,blockedCIDRs,
	// domainAllowlist} — the callback-URL SSRF policy the subscription
	// validator enforces at create/update and at each delivery. F-25.4.9.
	webhookAllowHTTP := flag.Bool("webhook-allow-http", envBool("LENNY_OPS_WEBHOOK_ALLOW_HTTP", false),
		"§25.5 ops.webhooks.allowHTTP: permit http:// callback URLs. Off requires HTTPS.")
	webhookBlockedCIDRs := flag.String("webhook-blocked-cidrs", os.Getenv("LENNY_OPS_WEBHOOK_BLOCKED_CIDRS"),
		"§25.5 ops.webhooks.blockedCIDRs: comma-separated CIDRs rejected in addition to the built-in private/reserved set (e.g. the cluster pod/service CIDRs).")
	webhookDomainAllowlist := flag.String("webhook-domain-allowlist", os.Getenv("LENNY_OPS_WEBHOOK_DOMAIN_ALLOWLIST"),
		"§25.5 ops.webhooks.domainAllowlist: comma-separated hosts (exact or *.suffix) callbacks are restricted to. Empty allows any non-blocked host.")
	// spec: §25.4 lines 1596-1601 — the GET /v1/admin/me platform context.
	// installationId is the stable per-install UUID; tier is the §25.16
	// deployment tier; opsServiceURL is the external lenny-ops entry point.
	// F-25.4.2.
	installationID := flag.String("installation-id", os.Getenv("LENNY_INSTALLATION_ID"),
		"§25.4: stable installation UUID surfaced in GET /v1/admin/me.platform.installationId. Override via LENNY_INSTALLATION_ID.")
	platformTier := flag.String("platform-tier", envOr("LENNY_PLATFORM_TIER", ""),
		"§25.16 deployment tier (tier1/tier2/tier3) surfaced in GET /v1/admin/me.platform.tier. Override via LENNY_PLATFORM_TIER.")
	opsServiceURL := flag.String("ops-service-url", os.Getenv("LENNY_OPS_SERVICE_URL"),
		"§25.4: external lenny-ops URL surfaced in GET /v1/admin/me.platform.opsServiceURL. Override via LENNY_OPS_SERVICE_URL.")
	// spec: §25.4 lines 1740-1974 ("Calling the Gateway" + GatewayClient).
	// The headless Service and TLS posture drive the per-replica fan-out
	// discovery and the NET-070 admin-API transport; the breaker and
	// fan-out timeout bound the §25.4 fallback path. F-25.4.8.
	gatewayInternalTLS := flag.Bool("gateway-internal-tls", envBool("LENNY_OPS_GATEWAY_INTERNAL_TLS", false),
		"§25.4 ops.tls.internalEnabled: when true the gateway admin-API link uses HTTPS on the gateway internal-TLS port (NET-070).")
	gatewayHeadlessSvc := flag.String("gateway-headless-service", envOr("LENNY_OPS_GATEWAY_HEADLESS_SERVICE", ""),
		"§25.4 ops.gateway.headlessService: the lenny-gateway-pods headless Service the per-replica fan-out resolves. Empty disables fan-out.")
	gatewayTLSPort := flag.Int("gateway-internal-tls-port", envInt("LENNY_OPS_GATEWAY_INTERNAL_TLS_PORT", 8443),
		"§25.4 gateway internal-TLS port the fan-out dials when --gateway-internal-tls is set.")
	gatewayPlaintextPort := flag.Int("gateway-internal-port", envInt("LENNY_OPS_GATEWAY_INTERNAL_PORT", 8080),
		"§25.4 gateway internal plaintext port the fan-out dials when --gateway-internal-tls is unset.")
	gatewayFanOutTimeout := flag.Duration("gateway-fanout-timeout", 2*time.Second,
		"§25.4 ops.gateway.fanOutTimeoutSeconds: per-replica fan-out request timeout.")
	gatewayBreakerThreshold := flag.Int("gateway-fanout-breaker-failure-threshold", envInt("LENNY_OPS_GATEWAY_FANOUT_BREAKER_THRESHOLD", 3),
		"§25.4 ops.gateway.fanOutCircuitBreaker.failureThreshold: consecutive per-replica failures before the breaker opens.")
	gatewayBreakerResetAfter := flag.Duration("gateway-fanout-breaker-reset-after", 60*time.Second,
		"§25.4 ops.gateway.fanOutCircuitBreaker.resetAfter: how long a tripped per-replica breaker stays open.")
	gatewaySATokenFile := flag.String("gateway-sa-token-file", envOr("LENNY_OPS_GATEWAY_SA_TOKEN_FILE", "/var/run/secrets/lenny/gateway/token"),
		"§25.4 projected ServiceAccount token volume the GatewayClient presents to the gateway admin API. Absent → dev-headers path.")
	gatewayTokenRefreshBefore := flag.Duration("gateway-token-refresh-before-expiry", durationOrDefault(envInt("LENNY_OPS_GATEWAY_TOKEN_REFRESH_BEFORE_EXPIRY_SECONDS", 0), 5*time.Minute),
		"§25.4 security.oidc.tokenRefreshBeforeExpirySeconds: pre-emptive token-refresh lead time.")
	gatewayTokenMinTTL := flag.Duration("gateway-token-min-ttl", durationOrDefault(envInt("LENNY_OPS_GATEWAY_TOKEN_MIN_TTL_SECONDS", 0), 0),
		"§25.4 security.oidc.minTokenTTLSeconds: reject a startup token whose remaining lifetime is below this floor. 0 disables.")
	gatewayCABundleFile := flag.String("gateway-ca-bundle-file", envOr("LENNY_OPS_GATEWAY_CA_BUNDLE_FILE", ""),
		"§25.4 ops.tls.caBundleConfigMap: PEM CA bundle augmenting the system trust store for the gateway admin-API TLS link. Empty uses system roots.")
	alertingBundleFormats := flag.String("alerting-bundle-formats", envOr("LENNY_ALERTING_BUNDLE_FORMATS", "prometheusrule"),
		"§25.13 line 4833 Helm value monitoring.format: comma-separated list of the formats the chart bundled the §16.5 alert catalogue into. The §25.4 line 1339 bundleRules reconciler re-stamps lenny_ops /metrics' lenny_alerting_rules_bundled{format} from it. Override via LENNY_ALERTING_BUNDLE_FORMATS.")
	alertingOverrideCount := flag.Int("alerting-override-count", envInt("LENNY_ALERTING_OVERRIDE_COUNT", 0),
		"§25.13 line 4834 Helm value len(monitoring.alertOverrides): count of operator-customized §16.5 rules. The §25.4 bundleRules reconciler re-stamps lenny_alerting_rule_overrides from it. Override via LENNY_ALERTING_OVERRIDE_COUNT.")
	flag.Parse()

	// spec: §25.4 ops.locks.{minTTLSeconds,defaultTTLSeconds,maxTTLSeconds}.
	// Configure the deployment-wide remediation-lock TTL policy once, before
	// the lock Service (and its tier stores) is built, so every tier clamps
	// requested TTLs identically. A zero value keeps the built-in bound. F-25.4.9.
	coordination.SetTTLBounds(*locksMinTTL, *locksDefaultTTL, *locksMaxTTL)

	// Replica identity: the pod name (the Helm chart sets POD_NAME from
	// the downward API), falling back to the hostname.
	replicaID := envOr("POD_NAME", "")
	if replicaID == "" {
		replicaID, _ = os.Hostname()
	}
	if replicaID == "" {
		replicaID = "lenny-ops"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// spec: §16.3 line 359 — install the process-wide TracerProvider and
	// W3C propagator so lenny-ops spans reach the OTLP Collector instead of
	// the no-op provider. With no OTEL endpoint a stdout exporter is used.
	// F-16.3.2.
	traceShutdown, err := tracing.InitProvider(ctx, tracing.ProviderConfig{
		ServiceName:  "lenny-ops",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	if err != nil {
		log.Fatalf("lenny-ops: tracing init: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = traceShutdown(shutdownCtx)
	}()

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
	storeRouter := buildStoreRouter(pgPool, redisClient)
	auditRecorder := buildPlatformAuditRecorder(storeRouter)

	// Kubernetes API: the §25.4 required dependency for diagnostics,
	// upgrade orchestration, backup Jobs, and leader election. When no
	// cluster connection is available lenny-ops still serves the HTTP
	// surface (the K8s probe reports unreachable) and skips leader
	// election — a single-process degraded mode for local development.
	var clientset *kubernetes.Clientset
	var dynClient dynamic.Interface
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
	}

	// The §25.4 dependency probes feed the readiness signal and the
	// §25.6 connectivity diagnostic.
	gatewayHTTP := &http.Client{Timeout: 5 * time.Second}
	probes := map[string]probe.Func{
		opsservice.ProbePostgres: opsservice.PostgresProbe(pgPool),
		opsservice.ProbeRedis:    opsservice.RedisProbe(redisClient),
		// spec: §25.2 line 169 — lenny-ops connects to MinIO and Prometheus;
		// the §25.6 connectivity report names both. Each probe is registered
		// unconditionally and reports "not configured" when its endpoint is
		// empty, so the report is honest about object-storage / metrics
		// availability rather than silently omitting the dependency. F-25.2.10.
		opsservice.ProbeMinIO:      opsservice.MinIOProbe(gatewayHTTP, *backupMinIOEndpoint),
		opsservice.ProbePrometheus: opsservice.PrometheusProbe(gatewayHTTP, *prometheusURL),
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
			clientset.CoreV1(), clientset.CoordinationV1(),
			// spec: §25.4 ops.leaderElection.{leaseDurationSeconds,renewDeadlineSeconds,
			// retryPeriodSeconds}. Zero fields keep the built-in 15s/10s/2s. F-25.4.9.
			opsservice.LeaseTimings{
				LeaseDuration: time.Duration(*leaderLeaseDuration) * time.Second,
				RenewDeadline: time.Duration(*leaderRenewDeadline) * time.Second,
				RetryPeriod:   time.Duration(*leaderRetryPeriod) * time.Second,
			})
		if err != nil {
			log.Fatalf("lenny-ops: build leader elector: %v", err)
		}
		elector = le
	}

	// The §25.5 operational-event stream service and emitter. lenny-ops
	// emits the events it originates (ops_health_status_changed,
	// escalation_*, remediation_lock_*, drift_detected,
	// platform_upgrade_*, operation_progressed) into this service;
	// subsystems take it as the §4.0 EventEmitter dependency. When Redis
	// is wired every emit also writes to the platform-scoped
	// ops:events:stream alongside the gateway-emitted events; without
	// Redis the local opsstream.Service in-memory buffer is the only
	// delivery surface (per §25.5 cold-start). Built before the webhook
	// worker so OnSelfHealthChange can emit through it and the worker can
	// consume the same Redis stream.
	eventStream := opsstream.New(opsstream.Options{ReplicaID: replicaID, OnGap: observeStreamGap})
	var opsEmitter events.EventEmitter = eventStream
	if redisClient != nil {
		opsEmitter = newRedisFanOutEmitter(redisClient, eventStream, replicaID, *eventsStreamMaxLen)
		log.Printf("lenny-ops: §25.5 operational events streaming to Redis %s (maxlen=%d)", events.DefaultStreamKey, *eventsStreamMaxLen)
	}

	// The §25.5 webhook delivery worker. The §25.5 EventSource is the
	// Redis ops:events:stream consumer when Redis is wired (so the worker
	// fans out events emitted by every replica — gateway, controllers,
	// peer lenny-ops); without Redis it runs against an empty source and
	// delivers nothing, the correct cold-start behavior.
	var eventSource opsservice.EventSource = emptyEventSource{}
	if redisClient != nil {
		eventSource = opsservice.NewRedisEventSource(redisClient, events.DefaultStreamKey)
		log.Printf("lenny-ops: §25.5 webhook worker consuming Redis stream %s", events.DefaultStreamKey)
	}

	// §25.5 line 2753: the cold-start health signal. When the subscription
	// cache cannot reach Postgres no webhook delivery occurs, so lenny-ops
	// emits ops_health_status_changed with subscriptionsUnavailable; a
	// later recovery emits the clear.
	var subsUnavailMu sync.Mutex
	subsUnavailEmitted := false
	onSubsAvailability := func(available bool) {
		subsUnavailMu.Lock()
		defer subsUnavailMu.Unlock()
		if available && !subsUnavailEmitted {
			return // healthy start or steady state — nothing to announce
		}
		subsUnavailEmitted = !available
		severity := "info"
		if !available {
			severity = "warning"
		}
		payload, _ := json.Marshal(map[string]any{
			"replicaId":                replicaID,
			"subscriptionsUnavailable": !available,
		})
		if err := opsEmitter.Emit(ctx, events.OperationalEvent{
			Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
			Subject:         "ops/" + replicaID,
			Severity:        severity,
			DataContentType: "application/json",
			Data:            payload,
		}); err != nil {
			log.Printf("lenny-ops: emit ops_health_status_changed (subscriptionsUnavailable=%t): %v", !available, err)
		}
	}

	// §25.5 line 2751: the invalidate-RPC token derives from the shared
	// HMAC key both replicas mount; an empty path (dev) disables the RPC.
	var webhookSharedKey []byte
	if *bearerTrustHMACKeyFile != "" {
		if b, rerr := os.ReadFile(*bearerTrustHMACKeyFile); rerr == nil {
			webhookSharedKey = b
		}
	}
	adminPort := 0
	if i := strings.LastIndex(*addr, ":"); i >= 0 {
		adminPort, _ = strconv.Atoi((*addr)[i+1:])
	}
	// §25.5 lines 2735-2745 ops.webhooks SSRF policy. The validator gates
	// subscription create/update and every delivery attempt. F-25.4.9.
	webhookSSRF := buildWebhookSSRF(*webhookAllowHTTP, *webhookBlockedCIDRs, *webhookDomainAllowlist)
	delivery := buildWebhookDelivery(ctx, webhookDeliveryDeps{
		Pool:         pgPool,
		Clientset:    clientset,
		Source:       eventSource,
		Emitter:      opsEmitter,
		SSRF:         webhookSSRF,
		Audit:        subscriptionAuditFunc(func(ev eventsubscription.AuditEvent) { log.Printf("lenny-ops: audit %s %v", ev.Type, ev.Details) }),
		SharedKey:    webhookSharedKey,
		Namespace:    envOr("POD_NAMESPACE", *leaderElectNS),
		ServiceName:  *opsServiceName,
		SelfIP:       os.Getenv("POD_IP"),
		AdminPort:    adminPort,
		TrackingMode: webhookdelivery.TrackingMode(*webhookTrackingMode),
		Retention: opsservice.RetentionPolicy{
			Retention:             time.Duration(*webhookRetentionDays) * 24 * time.Hour,
			FailuresOnlyRetention: time.Duration(*webhookFailuresRetentionDays) * 24 * time.Hour,
		},
		OnAvailabilityChange: onSubsAvailability,
	})
	webhook := delivery.Worker
	eventSubscriptions := delivery.Subscriptions
	subscriptionCache := delivery.Cache
	defer subscriptionCache.Stop()

	// The §25.4 gateway admin-API client (F-25.4.8). It refreshes the
	// service-account OIDC token, routes per-replica fan-out through the
	// headless Service with circuit breakers, and emits the NET-070
	// handshake metric (F-25.4.19) on every request. The gateway-auth
	// self-health check is its in-process consumer; the per-replica
	// health/recommendation aggregation endpoints consume the same client
	// as they land (F-25.4.3).
	gwClient, err := buildGatewayClient(gatewayClientConfig{
		gatewayURL:          *gatewayURL,
		headlessService:     *gatewayHeadlessSvc,
		namespace:           envOr("POD_NAMESPACE", *leaderElectNS),
		internalTLS:         *gatewayInternalTLS,
		internalTLSPort:     *gatewayTLSPort,
		plaintextPort:       *gatewayPlaintextPort,
		fanOutTimeout:       *gatewayFanOutTimeout,
		breakerThreshold:    *gatewayBreakerThreshold,
		breakerResetAfter:   *gatewayBreakerResetAfter,
		saTokenPath:         *gatewaySATokenFile,
		refreshBeforeExpiry: *gatewayTokenRefreshBefore,
		minTokenTTL:         *gatewayTokenMinTTL,
		caBundlePath:        *gatewayCABundleFile,
	}, prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: build gateway client: %v", err)
	}

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
		opsservice.CheckGatewayAuth:    opsservice.GatewayAuthCheck(gatewayAuthProbe(gwClient)),
	}
	// §25.8 cert-manager probe: reports the worst Lenny-managed certificate
	// expiry state. A nil source (no dynamic client) reports healthy with a
	// "not configured" note, matching the deployer-provided-Secret model
	// where there are no cert-manager Certificate resources to probe.
	if dynClient != nil {
		selfChecks[opsservice.CheckCertManager] = opsservice.CertManagerCheck(
			certManagerSource{
				client:    dynClient,
				namespace: envOr("POD_NAMESPACE", *leaderElectNS),
				onExpiry:  setCertExpiry,
			})
	} else {
		selfChecks[opsservice.CheckCertManager] = opsservice.CertManagerCheck(nil)
	}

	// §25.4 lines 2206-2212: the in-memory (Tier-3) lock policy. The mode
	// is validated at startup so an operator typo fails fast rather than
	// silently selecting an unintended safety posture. Under the
	// single-replica-only default, a multi-replica deployment rejects
	// uncoordinated in-memory acquisitions; the replica count comes from
	// the lenny-ops Service Endpoints (re-checked every 30s). Without a
	// cluster connection (single-process dev) the counter is nil and the
	// policy treats the deployment as a single replica.
	memTier, ok := coordination.ParseMemoryTier(*locksMemoryTier)
	if !ok {
		log.Fatalf("lenny-ops: invalid --locks-memory-tier %q: want single-replica-only, always, or never", *locksMemoryTier)
	}
	var replicaCounter coordination.ReplicaCounter
	if clientset != nil {
		ns := envOr("POD_NAMESPACE", *leaderElectNS)
		ep := opsservice.NewEndpointsReplicaCounter(clientset.CoreV1(), ns, *opsServiceName)
		// §25.4 line 2208: the startup lookup runs synchronously so the
		// policy has a real count before the first acquire; the 30s
		// re-check loop then keeps it current.
		if err := ep.Refresh(ctx); err != nil {
			log.Printf("lenny-ops: §25.4 startup replica-count lookup failed (assuming single replica): %v", err)
		}
		go ep.Run(ctx, opsservice.ReplicaPollInterval)
		replicaCounter = ep
	}
	lockCoordination := coordination.NewCoordinationGate(memTier, replicaCounter)
	log.Printf("lenny-ops: §25.4 remediation-lock memoryTier=%s", memTier)

	// The §25.4 tiered remediation-lock service: the Postgres Tier 1 store
	// (ops_remediation_locks, migration 0121) and the Redis Tier 2 store
	// (ops:lock:{scope}) over the always-present in-memory Tier 3 store,
	// with outage-epoch reconciliation and deterministic split-brain
	// resolution. The gate enforces ops.locks.memoryTier on the Tier 3
	// fall-through; the §25.4 lock metrics and audit events are emitted from
	// the service. The HTTP layer applies the §25.4 scope-based
	// authorization control before the service. F-25.4.6.
	lockSvc := buildLockService(pgPool, redisClient, lockCoordination,
		prometheus.DefaultRegisterer, opsEmitter, auditRecorder, replicaID)

	// The §25.11 BackupService. lenny-ops orchestrates backup/restore
	// Kubernetes Jobs through it. The Postgres-backed ops_backups store,
	// the client-go Job launcher, and the §25.4-remediation-lock-backed
	// restore:platform lock are selected when those seams are wired; the
	// in-memory store, fake launcher, and in-memory locker keep the §25.11
	// endpoints serving in a single-process degraded mode. Built after
	// lockSvc so the restore lock can adapt it. F-17.3.4 / F-25.11.3/.4.
	var backupClientset kubernetes.Interface
	if clientset != nil {
		backupClientset = clientset
	}
	// §12.8 lines 932-936: the per-region backup endpoint map and the
	// shard→region resolver. A parse failure (or a regions map with no
	// shard map) is a fatal config error so lenny-ops fails fast rather
	// than failing every backup at run time.
	backupRegionMap, backupShardResolver, err := parseBackupRegions(*backupRegions, *backupShardRegions)
	if err != nil {
		log.Fatalf("lenny-ops: §12.8 per-region backup config: %v", err)
	}
	backupSvc, backupJobs := buildBackupService(*production, backupDeps{
		Pool:            pgPool,
		Clientset:       backupClientset,
		Locks:           lockSvc,
		Recorder:        auditRecorder,
		Namespace:       envOr("POD_NAMESPACE", *leaderElectNS),
		LauncherImage:   *backupImage,
		MinIOEndpoint:   *backupMinIOEndpoint,
		MinIOBucket:     *backupMinIOBucket,
		KMSKeyID:        *backupKMSKeyID,
		ReportDSNSecret: *backupReportDSNSecret,
		Regions:         backupRegionMap,
		ShardRegions:    backupShardResolver,
	})

	// The §25.4 escalation service, the §25.10 configuration-drift
	// service, and the §25.6 DiagnosticService. Each runs against an
	// in-memory or unconfigured backing store in this single-process
	// degraded mode so the §25 endpoints serve and an agent can exercise
	// them; the durable backing stores are documented seams.
	escalationSvc := buildEscalationService(
		newStreamEscalationEmitter(opsEmitter, replicaID), pgPool, redisClient,
		auditRecorder,
		escalationConfig{
			RequireDurable:                *escalationRequireDurable,
			ReconciliationWritesPerSecond: *escalationReconcileWPS,
		})
	driftSvc := buildDriftService(driftServiceConfig{
		StaleWarningDays:        *driftSnapshotStaleWarningDays,
		RunningStateCacheTTLSec: *driftRunningStateCacheTTLSeconds,
	}, pgPool, opsEmitter, auditRecorder)
	diagnosticDeps := diagnosticSourceDeps{
		Pool:           pgPool,
		Gateway:        gwClient,
		Probes:         probes,
		ProbeTimeout:   2 * time.Second,
		AgentNamespace: *agentNamespace,
	}
	// Assign through the nil check so a nil *kubernetes.Clientset does not
	// become a non-nil kubernetes.Interface (the typed-nil interface trap).
	if clientset != nil {
		diagnosticDeps.Clientset = clientset
	}
	diagnosticSvc := buildDiagnosticService(diagnosticDeps)

	// §25.6 doctor auto-remediation orchestrator backing POST
	// /v1/admin/diagnostics/run[?fix=true]. Built only when a Kubernetes
	// client is available; otherwise the endpoint reports 503. F-25.6.2.
	dDeps := doctorDeps{
		ReleaseNS:    envOr("POD_NAMESPACE", *leaderElectNS),
		AllowedFixes: splitCSV(*doctorAllowedFixes),
		FixTimeout:   time.Duration(*doctorFixTimeout) * time.Second,
		Audit: func(ev doctor.Event) {
			auditRecorder.Record(string(ev.Type), ev.Fields, time.Time{})
		},
	}
	if *maintenanceMode {
		dDeps.MaintenanceMode = func() bool { return true }
	}
	if clientset != nil {
		dDeps.Clientset = clientset
	}
	if dynClient != nil {
		dDeps.Dynamic = dynClient
	}
	doctorSvc := buildDoctorService(dDeps)

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

	// The §25.8 platform-upgrade orchestrator (F-10.5.7) and its
	// upgrade-check client (F-10.5.5). The orchestrator drives the §25.8
	// phase machine and emits the §16.7 platform-upgrade lifecycle audit
	// events; the checker queries the operator-supplied release manifest
	// and emits platform_upgrade_available. The platform-upgrade-check
	// cron (§25.4 line 1338) runs leader-only alongside the backup jobs.
	// §25.2 historical baselines for the canonical Progress Envelope:
	// Postgres-backed (ops_operation_baselines, migration 0128) when a pool
	// is available, in-memory otherwise. The upgrade orchestrator records a
	// completion into it, and the Operations Inventory reads it to derive
	// the historical_p50 ETA. F-25.2.7.
	baselineStore := buildBaselineStore(pgPool)
	upgradeSvc := buildUpgradeService(pgPool, driftSvc, opsEmitter, baselineStore, auditRecorder)
	upgradeChecker := buildUpgradeChecker(*releaseChannelManifestPath, buildVersion, pgPool, opsEmitter, auditRecorder)
	// §25.8 live upgrade gauges (phase + duration): a collector that reads
	// the orchestrator's singleton at scrape time so the gauges advance
	// without a background goroutine. Registered on the default registry
	// so the §16.9 /metrics exposition scrapes them.
	metrics.MustRegister(prometheus.DefaultRegisterer, upgradeservice.NewMetricsCollector(upgradeSvc))
	// §25.8 GET /v1/admin/platform/version/full aggregator over the
	// component sources lenny-ops can reach; it also raises the
	// lenny_platform_version_drift gauge on each aggregation.
	versionAggregator := buildVersionAggregator(
		buildVersion, *gatewayURL, gatewayHTTP, pgPool, clientset,
		envOr("POD_NAMESPACE", *leaderElectNS))
	// §25.8 config diff/apply: the operator surface over the gateway's own
	// config API. Wired only when a gateway client exists; otherwise the
	// routes stay unmapped (404).
	platformConfigSvc := buildPlatformConfigService(gwClient)
	cronJobs := append(backupJobs, upgradeCheckJob(upgradeChecker), versionDriftJob(versionAggregator),
		deliveryRetentionJob(delivery.Store))

	// §25.4 Operations Inventory: a scatter-gather view over the wired
	// subsystem sources. The lock, escalation, and platform-upgrade
	// adapters project their live records; the §25.10 drift reconcile
	// tracker is already an operations.Source. The backup/restore,
	// idempotency, and webhook-delivery kinds plug in as their subsystems
	// expose enumeration. F-25.4.3.
	inventory := operations.New(
		opsinventory.NewLockSource(lockSvc),
		opsinventory.NewEscalationSource(escalationSvc),
		opsinventory.NewUpgradeSource(upgradeSvc),
		driftSvc.ReconcileSource(),
	)
	// §25.2 lines 357-396: enrich every in-progress operation's Progress
	// with the canonical ETA (historical_p50 from the baseline store) and
	// the cadence-relative stalledForSeconds on each List/Get. F-25.2.7.
	inventory.SetProgressBaselines(time.Now, baselineStore)
	// §25.2 lines 399-401 operations-observe loop: maintain the
	// lenny_ops_operations_stalled gauge (OperationStalled alert backing)
	// and emit operation_progressed on step transitions and percent-
	// threshold crossings. Runs leader-only via Reconcilers. F-25.2.7 /
	// F-25.2.14.
	operationsObserver := opsinventory.NewObserver(inventory, setOperationsStalled,
		func(ctx context.Context, ev opsinventory.ProgressUpdate) {
			payload, err := json.Marshal(map[string]any{
				"operationId":       ev.OperationID,
				"kind":              ev.Kind,
				"percent":           ev.Percent,
				"completedSteps":    ev.CompletedSteps,
				"totalSteps":        ev.TotalSteps,
				"currentStep":       ev.CurrentStep,
				"currentStepDetail": ev.CurrentStepDetail,
				"crossedThresholds": ev.CrossedThresholds,
				"stepTransition":    ev.StepTransition,
			})
			if err != nil {
				return
			}
			_ = opsEmitter.Emit(ctx, events.OperationalEvent{
				Type:            events.EventOperationProgressed.CloudEventsType(),
				Subject:         "operation/" + ev.OperationID,
				DataContentType: "application/json",
				Data:            payload,
			})
		})

	// §25.4 idempotency: durable when Postgres is available, in-memory in
	// single-process degraded mode. Built before the service body so the
	// leader-only idempotency-cleanup reconciler can drain it. Required-key
	// endpoints (upgrade start, restore execute, full backup) reject a
	// missing key and fail closed on a store outage at Tier 2/3.
	idemStore := buildIdempotencyStore(pgPool)

	// §25.4 line 1339 — the bundleRules reconciler. §25.13 line 4816
	// makes the bundled alerting rules static manifests rendered at Helm
	// install/upgrade time with no runtime mutation, so the leader-only
	// reconciler does not re-render rules; it keeps the §25.13 bundled-rules
	// observability gauges (lenny_alerting_rules_bundled{format} and
	// lenny_alerting_rule_overrides) current on the lenny-ops /metrics
	// surface from the chart-supplied bundle format + override count.
	alertingMx, err := alertingmetrics.New(prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: register §25.13 alerting metrics: %v", err)
	}
	bundleRulesReconcile := bundleRulesReconciler(alertingMx, splitAndTrim(*alertingBundleFormats), *alertingOverrideCount)

	// The §25.4 service body: leader election plus the background loops.
	// The §25.11 scheduled-backup cron jobs and the §25.8
	// platform-upgrade-check cron register here; the §25.4 leader-only
	// reconciliation goroutines (escalation flush, idempotency cleanup,
	// lock outage-epoch reconcile, drift snapshot validation) register via
	// Reconcilers. F-25.4.16.
	svc, err := opsservice.New(opsservice.Config{
		ReplicaID: replicaID,
		Elector:   elector,
		Webhook:   webhook,
		CronJobs:  cronJobs,
		// §25.4 line 1337: the leader-only reconciliation goroutines. Each
		// runs only on the replica holding the lenny-ops-leader Lease, so a
		// multi-replica deployment drives one flush/cleanup/reconcile loop,
		// not one per replica.
		Reconcilers: opsservice.Reconcilers{
			// §25.4 lines 2407-2415: drain the in-memory escalation buffer up
			// to a recovered durable tier (preserving the authoring timestamp
			// and the emitted flag). F-25.4.7.
			EscalationFlush: func(ctx context.Context) error {
				n, err := escalationSvc.Flush(ctx)
				if err == nil && n > 0 {
					log.Printf("lenny-ops: escalation flush promoted %d buffered escalation(s) to durable storage", n)
				}
				return err
			},
			// §25.4 lines 2070-2072, 2127: remove idempotency keys past their
			// TTL so the ops_idempotency_keys table does not grow unbounded.
			IdempotencyCleanup: func(ctx context.Context) error {
				n, err := idemStore.PruneExpired(ctx, time.Now().UTC())
				if err == nil && n > 0 {
					log.Printf("lenny-ops: idempotency cleanup removed %d expired key(s)", n)
				}
				return err
			},
			// §25.4 lines 2226-2267: resolve remediation locks orphaned by a
			// storage outage — bring the Postgres epoch up to MAX, copy the
			// Redis locks in, and apply the deterministic split-brain
			// resolution rule. F-25.4.6.
			LockEpochReconcile: func(ctx context.Context) error {
				return lockSvc.Reconcile(ctx)
			},
			// §25.4 line 1337: validate that the stored desired-state snapshot
			// is current, warning when it drifts past the staleness threshold.
			DriftSnapshotValidate: func(ctx context.Context) error {
				fresh, err := driftSvc.SnapshotFreshness(ctx)
				if err != nil {
					return err
				}
				switch {
				case !fresh.Present:
					log.Printf("lenny-ops: drift snapshot validation: no live bootstrap_seed_snapshot present")
				case fresh.Stale:
					log.Printf("lenny-ops: drift snapshot validation: live snapshot is stale (age %ds)", fresh.AgeSeconds)
				}
				return nil
			},
			// §25.2 lines 399-401: scan the Operations Inventory to maintain
			// the lenny_ops_operations_stalled gauge and emit
			// operation_progressed on step transitions and percent-threshold
			// crossings. Leader-only so a multi-replica deployment emits one
			// stream. F-25.2.7 / F-25.2.14.
			OperationsObserve: operationsObserver.Tick,
			// §25.4 line 1339: the bundleRules reconciler. Leader-only so a
			// multi-replica deployment re-asserts the §25.13 bundled-rules
			// gauges from one replica. F-25.4.17.
			BundleRulesReconcile: bundleRulesReconcile,
		},
		SelfHealthChecks:   selfChecks,
		SelfHealthInterval: *selfHealthInterval,
		OnSelfHealthChange: func(prev, next opsservice.SelfHealthReport) {
			// §25.5 line 2590: lenny-ops emits ops_health_status_changed
			// — one of the signals it originates itself — onto the same
			// ops:events:stream the gateway writes to, so subscribers,
			// pollers, and SSE clients on any replica observe the
			// transition. The local opsstream.Service buffer always
			// receives it; the Redis write is best-effort (logged on
			// failure) per the §25.5 buffer-fallback model.
			log.Printf("lenny-ops: self-health %s -> %s (replica %s)",
				prev.StatusText, next.StatusText, replicaID)
			fields := map[string]any{
				"replicaId":  replicaID,
				"previous":   prev.StatusText,
				"current":    next.StatusText,
				"transition": prev.StatusText + " -> " + next.StatusText,
			}
			// §11.7: commit the durable ops_health_status_changed audit row
			// (logged only in the degraded no-Postgres mode). F-25.4.22.
			auditRecorder.Record(string(events.EventOpsHealthStatusChanged), fields, time.Now())
			payload, _ := json.Marshal(fields)
			if err := opsEmitter.Emit(ctx, events.OperationalEvent{
				Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
				Subject:         "ops/" + replicaID,
				Severity:        selfHealthEventSeverity(next.StatusText),
				DataContentType: "application/json",
				Data:            payload,
			}); err != nil {
				log.Printf("lenny-ops: emit ops_health_status_changed: %v", err)
			}
		},
		// §16.8 line 704 — publish lenny_ops_self_health_status{check} on
		// every evaluation so the §16.9 /metrics scrape reflects the live
		// per-check status, not only the last transition.
		OnSelfHealthSample: publishSelfHealthMetric,
	})
	if err != nil {
		log.Fatalf("lenny-ops: build service: %v", err)
	}

	// §4.0 / §25.13: the in-process alert tracker. lenny-ops has no
	// PromQL backend wired in this commit, so the evaluator runs with
	// NoopExprEvaluator and no rule fires — the §25.5 alert events come
	// from Prometheus's `/api/v1/alerts` aggregation rather than this
	// evaluator until a real backend lands. The wiring is unconditional
	// so a future commit that supplies a real ExprEvaluator only swaps
	// the backend.
	//
	// spec: §25.16 line 5124. When --prometheus-url is set, the operator
	// has supplied a BYO Prometheus HTTP API endpoint; the §25.13
	// ExprEvaluator and the §25.4 cross-replica health aggregator should
	// route through it (the HTTP client is built here so the future
	// backend swap is a single-line change). When empty, lenny-ops
	// degrades to the per-replica fan-out fallback the §25.16 Minimal
	// block accepts. F-25.16.4.
	// §25.4 lines 1914-1916 — register lenny_prometheus_query_duration_seconds
	// {kind} on the default registry so the §16.9 /metrics surface exposes it.
	// The histogram receives observations once a PromQL query path consumes a
	// PrometheusClient built with this adapter (below); until then the
	// pre-stamped kind series read 0. F-25.4.18.
	queryMetrics, err := opsmetrics.NewPromQueryMetrics(prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: register §25.4 prometheus query-duration metric: %v", err)
	}
	if *prometheusURL != "" {
		log.Printf("lenny-ops: §25.16 BYO Prometheus configured at %s", *prometheusURL)
		// Build the §25.4 Prometheus client with the query-duration adapter
		// so each query the cross-replica health/recommendation aggregator
		// runs is timed. The aggregator consumer rides on the alert-evaluator
		// backend swap; the client + metric wiring is in place for it.
		if _, perr := opsmetrics.NewPrometheusClient(opsmetrics.PrometheusConfig{
			BaseURL: *prometheusURL,
			Metrics: queryMetrics,
		}); perr != nil {
			log.Printf("lenny-ops: §25.4 prometheus client config rejected: %v", perr)
		}
	} else {
		log.Printf("lenny-ops: §25.16 BYO Prometheus not configured; cross-replica health degrades to per-replica fan-out")
	}
	alertEvaluator := evaluator.NewWithEmitter(
		rules.Catalog(),
		evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{
			Emitter: opsEmitter,
			Source:  "//lenny.dev/ops/" + replicaID,
		},
	)
	go alertEvaluator.Run(ctx)

	// §25.4 lines 1562-1564: build the OIDC authentication + role gate.
	// The verify key is the shared HMAC signing key (the gateway Token
	// Service / §17.4 embedded OIDC key). When no key is configured the
	// surface is unauthenticated, which is rejected in production: serving
	// the platform-admin remediation-lock / backup / drift surface without
	// authentication is the §25.4 security regression the gate closes.
	authCfg, err := buildAuthConfig(*bearerTrustHMACKeyFile, *oidcIssuerURL, *authMultiTenant,
		*production, *rateLimitRPS, *rateLimitBurst)
	if err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}

	// The §25.4 HTTP surface. Every replica serves it, leader or not. It
	// hosts the §25.6 diagnostics, the §25.7 runbook index, the §25.4
	// self-health, remediation-lock, and escalation endpoints, the
	// §25.10 drift endpoints, the §25.11 backup endpoints, and the
	// §25.12 MCP management server.
	// §25.4 lines 2528-2534: the pod-log proxy reads container logs via the
	// Kubernetes API so agents do not need kubectl access. Wired only when a
	// cluster connection is available; otherwise the endpoint reports the
	// proxy unavailable.
	var podLogs opsserver.PodLogReader
	if clientset != nil {
		podLogs = k8sPodLogReader{pods: clientset.CoreV1()}
	}

	// §25.4 GET /v1/admin/me platform context + capabilities snapshot. The
	// capabilities reflect the actual wired state; opsReplicas reads the
	// live Endpoints count. F-25.4.2.
	meConfig := &opsserver.MeConfig{
		InstallationID:           *installationID,
		Version:                  buildVersion,
		Tier:                     *platformTier,
		Namespace:                envOr("POD_NAMESPACE", *leaderElectNS),
		OpsServiceURL:            *opsServiceURL,
		GatewayURL:               *gatewayURL,
		Issuer:                   *oidcIssuerURL,
		TokenRefreshBeforeExpiry: gatewayTokenRefreshBefore.String(),
		Capabilities: opsserver.Capabilities{
			PrometheusAvailable:     *prometheusURL != "",
			OpsReplicas:             1,
			MtlsInternal:            *gatewayInternalTLS,
			LockMemoryTier:          *locksMemoryTier,
			TenantFiltering:         *authMultiTenant,
			HeadlessServiceFallback: *gatewayHeadlessSvc != "",
		},
		LiveOpsReplicas: func() int {
			if replicaCounter != nil {
				return replicaCounter.ReplicaCount()
			}
			return 1
		},
	}

	// §25.4 audit recorder for identity.discovered (line 1641) +
	// operations.inventory_queried (line 1779). Each emits a durable §11.7
	// platform-audit row through auditRecorder (logged only in the degraded
	// no-Postgres mode). F-25.4.22.
	opsAudit := opsserver.LogAuditRecorder{Sink: func(event string, fields map[string]any) {
		auditRecorder.Record(event, fields, time.Time{})
	}}
	opsHandler := opsserver.New(opsserver.Options{
		Probes:             probes,
		Runbooks:           runbookSource,
		SelfHealth:         svc.Monitor(),
		Leader:             svc,
		Backups:            backupSvc,
		Diagnostics:        diagnosticSvc,
		Doctor:             doctorSvc,
		Drift:              driftSvc,
		Locks:              lockSvc,
		LockCoordination:   lockCoordination,
		Escalations:        escalationSvc,
		EventStream:        eventStream,
		EventSubscriptions: eventSubscriptions,
		// §25.5 line 2751: the subscription_cache_invalidate peer RPC.
		CacheInvalidator:     subscriptionCache,
		CacheInvalidateToken: delivery.InvalidateToken,
		ReleaseChannel:       releaseChannelPub,
		Upgrade:              upgradeSvc,
		UpgradeChecker:       upgradeChecker,
		VersionAggregator:    versionAggregator,
		PlatformConfig:       platformConfigSvc,
		PodLogs:              podLogs,
		Production:           *production,
		DiagnosticsAudit:     buildDiagnosticsAudit(*diagnosticsAuditRate, auditRecorder),
		Auth:                 authCfg,
		Idempotency:          idemStore,
		// §25.4 ops.idempotency.{keyTTLSeconds,longRunningKeyTTLSeconds}: a
		// zero value keeps the opsidem built-in (24h/7d). F-25.4.9.
		IdempotencyStandardTTL:    time.Duration(*idempotencyKeyTTL) * time.Second,
		IdempotencyLongRunningTTL: time.Duration(*idempotencyLongRunningTTL) * time.Second,
		Inventory:                 inventory,
		Me:                        meConfig,
		Audit:                     opsAudit,
		// §16.8 / §16.9: expose every instrument registered on the process
		// default registry (self-health, backup, drift, rate-limit,
		// diagnostics) on the mandatory lenny-ops scrape target.
		Metrics: promhttp.Handler(),
	})
	srv := &http.Server{
		Addr:              *addr,
		Handler:           opsHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// spec: §25.9 lines 3699-3700 — flush closed diagnostics-audit
	// coalescing windows on a periodic tick so a window emits even during
	// an idle period, and drain every open window on shutdown.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				opsHandler.FlushDiagnosticsAudit()
				return
			case t := <-ticker.C:
				opsHandler.SweepDiagnosticsAudit(t)
			}
		}
	}()

	// spec: §25.5 lines 2787, 2789 — refresh the event-stream gauges
	// (lenny_ops_events_stream_length from the Redis XLEN of
	// ops:events:stream, lenny_ops_events_sse_active_connections from the
	// live SSE subscriber count) on every replica so the §16.9 scrape
	// reflects current depth and connection count.
	// §25.4 lines 2491-2497 — refresh the self-health source gauges
	// (postgres pool-active connections, redis consumer lag, webhook
	// backlog) on the same cadence so the §16.9 scrape exposes the raw
	// inputs behind the lenny_ops_self_health_status{check} statuses.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		sample := func() {
			sampleEventStreamGauges(ctx, redisClient, eventStream)
			sampleSelfHealthSourceGauges(pgPool, nil, webhook.Backlog)
		}
		sample()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sample()
			}
		}
	}()

	// spec: §25.4 line 2193 — the remediation-lock reap. Expired locks are
	// cleaned up lazily on acquire and by this 60s periodic sweep on every
	// replica: the in-memory tier is replica-local so each replica reaps its
	// own, and the durable-tier DELETE is idempotent across replicas. The
	// sweep also emits the remediation.lock_expired audit event for each
	// in-memory lock removed.
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n := lockSvc.Reap(ctx); n > 0 {
					log.Printf("lenny-ops: remediation-lock reap removed %d expired lock(s)", n)
				}
			}
		}
	}()

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

// configureStructuredLogging installs the §25.4 JSON logger as the
// process-wide slog.Default. The pkg/observability/logging handler
// auto-attaches the §16.4 correlation fields (component, operation_id,
// agent_name, trace_id, …) from any context that carries a
// correlation.Fields value. The stdlib log package is redirected so
// existing log.Printf call sites also surface as structured records and
// no log line escapes the §25.4 format.
//
// spec: §25.4 lines 2499-2526; §16.4 lines 370-372. Delegates to the shared
// logging.Setup so the gateway, lenny-ops, and every other binary install
// the identical §16.4 handler and stdlib-log bridge (component, ts in UTC,
// and any context-borne correlation fields). lenny-ops logs to stderr.
func configureStructuredLogging() {
	opsLogging.Setup(os.Stderr, "lenny-ops")
}

// envOr returns the environment variable name when set, else fallback.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// buildWebhookSSRF assembles the §25.5 callback-URL SSRF validator from
// the ops.webhooks.{allowHTTP,blockedCIDRs,domainAllowlist} Helm values.
// blockedCIDRs and domainAllowlist are comma-separated; a malformed CIDR
// entry is logged and skipped so one typo does not disable the whole
// policy. spec: §25.5 lines 2735-2745. F-25.4.9.
func buildWebhookSSRF(allowHTTP bool, blockedCIDRs, domainAllowlist string) *eventsubscription.SSRFValidator {
	cfg := eventsubscription.SSRFConfig{
		AllowHTTP:       allowHTTP,
		DomainAllowlist: splitCSV(domainAllowlist),
	}
	for _, raw := range splitCSV(blockedCIDRs) {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			log.Printf("lenny-ops: ignoring malformed ops.webhooks.blockedCIDRs entry %q: %v", raw, err)
			continue
		}
		cfg.BlockedCIDRs = append(cfg.BlockedCIDRs, p)
	}
	return eventsubscription.NewSSRFValidator(cfg)
}

// splitCSV splits a comma-separated flag value into trimmed, non-empty
// tokens. An empty input yields nil so the caller's zero-policy default
// (no allowlist, no extra CIDRs) holds.
func splitCSV(s string) []string {
	var out []string
	for _, tok := range strings.Split(s, ",") {
		if t := strings.TrimSpace(tok); t != "" {
			out = append(out, t)
		}
	}
	return out
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

// envInt parses the named environment variable as an int, falling back
// when it is unset or malformed.
func envInt(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// durationOrDefault converts a seconds count to a Duration, returning def
// when seconds is non-positive. The §25.4 ops.security.oidc values arrive
// from the chart as seconds; this keeps the flag defaults expressed as a
// single source of truth.
func durationOrDefault(seconds int, def time.Duration) time.Duration {
	if seconds <= 0 {
		return def
	}
	return time.Duration(seconds) * time.Second
}

// buildAuthConfig assembles the §25.4 lines 1562-1564 OIDC
// authentication + role gate for the operability surface.
//
// The v1 verify key is the shared HMAC signing key at hmacKeyFile (the
// gateway Token Service / §17.4 embedded OIDC key). When an issuer is
// supplied the verifier additionally asserts the iss claim. The per-
// service-account rate limiter (§25.4 line 2001) is always attached when
// auth is enabled.
//
// When no key file is configured the surface is unauthenticated: that is
// admitted only outside production. In production it is a fatal
// misconfiguration (serving the platform-admin remediation-lock / backup
// / drift surface anonymously is the §25.4 security regression the gate
// exists to close), reported as an error so the caller can refuse to
// start.
func buildAuthConfig(hmacKeyFile, issuer string, multiTenant, production bool, rps float64, burst int) (*opsserver.AuthConfig, error) {
	if hmacKeyFile == "" {
		if production {
			return nil, errors.New("§25.4 line 1562 requires authentication in production: set --bearer-trust-hmac-key-file (LENNY_BEARER_TRUST_HMAC_KEY_FILE)")
		}
		log.Printf("lenny-ops: §25.4 WARNING — no bearer verify key configured; the operability surface is UNAUTHENTICATED (dev only)")
		return nil, nil
	}
	signer, err := jwt.LoadHMACKeyFile(hmacKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load bearer trust key %s: %w", hmacKeyFile, err)
	}
	var verifier jwt.Verifier = signer
	if issuer != "" {
		verifier = jwt.NewClaimChecker(verifier, jwt.ExpectedClaims{Issuer: issuer})
	}
	return &opsserver.AuthConfig{
		Options: authmw.Options{
			Verifier:    verifier,
			MultiTenant: multiTenant,
			// Outside production the dev headers (X-Lenny-Tenant-ID /
			// X-Lenny-User-ID / X-Lenny-Roles) remain a convenience
			// transport; production anchors every claim to the bearer JWT.
			AllowDevHeaders: !production,
			AllowDevRoles:   !production,
		},
		RateLimiter: opsserver.NewRateLimiter(rps, burst),
	}, nil
}

// envFloat parses the named environment variable as a float64, falling
// back when it is unset or malformed.
func envFloat(name string, fallback float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
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
