// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// opsFlags carries every lenny-ops command-line flag as the pointer the
// flag package populates at parse time. The §25.4 composition root parses
// its inputs once into this value and threads them to each subsystem
// builder, so a build step reads a flag through the embedded *opsFlags
// rather than re-deriving it. The fields keep the names the former inline
// composition root used, so the moved construct-and-wire blocks read
// unchanged.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root parses inputs once and threads them to every
// subsystem builder; §25.4 names the lenny-ops flag surface.
type opsFlags struct {
	addr                             *string
	postgresDSN                      *string
	redisURL                         *string
	redisSentinelAddrs               *string
	redisSentinelMaster              *string
	redisPassword                    *string
	redisSentinelPassword            *string
	redisTLS                         *bool
	redisAllowInsecure               *bool
	gatewayURL                       *string
	leaderElectNS                    *string
	agentNamespace                   *string
	runbookDir                       *string
	doctorFixTimeout                 *int
	doctorAllowedFixes               *string
	doctorRenderDir                  *string
	maintenanceMode                  *bool
	backupImage                      *string
	backupMinIOEndpoint              *string
	backupMinIOBucket                *string
	backupKMSKeyID                   *string
	backupReportDSNSecret            *string
	backupIncludeSensitive           *bool
	backupExcludeTables              *string
	backupRedactColumns              *string
	backupRegions                    *string
	backupShardRegions               *string
	selfHealthInterval               *time.Duration
	eventsStreamMaxLen               *int64
	webhookTrackingMode              *string
	webhookRetentionDays             *int
	webhookFailuresRetentionDays     *int
	memoryLimitBytes                 *int64
	production                       *bool
	releaseChannelKeyPath            *string
	releaseChannelKeyID              *string
	releaseChannelPrevKeyPath        *string
	releaseChannelPrevKeyID          *string
	releaseChannelManifestPath       *string
	registryURL                      *string
	registryPullSecret               *string
	registryRequireDigest            *bool
	registryOverrides                *string
	opsRollTimeout                   *int
	gatewayRollTimeout               *int
	controllerRollTimeout            *int
	shutdownTimeout                  *time.Duration
	pgauditLogFile                   *string
	pgauditTenantID                  *string
	prometheusURL                    *string
	diagnosticsAuditRate             *int
	driftSnapshotStaleWarningDays    *int
	driftRunningStateCacheTTLSeconds *int
	driftHelmValuesConfigMap         *string
	driftHelmValuesKey               *string
	escalationRequireDurable         *bool
	escalationReconcileWPS           *int
	oidcIssuerURL                    *string
	bearerTrustHMACKeyFile           *string
	authMultiTenant                  *bool
	rateLimitRPS                     *float64
	rateLimitBurst                   *int
	locksMemoryTier                  *string
	opsServiceName                   *string
	locksMinTTL                      *int
	locksDefaultTTL                  *int
	locksMaxTTL                      *int
	idempotencyKeyTTL                *int
	idempotencyLongRunningTTL        *int
	leaderLeaseDuration              *int
	leaderRenewDeadline              *int
	leaderRetryPeriod                *int
	webhookAllowHTTP                 *bool
	webhookBlockedCIDRs              *string
	webhookDomainAllowlist           *string
	installationID                   *string
	platformTier                     *string
	opsServiceURL                    *string
	gatewayInternalTLS               *bool
	gatewayHeadlessSvc               *string
	gatewayTLSPort                   *int
	gatewayPlaintextPort             *int
	gatewayFanOutTimeout             *time.Duration
	gatewayBreakerThreshold          *int
	gatewayBreakerResetAfter         *time.Duration
	gatewaySATokenFile               *string
	gatewayTokenRefreshBefore        *time.Duration
	gatewayTokenMinTTL               *time.Duration
	gatewayCABundleFile              *string
	alertingBundleFormats            *string
	alertingOverrideCount            *int
}

// parseFlags defines every lenny-ops flag on flag.CommandLine, parses the
// command line, and returns the populated opsFlags. It performs no wiring;
// the caller hands the value to runOps. The flag definitions are grouped
// into per-domain register helpers so each stays a navigable block rather
// than one flat list, mirroring the gateway composition root.
//
// spec: §25.4 — the lenny-ops flag surface; §4.1 — the composition root
// parses its inputs once and threads them to each subsystem builder.
func parseFlags() *opsFlags {
	f := &opsFlags{}
	f.registerCoreFlags()
	f.registerBackupFlags()
	f.registerEventFlags()
	f.registerUpgradeFlags()
	f.registerObservabilityFlags()
	f.registerAuthFlags()
	f.registerLockFlags()
	f.registerWebhookFlags()
	f.registerGatewayClientFlags()
	flag.Parse()
	return f
}

// registerCoreFlags registers the listener, Postgres, Redis, namespace, and
// runbook flags. spec: §25.4.
func (f *opsFlags) registerCoreFlags() {
	f.addr = flag.String("addr", ":8090", "address the lenny-ops HTTP server binds to")
	f.postgresDSN = flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, lenny-ops uses it for audit, backup, "+
			"and upgrade state; when empty those features degrade. Override via LENNY_POSTGRES_DSN.")
	f.redisURL = flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When empty the "+
			"event stream falls back to the gateway buffer. Mutually exclusive with "+
			"--redis-sentinel-addrs. Override via LENNY_REDIS_URL.")
	f.redisSentinelAddrs = flag.String("redis-sentinel-addrs", os.Getenv("LENNY_REDIS_SENTINEL_ADDRS"),
		"Comma-separated list of §12.8 Redis Sentinel host:port pairs. When set with "+
			"--redis-sentinel-master, lenny-ops discovers the master via Sentinel and follows "+
			"automatic failover. Mutually exclusive with --redis-url.")
	f.redisSentinelMaster = flag.String("redis-sentinel-master", os.Getenv("LENNY_REDIS_SENTINEL_MASTER"),
		"§12.8 Redis Sentinel monitored master name (e.g., lenny-master). Required when "+
			"--redis-sentinel-addrs is set.")
	f.redisPassword = flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password applied to both direct and Sentinel modes. §12.4 requires AUTH; an empty password fails startup unless --redis-allow-insecure is set. Override via LENNY_REDIS_PASSWORD.")
	f.redisSentinelPassword = flag.String("redis-sentinel-password", os.Getenv("LENNY_REDIS_SENTINEL_PASSWORD"),
		"AUTH password for the sentinels themselves. Optional; sentinels typically run unauthenticated.")
	f.redisTLS = flag.Bool("redis-tls", envBool("LENNY_REDIS_TLS", false),
		"§12.4 request TLS on the Sentinel path. The direct-URL path derives TLS from the rediss:// scheme instead. TLS is mandatory unless --redis-allow-insecure is set. Override via LENNY_REDIS_TLS.")
	f.redisAllowInsecure = flag.Bool("redis-allow-insecure", envBool("LENNY_REDIS_ALLOW_INSECURE", false),
		"§12.4 opt out of the mandatory Redis AUTH-and-TLS startup invariant. Defaults off; set only for a dev or local Redis. Override via LENNY_REDIS_ALLOW_INSECURE.")
	f.gatewayURL = flag.String("gateway-url", os.Getenv("LENNY_GATEWAY_URL"),
		"§25.4 gateway admin API base URL (the internal ClusterIP Service). Used for the "+
			"connectivity probe and gateway-backed diagnostics. Override via LENNY_GATEWAY_URL.")
	f.leaderElectNS = flag.String("leader-election-namespace", envOr("LENNY_LEADER_ELECTION_NAMESPACE", "lenny-system"),
		"namespace that holds the §25.4 lenny-ops-leader Lease")
	f.agentNamespace = flag.String("agent-namespace", envOr("LENNY_AGENT_NAMESPACE", "lenny-system"),
		"namespace agent pods run in; the §25.6 session diagnosis reads pod failure "+
			"signals and stamps the relatedLogs reference against it")
	f.runbookDir = flag.String("runbook-dir", envOr("LENNY_RUNBOOK_DIR", "docs/runbooks"),
		"directory of §25.7 operational-runbook markdown files the runbook index serves")
	f.shutdownTimeout = flag.Duration("shutdown-timeout", 10*time.Second, "graceful shutdown timeout")
}

// registerBackupFlags registers the §25.6 doctor and §25.11 backup flags.
// spec: §25.6, §25.11.
func (f *opsFlags) registerBackupFlags() {
	// §25.6 doctor auto-remediation guardrails (admin.doctor.* / global.maintenanceMode).
	f.doctorFixTimeout = flag.Int("doctor-fix-timeout-seconds", 120,
		"§25.6 admin.doctor.fixTimeoutSeconds: per-remediation timeout for `doctor --fix`")
	f.doctorAllowedFixes = flag.String("doctor-allowed-fixes", os.Getenv("LENNY_DOCTOR_ALLOWED_FIXES"),
		"§25.6 admin.doctor.allowedFixes: comma-separated allowlist of fixable findings; empty means the full set")
	f.doctorRenderDir = flag.String("doctor-render-dir", os.Getenv("LENNY_DOCTOR_RENDER_DIR"),
		"§25.6 directory of Helm-rendered references the bootstrapConfigDrift and prometheusRuleMissing "+
			"fixes re-apply (operator-mounted via chart values). Empty leaves both findings undetected "+
			"(reported not_detected).")
	f.maintenanceMode = flag.Bool("maintenance-mode", envOr("LENNY_MAINTENANCE_MODE", "false") == "true",
		"§25.6 global.maintenanceMode: when true, `doctor --fix` skips every remediation")
	f.backupImage = flag.String("backup-image", os.Getenv("LENNY_BACKUP_IMAGE"),
		"§25.11 lenny-backup image ({platform.registry.url}/lenny-backup:{version}) the "+
			"Kubernetes JobLauncher runs. Empty (with no cluster) keeps the in-process fake "+
			"launcher; set it to orchestrate real backup/restore Jobs. Override via LENNY_BACKUP_IMAGE.")
	f.backupMinIOEndpoint = flag.String("backup-minio-endpoint", os.Getenv("LENNY_BACKUP_MINIO_ENDPOINT"),
		"§25.11 MinIO endpoint (host:port) the backup Job uploads archives to")
	f.backupMinIOBucket = flag.String("backup-minio-bucket", envOr("LENNY_BACKUP_MINIO_BUCKET", "lenny-backups"),
		"§25.11 MinIO backup bucket")
	f.backupKMSKeyID = flag.String("backup-kms-key-id", os.Getenv("LENNY_BACKUP_KMS_KEY_ID"),
		"§12.9 SSE-KMS key for the backup upload; empty selects SSE-S3")
	f.backupReportDSNSecret = flag.String("backup-report-dsn-secret", os.Getenv("LENNY_BACKUP_REPORT_DSN_SECRET"),
		"name of the Secret whose report-dsn key holds the lenny-ops DSN the backup Job uses for "+
			"the §25.11 step-8 ops_backups update; empty leaves it unset")
	f.backupIncludeSensitive = flag.Bool("backup-include-sensitive-tables",
		os.Getenv("LENNY_BACKUP_INCLUDE_SENSITIVE_TABLES") == "true",
		"§25.11 contentPolicy.includeSensitiveTables: forward --include-sensitive-tables to scheduled backups")
	f.backupExcludeTables = flag.String("backup-exclude-tables", os.Getenv("LENNY_BACKUP_EXCLUDE_TABLES"),
		"§25.11 contentPolicy.excludeTables: comma-separated tables excluded from scheduled backups beyond the defaults")
	f.backupRedactColumns = flag.String("backup-redact-columns", os.Getenv("LENNY_BACKUP_REDACT_COLUMNS"),
		"§25.11 contentPolicy.redactColumns: comma-separated columns (bare or table.column) redacted in scheduled backups")
	f.backupRegions = flag.String("backups-regions", os.Getenv("LENNY_OPS_BACKUPS_REGIONS"),
		"§12.8 backups.regions per-region backup endpoint map as JSON "+
			"({\"eu\":{\"minioEndpoint\":...,\"kmsKeyId\":...,\"accessCredentialSecret\":...}}). "+
			"Empty keeps the single-region global dump. Override via LENNY_OPS_BACKUPS_REGIONS.")
	f.backupShardRegions = flag.String("backups-shard-regions", os.Getenv("LENNY_OPS_BACKUP_SHARD_REGIONS"),
		"§12.8 shard→region map as JSON ([{\"shardId\":...,\"region\":...}]) used to dispatch "+
			"one pg_dump per region. Required when backups-regions is set. Override via "+
			"LENNY_OPS_BACKUP_SHARD_REGIONS.")
}

// registerEventFlags registers the §25.5 event-stream, webhook-tracking,
// and self-health flags. spec: §25.4, §25.5.
func (f *opsFlags) registerEventFlags() {
	f.selfHealthInterval = flag.Duration("self-health-interval", 10*time.Second,
		"§25.4 ops.selfHealth.checkIntervalSeconds — how often the self-monitor runs")
	f.eventsStreamMaxLen = flag.Int64("events-stream-max-len", envInt64("LENNY_OPS_EVENTS_STREAM_MAX_LEN", eventbuffer.DefaultStreamMaxLen),
		"§25.5 ops.events.streamMaxLen — MAXLEN of the platform-scoped ops:events:stream Redis stream. Tier 1 default 10,000; tier presets raise it (50,000 at Tier 2, 100,000 at Tier 3). Override via LENNY_OPS_EVENTS_STREAM_MAX_LEN. F-17.8.1.")
	f.webhookTrackingMode = flag.String("webhook-tracking-mode", envOr("LENNY_OPS_WEBHOOK_TRACKING_MODE", string(webhookdelivery.TrackingFull)),
		"§25.5 ops.webhooks.deliveryTrackingMode — full, failures-only, or metric-only. Override via LENNY_OPS_WEBHOOK_TRACKING_MODE.")
	f.webhookRetentionDays = flag.Int("webhook-delivery-retention-days", envInt("LENNY_OPS_WEBHOOK_DELIVERY_RETENTION_DAYS", 7),
		"§25.5 ops.webhooks.deliveryRetentionDays — lifetime of a delivery row. Tier defaults 1/7/30 days. Override via LENNY_OPS_WEBHOOK_DELIVERY_RETENTION_DAYS.")
	f.webhookFailuresRetentionDays = flag.Int("webhook-failures-retention-days", envInt("LENNY_OPS_WEBHOOK_FAILURES_ONLY_RETENTION_DAYS", 0),
		"§25.5 ops.webhooks.failuresOnlyRetentionDays — lifetime of failed delivery rows when longer than the default; 0 uses deliveryRetentionDays. Override via LENNY_OPS_WEBHOOK_FAILURES_ONLY_RETENTION_DAYS.")
	f.memoryLimitBytes = flag.Int64("memory-limit-bytes", envInt64("LENNY_MEMORY_LIMIT_BYTES", 0),
		"§25.4 container memory limit in bytes for the memory_pressure self-health check; "+
			"0 disables the check. Override via LENNY_MEMORY_LIMIT_BYTES.")
	f.production = flag.Bool("production", envBool("LENNY_PRODUCTION", false),
		"§25.11: when set, a full backup requires confirm:true. Override via LENNY_PRODUCTION.")
}

// registerUpgradeFlags registers the §25.8 release-channel, registry, and
// platform-upgrade roll-timeout flags. spec: §25.8.
func (f *opsFlags) registerUpgradeFlags() {
	f.releaseChannelKeyPath = flag.String("release-channel-key-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_KEY_FILE"),
		"path to the PEM file carrying the §25.8 release-channel Ed25519 signing key (PKCS8). "+
			"When empty the release-channel publisher is not registered and GET /v1/latest returns 404. "+
			"Override via LENNY_RELEASE_CHANNEL_KEY_FILE.")
	f.releaseChannelKeyID = flag.String("release-channel-key-id",
		envOr("LENNY_RELEASE_CHANNEL_KEY_ID", ""),
		"identifier of the §25.8 release-channel signing key (appears in the "+
			"X-Lenny-Release-Signature envelope). Required when --release-channel-key-file is set.")
	f.releaseChannelPrevKeyPath = flag.String("release-channel-previous-key-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_FILE"),
		"path to the PEM file carrying the §25.8 release-channel previous public key. "+
			"When set, signatures emitted under the previous key remain valid during the "+
			"rotation overlap window. Override via LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_FILE.")
	f.releaseChannelPrevKeyID = flag.String("release-channel-previous-key-id",
		envOr("LENNY_RELEASE_CHANNEL_PREVIOUS_KEY_ID", ""),
		"identifier of the §25.8 previous release-channel key during the rotation overlap window.")
	f.releaseChannelManifestPath = flag.String("release-channel-manifest-file",
		os.Getenv("LENNY_RELEASE_CHANNEL_MANIFEST_FILE"),
		"path to the §25.8 release-channel manifest JSON the publisher serves. When set "+
			"the publisher loads this file at startup and serves it on GET /v1/latest. "+
			"Override via LENNY_RELEASE_CHANNEL_MANIFEST_FILE.")
	// §25.8 platform.registry.* — the base image-registry configuration the
	// runtime registry API (GET/PUT /v1/admin/platform/registry) overlays.
	f.registryURL = flag.String("registry-url", envOr("LENNY_PLATFORM_REGISTRY_URL", "ghcr.io/lennylabs"),
		"§25.8 platform.registry.url: the base image registry the upgrade "+
			"system resolves component images against. Override via LENNY_PLATFORM_REGISTRY_URL.")
	f.registryPullSecret = flag.String("registry-pull-secret", os.Getenv("LENNY_PLATFORM_REGISTRY_PULL_SECRET"),
		"§25.8 platform.registry.pullSecretName: the image-pull Secret name (value not stored). "+
			"Override via LENNY_PLATFORM_REGISTRY_PULL_SECRET.")
	f.registryRequireDigest = flag.Bool("registry-require-digest", envBool("LENNY_PLATFORM_REGISTRY_REQUIRE_DIGEST", false),
		"§25.8 platform.registry.requireDigest: require digest-pinned references. "+
			"Override via LENNY_PLATFORM_REGISTRY_REQUIRE_DIGEST.")
	f.registryOverrides = flag.String("registry-overrides", os.Getenv("LENNY_PLATFORM_REGISTRY_OVERRIDES"),
		"§25.8 platform.registry.overrides as a JSON object mapping component short name to "+
			"full image reference. Override via LENNY_PLATFORM_REGISTRY_OVERRIDES.")
	// §25.8 platform.upgrade.* roll timeouts for the OpsRoll watchdog.
	f.opsRollTimeout = flag.Int("ops-roll-timeout-seconds", envInt("LENNY_PLATFORM_OPS_ROLL_TIMEOUT_SECONDS", 600),
		"§25.8 platform.upgrade.opsRollTimeoutSeconds: the OpsRoll watchdog auto-rollback timeout.")
	f.gatewayRollTimeout = flag.Int("gateway-roll-timeout-seconds", envInt("LENNY_PLATFORM_GATEWAY_ROLL_TIMEOUT_SECONDS", 1200),
		"§25.8 platform.upgrade.gatewayRollTimeoutSeconds.")
	f.controllerRollTimeout = flag.Int("controller-roll-timeout-seconds", envInt("LENNY_PLATFORM_CONTROLLER_ROLL_TIMEOUT_SECONDS", 600),
		"§25.8 platform.upgrade.controllerRollTimeoutSeconds.")
}

// registerObservabilityFlags registers the §4.4/§11.7 pgaudit, §25.16 BYO
// Prometheus, §25.9 diagnostics-audit, and §25.10 drift flags.
// spec: §4.4, §11.7, §25.9, §25.10, §25.16.
func (f *opsFlags) registerObservabilityFlags() {
	// §4.4 line 232 / §11.7 pgaudit sink consumer wiring.
	f.pgauditLogFile = flag.String("pgaudit-log-file", os.Getenv("LENNY_PGAUDIT_LOG_FILE"),
		"§4.4 / §11.7 pgaudit log file path. When set, lenny-ops tails the file, "+
			"translates each pgaudit record to OCSF, and delivers it to the configured "+
			"pgaudit sink. Override via LENNY_PGAUDIT_LOG_FILE.")
	f.pgauditTenantID = flag.String("pgaudit-tenant-id", envOr("LENNY_PGAUDIT_TENANT_ID", "platform"),
		"Tenant stamped on every pgaudit-sourced OCSF record (defaults to 'platform' for the "+
			"regulated-Postgres-instance case). Override via LENNY_PGAUDIT_TENANT_ID.")
	// spec: §25.16 Production "Prometheus (BYO)" block (lines 5124-5132).
	// When set, lenny-ops uses the supplied HTTP API endpoint as the
	// §25.13 ExprEvaluator backend and the §25.4 cross-replica health
	// aggregator. When empty (the §25.16 Minimal default) lenny-ops
	// degrades to the per-replica fan-out fallback the spec permits at
	// Tier 1. F-25.16.4.
	f.prometheusURL = flag.String("prometheus-url", os.Getenv("LENNY_PROMETHEUS_URL"),
		"§25.16 BYO Prometheus HTTP API base URL (e.g. http://prometheus.monitoring.svc:9090). "+
			"When empty the §25.4 cross-replica health aggregator falls back to per-replica fan-out. "+
			"Override via LENNY_PROMETHEUS_URL.")
	// spec: §25.9 line 3700. ops.audit.diagnosticsRatePerMinute caps the
	// distinct diagnostic audit events a service account may emit per
	// minute before excess is dropped; repeated calls for one resource
	// coalesce into a single event. Default 60. F-25.9.15.
	f.diagnosticsAuditRate = flag.Int("diagnostics-audit-rate-per-minute",
		envInt("LENNY_OPS_DIAGNOSTICS_AUDIT_RATE_PER_MINUTE", auditrate.DefaultRatePerMinute),
		"§25.9 ops.audit.diagnosticsRatePerMinute — per-service-account cap on distinct "+
			"diagnostic audit events per minute. Default 60. Override via "+
			"LENNY_OPS_DIAGNOSTICS_AUDIT_RATE_PER_MINUTE.")
	// spec: §25.10 line 3809. ops.drift.snapshotStaleWarningDays sets the
	// threshold at which a stored desired-state snapshot is flagged stale
	// in the GET /v1/admin/drift report. Default 7 days; 0 disables the
	// warning entirely. F-25.10.9.
	f.driftSnapshotStaleWarningDays = flag.Int("drift-snapshot-stale-warning-days",
		envInt("LENNY_DRIFT_SNAPSHOT_STALE_WARNING_DAYS", driftservice.DefaultStaleWarningDays),
		"§25.10 ops.drift.snapshotStaleWarningDays — threshold (in days) for the "+
			"bootstrap_seed_snapshot staleness warning on GET /v1/admin/drift. Default 7; "+
			"0 disables the warning. Override via LENNY_DRIFT_SNAPSHOT_STALE_WARNING_DAYS.")
	// spec: §25.10 line 3824. ops.drift.runningStateCacheTTLSeconds caps
	// how long the §25.10 running-state cache holds the gateway-aggregated
	// running state. ?fresh=true on the drift report bypasses the cache.
	// Default 60s; 0 disables caching entirely. F-25.10.7.
	f.driftRunningStateCacheTTLSeconds = flag.Int("drift-running-state-cache-ttl-seconds",
		envInt("LENNY_DRIFT_RUNNING_STATE_CACHE_TTL_SECONDS",
			int(driftservice.DefaultRunningStateCacheTTL/time.Second)),
		"§25.10 ops.drift.runningStateCacheTTLSeconds — TTL (in seconds) for the §25.10 "+
			"line 3822 running-state cache that backs GET /v1/admin/drift. Default 60; "+
			"0 disables caching (every report reads fresh). "+
			"Override via LENNY_DRIFT_RUNNING_STATE_CACHE_TTL_SECONDS.")
	// spec: §25.10 line 3788. ops.drift.helmValuesConfigMap names the chart-
	// rendered ConfigMap the new lenny-ops binary reads its own rendered
	// Helm values from to write bootstrap_seed_snapshot_target early in
	// OpsRoll. An empty name leaves the source unconfigured, so the new pod
	// still self-advances OpsRoll→CRDUpdate but writes no target snapshot
	// and GET /v1/admin/drift?against=target reports DRIFT_NO_TARGET_SNAPSHOT.
	f.driftHelmValuesConfigMap = flag.String("drift-helm-values-configmap",
		os.Getenv("LENNY_DRIFT_HELM_VALUES_CONFIGMAP"),
		"§25.10 ops.drift.helmValuesConfigMap — name of the chart-rendered ConfigMap holding "+
			"the rendered Helm values the OpsRoll startup hook writes into bootstrap_seed_snapshot_target. "+
			"Empty leaves the target-snapshot write unconfigured. Override via LENNY_DRIFT_HELM_VALUES_CONFIGMAP.")
	f.driftHelmValuesKey = flag.String("drift-helm-values-key",
		envOr("LENNY_DRIFT_HELM_VALUES_KEY", "values.yaml"),
		"§25.10 ops.drift.helmValuesKey — ConfigMap data key holding the rendered Helm values "+
			"document (YAML or JSON). Default values.yaml. Override via LENNY_DRIFT_HELM_VALUES_KEY.")
}

// registerAuthFlags registers the §25.4 escalation, OIDC authentication,
// and rate-limit flags. spec: §25.4, §17.
func (f *opsFlags) registerAuthFlags() {
	// spec: §25.4 line 2396. ops.escalation.requireDurable rejects an
	// escalation create with ESCALATION_NO_DURABLE_STORE when neither
	// Postgres nor Redis accepts it, instead of buffering in memory — the
	// conservative posture for deployers who prefer an explicit failure
	// over a silent durability gap during a storage outage. Default false.
	// F-25.4.7.
	f.escalationRequireDurable = flag.Bool("escalation-require-durable",
		envBool("LENNY_OPS_ESCALATION_REQUIRE_DURABLE", false),
		"§25.4 ops.escalation.requireDurable — reject an escalation create with "+
			"ESCALATION_NO_DURABLE_STORE when both Postgres and Redis are unavailable "+
			"instead of buffering in memory. Override via LENNY_OPS_ESCALATION_REQUIRE_DURABLE.")
	// spec: §25.4 line 2414. ops.escalation.reconciliationWritesPerSecond
	// paces the leader-only flush that promotes buffered escalations to a
	// recovered durable tier, so a large recovery does not spike Postgres.
	// Default 20. F-25.4.7.
	f.escalationReconcileWPS = flag.Int("escalation-reconciliation-writes-per-second",
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
	f.oidcIssuerURL = flag.String("oidc-issuer-url", os.Getenv("LENNY_OIDC_ISSUER_URL"),
		"§25.4/§17 security.oidc.issuerUrl: the OIDC issuer whose tokens lenny-ops trusts. "+
			"When set, a bearer whose iss claim differs is rejected. Override via LENNY_OIDC_ISSUER_URL.")
	f.bearerTrustHMACKeyFile = flag.String("bearer-trust-hmac-key-file", os.Getenv("LENNY_BEARER_TRUST_HMAC_KEY_FILE"),
		"§25.4 line 1562: path to the shared HMAC signing key (the gateway Token Service / "+
			"embedded OIDC key file) lenny-ops verifies bearer JWTs against. Required when "+
			"--production is set; when empty in dev the operability surface is unauthenticated. "+
			"Override via LENNY_BEARER_TRUST_HMAC_KEY_FILE.")
	f.authMultiTenant = flag.Bool("auth-multi-tenant", envBool("LENNY_AUTH_MULTI_TENANT", false),
		"§10.2: when true, lenny-ops extracts the tenant identifier from the JWT tenant claim "+
			"(so tenant-admin scoping resolves to the bearer's tenant); when false every caller "+
			"resolves to the built-in default tenant. Override via LENNY_AUTH_MULTI_TENANT.")
	f.rateLimitRPS = flag.Float64("rate-limit-rps", envFloat("LENNY_OPS_RATE_LIMIT_RPS", opsserver.DefaultRateLimitRPS),
		"§25.4 line 2001 ops.rateLimiting.requestsPerSecond: per-service-account token-bucket "+
			"refill rate. Override via LENNY_OPS_RATE_LIMIT_RPS.")
	f.rateLimitBurst = flag.Int("rate-limit-burst", envInt("LENNY_OPS_RATE_LIMIT_BURST", opsserver.DefaultRateLimitBurst),
		"§25.4 line 2001 ops.rateLimiting.burst: per-service-account token-bucket depth. "+
			"Override via LENNY_OPS_RATE_LIMIT_BURST.")
}

// registerLockFlags registers the §25.4 remediation-lock tier, TTL,
// idempotency, and leader-election flags. spec: §25.4.
func (f *opsFlags) registerLockFlags() {
	// spec: §25.4 lines 2206-2212 ops.locks.memoryTier. Governs whether the
	// in-memory (Tier-3) remediation-lock store grants an acquisition when
	// both Postgres and Redis are down. "single-replica-only" (default)
	// rejects acquisitions in a multi-replica deployment (detected via the
	// lenny-ops Service Endpoints); "always" grants with a replica-local
	// warning; "never" disables Tier 3. F-25.4.26.
	f.locksMemoryTier = flag.String("locks-memory-tier",
		envOr("LENNY_OPS_LOCKS_MEMORY_TIER", string(coordination.MemoryTierSingleReplicaOnly)),
		"§25.4 ops.locks.memoryTier: in-memory lock-tier policy — "+
			"single-replica-only | always | never. Override via LENNY_OPS_LOCKS_MEMORY_TIER.")
	f.opsServiceName = flag.String("ops-service-name", envOr("LENNY_OPS_SERVICE_NAME", "lenny-ops"),
		"§25.4 line 2208: name of the lenny-ops Service whose Endpoints the single-replica-only "+
			"lock policy reads to count ready replicas. Override via LENNY_OPS_SERVICE_NAME.")
	// spec: §25.4 ops.locks.{minTTLSeconds,defaultTTLSeconds,maxTTLSeconds} —
	// the deployment-wide remediation-lock TTL policy. A request omitting
	// ttlSeconds takes the default; one below the floor is raised, one above
	// the ceiling is clamped. Every tier (Postgres, Redis, in-memory) clamps
	// identically through coordination.SetTTLBounds. F-25.4.9.
	f.locksMinTTL = flag.Int("locks-min-ttl-seconds", envInt("LENNY_OPS_LOCKS_MIN_TTL_SECONDS", 0),
		"§25.4 ops.locks.minTTLSeconds: floor a requested remediation-lock TTL is raised to. 0 = no floor.")
	f.locksDefaultTTL = flag.Int("locks-default-ttl-seconds", envInt("LENNY_OPS_LOCKS_DEFAULT_TTL_SECONDS", 0),
		"§25.4 ops.locks.defaultTTLSeconds: TTL applied when a lock request omits ttlSeconds. 0 = built-in 300s.")
	f.locksMaxTTL = flag.Int("locks-max-ttl-seconds", envInt("LENNY_OPS_LOCKS_MAX_TTL_SECONDS", 0),
		"§25.4 ops.locks.maxTTLSeconds: ceiling a requested remediation-lock TTL is clamped to. 0 = built-in 3600s.")
	// spec: §25.4 ops.idempotency.{keyTTLSeconds,longRunningKeyTTLSeconds} —
	// the lifetime of a stored idempotency record. The standard class covers
	// single-request mutations (24h default); the long-running class covers
	// multi-phase operations such as upgrade and restore (7d default). F-25.4.9.
	f.idempotencyKeyTTL = flag.Int("idempotency-key-ttl-seconds", envInt("LENNY_OPS_IDEMPOTENCY_KEY_TTL_SECONDS", 0),
		"§25.4 ops.idempotency.keyTTLSeconds: standard idempotency-key lifetime. 0 = built-in 24h.")
	f.idempotencyLongRunningTTL = flag.Int("idempotency-long-running-key-ttl-seconds", envInt("LENNY_OPS_IDEMPOTENCY_LONG_RUNNING_KEY_TTL_SECONDS", 0),
		"§25.4 ops.idempotency.longRunningKeyTTLSeconds: long-running idempotency-key lifetime. 0 = built-in 7d.")
	// spec: §25.4 ops.leaderElection.{leaseDurationSeconds,renewDeadlineSeconds,
	// retryPeriodSeconds} — the client-go leader-election lease timings for the
	// lenny-ops-leader Lease. F-25.4.9.
	f.leaderLeaseDuration = flag.Int("leader-lease-duration-seconds", envInt("LENNY_OPS_LEADER_LEASE_DURATION_SECONDS", 0),
		"§25.4 ops.leaderElection.leaseDurationSeconds: leader-election lease duration. 0 = built-in 15s.")
	f.leaderRenewDeadline = flag.Int("leader-renew-deadline-seconds", envInt("LENNY_OPS_LEADER_RENEW_DEADLINE_SECONDS", 0),
		"§25.4 ops.leaderElection.renewDeadlineSeconds: leader renew deadline. 0 = built-in 10s.")
	f.leaderRetryPeriod = flag.Int("leader-retry-period-seconds", envInt("LENNY_OPS_LEADER_RETRY_PERIOD_SECONDS", 0),
		"§25.4 ops.leaderElection.retryPeriodSeconds: leader-election retry period. 0 = built-in 2s.")
}

// registerWebhookFlags registers the §25.5 webhook SSRF policy and the
// §25.4 GET /v1/admin/me platform-context flags. spec: §25.4, §25.5.
func (f *opsFlags) registerWebhookFlags() {
	// spec: §25.5 lines 2735-2745 ops.webhooks.{allowHTTP,blockedCIDRs,
	// domainAllowlist} — the callback-URL SSRF policy the subscription
	// validator enforces at create/update and at each delivery. F-25.4.9.
	f.webhookAllowHTTP = flag.Bool("webhook-allow-http", envBool("LENNY_OPS_WEBHOOK_ALLOW_HTTP", false),
		"§25.5 ops.webhooks.allowHTTP: permit http:// callback URLs. Off requires HTTPS.")
	f.webhookBlockedCIDRs = flag.String("webhook-blocked-cidrs", os.Getenv("LENNY_OPS_WEBHOOK_BLOCKED_CIDRS"),
		"§25.5 ops.webhooks.blockedCIDRs: comma-separated CIDRs rejected in addition to the built-in private/reserved set (e.g. the cluster pod/service CIDRs).")
	f.webhookDomainAllowlist = flag.String("webhook-domain-allowlist", os.Getenv("LENNY_OPS_WEBHOOK_DOMAIN_ALLOWLIST"),
		"§25.5 ops.webhooks.domainAllowlist: comma-separated hosts (exact or *.suffix) callbacks are restricted to. Empty allows any non-blocked host.")
	// spec: §25.4 lines 1596-1601 — the GET /v1/admin/me platform context.
	// installationId is the stable per-install UUID; tier is the §25.16
	// deployment tier; opsServiceURL is the external lenny-ops entry point.
	// F-25.4.2.
	f.installationID = flag.String("installation-id", os.Getenv("LENNY_INSTALLATION_ID"),
		"§25.4: stable installation UUID surfaced in GET /v1/admin/me.platform.installationId. Override via LENNY_INSTALLATION_ID.")
	f.platformTier = flag.String("platform-tier", envOr("LENNY_PLATFORM_TIER", ""),
		"§25.16 deployment tier (tier1/tier2/tier3) surfaced in GET /v1/admin/me.platform.tier. Override via LENNY_PLATFORM_TIER.")
	f.opsServiceURL = flag.String("ops-service-url", os.Getenv("LENNY_OPS_SERVICE_URL"),
		"§25.4: external lenny-ops URL surfaced in GET /v1/admin/me.platform.opsServiceURL. Override via LENNY_OPS_SERVICE_URL.")
}

// registerGatewayClientFlags registers the §25.4 gateway admin-API client
// (headless Service fan-out, TLS, breaker, token) and the §25.13 alerting
// bundle-format flags. spec: §25.4, §25.13.
func (f *opsFlags) registerGatewayClientFlags() {
	// spec: §25.4 lines 1740-1974 ("Calling the Gateway" + GatewayClient).
	// The headless Service and TLS posture drive the per-replica fan-out
	// discovery and the NET-070 admin-API transport; the breaker and
	// fan-out timeout bound the §25.4 fallback path. F-25.4.8.
	f.gatewayInternalTLS = flag.Bool("gateway-internal-tls", envBool("LENNY_OPS_GATEWAY_INTERNAL_TLS", false),
		"§25.4 ops.tls.internalEnabled: when true the gateway admin-API link uses HTTPS on the gateway internal-TLS port (NET-070).")
	f.gatewayHeadlessSvc = flag.String("gateway-headless-service", envOr("LENNY_OPS_GATEWAY_HEADLESS_SERVICE", ""),
		"§25.4 ops.gateway.headlessService: the lenny-gateway-pods headless Service the per-replica fan-out resolves. Empty disables fan-out.")
	f.gatewayTLSPort = flag.Int("gateway-internal-tls-port", envInt("LENNY_OPS_GATEWAY_INTERNAL_TLS_PORT", 8443),
		"§25.4 gateway internal-TLS port the fan-out dials when --gateway-internal-tls is set.")
	f.gatewayPlaintextPort = flag.Int("gateway-internal-port", envInt("LENNY_OPS_GATEWAY_INTERNAL_PORT", 8080),
		"§25.4 gateway internal plaintext port the fan-out dials when --gateway-internal-tls is unset.")
	f.gatewayFanOutTimeout = flag.Duration("gateway-fanout-timeout", 2*time.Second,
		"§25.4 ops.gateway.fanOutTimeoutSeconds: per-replica fan-out request timeout.")
	f.gatewayBreakerThreshold = flag.Int("gateway-fanout-breaker-failure-threshold", envInt("LENNY_OPS_GATEWAY_FANOUT_BREAKER_THRESHOLD", 3),
		"§25.4 ops.gateway.fanOutCircuitBreaker.failureThreshold: consecutive per-replica failures before the breaker opens.")
	f.gatewayBreakerResetAfter = flag.Duration("gateway-fanout-breaker-reset-after", 60*time.Second,
		"§25.4 ops.gateway.fanOutCircuitBreaker.resetAfter: how long a tripped per-replica breaker stays open.")
	f.gatewaySATokenFile = flag.String("gateway-sa-token-file", envOr("LENNY_OPS_GATEWAY_SA_TOKEN_FILE", "/var/run/secrets/lenny/gateway/token"),
		"§25.4 projected ServiceAccount token volume the GatewayClient presents to the gateway admin API. Absent → dev-headers path.")
	f.gatewayTokenRefreshBefore = flag.Duration("gateway-token-refresh-before-expiry", durationOrDefault(envInt("LENNY_OPS_GATEWAY_TOKEN_REFRESH_BEFORE_EXPIRY_SECONDS", 0), 5*time.Minute),
		"§25.4 security.oidc.tokenRefreshBeforeExpirySeconds: pre-emptive token-refresh lead time.")
	f.gatewayTokenMinTTL = flag.Duration("gateway-token-min-ttl", durationOrDefault(envInt("LENNY_OPS_GATEWAY_TOKEN_MIN_TTL_SECONDS", 0), 0),
		"§25.4 security.oidc.minTokenTTLSeconds: reject a startup token whose remaining lifetime is below this floor. 0 disables.")
	f.gatewayCABundleFile = flag.String("gateway-ca-bundle-file", envOr("LENNY_OPS_GATEWAY_CA_BUNDLE_FILE", ""),
		"§25.4 ops.tls.caBundleConfigMap: PEM CA bundle augmenting the system trust store for the gateway admin-API TLS link. Empty uses system roots.")
	f.alertingBundleFormats = flag.String("alerting-bundle-formats", envOr("LENNY_ALERTING_BUNDLE_FORMATS", "prometheusrule"),
		"§25.13 line 4833 Helm value monitoring.format: comma-separated list of the formats the chart bundled the §16.5 alert catalogue into. The §25.4 line 1339 bundleRules reconciler re-stamps lenny_ops /metrics' lenny_alerting_rules_bundled{format} from it. Override via LENNY_ALERTING_BUNDLE_FORMATS.")
	f.alertingOverrideCount = flag.Int("alerting-override-count", envInt("LENNY_ALERTING_OVERRIDE_COUNT", 0),
		"§25.13 line 4834 Helm value len(monitoring.alertOverrides): count of operator-customized §16.5 rules. The §25.4 bundleRules reconciler re-stamps lenny_alerting_rule_overrides from it. Override via LENNY_ALERTING_OVERRIDE_COUNT.")
}
