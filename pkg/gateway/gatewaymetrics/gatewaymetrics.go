// SPDX-License-Identifier: MIT

// Package gatewaymetrics registers the §16.1 gateway metrics and
// exposes the Prometheus `/metrics` scrape target. It composes the
// pkg/observability/metrics constructors (which enforce the §16.1.1
// label-hygiene rules) with a private prometheus.Registry so the
// gateway's metrics are isolated from the process-global default
// registry.
package gatewaymetrics

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics holds the registered §16.1 gateway metric vectors.
type Metrics struct {
	reg *prometheus.Registry

	requestsTotal         *prometheus.CounterVec
	requestDuration       *prometheus.HistogramVec
	activeSessions        prometheus.Gauge
	activeStreams         prometheus.Gauge
	requestQueueDepth     prometheus.Gauge
	rejectionRate         prometheus.Gauge
	maxSessionsPerReplica *prometheus.GaugeVec
	minReplicas           prometheus.Gauge
	streamCeiling         prometheus.Gauge
	replicaCount          prometheus.Gauge
	// billingCorrectionRateThreshold is the §11.2.1 / §16.5 startup-set
	// gauge that exposes the deployer-configurable percentage (default
	// 5%) the BillingCorrectionRateHigh alert reads via
	// scalar(lenny_billing_correction_rate_threshold). F-11.2.23.
	billingCorrectionRateThreshold prometheus.Gauge
	// gatewayQueueDepthThreshold / gatewayLatencyThresholdSeconds /
	// credentialPoolLowThreshold expose the §25.13 line 4737
	// tier-dependent alert thresholds as startup-set gauges. The
	// §16.5 GatewayQueueDepthHigh, GatewayLatencyHigh, and
	// CredentialPoolLow alerts read each value via `scalar(...)` so
	// the bundled expressions resolve against operator-tunable inputs
	// rather than literal constants. F-25.13.2.
	gatewayQueueDepthThreshold     prometheus.Gauge
	gatewayLatencyThresholdSeconds prometheus.Gauge
	credentialPoolLowThreshold     prometheus.Gauge
	extractionThreshold            *prometheus.GaugeVec
	storageQuotaUsed               *prometheus.GaugeVec
	storageQuotaLimit              *prometheus.GaugeVec
	circuitBreakerOpen             *prometheus.GaugeVec
	cbRejections                   *prometheus.CounterVec
	cbRejectionsSuppressed         *prometheus.CounterVec
	cbCacheStale                   prometheus.Gauge
	cbCacheInitialized             prometheus.Gauge
	elicitationDropped             *prometheus.CounterVec
	elicitationTamperDetected      *prometheus.CounterVec
	// elicitationIntegrityWeakened is the §16.5 line 460 standing-alert
	// gauge: the count of active tenants whose §9.2 effective
	// elicitation content-integrity mode is weaker than enforce. A
	// gateway reconciliation loop refreshes it from the tenant store.
	// F-9.2.5.
	elicitationIntegrityWeakened prometheus.Gauge
	// elicitationPending is the §16.1 line 61 unlabelled gauge of
	// in-flight elicitations the §16.5 ElicitationBacklogHigh alert
	// keys on (`lenny_elicitation_pending > 50 for > 30s`). The
	// dispatcher's request_elicitation handler increments on admit
	// and decrements on terminal phase. spec: §16.1 line 61; §16.5
	// line 458. F-9.2.14.
	elicitationPending prometheus.Gauge
	// elicitationTimeout counts §9.1 line 103 elicitation drops on the
	// maxElicitationWait deadline. spec: §16.1 line 63. F-9.2.14.
	elicitationTimeout prometheus.Counter
	// elicitationSuppressed counts §9.2 depth-policy suppressions and
	// per-session budget exhaustions. spec: §16.1 line 62. F-9.2.14.
	elicitationSuppressed prometheus.Counter
	// elicitationRoundtripSeconds observes the wall-clock duration
	// from admit to resolve / dismiss / timeout per the §16.1 line 60
	// histogram contract. spec: §16.1 line 60. F-9.2.14.
	elicitationRoundtripSeconds prometheus.Observer
	experimentIsoRej            *prometheus.CounterVec
	experimentTargetingDur      *prometheus.HistogramVec
	experimentTargetingErr      *prometheus.CounterVec
	experimentTargetingCircuit  *prometheus.GaugeVec
	sessionTotal                *prometheus.CounterVec
	sessionError                *prometheus.CounterVec
	sessionDuration             *prometheus.HistogramVec
	evalScore                   *prometheus.HistogramVec
	noEnvPolicyAllowAll         *prometheus.CounterVec
	erasureJobFailed            *prometheus.CounterVec
	gcPauseP99Ms                prometheus.Gauge
	// replayBufferUtilization is the §16.1
	// lenny_event_bus_replay_buffer_utilization gauge — ratio of the
	// in-memory replay buffer in use relative to capacity (0..1; 1.0
	// means the buffer is full and oldest events are being evicted).
	// The gateway samples the worst per-session ratio across the
	// session SSE bus periodically. spec: §10.4 line 389, §16 catalog.
	// F-10.4.11.
	replayBufferUtilization prometheus.Gauge
	// pdbBlockedEvictions is the §16.1
	// lenny_pdb_blocked_evictions_total counter labelled by `pdb` and
	// `controller`; increments each time the §10.4 PDB-status poller
	// observes the PDB blocking (DisruptionsAllowed=0). spec: §16
	// catalog. F-10.4.4.
	pdbBlockedEvictions *prometheus.CounterVec
	// kmsSigningErrors counts §10.2 line 225 JWTSigner failures. The
	// §16.5 KMSSigningUnavailable alert keys on
	// `rate(lenny_gateway_kms_signing_errors_total[30s]) > 1`. The
	// `reason` label discriminates `inner` (KMS surfaced an error) from
	// `rejected` (breaker open). spec: §10.2 line 225. F-10.2.6.
	kmsSigningErrors *prometheus.CounterVec
	// kmsSigningCircuitState is the §10.2 line 225 JWTSigner breaker
	// gauge: 0=closed, 1=half-open, 2=open. F-10.2.6.
	kmsSigningCircuitState prometheus.Gauge
	// tokenServiceCircuitState reflects the §4.3 / §4.1 Token Service
	// per-subsystem circuit-breaker state (0 closed, 1 half-open, 2
	// open). The §16.5 TokenServiceUnavailable alert reads it via
	// `lenny_token_service_circuit_state == 2`. spec: §4.3 line 211.
	tokenServiceCircuitState prometheus.Gauge
	// checkpointStaleSessions reports the §4.4 line 256 freshness
	// counter labelled by pool and level. The §16.5 `CheckpointStale`
	// alert fires when any label combination reports a non-zero value
	// for > 60 s.
	checkpointStaleSessions *prometheus.GaugeVec
	// partialManifestCleanup counts the §4.4 line 236 partial-manifest
	// cleanup outcomes (success | failed_deleted | gc_collected).
	partialManifestCleanup *prometheus.CounterVec
	// checkpointPartialManifestsSuperseded counts the §10.1
	// supersede-on-write transactions for prior partial manifests.
	checkpointPartialManifestsSuperseded *prometheus.CounterVec
	// checkpointOrphanedObjects counts the §4.4 line 248 abort-cleanup
	// failures: a `DeleteObject` that could not remove a partially-
	// uploaded checkpoint chunk. Labeled by pool and trigger.
	checkpointOrphanedObjects *prometheus.CounterVec
	// checkpointSizeExceeded counts the §4.4 line 254 pre-checkpoint
	// workspace-size probe rejections. Labeled by pool and level.
	checkpointSizeExceeded *prometheus.CounterVec
	// sessionEvictionTotalLoss counts the §4.4 lines 283–289 total-loss
	// events: both MinIO and Postgres failed for an eviction checkpoint
	// and the session is unrecoverable. Labeled by pool and
	// `had_prior_checkpoint`.
	sessionEvictionTotalLoss *prometheus.CounterVec
	// checkpointEvictionPartialKeysLogged counts the §4.4 line 279
	// partial-MinIO-key WARN log emissions on the eviction loss path.
	// Labels: `pool` (finite, sandbox-warm-pool registry) and
	// `keys_committed` ("0" for total-MinIO-failure, "1+" for
	// partial-upload scenarios).
	checkpointEvictionPartialKeysLogged *prometheus.CounterVec
	// checkpointDuration is the §4.4 line 254 end-to-end checkpoint
	// wall time histogram. Observed at the end of every checkpoint
	// snapshot regardless of trigger. Labels: `pool` (finite,
	// sandbox-warm-pool registry), `level` (one of the four §4.4
	// levels), and `trigger` (periodic | pre_scale_down | eviction).
	checkpointDuration *prometheus.HistogramVec
	// sessionStartupDuration is the §16.1 line 14 / §6.3 line 348
	// end-to-end pod-warm startup histogram: pod claim through agent
	// session ready, excluding client file upload and workspace
	// materialization. Labels: `pool`, `runtime_class`,
	// `isolation_profile`. Consumed by the StartupLatencyBurnRate and
	// StartupLatencyGVisorBurnRate alerts via the
	// lenny_session_startup_duration_slow_ratio recording rule. Buckets
	// straddle the 2s (runc) and 5s (gVisor) SLO thresholds.
	sessionStartupDuration *prometheus.HistogramVec
	// sessionStartupPhaseDuration is the §6.3 line 372 per-phase
	// hot-path histogram. Labels: `phase` (pod_claim,
	// workspace_materialization, setup_commands, credential_assignment,
	// agent_session_start) and `runtime_class`. Each phase is observed
	// independently so a slow startup can be localized to one phase.
	sessionStartupPhaseDuration *prometheus.HistogramVec
	// sessionTimeToFirstToken is the §16.1 line 15 / §6.3 line 356
	// end-to-end TTFT histogram: session start request (POST
	// /v1/sessions admission) through the first agent-streamed
	// response event emitted to the SSE client. Labels: `pool`,
	// `runtime_class`, `isolation_profile`. Consumed by the
	// TTFTBurnRate alert via an inline expression against the le="10"
	// bucket boundary (the §6.3 10s SLO threshold).
	sessionTimeToFirstToken *prometheus.HistogramVec
	// warmpoolClaims is the §16.1 line 122 / §6.3 line 352
	// `lenny_warmpool_claims_total{pool,runtime_class}` counter:
	// incremented on each idle→claimed transition in the §6.1 warm
	// pool. It is the denominator of the §6.3 line 352 SDK-warm
	// demotion-rate ratio. spec: §6.3 line 352, §16.1 line 122.
	warmpoolClaims *prometheus.CounterVec
	// sessionRetryTotal counts the §16.1 / §7.3
	// `lenny_session_retry_total{failure_class}` retries of a logical
	// session. Each successful pod recovery (the v1 retry path) bumps
	// the counter; the failure_class label echoes the row's §7.1
	// FailureClass at retry time. F-7.3.10.
	sessionRetryTotal *prometheus.CounterVec
	// sessionResumeAttempts counts the §16.1 / §7.3
	// `lenny_session_resume_attempts_total{pool, outcome}` counter:
	// every POST /v1/sessions/{id}/resume that passes the precondition
	// gate bumps it once with outcome "success" or "failure". F-7.3.10.
	sessionResumeAttempts *prometheus.CounterVec
	// warmpoolWarmupFailure is the §16.1 line 124
	// `lenny_warmpool_warmup_failure_total{error_type}` counter:
	// incremented whenever a warm-pool-side §6.3 startup phase
	// (workspace prep, setup_command, credential assignment, etc.)
	// fails. error_type is the §7.3 non-retryable failure category
	// the gateway classified. F-7.5.9.
	// spec: §16.1 line 124, §7.3 line 387.
	warmpoolWarmupFailure *prometheus.CounterVec
	// workspaceSealDuration is the §7.1 line 112 seal-and-export
	// completion-time histogram. Observed once per terminal session at
	// teardown. Labels: `pool` (the session runtime) and `outcome`
	// (success | timeout). The §16.5 WorkspaceSealStuck alert fires on a
	// nonzero count for outcome="timeout".
	workspaceSealDuration *prometheus.HistogramVec
	// checkpointStorageFailure counts the §4.4 line 262 non-eviction
	// MinIO-upload failures (all retries exhausted, the failed
	// checkpoint is discarded). Labels: `pool`, `level`, and `trigger`.
	checkpointStorageFailure *prometheus.CounterVec
	// checkpointEvictionFallback counts the §4.4 line 263 eviction-
	// fallback writes to Postgres. Labels: `pool` and
	// `had_prior_checkpoint`.
	checkpointEvictionFallback *prometheus.CounterVec
	// podClaimFallbackSkipped counts the §4.6.1 Postgres-backed pod-claim
	// fallback skip events. Labels: `reason` (`mirror_stale` or
	// `apiserver_unreachable`).
	podClaimFallbackSkipped *prometheus.CounterVec
	// slotAssignmentConflict counts the §5.2 line 519 concurrent-mode
	// slot reservation failures due to slot contention (a candidate pod
	// was at its maxConcurrent bound). Labeled by `pool` (finite, the
	// warm-pool registry), it lets operators detect pool under-sizing.
	slotAssignmentConflict *prometheus.CounterVec
	// credentialPreclaimMismatch counts the §4.9 line 1220 races where
	// the pre-claim credential availability check passed but the
	// subsequent lease assignment failed. Labeled by `pool` and
	// `provider` (both finite, the credential-pool registry), it lets
	// operators detect pool contention and tune pool sizing.
	credentialPreclaimMismatch *prometheus.CounterVec
	// credentialLeaseAssignments counts the §16.1 cumulative credential
	// leases issued from a pool. Labels: `provider`, `pool` (both finite,
	// the credential-pool registry) and `source` (`primary` | `fallback`
	// | `cached`).
	credentialLeaseAssignments *prometheus.CounterVec
	// credentialLeaseDuration observes the §16.1 wall-clock duration of
	// each issued credential lease from assignment to release. Labels:
	// `provider`, `pool`.
	credentialLeaseDuration *prometheus.HistogramVec
	// credentialRotation counts §4.9 fault-driven credential rotations
	// by error type (lenny_credential_rotation_total). Incremented by
	// the LLM-proxy Fallback Flow when a faulted lease is rotated to the
	// chain's next pool. spec: §16.1 line 118.
	credentialRotation *prometheus.CounterVec
	// credentialFallbackExhausted counts §4.9 fallback-chain exhaustions
	// (lenny_gateway_credential_fallback_exhausted_total), labeled by
	// pool, provider, and error type. spec: §4.9 line 1395.
	credentialFallbackExhausted *prometheus.CounterVec
	// credentialPoolUtilization is the §16.1 ratio of in-use credentials
	// to total pool credentials, in [0,1]. Labeled by `pool`; the
	// CredentialPoolLow alert fires above 0.80.
	credentialPoolUtilization *prometheus.GaugeVec
	// llmProxyActiveConnections is the §16.1 count of in-flight LLM proxy
	// requests on a replica. No labels.
	llmProxyActiveConnections prometheus.Gauge
	// llmTranslationDuration observes the §16.1 native-translator CPU time
	// per leg. Labels: `pool`, `provider`, `proxy_dialect`, `direction`
	// (`request` | `response`).
	llmTranslationDuration *prometheus.HistogramVec
	// llmTranslationErrors counts the §16.1 native-translator failures by
	// category. Labels: `pool`, `provider`, `error_type` (the §4.9
	// translator taxonomy).
	llmTranslationErrors *prometheus.CounterVec
	// slotFailure counts the §5.2 line 12 concurrent-workspace slot bind
	// failures after a slot was reserved. Labels: `error_type` (bind
	// stage), `pool` (finite, the warm-pool registry), and `k8s_pod_name`
	// (the §16.1.1-sanctioned pod label for this metric).
	slotFailure *prometheus.CounterVec
	// slotPodReplacement counts the §5.2 whole-pod replacements the
	// concurrent-workspace slot retry policy triggers when a pod crosses
	// the ceil(maxConcurrent/2) fail-or-leak threshold. Labeled by pool.
	slotPodReplacement *prometheus.CounterVec
	// slotRehydration counts the §5.2 line 521 post-recovery slot-counter
	// rehydration events: a pod's active_slots counter was seeded from
	// Postgres after a Redis restart. Labels: `pod` and `pool` (both
	// bounded — at most one rehydration per pod per Redis restart).
	slotRehydration *prometheus.CounterVec
	// checkpointPartialTotal counts the §4.4 line 234 / §10.1 partial-
	// manifest row writes. Labels: `pool` (finite, sandbox-warm-pool
	// registry).
	checkpointPartialTotal *prometheus.CounterVec
	// prestopCapSelection counts the §10.1 preStop tiered-cap
	// selection by source. Labels: `pool`, `service_instance_id`,
	// and `source` (postgres | postgres_null | cache_hit |
	// cache_miss_max_tier).
	prestopCapSelection *prometheus.CounterVec
	// sigkillStreams counts the §10.1 line 161 in-flight streams the
	// kubelet SIGKILLs at the grace deadline because their eviction
	// checkpoint did not finish in budget. Labels: `pool`,
	// `service_instance_id`.
	sigkillStreams *prometheus.CounterVec
	// gcTombstonesPruned counts the §12.5 ll. 341 hard-prune
	// removals: rows whose tombstone deadline has elapsed and were
	// physically removed once the retention window passed. The `table`
	// label distinguishes the two GC-managed row classes the single
	// hard-prune pass sweeps: `artifact_store` (blob catalog rows) and
	// `partial_manifest` (partial-checkpoint manifest rows in the
	// checkpoint metadata table).
	//
	// spec: §12.5 ll. 341 — `lenny_gc_tombstones_pruned_total`
	// (counter, labeled by `table: artifact_store|partial_manifest`).
	gcTombstonesPruned *prometheus.CounterVec
	// gcRuns counts §12.5 ll. 321 retention GC sweep invocations by
	// outcome ("success" or "error"). One Inc per Tick.
	gcRuns *prometheus.CounterVec
	// gcArtifactsDeleted counts artifacts removed by the retention
	// sweep, labelled by `store` (the §12.5 per-store adapter name —
	// for instance `artifacts`, `transcripts`, `session_logs`).
	gcArtifactsDeleted *prometheus.CounterVec
	// gcErrors counts retention GC errors observed per sweep, labelled
	// by `store` so a chronic failure in one adapter is distinguishable.
	gcErrors *prometheus.CounterVec
	// gcDuration is the §12.5 ll. 321 sweep wall-clock duration
	// histogram. Observed once per Tick regardless of outcome.
	gcDuration prometheus.Observer
	// drainReadinessChecks counts §12.5 ll. 291 drain-readiness
	// admission decisions, labelled by `outcome`
	// (`allowed | blocked | forced`).
	drainReadinessChecks *prometheus.CounterVec
	// legalHoldCheckpointGaps counts §12.8 ll. 739 legal-hold sessions
	// in which the reconciler detected a checkpoint gap. Labelled by
	// tenant so per-tenant attribution lands in the alert.
	legalHoldCheckpointGaps *prometheus.CounterVec
	// artifactUploadError counts §12.5 ll. 282 ArtifactStore PUT
	// failures after the retry budget. Labels: `tenant_id` (caller
	// tenant) and `error_type` (`minio_unreachable | auth |
	// quota_exceeded | other`). The §16.5 MinIOUnavailable alert reads
	// the `minio_unreachable` label.
	artifactUploadError *prometheus.CounterVec
	// delegationDepth observes the §16.1 / §8.2 per-session delegation
	// depth at admission. The catalog comment positions it as a
	// session-completion histogram; depth is set at admission and
	// invariant for the session's lifetime, so the distribution is
	// identical whether sampled at admission or terminal. Labelled by
	// `pool` per §16.1.
	delegationDepth *prometheus.HistogramVec
	// delegationWouldHaveBlocked counts §8.2 self-recursion hops where
	// any layer of the three-layer AND gate evaluated `false`. Labels
	// `pool`, `tenant_id`, `layer` (`platform` | `runtime` | `policy`),
	// and `mode` (`enforce` | `warn`). Under `mode: enforce` this is a
	// counter of "rejection causes" (the delegation is rejected, one
	// row per failing layer); under `mode: warn` it counts the same
	// per-layer breakdown for diagnostic rollouts (the delegation is
	// admitted). Not emitted under `mode: permissive`.
	delegationWouldHaveBlocked *prometheus.CounterVec
	// delegationTreeCycleDetected counts §8.9 tree-walker cycle
	// detections. Labels `tenant_id` and `source` (`rest` for the
	// /v1/sessions/{id}/tree handler, `mcp` for the lenny/get_task_tree
	// platform tool and lenny/await_children tree walks). Emission
	// implies a corrupt ParentSessionID lineage that bypassed the §8.2
	// pre-delegation cycle detector — typically a §8.10 recovery write
	// that re-parented a node. spec: §8.9 line 1003; F-8.9.10.
	delegationTreeCycleDetected *prometheus.CounterVec
	// delegationParallelChildrenHWM observes the §8.3 line 379 maximum
	// simultaneous in-flight children per delegation tree, sampled once
	// when the tree root reaches a terminal state. Labels `pool` and
	// `tenant_id` per §16.1; `root_session_id` is deliberately not a
	// label (unbounded cardinality). F-8.9.6.
	delegationParallelChildrenHWM *prometheus.HistogramVec
	// rateLimitRejected counts §11.1 line 7 admission rejections by
	// the ratelimit middleware. Labelled by `scope` (`global` | `user`)
	// so operators can attribute rejection volume to the scope that
	// fired. Required by §11.1's observability contract; the metric is
	// not in §16.1 because §11.1 leaves the metric name to the
	// implementation, but the catalog still excludes it to keep the
	// §16.1 surface in sync with that table.
	// spec: §11.1 line 7.
	rateLimitRejected *prometheus.CounterVec
	// rateLimitFailopenActive is the §16.5 RateLimitDegraded alert's
	// source gauge. Set to 1 once the ratelimit middleware has
	// observed a counter error in the current process; cleared back
	// to 0 on the next successful Incr. The §16.5 alert reads
	// `lenny_rate_limit_failopen_active == 1`. spec: §16.5
	// RateLimitDegraded; §11.1 line 7 fail-open semantics.
	rateLimitFailopenActive prometheus.Gauge
	// rateLimitCounterFailure counts §11.1 ratelimit middleware
	// counter errors. Distinct from `rateLimitFailopenActive`: this is
	// a monotonic per-error counter the operator can rate-aggregate
	// to detect a partial Redis outage that produces sporadic Incr
	// errors but stays under the alert's persistence window.
	// spec: §11.1 line 7 fail-open observability.
	rateLimitCounterFailure prometheus.Counter
	// dualStoreUnavailable is the §10.1 line 45 DualStoreUnavailable
	// alert's source gauge: 1 while this replica observes Postgres and
	// Redis simultaneously unreachable, 0 otherwise. The §16.5 alert
	// reads `lenny_dual_store_unavailable == 1`.
	// spec: §10.1 line 45.
	dualStoreUnavailable prometheus.Gauge

	// billingFlushPressure counts §12.3 line 76 billing_flush_pressure
	// events: each Append that finds the failover Tier 2 write-ahead
	// buffer over the configured billingFlushMaxPending threshold. The
	// spec names the bare metric `billing_flush_pressure`; the lenny_
	// prefix and _total suffix follow the §16.1.1 naming rules. It is
	// not in the §16.1 catalog because §12.3 leaves the metric name to
	// the implementation, so the catalog (which transcribes §16.1 only)
	// excludes it. F-12.3.13.
	billingFlushPressure prometheus.Counter
	// postgresWriteIops is the §12.3 lines 115-125 sustained Postgres
	// write-IOPS gauge the §16.5 PostgresWriteSaturation alert reads as
	// the numerator of `lenny_postgres_write_iops /
	// scalar(lenny_postgres_write_ceiling_iops)`. A periodic sampler
	// (cmd/lenny-gateway) sets it from the pg_stat_database row-write
	// delta rate. Not in the §16.1 catalog: §16.5 names the alert in
	// prose but §16.1 does not define the metric. F-12.3.7.
	postgresWriteIops prometheus.Gauge
	// postgresWriteCeilingIops is the §12.3 line 123
	// postgres.writeCeilingIops configured ceiling, emitted unlabelled
	// at startup so the PostgresWriteSaturation alert resolves
	// scalar(lenny_postgres_write_ceiling_iops) to an operator-tunable
	// value rather than a literal. Not in the §16.1 catalog. F-12.3.8.
	postgresWriteCeilingIops prometheus.Gauge
	// auditChainIntegrity is the §16.1 lenny_audit_chain_integrity_total
	// counter classified by `state` (the §11.7 ChainIntegrity enum). The
	// §12.3 line 101 startup chain-continuity check increments it once
	// per tenant; the §16.5 AuditChainGap alert reads the broken series
	// via `increase(lenny_audit_chain_integrity_total{state="broken"}[15m])`.
	// F-12.3.9.
	auditChainIntegrity *prometheus.CounterVec
	// auditGrantDrift is the §11.7 item 2 lenny_audit_grant_drift_total
	// counter. The periodic background integrity check increments it when
	// it detects unexpected UPDATE/DELETE grants (or a disabled
	// tamper-evidence trigger / dropped erasure guard) on the append-only
	// ledgers after startup; the §16.5 AuditGrantDrift alert reads
	// `lenny_audit_grant_drift_total > 0`. F-11.7.3.
	auditGrantDrift prometheus.Counter
	// auditOCSFTranslationFailed is the §16.1
	// lenny_audit_ocsf_translation_failed_total counter, labeled by
	// `event_type` and `error_class` (the §11.7 ocsf.ErrorClass enum). The
	// §11.7 OCSF translator increments it on every per-row translation
	// failure as the background state machine advances the row toward
	// retry_pending / dead_lettered. F-11.7.1 / F-11.7.15.
	auditOCSFTranslationFailed *prometheus.CounterVec
	// idempotencyCacheWriteFailures counts §11.5 idempotency-key cache
	// Put failures: the inner handler already executed (the client
	// already got the response), but the durable store rejected the
	// cache row, so the next retry with the same key WILL re-execute
	// the operation. Labelled by tenant so a noisy tenant doesn't
	// hide a platform-wide spike. spec: §11.5 line 277; F-11.5.4.
	idempotencyCacheWriteFailures *prometheus.CounterVec
	// idempotencyCacheSkipped counts cache writes the middleware
	// declined by policy — currently `server_error` (inner-handler
	// 5xx, must not be replayed for the 24-hour TTL). spec: §11.5
	// line 277; F-11.5.3.
	idempotencyCacheSkipped *prometheus.CounterVec

	// maxOrphanTasksPerTenant is the §8.10 line 1103 deployer-configured
	// orphan-cap exposed as an unlabeled gauge so the §16.5
	// OrphanTasksPerTenantHigh alert resolves
	// `scalar(lenny_max_orphan_tasks_per_tenant)` to the live ceiling.
	// F-8.10.13.
	maxOrphanTasksPerTenant prometheus.Gauge

	// §8.10 / §16.1 orphan-cleanup and tree-recovery observability.
	// orphanCleanupRuns counts the §8.10 line 1091 background sweep
	// invocations; one Inc per Tick regardless of outcome. orphanTasksTerminated
	// counts the per-sweep terminated-orphan count (in lockstep with the
	// existing log line). orphanTasksActive is the fleet-wide active orphan
	// gauge (sum over tenants); orphanTasksActivePerTenant is the per-tenant
	// gauge the OrphanTasksPerTenantHigh alert reads. treeRecoveryDuration
	// observes one wall-clock duration per tree-recovery operation;
	// treeRecoveryTimeout counts the per-timeout-type rollups.
	// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 144-149. F-8.10.7.
	orphanCleanupRuns          prometheus.Counter
	orphanTasksTerminated      prometheus.Counter
	orphanTasksActive          prometheus.Gauge
	orphanTasksActivePerTenant *prometheus.GaugeVec
	treeRecoveryDuration       *prometheus.HistogramVec
	treeRecoveryTimeout        *prometheus.CounterVec

	// statelessRequests is the §5.2 line 573 cumulative request count
	// arriving at the pool's Kubernetes Service in concurrent-stateless
	// mode. The PoolScalingController reads
	// `rate(lenny_stateless_requests_total[5m])` as `base_demand_p95`
	// for stateless pools (concurrent-stateless bypasses the gateway
	// claim model). Labeled by `pool` (the SandboxTemplate name) — the
	// emitter lands with the tenant-affinity routing layer (F-5.2.3),
	// the metric is registered here so the catalog test sees the
	// declared surface and operators can scrape it as soon as the
	// producer exists.
	statelessRequests *prometheus.CounterVec
	// statelessConcurrentActive is the §5.2 line 573 instantaneous
	// per-pod concurrent active-slot count. The PoolScalingController
	// reads `max_over_time(lenny_stateless_concurrent_active[5m])` as
	// `burst_p99_claims` for stateless pools. Labeled by `pool` — the
	// per-pod dimension is intentionally dropped to keep the cardinality
	// bound and because the controller aggregates across pods anyway.
	// Emitter lands with F-5.2.3.
	statelessConcurrentActive *prometheus.GaugeVec

	// taskReuseCount is the §5.2 line 569 / §16.1 line 124 histogram of
	// tasks executed on a single pod in task mode. The
	// PoolScalingController reads `histogram_quantile(0.50, ...)` as the
	// mode-adjusted `mode_factor` for task-mode pools with preConnect:
	// true so the scaling formula converges on observed reuse. Labeled
	// by `pool` and `k8s_pod_name` per §16.1; the emitter lands with the
	// task-mode lifecycle (F-5.2.1 / F-5.2.18).
	taskReuseCount *prometheus.HistogramVec

	// delegationLeaseExtension is the §16 line 66 counter for §8.6
	// lease extensions. Labelled by `tenant_id` and `outcome` (one of
	// `approved` / `capped` / `denied` — the §8.6 line 743 audit
	// classification). The §16.1 catalog declares the metric with no
	// labels; tenant_id and outcome are tier-2 dimensions that keep
	// cardinality bounded (tenants × 3 outcomes) and let the
	// per-tenant dashboards under §16 surface the rejection-rate split
	// the §8.6 elicitation flow drives. Emitted from leasecontrol.Service
	// on every ExtendLease decision. F-8.6.13.
	delegationLeaseExtension *prometheus.CounterVec

	// exportFileScans is the §16.1 line 80 counter for per-file
	// PreExportMaterialization scan outcomes on the §8.7 delegation
	// file-export path. Labels: pool, tenant_id, policy_name,
	// interceptor_ref, outcome (admitted | modified | rejected |
	// failed_open | failed_closed). The `failed_open` series drives the
	// §16.5 ExportFileScanFailOpen alert; `rejected` and `failed_closed`
	// surface in delegation-rejection dashboards. F-8.7.10.
	exportFileScans *prometheus.CounterVec
	// exportFileScanDuration is the §16.1 line 81 histogram for the
	// per-file PreExportMaterialization interceptor latency. Labels:
	// pool, tenant_id, interceptor_ref (no outcome/policy_name — latency
	// is per file regardless of decision). A sustained P99 > 1s flags
	// the interceptor as being on the delegation hot path. F-8.7.10.
	exportFileScanDuration *prometheus.HistogramVec

	// memoryStoreOperationDuration is the §9.4 / §16.1 line 151
	// MemoryStore per-operation duration histogram. Labels: `operation`
	// (one of write, query, delete, list, delete_by_user,
	// delete_by_tenant) and `backend` (`postgres` | `custom` | `memory`
	// for the test backend). The §16.5 MemoryStoreErasureDurationHigh
	// alert reads the `delete_by_user` / `delete_by_tenant` quantiles.
	// F-9.4.1.
	memoryStoreOperationDuration *prometheus.HistogramVec
	// memoryStoreErrors is the §9.4 / §16.1 line 152 MemoryStore per-
	// operation error counter. Labels: `operation`, `backend`,
	// `error_type`. F-9.4.1.
	memoryStoreErrors *prometheus.CounterVec
	// memoryStoreRecordCount is the §9.4 / §16.1 line 153 approximate
	// per-tenant gauge of stored memory records. Labelled by `tenant_id`
	// only — per-user resolution is forbidden by §16.1.1; the per-user
	// headroom signal is the threshold counter below. F-9.4.1.
	memoryStoreRecordCount *prometheus.GaugeVec
	// memoryStoreUserOverThreshold is the §9.4 / §16.1 line 154 counter
	// of MemoryStore.Write commits that left the writing user at >= 80%
	// of memory.maxMemoriesPerUser. Labels: `tenant_id` and `backend`.
	// Drives the §16.5 MemoryStoreGrowthHigh alert. F-9.4.6.
	memoryStoreUserOverThreshold *prometheus.CounterVec

	// timeDrift is the §13.3 line 595 / §16.1 lenny_time_drift_seconds
	// gauge: signed offset (seconds) from the NTP reference. The
	// driftmonitor sampler refreshes this gauge on its tick. The §16.5
	// GatewayClockDrift alert keys on `abs(lenny_time_drift_seconds) >
	// 0.5`; the replica self-degrades once |drift| >= 5s. F-13.3.5.
	timeDrift prometheus.Gauge

	// inflight tracks the number of HTTP requests currently being
	// handled by the §16.1 Middleware-wrapped mux. It is the source of
	// the lenny_gateway_request_queue_depth gauge (the §4.1 SCL-026
	// primary HPA scale-out trigger): incremented on entry and
	// decremented on exit so the watchdog poller's SetRequestQueueDepth
	// call reflects the instantaneous concurrent-request count.
	inflight int64
}

// New constructs and registers the gateway metric set against a
// fresh private registry.
func New() (*Metrics, error) {
	reg := prometheus.NewRegistry()

	requestsTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_requests_total",
		Help: "Total gateway HTTP requests, labelled by method, route, and status class.",
	}, []string{"method", "route", "status_class"})
	if err != nil {
		return nil, err
	}
	requestDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gateway_request_duration_seconds",
		Help:    "Gateway HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	if err != nil {
		return nil, err
	}
	activeSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_sessions",
		Help: "Current count of non-terminal sessions tracked by the gateway.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §10.1 / §4.1 horizontal-scaling signals. activeStreams is the
	// secondary HPA metric (SCL-026); requestQueueDepth is the primary
	// HPA scale-out trigger (SCL-026) and rejectionRate is the second
	// leading indicator that detects saturation before CPU rises. All
	// three surface on /metrics so the §10.1 custom-metrics pipeline
	// (Prometheus Adapter or KEDA) can scrape them.
	activeStreams, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_streams",
		Help: "Open streaming connections on this gateway replica.",
	}, nil)
	if err != nil {
		return nil, err
	}
	requestQueueDepth, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_request_queue_depth",
		Help: "Requests queued on this gateway replica awaiting a handler goroutine.",
	}, nil)
	if err != nil {
		return nil, err
	}
	rejectionRate, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_rejection_rate",
		Help: "Gateway requests rejected with 429/503 per second on this replica.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §4.1 / §16.1: maxSessionsPerReplica is a startup-set gauge. It is
	// the denominator of the §16.5 GatewaySessionBudgetNearExhaustion
	// alert and the §17.8.2 burst-absorption minReplicas formula. The
	// delivery_mode label distinguishes proxy from direct deliveryMode
	// (per spec, two gauge values are always reported per replica).
	maxSessionsPerReplica, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_max_sessions_per_replica",
		Help: "Maximum concurrent sessions this replica can serve under the given delivery mode (§4.1).",
	}, []string{"delivery_mode"})
	if err != nil {
		return nil, err
	}
	// §4.1 / §16.5: emit the configured HPA-minimum replica count and
	// the per-replica stream ceiling as startup-set scalar gauges so the
	// `GatewayNoHealthyReplicas` and `GatewayActiveStreamsHigh` alert
	// expressions in pkg/alerting/rules/rules.go can read them via
	// scalar(...). The replicaCount gauge is emitted per-replica as the
	// constant 1 so the Prometheus `sum()` recording rule yields the
	// fleet-wide ready-replica count for the `lenny_gateway_replica_count`
	// alert numerator.
	minReplicas, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_min_replicas",
		Help: "Configured HPA minReplicas floor for the gateway Deployment (§4.1 / §16.5).",
	}, nil)
	if err != nil {
		return nil, err
	}
	streamCeiling, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_stream_ceiling",
		Help: "Configured per-replica streaming-connection ceiling used by the GatewayActiveStreamsHigh alert (§4.1 / §16.5).",
	}, nil)
	if err != nil {
		return nil, err
	}
	replicaCount, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_replica_count",
		Help: "Per-replica ready indicator; the recording rule sum() yields the fleet-wide ready replica count (§4.1 / §16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §11.2.1 line 187 — "deployer-configurable percentage (default
	// 5%)". The §16.5 BillingCorrectionRateHigh alert evaluates
	// lenny_billing_correction_rate_24h > scalar(lenny_billing_correction_rate_threshold);
	// the gateway emits this gauge at startup from the
	// billing.correctionRateThreshold Helm value. F-11.2.23.
	billingCorrectionRateThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_billing_correction_rate_threshold",
		Help: "Deployer-configurable BillingCorrectionRateHigh alert threshold as a fraction (§11.2.1 / §16.5; default 0.05).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §25.13 line 4737 — tier-dependent §16.5 thresholds. The
	// chart emits the configured Helm values into these gauges at
	// gateway startup; the bundled alert expressions read them via
	// `scalar(...)` so a tier preset tightening the threshold flows
	// through to the rendered manifest without re-rendering the rule
	// expressions. F-25.13.2.
	gatewayQueueDepthThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_queue_depth_threshold",
		Help: "Configured §16.5 GatewayQueueDepthHigh ceiling (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return nil, err
	}
	gatewayLatencyThresholdSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_latency_threshold_seconds",
		Help: "Configured §16.5 GatewayLatencyHigh p95 ceiling in seconds (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return nil, err
	}
	credentialPoolLowThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_credential_pool_low_threshold",
		Help: "Configured §16.5 CredentialPoolLow utilisation fraction (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §4.1: surface the configured per-subsystem extraction
	// thresholds as a startup-set gauge so the values used for an
	// extraction decision are auditable against /metrics and the
	// Helm release history. The subsystem and metric labels match
	// the gateway.extractionThresholds.<subsystem>.<metric> Helm
	// keys.
	extractionThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_extraction_threshold",
		Help: "Configured §4.1 per-subsystem extraction threshold by metric.",
	}, []string{"subsystem", "metric"})
	if err != nil {
		return nil, err
	}
	// §4.1 shared-process GC pressure signal. Periodic collector
	// reads runtime/debug.ReadGCStats and pushes the p99 over a
	// sliding window into this gauge so the Tier3GCPressureHigh
	// alert (and the Tier 3 promotion criterion) can evaluate.
	gcPauseP99Ms, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_gc_pause_p99_ms",
		Help: "Process-level GC pause p99 (ms) over the last sliding window (§4.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §10.4 line 389 / §16 catalog — operator-facing visibility
	// signal for the per-session SSE replay buffer. The gateway samples
	// MaxReplayBufferUtilization on the periodic poller cadence. F-10.4.11.
	replayBufferUtilization, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_event_bus_replay_buffer_utilization",
		Help: "Ratio of in-memory replay buffer in use relative to capacity (0..1); 1.0 means full and oldest events are being evicted (§10.4 line 389 / §16).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §16 catalog / §10.4 — PDB-blocking observation. Incremented
	// per polling sample where the gateway PDB has DisruptionsAllowed=0;
	// the §16.5 PDBBlockedEvictions alert evaluates a 10-minute rate.
	// F-10.4.4.
	pdbBlockedEvictions, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pdb_blocked_evictions_total",
		Help: "PodDisruptionBudget-blocked evictions; labelled by `pdb` (resource name) and `controller` (eviction source).",
	}, []string{"pdb", "controller"})
	if err != nil {
		return nil, err
	}
	storageQuotaUsed, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_storage_quota_bytes_used",
		Help: "Per-tenant artifact storage bytes reserved-plus-committed (§11.2).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	storageQuotaLimit, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_tenant_storage_quota_bytes",
		Help: "Per-tenant configured storage quota in bytes (§11.2 storageQuotaBytes).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	circuitBreakerOpen, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_open",
		Help: "1 when the named §11.6 circuit breaker is open, 0 when closed.",
	}, []string{"circuit_name"})
	if err != nil {
		return nil, err
	}
	// spec: §11.6 line 333 — every breaker-caused admission rejection
	// increments rejections_total (including those whose audit row is
	// elided by sampling); the sampled-away subset increments
	// rejections_suppressed_total. The limit_tier label carries the
	// breaker scope vocabulary so a metric spike correlates 1:1 with
	// its sampled `admission.circuit_breaker_rejected` audit rows.
	cbRejections, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_circuit_breaker_rejections_total",
		Help: "Total §11.6 admission rejections caused by a tripped circuit breaker, labelled by tenant_id, circuit_name, and limit_tier.",
	}, []string{"tenant_id", "circuit_name", "limit_tier"})
	if err != nil {
		return nil, err
	}
	cbRejectionsSuppressed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_circuit_breaker_rejections_suppressed_total",
		Help: "§11.6 breaker rejections whose audit row was elided by per-(tenant_id, circuit_name, caller_sub) 10s sampling, labelled by tenant_id, circuit_name, and limit_tier.",
	}, []string{"tenant_id", "circuit_name", "limit_tier"})
	if err != nil {
		return nil, err
	}
	cbCacheStale, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_stale_seconds",
		Help: "Wall seconds since the circuit-breaker cache last refreshed from Redis.",
	}, nil)
	if err != nil {
		return nil, err
	}
	cbCacheInitialized, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_initialized",
		Help: "1 once the circuit-breaker cache has completed its first refresh.",
	}, nil)
	if err != nil {
		return nil, err
	}
	elicitationDropped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_dropped_total",
		Help: "Total elicitations the gateway dropped, labelled by drop reason (§9.1).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 64; §9.2 line 60 — the tamper counter is labelled
	// by origin_pod, tampering_pod, and enforcement_mode. origin_pod is
	// the pod that legitimately originated the elicitation; tampering_pod
	// is the forwarding pod whose re-emission diverged. Both are bounded
	// by the active delegation-tree depth, so cardinality is safe under
	// the §16.1.1 attribute-naming rule. The enforcement_mode label is
	// one of enforce | detect-only only — the detector does not run under
	// effective mode off, so no stream is emitted there. F-9.2.4.
	elicitationTamperDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_content_tamper_detected_total",
		Help: "Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by origin_pod, tampering_pod, and enforcement_mode (enforce | detect-only) per §16.1 line 64 so the §16.5 alert can fire on the enforce-mode stream only.",
	}, []string{"origin_pod", "tampering_pod", "enforcement_mode"})
	if err != nil {
		return nil, err
	}
	// spec: §16.5 line 460 — the standing ElicitationContentIntegrityWeakened
	// alert reads this gauge. It is a gateway-process count of active
	// tenants whose §9.2 effective elicitation content-integrity mode
	// (max(platformFloor, tenantStored)) is weaker than enforce. The
	// alert fires while the value is > 0 and resolves once every active
	// tenant's effective mode is enforce. Unlabelled to keep the gauge
	// cardinality-free; operators identify which tenants are weakened
	// from the paired tenant.elicitation_content_integrity_changed audit
	// events, per the §16.5 runbook. F-9.2.5.
	elicitationIntegrityWeakened, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce",
		Help: "Active tenants whose §9.2 effective elicitation content-integrity enforcement mode is weaker than enforce (§16.5 line 460). Standing-alert numerator; zero when every active tenant resolves to enforce.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 61 — in-flight elicitation gauge the §16.5
	// ElicitationBacklogHigh alert reads. Unlabelled; the gauge is a
	// gateway-process count. F-9.2.14.
	elicitationPending, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_pending",
		Help: "In-flight §9.2 elicitations awaiting human or intercepting-parent resolution (§16.1 line 61).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 63 — elicitation-timeout counter the operator
	// dashboards read; the maxElicitationWait drop site increments it.
	// F-9.2.14.
	elicitationTimeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_timeout_total",
		Help: "§9.1 line 103 elicitation drops on the maxElicitationWait deadline (§16.1 line 63).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 62 — suppression / budget-exhaustion counter.
	// F-9.2.14.
	elicitationSuppressed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_suppressed_total",
		Help: "§9.2 elicitations dropped by depth policy or per-session budget (§16.1 line 62).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 60 — admit-to-terminal round-trip histogram.
	// Buckets cover the §9.1 default elicitationTimeout (600s) with
	// reasonable granularity on the typical-human-response range
	// (1s..10min). F-9.2.14.
	elicitationRoundtripSeconds, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_elicitation_roundtrip_seconds",
		Help:    "§9.2 elicitation admit-to-terminal wall-clock latency (§16.1 line 60).",
		Buckets: []float64{0.5, 1, 5, 15, 30, 60, 120, 300, 600},
	}, nil)
	if err != nil {
		return nil, err
	}
	experimentIsoRej, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_isolation_rejections_total",
		Help: "Total sessions the §10.7 ExperimentRouter rejected closed because the variant pool's isolation profile was weaker than the session's.",
	}, []string{"tenant_id", "experiment_id", "variant_id"})
	if err != nil {
		return nil, err
	}
	// spec: §10.7 line 833 / §16.1 lines 156-157 — external experiment
	// targeting observability. The `provider` label carries the
	// OpenFeature provider name; for provider:ofrep the OFREP endpoint
	// hostname is used (§16.1 line 156). Buckets resolve the sub-second
	// range the §10.7 200ms targeting timeout is sized against.
	experimentTargetingDur, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_experiment_targeting_duration_seconds",
		Help:    "§10.7 external experiment targeting evaluation latency by provider (§16.1 line 156).",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
	}, []string{"provider"})
	if err != nil {
		return nil, err
	}
	// spec: §10.7 line 833 / §16.1 line 157 — targeting_failed counter.
	// error_type classifies the §10.7 failure cause (timeout, transport,
	// or the OFREP errorCode).
	experimentTargetingErr, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_targeting_error_total",
		Help: "§10.7 external experiment targeting evaluation failures by provider and error_type (§16.1 line 157).",
	}, []string{"provider", "error_type"})
	if err != nil {
		return nil, err
	}
	// spec: §10.7 lines 835-844 (SCL-023) / §16.1 line 64 — the per-tenant
	// targeting circuit-breaker gauge: 1 while the breaker is open (the
	// gateway is skipping the OpenFeature call), 0 when closed. The §16.5
	// ExperimentTargetingCircuitOpen alert fires on a sustained 1.
	experimentTargetingCircuit, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_experiment_targeting_circuit_open",
		Help: "§10.7 SCL-023 targeting circuit-breaker state by tenant and provider: 1=open, 0=closed.",
	}, []string{"tenant_id", "provider"})
	if err != nil {
		return nil, err
	}
	// spec: §16.1 lines 161-163 / §10.7 lines 1120-1132 — the variant-labelled
	// rollback-trigger metric family. session_type carries the session's
	// §5.2 ExecutionMode ("session", "task", "concurrent"); variant_id carries
	// the §10.7 experiment enrollment ("" for control / un-enrolled sessions).
	// lenny_session_total is the denominator for the variant error rate
	// (§16.1 line 162); lenny_session_error_total is the numerator (line 161).
	sessionTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_total",
		Help: "§16.1 line 162 sessions total by variant; denominator for the §10.7 rollback-trigger error rate.",
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return nil, err
	}
	sessionError, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_error_total",
		Help: "§16.1 line 161 session errors by variant; numerator for the §10.7 rollback-trigger error rate.",
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 163 — per-session wall-clock duration sampled at
	// completion. Buckets span the §10.7 / §6 session lifetime (1s to the
	// 4-hour cert-expiry bound) so histogram_quantile(0.95, ...) resolves
	// the variant-vs-control p95 comparison the rollback table cites.
	sessionDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_duration_seconds",
		Help:    "§16.1 line 163 per-session wall-clock duration by variant, sampled at completion.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400},
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return nil, err
	}
	// spec: §16.1 line 164 — one observation per submitted eval run. Scores
	// are normalized 0.0-1.0; the 0.95 bucket resolves the §10.7 line 1128
	// safety-score-regression threshold.
	evalScore, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_eval_score",
		Help:    "§16.1 line 164 eval score by variant; one observation per submitted eval run.",
		Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0},
	}, []string{"tenant_id", "scorer", "variant_id"})
	if err != nil {
		return nil, err
	}
	noEnvPolicyAllowAll, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_noenvironmentpolicy_allowall_total",
		Help: "Total tenant rbac-config writes that set noEnvironmentPolicy to allow-all (§10.6).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	// spec: §12.8 CMP-026 / §16.1 line 262 — user-level erasure job
	// failures by failure phase. failure_phase distinguishes the §12.8
	// failure modes (store_delete, pseudonymization, verification, and the
	// MemoryStore erasure preflight memory_store_preflight); the §16.5
	// ErasureJobFailed alert fires on any increase.
	erasureJobFailed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_erasure_job_failed_total",
		Help: "§12.8 user-level erasure job failures by tenant and failure_phase.",
	}, []string{"tenant_id", "failure_phase"})
	if err != nil {
		return nil, err
	}
	// §4.3 line 211 / §16.5 TokenServiceUnavailable alert reads this
	// gauge. 0 = closed, 1 = half-open, 2 = open. spec: §4.3 line 211.
	tokenServiceCircuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_token_service_circuit_state",
		Help: "§4.3 Token Service circuit breaker state: 0=closed, 1=half-open, 2=open.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §10.2 line 225 / §16.5 KMSSigningUnavailable alert reads
	// rate(lenny_gateway_kms_signing_errors_total[30s]) > 1. F-10.2.6.
	kmsSigningErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_kms_signing_errors_total",
		Help: "§10.2 JWTSigner signing failures. Labels: reason ∈ {inner, rejected}.",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	kmsSigningCircuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_kms_signing_circuit_state",
		Help: "§10.2 JWTSigner circuit breaker state: 0=closed, 1=half-open, 2=open.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §4.4 line 256 — `lenny_checkpoint_stale_sessions` per-pool/level
	// gauge counts active sessions whose `last_successful_checkpoint_at`
	// is older than `periodicCheckpointIntervalSeconds`. The §16.5
	// `CheckpointStale` alert fires when any label combination reports
	// a non-zero value for > 60 s. The labels are bounded: `pool` is
	// drawn from the (finite) §5.2 sandbox-warm-pool registry, `level`
	// is one of the four §4.4 levels.
	checkpointStaleSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_checkpoint_stale_sessions",
		Help: "Active sessions whose last successful checkpoint age exceeds periodicCheckpointIntervalSeconds (§4.4).",
	}, []string{"pool", "level"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 236 — `lenny_partial_manifest_cleanup_total` records
	// the outcome of every partial-manifest cleanup invocation:
	// `success` (chunks deleted + row soft-deleted by the primary
	// resume path), `failed_deleted` (cleanup encountered a MinIO
	// delete error and will be retried), `gc_collected` (the §12.5
	// backstop sweep ran after the resume window expired).
	partialManifestCleanup, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_partial_manifest_cleanup_total",
		Help: "Partial checkpoint manifest cleanup outcomes (§4.4 line 236).",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	// §10.1 — `lenny_checkpoint_partial_manifests_superseded_total`
	// counts every prior partial manifest that was soft-deleted by a
	// supersede-on-write transaction. Repeated drain-timeout patterns
	// on the same session bump this counter so operators can detect
	// tenant or pool-specific instabilities.
	checkpointPartialManifestsSuperseded, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_partial_manifests_superseded_total",
		Help: "Prior partial manifests soft-deleted on supersede (§10.1).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 248 — `lenny_checkpoint_orphaned_objects_total` is
	// incremented when a checkpoint abort-cleanup DeleteObject call
	// failed (the orphan persists in MinIO until the §12.5 backstop
	// sweeps it). Labels are bounded: `pool` is finite (warm-pool
	// registry); `trigger` is one of `periodic`, `pre_scale_down`,
	// `eviction`.
	checkpointOrphanedObjects, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_orphaned_objects_total",
		Help: "Checkpoint abort cleanup failed to delete partial objects (§4.4 line 248).",
	}, []string{"pool", "trigger"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 254 — `lenny_checkpoint_size_exceeded_total` is
	// incremented when the pre-checkpoint workspace-size probe
	// rejects the run. Labels are bounded: `pool` is finite;
	// `level` is one of the four §4.4 runtime-integration levels.
	checkpointSizeExceeded, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_size_exceeded_total",
		Help: "Pre-checkpoint workspace-size probe exceeded the limit (§4.4 line 254).",
	}, []string{"pool", "level"})
	if err != nil {
		return nil, err
	}
	// §4.4 lines 283–289 — `lenny_session_eviction_total_loss_total` is
	// incremented when both MinIO and Postgres failed for an eviction
	// checkpoint and the session is unrecoverable. Labels are bounded:
	// `pool` is finite; `had_prior_checkpoint` is "true"|"false".
	sessionEvictionTotalLoss, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_eviction_total_loss_total",
		Help: "Session eviction total-loss events (§4.4 lines 283-289).",
	}, []string{"pool", "had_prior_checkpoint"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 279 — `lenny_checkpoint_eviction_partial_keys_logged_total`
	// is incremented on every MinIO-then-Postgres-fail eviction WARN log
	// emission. Labels are bounded: `pool` is finite; `keys_committed`
	// is "0" or "1+".
	checkpointEvictionPartialKeysLogged, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_eviction_partial_keys_logged_total",
		Help: "Partial MinIO key sets logged on the eviction loss path (§4.4 line 279).",
	}, []string{"pool", "keys_committed"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 254 — `lenny_checkpoint_duration_seconds` is observed
	// at the end of every checkpoint snapshot regardless of trigger.
	// Labels are bounded: `pool` is finite; `level` is one of the four
	// §4.4 levels; `trigger` is one of `periodic`, `pre_scale_down`,
	// `eviction`. The §16.5 CheckpointDurationHigh alert fires when
	// the P95 of this histogram for Full-level or embedded-adapter
	// pools exceeds 2.5 s over a 5-minute window.
	checkpointDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_checkpoint_duration_seconds",
		Help:    "End-to-end checkpoint wall time in seconds (§4.4 line 254).",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
	}, []string{"pool", "level", "trigger"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 14 / §6.3 line 348 — `lenny_session_startup_duration_seconds`
	// measures pod claim through agent session ready, excluding client
	// file upload and workspace materialization. Explicit buckets
	// include the 2s (runc) and 5s (gVisor) SLO thresholds so the
	// lenny_session_startup_duration_slow_ratio recording rule can read
	// the bucket boundary directly.
	sessionStartupDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_startup_duration_seconds",
		Help:    "End-to-end pod-warm session startup: pod claim through agent session ready, excluding upload and workspace materialization (§6.3 line 348).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 1.5, 2, 3, 5, 8, 13},
	}, []string{"pool", "runtime_class", "isolation_profile"})
	if err != nil {
		return nil, err
	}
	// §6.3 line 372 — `lenny_session_startup_phase_duration_seconds`
	// instruments each hot-path phase independently so a slow startup
	// can be localized. Buckets cover the per-phase budgets from the
	// §6.3 table (≤100ms claim/credential through ≤4.5s gVisor agent
	// start).
	sessionStartupPhaseDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_startup_phase_duration_seconds",
		Help:    "Per-phase pod-warm session startup latency (§6.3 line 372).",
		Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 1.5, 3, 4.5, 8},
	}, []string{"phase", "runtime_class"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 15 / §6.3 line 356 —
	// `lenny_session_time_to_first_token_seconds` measures session
	// start request (POST /v1/sessions admission) through the first
	// agent-streamed response event. Explicit buckets straddle the
	// 10s TTFT SLO threshold so the TTFTBurnRate alert reads the
	// le="10" bucket boundary directly. spec: §16.1 line 15; §6.3
	// line 356.
	sessionTimeToFirstToken, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_time_to_first_token_seconds",
		Help:    "End-to-end time to first token: session start request to first agent-streamed response event (§6.3 line 356, §16.1 line 15).",
		Buckets: []float64{0.1, 0.5, 1, 2, 3, 5, 8, 10, 15, 30, 60},
	}, []string{"pool", "runtime_class", "isolation_profile"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 122 / §6.3 line 352 — `lenny_warmpool_claims_total`
	// counts the idle→claimed transitions in the §6.1 warm pool and
	// is the denominator of the SDK-warm demotion-rate ratio
	// (`lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total`).
	warmpoolClaims, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_claims_total",
		Help: "Idle→claimed warm-pool transitions per pool (§6.3 line 352, §16.1 line 122).",
	}, []string{"pool", "runtime_class"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §7.3 — `lenny_session_retry_total{failure_class}` counts
	// the retries of a logical session. Each pod-recovery retry bumps
	// the counter with the failure_class label echoing the row's §7.1
	// FailureClass at retry time. F-7.3.10.
	sessionRetryTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_retry_total",
		Help: "Session-level retry attempts by failure class (§7.3, §16.1).",
	}, []string{"failure_class"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §7.3 — `lenny_session_resume_attempts_total{pool, outcome}`
	// counts every POST /v1/sessions/{id}/resume call that passes the
	// precondition gate. Outcome is "success" when the row transitions
	// to running or "failure" when the pod-claim step fails. F-7.3.10.
	sessionResumeAttempts, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_resume_attempts_total",
		Help: "Session resume attempts by outcome (§7.3, §16.1).",
	}, []string{"pool", "outcome"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 124 — `lenny_warmpool_warmup_failure_total{error_type}`
	// counts warm-pool-side startup failures by §7.3 non-retryable
	// failure category. F-7.5.9.
	warmpoolWarmupFailure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_warmup_failure_total",
		Help: "Warm-pool warm-up failures by error_type (§16.1 line 124, §7.3 line 387).",
	}, []string{"error_type"})
	if err != nil {
		return nil, err
	}
	// §7.1 line 112 — `lenny_workspace_seal_duration_seconds` tracks
	// seal-and-export completion time across all terminal sessions,
	// labeled by `pool` (session runtime) and `outcome` (success |
	// timeout). The §16.5 WorkspaceSealStuck alert fires when the
	// outcome="timeout" count increases over a 5-minute window. Buckets
	// span the per-attempt 5s–60s backoff and the 300s default window.
	workspaceSealDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_workspace_seal_duration_seconds",
		Help:    "Workspace seal-and-export completion time by outcome (§7.1 line 112).",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"pool", "outcome"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 262 / §12.5 ll. 303 —
	// `lenny_checkpoint_storage_failure_total` counts non-eviction
	// MinIO-upload failures (all retries exhausted, the failed
	// checkpoint is discarded) AND the §12.5 fail-closed T4 KMS-
	// unavailable rejections. Labels: `pool`, `level`, `trigger`,
	// and `reason` — the §12.5 ll. 303 `{reason="kms_unavailable"}`
	// filter that the CheckpointStorageUnavailable alert reads.
	// Existing retry-exhaustion callers stamp `reason="retry_exhausted"`;
	// the blobstore's T4 fail-closed branch stamps
	// `reason="kms_unavailable"` and leaves `pool`/`level`/`trigger`
	// empty because the rejection fires before the adapter has the
	// wider context.
	checkpointStorageFailure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_storage_failure_total",
		Help: "Checkpoint or artifact writes failed (§4.4 line 262 retry-exhausted, §12.5 ll. 303 kms_unavailable).",
	}, []string{"pool", "level", "trigger", "reason"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 263 — `lenny_checkpoint_eviction_fallback_total`
	// counts the entries into the eviction Postgres-fallback writer
	// when all MinIO retries for an eviction checkpoint failed.
	// Labels: `pool` (finite) and `had_prior_checkpoint` ("true" |
	// "false").
	checkpointEvictionFallback, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_eviction_fallback_total",
		Help: "Checkpoint storage fallbacks to Postgres minimal state (§4.4 line 263).",
	}, []string{"pool", "had_prior_checkpoint"})
	if err != nil {
		return nil, err
	}
	// §4.6.1 — `lenny_pod_claim_fallback_skipped_total` counts the
	// Postgres-backed fallback claim skips when a precondition fails.
	// Labels: `reason` (`mirror_stale` | `apiserver_unreachable`).
	podClaimFallbackSkipped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pod_claim_fallback_skipped_total",
		Help: "Postgres-backed pod-claim fallback skips by precondition (§4.6.1).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 519 — `lenny_slot_assignment_conflict_total` increments
	// when a concurrent-mode slot reservation found a candidate pod at
	// its maxConcurrent bound. `pool` is bounded by the warm-pool
	// registry.
	slotAssignmentConflict, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_slot_assignment_conflict_total",
		Help: "Concurrent-mode slot reservation failures due to slot contention (§5.2 line 519).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §4.9 line 1220 — `lenny_credential_preclaim_mismatch_total`
	// increments when the pre-claim availability check passed but the
	// subsequent assignment failed (a credential became unavailable
	// between check and assignment). `pool` and `provider` are bounded
	// by the credential-pool registry.
	credentialPreclaimMismatch, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_credential_preclaim_mismatch_total",
		Help: "Pre-claim credential availability check passed but assignment failed (§4.9 line 1220).",
	}, []string{"pool", "provider"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 51 — `lenny_credential_lease_assignments_total` counts
	// the cumulative credential leases issued from a pool. `source` is
	// `primary` | `fallback` | `cached`.
	credentialLeaseAssignments, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_credential_lease_assignments_total",
		Help: "Credential leases issued from a pool by source (§16.1).",
	}, []string{"provider", "pool", "source"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 55 — `lenny_credential_lease_duration_seconds` observes
	// the wall-clock duration of each issued lease from assignment to
	// release. Buckets span a few seconds to a multi-hour session.
	credentialLeaseDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_credential_lease_duration_seconds",
		Help:    "Wall-clock duration of each issued credential lease (§16.1).",
		Buckets: prometheus.ExponentialBuckets(15, 2, 12),
	}, []string{"provider", "pool"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 118 — `lenny_credential_rotation_total` counts
	// fault-driven credential rotations by error type. The §4.9 Fallback
	// Flow increments it when a faulted lease is rotated to the chain's
	// next pool.
	credentialRotation, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_credential_rotation_total",
		Help: "Credential rotations by error type (§16.1).",
	}, []string{"error_type"})
	if err != nil {
		return nil, err
	}
	// §4.9 line 1395 — `lenny_gateway_credential_fallback_exhausted_total`
	// counts fallback-chain exhaustions, labeled by pool, provider, and
	// error type. The CredentialFallbackExhausted condition is terminal
	// for the session.
	credentialFallbackExhausted, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_credential_fallback_exhausted_total",
		Help: "Credential fallback-chain exhaustions by pool, provider, and error type (§4.9).",
	}, []string{"pool", "provider", "error_type"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 53 — `lenny_credential_pool_utilization` is the ratio of
	// in-use credentials to total pool credentials, in [0,1]. The
	// CredentialPoolLow alert fires above 0.80.
	credentialPoolUtilization, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_credential_pool_utilization",
		Help: "Ratio of in-use credentials to total pool credentials (§16.1).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 97 — `lenny_gateway_llm_proxy_active_connections` is the
	// count of in-flight LLM proxy requests on a replica.
	llmProxyActiveConnections, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_llm_proxy_active_connections",
		Help: "LLM Proxy active connections (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §16.1 line 99 — `lenny_gateway_llm_translation_duration_seconds`
	// observes the native-translator CPU time per leg (upstream network
	// time excluded). `direction` is `request` | `response`.
	llmTranslationDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gateway_llm_translation_duration_seconds",
		Help:    "Native LLM translator CPU time per leg (§16.1).",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12),
	}, []string{"pool", "provider", "proxy_dialect", "direction"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 100 — `lenny_gateway_llm_translation_errors_total`
	// counts native-translator failures by the §4.9 error taxonomy.
	llmTranslationErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_llm_translation_errors_total",
		Help: "LLM translator failures by error type (§16.1).",
	}, []string{"pool", "provider", "error_type"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 12 / §16.1 — `lenny_slot_failure_total` counts
	// concurrent-workspace slot bind failures after a slot was reserved.
	// `error_type` names the bind stage; `k8s_pod_name` is sanctioned for
	// this metric by §16.1.
	slotFailure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_slot_failure_total",
		Help: "Concurrent-workspace slot failure count (§5.2 line 12).",
	}, []string{"error_type", "pool", "k8s_pod_name"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 521 / §12.4 — `lenny_slot_rehydration_total` counts
	// post-recovery slot-counter rehydration events (seeding a pod's
	// active_slots from Postgres after a Redis restart). `pod` and `pool`
	// are bounded: at most one rehydration per pod per Redis restart.
	slotRehydration, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_slot_rehydration_total",
		Help: "Post-recovery slot-counter rehydration events (§5.2 line 521).",
	}, []string{"pod", "pool"})
	if err != nil {
		return nil, err
	}
	// §5.2 — `lenny_slot_pod_replacement_total` counts whole-pod
	// replacements triggered by the concurrent-workspace slot retry policy:
	// a pod is marked unhealthy and drained when ceil(maxConcurrent/2) or
	// more of its slots fail or leak within the rolling 5-minute window.
	// Labels: `pool` (finite).
	slotPodReplacement, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_slot_pod_replacement_total",
		Help: "Concurrent-workspace whole-pod replacements on the unhealthy-slot threshold (§5.2).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §4.4 line 234 — `lenny_checkpoint_partial_total` counts the
	// partial-manifest row writes. Labels: `pool` (finite).
	checkpointPartialTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_partial_total",
		Help: "Partial-manifest checkpoint writes (§4.4 line 234).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §10.1 — `lenny_prestop_cap_selection_total` counts the preStop
	// tiered-cap selection by source. Labels: `pool` (finite),
	// `service_instance_id` (the OTel service.instance.id of the
	// replica performing the selection — at most one value per
	// replica), and `source` (postgres | postgres_null | cache_hit |
	// cache_miss_max_tier). The §16.5 PreStopCapFallbackRateHigh
	// alert reads the combined (postgres_null + cache_miss_max_tier)
	// rate.
	prestopCapSelection, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_prestop_cap_selection_total",
		Help: "preStop tiered checkpoint cap selections by source (§10.1).",
	}, []string{"pool", "service_instance_id", "source"})
	if err != nil {
		return nil, err
	}
	// spec: §10.1 line 161 — `lenny_gateway_sigkill_streams_total`
	// counts in-flight streams forcibly terminated when the kubelet
	// SIGKILLs the pod at the grace deadline because their eviction
	// checkpoint did not complete in budget. Labels mirror the cap
	// selection counter so per-replica / per-pool alert expressions
	// can correlate the two.
	sigkillStreams, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_sigkill_streams_total",
		Help: "In-flight streams forcibly terminated at the SIGKILL deadline (§10.1).",
	}, []string{"pool", "service_instance_id"})
	if err != nil {
		return nil, err
	}
	// §12.5 ll. 341 — `lenny_gc_tombstones_pruned_total` counts the
	// soft-deleted rows physically removed by the hard-prune sweep once
	// the tombstone retention window has elapsed. The `table` label
	// (`artifact_store|partial_manifest`) names which GC-managed row
	// class the single hard-prune pass removed.
	gcTombstonesPruned := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lenny_gc_tombstones_pruned_total",
		Help: "Soft-deleted rows physically removed by the §12.5 hard-prune sweep, labeled by table (artifact_store|partial_manifest).",
	}, []string{"table"})

	// §12.5 ll. 321 — retention GC sweep observability. `outcome` is
	// `success` or `error`. The four metrics together cover sweep
	// liveness (`runs_total`), throughput (`artifacts_deleted`),
	// reliability (`errors_total`), and latency (`duration_seconds`).
	gcRuns, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_runs_total",
		Help: "Artifact retention GC sweep invocations by outcome (§12.5 line 321).",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	gcArtifactsDeleted, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_artifacts_deleted",
		Help: "Artifacts removed by the retention GC sweep (§12.5 line 321). Labelled by per-store adapter name.",
	}, []string{"store"})
	if err != nil {
		return nil, err
	}
	gcErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_errors_total",
		Help: "Retention GC errors observed per sweep (§12.5 line 321). Labelled by per-store adapter name.",
	}, []string{"store"})
	if err != nil {
		return nil, err
	}
	gcDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gc_duration_seconds",
		Help:    "Retention GC sweep wall-clock duration in seconds (§12.5 line 321).",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12),
	}, nil)
	if err != nil {
		return nil, err
	}
	// §12.5 ll. 291 — `lenny_drain_readiness_checks_total` counts
	// eviction-admission decisions made by the
	// lenny-drain-readiness webhook. `outcome` is one of `allowed`,
	// `blocked`, or `forced`; the `forced` branch is the
	// drain-force-override path that also emits the §16.7
	// `node.drain.forced` audit event.
	drainReadinessChecks, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_drain_readiness_checks_total",
		Help: "Drain readiness admission decisions (§12.5 line 291) by outcome.",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	// §12.8 ll. 739 — `lenny_legal_hold_checkpoint_gaps_total` counts
	// sessions where the §12.8 legal-hold reconciler detected a
	// checkpoint sequence gap. Labelled by tenant for attribution; the
	// `LegalHoldCheckpointGapsDetected` alert reads this counter.
	legalHoldCheckpointGaps, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_legal_hold_checkpoint_gaps_total",
		Help: "Legal-hold sessions where the §12.8 reconciler detected a checkpoint gap.",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	// §12.5 ll. 282 — `lenny_artifact_upload_error_total` counts
	// ArtifactStore PUT failures after the retry budget. Labels:
	// `tenant_id` and `error_type` (bounded to
	// `minio_unreachable | auth | quota_exceeded | other` so the
	// metric does not explode under high tenancy + diverse errors).
	// The §16.5 MinIOUnavailable alert keys on the `minio_unreachable`
	// label value.
	artifactUploadError, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_artifact_upload_error_total",
		Help: "ArtifactStore PUT failures after the retry budget (§12.5 line 282) by error type.",
	}, []string{"tenant_id", "error_type"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §8.2 — `lenny_delegation_depth` per-session delegation
	// depth histogram, labelled by `pool` (per §16.1). Buckets cover
	// the §8.2.bis maxDelegationDepth ceiling and a head-room margin.
	delegationDepth, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_depth",
		Help:    "Per-session delegation depth observed at delegation admission (§8.2).",
		Buckets: []float64{0, 1, 2, 3, 5, 8, 13, 21},
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §8.2 — `lenny_delegation_would_have_blocked_total` counter
	// of self-recursion hops where any layer of the §8.2 three-layer
	// AND gate evaluated `false`. Labels match the §16.1 catalog row
	// (`pool`, `tenant_id`, `layer`, `mode`).
	delegationWouldHaveBlocked, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_would_have_blocked_total",
		Help: "Self-recursion would-have-blocked counter by layer and cycle-detection mode (§8.2).",
	}, []string{"pool", "tenant_id", "layer", "mode"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §8.9 — `lenny_delegation_tree_cycle_detected_total`
	// counts the tree-walker defensive cycle hits. The §8.2 cycle
	// detector prevents cycles at delegation time; a non-zero rate
	// here implies the persistent store has been corrupted (e.g., a
	// §8.10 recovery write re-parented a node). F-8.9.10.
	delegationTreeCycleDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_cycle_detected_total",
		Help: "Tree walker hit a cycle in the §8.2 ParentSessionID lineage (corrupt store).",
	}, []string{"tenant_id", "source"})
	if err != nil {
		return nil, err
	}
	// §16.1 / §8.3 line 379 — `lenny_delegation_parallel_children_high_watermark`
	// records the maximum simultaneous in-flight children observed for
	// each delegation tree at tree completion, labelled by `pool` and
	// `tenant_id`. Buckets cover the typical maxParallelChildren range
	// (the §8.2 default is 4) with head-room above it. F-8.9.6.
	delegationParallelChildrenHWM, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_parallel_children_high_watermark",
		Help:    "Maximum simultaneous in-flight children per delegation tree at completion (§8.3).",
		Buckets: []float64{0, 1, 2, 4, 8, 16, 32, 64},
	}, []string{"pool", "tenant_id"})
	if err != nil {
		return nil, err
	}
	// §11.1 line 7 — `lenny_rate_limit_rejected_total` counts ratelimit
	// middleware 429 rejections, labelled by `scope` (`global` | `user`)
	// so operators can attribute rejection volume per enforcement axis.
	rateLimitRejected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_rate_limit_rejected_total",
		Help: "§11.1 ratelimit middleware admission rejections by scope (global | user).",
	}, []string{"scope"})
	if err != nil {
		return nil, err
	}
	// §16.5 RateLimitDegraded — `lenny_rate_limit_failopen_active` is
	// the per-replica fail-open gauge: 1 once a counter outage has been
	// observed (the middleware is admitting traffic without enforcement),
	// 0 once the next Incr returns successfully. The §16.5 alert reads
	// `lenny_rate_limit_failopen_active == 1`.
	rateLimitFailopenActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_rate_limit_failopen_active",
		Help: "§11.1 ratelimit fail-open state: 1 while the counter is degraded, 0 when healthy.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §11.1 line 7 — `lenny_rate_limit_counter_failure_total` counts
	// every counter error observed by the ratelimit middleware. Bumps
	// at fail-open entry and on each subsequent error in the outage
	// window so the operator sees a rate even when the gauge is
	// pinned to 1.
	// §10.1 line 45 DualStoreUnavailable — `lenny_dual_store_unavailable`
	// is the per-replica gauge the §10.1 dual-store monitor pins to 1
	// while both Postgres and Redis are unreachable and clears to 0 on
	// recovery. The §16.5 DualStoreUnavailable alert reads `== 1`.
	dualStoreUnavailable, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_dual_store_unavailable",
		Help: "§10.1 dual-store degraded mode: 1 while Postgres and Redis are both unreachable, 0 when at least one recovers.",
	}, nil)
	if err != nil {
		return nil, err
	}
	rateLimitCounterFailure := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_rate_limit_counter_failure_total",
		Help: "§11.1 ratelimit counter errors observed by the middleware.",
	})
	// §11.5 line 277 — `lenny_idempotency_cache_write_failures_total`
	// counts §11.5 idempotency-key Put failures (inner handler ran,
	// durable store rejected the cache row; next retry WILL
	// re-execute). spec: §11.5 line 277; F-11.5.4.
	idempotencyCacheWriteFailures, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_idempotency_cache_write_failures_total",
		Help: "§11.5 idempotency-key cache Put failures (silent re-execution risk).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	// §11.5 line 277 — `lenny_idempotency_cache_skipped_total` counts
	// cache writes the middleware declined by policy. reason:
	// `server_error` (inner-handler 5xx; not replayed for the 24-hour
	// TTL). spec: §11.5 line 277; F-11.5.3.
	idempotencyCacheSkipped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_idempotency_cache_skipped_total",
		Help: "§11.5 idempotency-key cache writes the middleware declined by policy.",
	}, []string{"tenant_id", "reason"})
	if err != nil {
		return nil, err
	}
	// §12.3 line 76 — billing_flush_pressure: emitted when the failover
	// Tier 2 write-ahead buffer crosses billingFlushMaxPending. F-12.3.13.
	billingFlushPressure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_billing_flush_pressure_total",
		Help: "§12.3 billing_flush_pressure: billing write-ahead buffer crossed billingFlushMaxPending and was force-flushed.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §12.3 lines 115-125 — sustained Postgres write IOPS, sampled from
	// pg_stat_database row-write deltas. Numerator of the §16.5
	// PostgresWriteSaturation ratio. F-12.3.7.
	postgresWriteIops, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_postgres_write_iops",
		Help: "§12.3 sustained Postgres write IOPS sampled from pg_stat_database row-write deltas.",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §12.3 line 123 — configured postgres.writeCeilingIops, emitted at
	// startup so PostgresWriteSaturation reads an operator-tunable
	// scalar() denominator. F-12.3.8.
	postgresWriteCeilingIops, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_postgres_write_ceiling_iops",
		Help: "§12.3 line 123 configured Postgres sustained write-IOPS ceiling (postgres.writeCeilingIops).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §16.1 lenny_audit_chain_integrity_total — the §12.3 line 101
	// startup chain-continuity check classifies each tenant's chain by
	// §11.7 state; the §16.5 AuditChainGap alert reads state="broken".
	// F-12.3.9.
	auditChainIntegrity, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_chain_integrity_total",
		Help: "§11.7 audit chain integrity classifications by state, incremented by the §12.3 startup chain-continuity check.",
	}, []string{"state"})
	if err != nil {
		return nil, err
	}
	// §11.7 item 2 line 359 lenny_audit_grant_drift_total — the periodic
	// background integrity check increments it when it detects a grant /
	// trigger / erasure-guard drift after startup. Unlabeled to match the
	// §16.5 AuditGrantDrift alert expression (`> 0`). F-11.7.3.
	auditGrantDrift := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_grant_drift_total",
		Help: "§11.7 item 2 unexpected UPDATE/DELETE grants (or disabled tamper triggers) detected on audit tables by the periodic background integrity check.",
	})
	// §16.1 lenny_audit_ocsf_translation_failed_total — the §11.7 OCSF
	// translator (now wired into the gateway, F-11.7.1) increments this on
	// each per-row translation failure, labeled by the event type and the
	// ocsf.ErrorClass; it feeds the §16.5 OCSFTranslationBacklog signal.
	// F-11.7.15.
	auditOCSFTranslationFailed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_ocsf_translation_failed_total",
		Help: "§11.7 per-row OCSF translation failures by event_type and error_class.",
	}, []string{"event_type", "error_class"})
	if err != nil {
		return nil, err
	}
	// spec: §8.10 line 1103, §16.5 OrphanTasksPerTenantHigh alert reads
	// `scalar(lenny_max_orphan_tasks_per_tenant)` as the cap denominator.
	// Exposing the ceiling as an unlabeled gauge lets the alert resolve
	// without hard-coding a value, so a deployer override flows through
	// to the rule automatically. F-8.10.13.
	maxOrphanTasksPerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_max_orphan_tasks_per_tenant",
		Help: "Configured maxOrphanTasksPerTenant ceiling — drives the OrphanTasksPerTenantHigh alert threshold (§8.10 line 1103).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 144-149 — the
	// orphan-cleanup + tree-recovery observability surface. F-8.10.7.
	orphanCleanupRuns := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_cleanup_runs_total",
		Help: "Background orphan cleanup job executions (§8.10 line 1091 / §16.1).",
	})
	orphanTasksTerminated := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_orphan_tasks_terminated",
		Help: "Orphan tasks terminated by the §8.10 cleanup job (§16.1).",
	})
	orphanTasksActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active",
		Help: "Currently active orphan tasks awaiting cleanup, summed across tenants (§8.10 / §16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	orphanTasksActivePerTenant, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_orphan_tasks_active_per_tenant",
		Help: "Per-tenant active orphan task count; drives the OrphanTasksPerTenantHigh alert (§8.10 line 1103 / §16.1).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	treeRecoveryDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_delegation_tree_recovery_duration_seconds",
		Help:    "Delegation tree-recovery wall-clock duration by outcome (§8.10 / §16.1 line 144).",
		Buckets: []float64{0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1200, 1800},
	}, []string{"pool", "outcome"})
	if err != nil {
		return nil, err
	}
	treeRecoveryTimeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_recovery_timeout_total",
		Help: "Delegation tree-recovery timeouts by timeout type (`level` | `tree`) (§8.10 / §16.1 line 145).",
	}, []string{"pool", "timeout_type"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 573 — `lenny_stateless_requests_total` is the cumulative
	// request count arriving at a concurrent-stateless pool's Kubernetes
	// Service; the PoolScalingController reads
	// `rate(lenny_stateless_requests_total[5m])` for stateless pool
	// `base_demand_p95`. The producer lands with the tenant-affinity
	// routing layer (F-5.2.3).
	statelessRequests, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_stateless_requests_total",
		Help: "Concurrent-stateless requests routed through the pool's Service (§5.2 line 573).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 573 — `lenny_stateless_concurrent_active` is the
	// instantaneous active-slot count per concurrent-stateless pool. The
	// PoolScalingController reads `max_over_time(...[5m])` for stateless
	// pool `burst_p99_claims`. Producer lands with F-5.2.3.
	statelessConcurrentActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_stateless_concurrent_active",
		Help: "Concurrent-stateless pool peak active slot count (§5.2 line 573).",
	}, []string{"pool"})
	if err != nil {
		return nil, err
	}
	// §5.2 line 569 / §16.1 line 124 — `lenny_task_reuse_count` is a
	// per-pod histogram of completed task counts in task mode. The
	// PoolScalingController reads the median over the rolling window as
	// the mode-adjusted `mode_factor` for task-mode pools with
	// preConnect: true. Emitter lands with the task-mode lifecycle
	// (F-5.2.1 / F-5.2.18).
	taskReuseCount, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_task_reuse_count",
		Help:    "Tasks executed on a single pod in task mode (§5.2 line 569 / §16.1).",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"pool", "k8s_pod_name"})
	if err != nil {
		return nil, err
	}
	// §16 line 66 — `lenny_delegation_lease_extension_total` counter
	// of §8.6 lease-extension decisions, labelled by `tenant_id` and
	// the §8.6 line 743 `outcome` classification (approved/capped/
	// denied). Driven by leasecontrol.Service.auditFull on every
	// ExtendLease return — wired in cmd/lenny-gateway/main.go via the
	// Options.Metrics field. F-8.6.13.
	delegationLeaseExtension, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_lease_extension_total",
		Help: "§8.6 delegation lease-extension decisions by tenant and §8.6 line 743 outcome.",
	}, []string{"tenant_id", "outcome"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 80 — per-file PreExportMaterialization scan outcomes on
	// the §8.7 delegation file-export path. F-8.7.10.
	exportFileScans, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_export_file_scans_total",
		Help: "§8.7 PreExportMaterialization per-file scan outcomes (§16.1 line 80).",
	}, []string{"pool", "tenant_id", "policy_name", "interceptor_ref", "outcome"})
	if err != nil {
		return nil, err
	}
	// §16.1 line 81 — per-file PreExportMaterialization interceptor
	// latency. Buckets span sub-millisecond to several seconds so the
	// P99 > 1s hot-path signal is observable. F-8.7.10.
	exportFileScanDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_export_file_scan_duration_seconds",
		Help:    "§8.7 PreExportMaterialization per-file interceptor latency (§16.1 line 81).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"pool", "tenant_id", "interceptor_ref"})
	if err != nil {
		return nil, err
	}
	// §9.4 line 200 / §16.1 line 151 — MemoryStore per-operation
	// duration histogram. Six operation labels (write, query, delete,
	// list, delete_by_user, delete_by_tenant); backend distinguishes
	// the default Postgres backend from custom implementations.
	// Buckets span the per-record write/query path through the whole-
	// scope erasure SLO (§16.5 alert fires at 60s for delete_by_user,
	// 300s for delete_by_tenant).
	memoryStoreOperationDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_memory_store_operation_duration_seconds",
		Help:    "MemoryStore per-operation duration (§9.4 line 200 / §16.1 line 151).",
		Buckets: []float64{0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 5, 30, 60, 300, 900},
	}, []string{"operation", "backend"})
	if err != nil {
		return nil, err
	}
	// §9.4 line 200 / §16.1 line 152 — MemoryStore per-operation error
	// counter.
	memoryStoreErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_memory_store_errors_total",
		Help: "MemoryStore per-operation errors (§9.4 line 200 / §16.1 line 152).",
	}, []string{"operation", "backend", "error_type"})
	if err != nil {
		return nil, err
	}
	// §9.4 line 202 / §16.1 line 153 — MemoryStore per-tenant
	// approximate record count gauge. tenant_id only; user_id is on the
	// §16.1.1 forbidden-label list as a cardinality hot-spot.
	memoryStoreRecordCount, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_memory_store_record_count",
		Help: "Approximate stored memory records per tenant (§9.4 line 202 / §16.1 line 153).",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	// §9.4 line 202 / §16.1 line 154 — MemoryStore per-user 80% headroom
	// counter. Increments on each Write commit that leaves the writing
	// user at >= 80% of memory.maxMemoriesPerUser. Labels: tenant_id
	// and backend; no user_id label (forbidden by §16.1.1).
	memoryStoreUserOverThreshold, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_memory_store_user_count_over_threshold_total",
		Help: "MemoryStore writes that leave a user at >= 80% of memory.maxMemoriesPerUser (§9.4 line 202 / §16.1 line 154).",
	}, []string{"tenant_id", "backend"})
	if err != nil {
		return nil, err
	}
	// §13.3 line 595 / §16.1 — NTP drift self-monitor gauge populated by
	// pkg/driftmonitor on its periodic sample. The §16.5
	// GatewayClockDrift alert keys on `abs(lenny_time_drift_seconds) >
	// 0.5`. F-13.3.5.
	timeDriftGauge, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_time_drift_seconds",
		Help: "Gateway wall-clock signed offset (seconds) from the NTP reference (§13.3 line 595 / §16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}

	reg.MustRegister(requestsTotal, requestDuration, maxSessionsPerReplica,
		extractionThreshold,
		storageQuotaUsed, storageQuotaLimit, circuitBreakerOpen,
		cbRejections, cbRejectionsSuppressed, elicitationDropped,
		elicitationTamperDetected, elicitationIntegrityWeakened,
		elicitationPending, elicitationTimeout, elicitationSuppressed,
		elicitationRoundtripSeconds,
		experimentIsoRej,
		experimentTargetingDur, experimentTargetingErr, experimentTargetingCircuit,
		sessionTotal, sessionError, sessionDuration, evalScore,
		noEnvPolicyAllowAll, erasureJobFailed, tokenServiceCircuitState,
		kmsSigningErrors, kmsSigningCircuitState,
		checkpointStaleSessions,
		partialManifestCleanup, checkpointPartialManifestsSuperseded,
		checkpointOrphanedObjects, checkpointSizeExceeded, sessionEvictionTotalLoss,
		checkpointEvictionPartialKeysLogged,
		checkpointDuration, sessionStartupDuration, sessionStartupPhaseDuration,
		sessionTimeToFirstToken, warmpoolClaims,
		sessionRetryTotal, sessionResumeAttempts,
		warmpoolWarmupFailure,
		workspaceSealDuration,
		checkpointStorageFailure,
		checkpointEvictionFallback, podClaimFallbackSkipped, slotAssignmentConflict,
		credentialPreclaimMismatch,
		credentialLeaseAssignments, credentialLeaseDuration, credentialPoolUtilization,
		llmTranslationDuration, llmTranslationErrors, slotFailure,
		slotRehydration, slotPodReplacement,
		checkpointPartialTotal, prestopCapSelection, sigkillStreams,
		gcTombstonesPruned,
		gcRuns, gcArtifactsDeleted, gcErrors, gcDuration,
		drainReadinessChecks, legalHoldCheckpointGaps,
		artifactUploadError,
		delegationDepth, delegationWouldHaveBlocked, delegationTreeCycleDetected,
		delegationParallelChildrenHWM,
		rateLimitRejected, rateLimitFailopenActive, rateLimitCounterFailure,
		dualStoreUnavailable,
		idempotencyCacheWriteFailures, idempotencyCacheSkipped,
		billingFlushPressure, postgresWriteIops, postgresWriteCeilingIops,
		auditChainIntegrity, auditGrantDrift, auditOCSFTranslationFailed,
		maxOrphanTasksPerTenant,
		orphanCleanupRuns, orphanTasksTerminated, orphanTasksActive,
		orphanTasksActivePerTenant,
		treeRecoveryDuration, treeRecoveryTimeout,
		statelessRequests, statelessConcurrentActive, taskReuseCount,
		delegationLeaseExtension,
		exportFileScans, exportFileScanDuration,
		memoryStoreOperationDuration, memoryStoreErrors,
		memoryStoreRecordCount, memoryStoreUserOverThreshold,
		timeDriftGauge)
	gauge := activeSessions.WithLabelValues()
	streams := activeStreams.WithLabelValues()
	queueDepth := requestQueueDepth.WithLabelValues()
	rejections := rejectionRate.WithLabelValues()
	cbStale := cbCacheStale.WithLabelValues()
	cbInit := cbCacheInitialized.WithLabelValues()
	gcPause := gcPauseP99Ms.WithLabelValues()
	// §4.1 / §16.5: materialize the child series for the scalar gauges
	// at construction so /metrics emits them even before
	// SetMinReplicas / SetStreamCeiling are called; otherwise the
	// scalar(...) expression in the alert rule evaluates to NaN until
	// the gateway main has wired the configuration.
	minReplicasChild := minReplicas.WithLabelValues()
	streamCeilingChild := streamCeiling.WithLabelValues()
	replicaCountChild := replicaCount.WithLabelValues()
	// §11.2.1 / §16.5: pre-materialize the unlabelled threshold gauge so
	// /metrics emits the default (0.05) reading before
	// SetBillingCorrectionRateThreshold is called; otherwise the
	// scalar(lenny_billing_correction_rate_threshold) in the alert rule
	// evaluates to NaN until the gateway main has wired the configuration.
	billingCorrectionRateThresholdChild := billingCorrectionRateThreshold.WithLabelValues()
	billingCorrectionRateThresholdChild.Set(0.05)
	// spec: §25.13 line 4737 / §16.5 — pre-materialize the §25.13
	// tier-dependent threshold gauges with their base-Helm defaults so
	// scalar(...) in the bundled alert expressions evaluates to a
	// finite value even before the gateway main has called the Set*
	// helpers. F-25.13.2.
	gatewayQueueDepthThresholdChild := gatewayQueueDepthThreshold.WithLabelValues()
	gatewayQueueDepthThresholdChild.Set(20)
	gatewayLatencyThresholdSecondsChild := gatewayLatencyThresholdSeconds.WithLabelValues()
	gatewayLatencyThresholdSecondsChild.Set(3.0)
	credentialPoolLowThresholdChild := credentialPoolLowThreshold.WithLabelValues()
	credentialPoolLowThresholdChild.Set(0.80)
	// spec: §10.4 / §16 — pre-materialize the unlabelled replay buffer
	// utilization gauge so /metrics emits the series even before the
	// gateway has published any session events. F-10.4.11.
	replayBufferUtilizationChild := replayBufferUtilization.WithLabelValues()
	// §13.3 line 595: pre-materialize the unlabelled drift gauge so
	// /metrics emits a 0 reading before the first driftmonitor sample.
	// F-13.3.5.
	timeDriftChild := timeDriftGauge.WithLabelValues()
	llmProxyConns := llmProxyActiveConnections.WithLabelValues()
	reg.MustRegister(activeSessions, activeStreams, requestQueueDepth,
		rejectionRate, cbCacheStale, cbCacheInitialized, gcPauseP99Ms,
		minReplicas, streamCeiling, replicaCount, llmProxyActiveConnections,
		replayBufferUtilization, pdbBlockedEvictions,
		billingCorrectionRateThreshold,
		gatewayQueueDepthThreshold,
		gatewayLatencyThresholdSeconds,
		credentialPoolLowThreshold)

	tokenServiceCircuitChild := tokenServiceCircuitState.WithLabelValues()
	kmsSigningCircuitChild := kmsSigningCircuitState.WithLabelValues()
	return &Metrics{
		reg:                                  reg,
		requestsTotal:                        requestsTotal,
		requestDuration:                      requestDuration,
		activeSessions:                       gauge,
		activeStreams:                        streams,
		requestQueueDepth:                    queueDepth,
		rejectionRate:                        rejections,
		maxSessionsPerReplica:                maxSessionsPerReplica,
		minReplicas:                          minReplicasChild,
		streamCeiling:                        streamCeilingChild,
		replicaCount:                         replicaCountChild,
		billingCorrectionRateThreshold:       billingCorrectionRateThresholdChild,
		gatewayQueueDepthThreshold:           gatewayQueueDepthThresholdChild,
		gatewayLatencyThresholdSeconds:       gatewayLatencyThresholdSecondsChild,
		credentialPoolLowThreshold:           credentialPoolLowThresholdChild,
		extractionThreshold:                  extractionThreshold,
		storageQuotaUsed:                     storageQuotaUsed,
		storageQuotaLimit:                    storageQuotaLimit,
		circuitBreakerOpen:                   circuitBreakerOpen,
		cbRejections:                         cbRejections,
		cbRejectionsSuppressed:               cbRejectionsSuppressed,
		cbCacheStale:                         cbStale,
		cbCacheInitialized:                   cbInit,
		elicitationDropped:                   elicitationDropped,
		elicitationTamperDetected:            elicitationTamperDetected,
		elicitationIntegrityWeakened:         elicitationIntegrityWeakened.WithLabelValues(),
		elicitationPending:                   elicitationPending.WithLabelValues(),
		elicitationTimeout:                   elicitationTimeout.WithLabelValues(),
		elicitationSuppressed:                elicitationSuppressed.WithLabelValues(),
		elicitationRoundtripSeconds:          elicitationRoundtripSeconds.WithLabelValues(),
		experimentIsoRej:                     experimentIsoRej,
		experimentTargetingDur:               experimentTargetingDur,
		experimentTargetingErr:               experimentTargetingErr,
		experimentTargetingCircuit:           experimentTargetingCircuit,
		sessionTotal:                         sessionTotal,
		sessionError:                         sessionError,
		sessionDuration:                      sessionDuration,
		evalScore:                            evalScore,
		noEnvPolicyAllowAll:                  noEnvPolicyAllowAll,
		erasureJobFailed:                     erasureJobFailed,
		gcPauseP99Ms:                         gcPause,
		replayBufferUtilization:              replayBufferUtilizationChild,
		pdbBlockedEvictions:                  pdbBlockedEvictions,
		tokenServiceCircuitState:             tokenServiceCircuitChild,
		kmsSigningErrors:                     kmsSigningErrors,
		kmsSigningCircuitState:               kmsSigningCircuitChild,
		checkpointStaleSessions:              checkpointStaleSessions,
		partialManifestCleanup:               partialManifestCleanup,
		checkpointPartialManifestsSuperseded: checkpointPartialManifestsSuperseded,
		checkpointOrphanedObjects:            checkpointOrphanedObjects,
		checkpointSizeExceeded:               checkpointSizeExceeded,
		sessionEvictionTotalLoss:             sessionEvictionTotalLoss,
		checkpointEvictionPartialKeysLogged:  checkpointEvictionPartialKeysLogged,
		checkpointDuration:                   checkpointDuration,
		sessionStartupDuration:               sessionStartupDuration,
		workspaceSealDuration:                workspaceSealDuration,
		sessionStartupPhaseDuration:          sessionStartupPhaseDuration,
		sessionTimeToFirstToken:              sessionTimeToFirstToken,
		warmpoolClaims:                       warmpoolClaims,
		sessionRetryTotal:                    sessionRetryTotal,
		sessionResumeAttempts:                sessionResumeAttempts,
		warmpoolWarmupFailure:                warmpoolWarmupFailure,
		checkpointStorageFailure:             checkpointStorageFailure,
		checkpointEvictionFallback:           checkpointEvictionFallback,
		podClaimFallbackSkipped:              podClaimFallbackSkipped,
		slotAssignmentConflict:               slotAssignmentConflict,
		credentialPreclaimMismatch:           credentialPreclaimMismatch,
		credentialLeaseAssignments:           credentialLeaseAssignments,
		credentialRotation:                   credentialRotation,
		credentialFallbackExhausted:          credentialFallbackExhausted,
		credentialLeaseDuration:              credentialLeaseDuration,
		credentialPoolUtilization:            credentialPoolUtilization,
		llmProxyActiveConnections:            llmProxyConns,
		llmTranslationDuration:               llmTranslationDuration,
		llmTranslationErrors:                 llmTranslationErrors,
		slotFailure:                          slotFailure,
		slotRehydration:                      slotRehydration,
		slotPodReplacement:                   slotPodReplacement,
		checkpointPartialTotal:               checkpointPartialTotal,
		prestopCapSelection:                  prestopCapSelection,
		sigkillStreams:                       sigkillStreams,
		gcTombstonesPruned:                   gcTombstonesPruned,
		gcRuns:                               gcRuns,
		gcArtifactsDeleted:                   gcArtifactsDeleted,
		gcErrors:                             gcErrors,
		gcDuration:                           gcDuration.WithLabelValues(),
		drainReadinessChecks:                 drainReadinessChecks,
		legalHoldCheckpointGaps:              legalHoldCheckpointGaps,
		artifactUploadError:                  artifactUploadError,
		delegationDepth:                      delegationDepth,
		delegationWouldHaveBlocked:           delegationWouldHaveBlocked,
		delegationTreeCycleDetected:          delegationTreeCycleDetected,
		delegationParallelChildrenHWM:        delegationParallelChildrenHWM,
		rateLimitRejected:                    rateLimitRejected,
		rateLimitFailopenActive:              rateLimitFailopenActive.WithLabelValues(),
		rateLimitCounterFailure:              rateLimitCounterFailure,
		dualStoreUnavailable:                 dualStoreUnavailable.WithLabelValues(),
		idempotencyCacheWriteFailures:        idempotencyCacheWriteFailures,
		idempotencyCacheSkipped:              idempotencyCacheSkipped,
		billingFlushPressure:                 billingFlushPressure.WithLabelValues(),
		postgresWriteIops:                    postgresWriteIops.WithLabelValues(),
		postgresWriteCeilingIops:             postgresWriteCeilingIops.WithLabelValues(),
		auditChainIntegrity:                  auditChainIntegrity,
		auditGrantDrift:                      auditGrantDrift,
		auditOCSFTranslationFailed:           auditOCSFTranslationFailed,
		maxOrphanTasksPerTenant:              maxOrphanTasksPerTenant.WithLabelValues(),
		orphanCleanupRuns:                    orphanCleanupRuns,
		orphanTasksTerminated:                orphanTasksTerminated,
		orphanTasksActive:                    orphanTasksActive.WithLabelValues(),
		orphanTasksActivePerTenant:           orphanTasksActivePerTenant,
		treeRecoveryDuration:                 treeRecoveryDuration,
		treeRecoveryTimeout:                  treeRecoveryTimeout,
		statelessRequests:                    statelessRequests,
		statelessConcurrentActive:            statelessConcurrentActive,
		taskReuseCount:                       taskReuseCount,
		delegationLeaseExtension:             delegationLeaseExtension,
		exportFileScans:                      exportFileScans,
		exportFileScanDuration:               exportFileScanDuration,
		memoryStoreOperationDuration:         memoryStoreOperationDuration,
		memoryStoreErrors:                    memoryStoreErrors,
		memoryStoreRecordCount:               memoryStoreRecordCount,
		memoryStoreUserOverThreshold:         memoryStoreUserOverThreshold,
		timeDrift:                            timeDriftChild,
	}, nil
}

// IncDelegationLeaseExtension records a §8.6 lease-extension
// decision against the §16 line 66
// `lenny_delegation_lease_extension_total` counter. The `outcome`
// label is the §8.6 line 743 audit classification
// (`approved`/`capped`/`denied`) — emitted by
// leasecontrol.Service alongside its audit record so the dashboard and
// the audit chain share one source of truth. spec: §16 line 66; §8.6
// line 743; F-8.6.13.
func (m *Metrics) IncDelegationLeaseExtension(tenantID, outcome string) {
	if m == nil {
		return
	}
	m.delegationLeaseExtension.WithLabelValues(tenantID, outcome).Inc()
}

// IncExportFileScan records one §8.7 PreExportMaterialization per-file
// scan outcome. `outcome` is one of admitted, modified, rejected,
// failed_open, or failed_closed. spec: §16.1 line 80; F-8.7.10.
func (m *Metrics) IncExportFileScan(pool, tenantID, policyName, interceptorRef, outcome string) {
	if m == nil {
		return
	}
	m.exportFileScans.WithLabelValues(pool, tenantID, policyName, interceptorRef, outcome).Inc()
}

// ObserveExportFileScanDuration records the per-file §8.7
// PreExportMaterialization interceptor latency in seconds. spec: §16.1
// line 81; F-8.7.10.
func (m *Metrics) ObserveExportFileScanDuration(pool, tenantID, interceptorRef string, seconds float64) {
	if m == nil {
		return
	}
	m.exportFileScanDuration.WithLabelValues(pool, tenantID, interceptorRef).Observe(seconds)
}

// IncStatelessRequest records a §5.2 line 573 stateless-pool request.
// `pool` is the SandboxTemplate name. spec: §5.2 line 573.
func (m *Metrics) IncStatelessRequest(pool string) {
	if m == nil {
		return
	}
	m.statelessRequests.WithLabelValues(pool).Inc()
}

// SetStatelessConcurrentActive sets the instantaneous concurrent active
// slot count for a stateless pool. spec: §5.2 line 573.
func (m *Metrics) SetStatelessConcurrentActive(pool string, value float64) {
	if m == nil {
		return
	}
	m.statelessConcurrentActive.WithLabelValues(pool).Set(value)
}

// ObserveTaskReuseCount records the completed-task count of a retiring
// task-mode pod. `pool` is the SandboxTemplate name and `k8sPodName`
// is the pod whose retirement triggered the observation. spec: §5.2
// line 569 / §16.1 line 124.
func (m *Metrics) ObserveTaskReuseCount(pool, k8sPodName string, count int) {
	if m == nil {
		return
	}
	m.taskReuseCount.WithLabelValues(pool, k8sPodName).Observe(float64(count))
}

// TaskReuseQuantile reads the in-process median of the task-reuse
// histogram for one pool. The PoolScalingController uses it as the
// mode-adjusted `mode_factor` for task-mode pools with preConnect:
// true (§5.2 line 569). q must be in (0,1]. ok is false until at least
// one observation has been recorded for the pool (cold start). spec:
// §5.2 line 569.
func (m *Metrics) TaskReuseQuantile(pool string, q float64) (value float64, ok bool) {
	if m == nil {
		return 0, false
	}
	if q <= 0 || q > 1 {
		return 0, false
	}
	families, err := m.reg.Gather()
	if err != nil {
		return 0, false
	}
	var totalCount uint64
	var buckets []bucketSample
	for _, fam := range families {
		if fam.GetName() != "lenny_task_reuse_count" {
			continue
		}
		for _, mtr := range fam.GetMetric() {
			var match bool
			for _, lp := range mtr.GetLabel() {
				if lp.GetName() == "pool" && lp.GetValue() == pool {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			h := mtr.GetHistogram()
			if h == nil {
				continue
			}
			totalCount += h.GetSampleCount()
			for _, b := range h.GetBucket() {
				buckets = append(buckets, bucketSample{ub: b.GetUpperBound(), count: b.GetCumulativeCount()})
			}
		}
	}
	if totalCount == 0 {
		return 0, false
	}
	// Buckets across pods are summed by upper bound; histogram_quantile
	// from Prometheus does the same accumulation pre-quantile.
	merged := mergeBuckets(buckets)
	if len(merged) == 0 {
		return 0, false
	}
	// totalCount equals merged[last].count when bucket UB is +Inf, which
	// promauto NewHistogram always includes; recompute defensively.
	totalCount = merged[len(merged)-1].count
	threshold := uint64(float64(totalCount) * q)
	for i, b := range merged {
		if b.count >= threshold {
			lower := 0.0
			if i > 0 {
				lower = merged[i-1].ub
			}
			lowerCount := uint64(0)
			if i > 0 {
				lowerCount = merged[i-1].count
			}
			band := float64(b.count - lowerCount)
			if band == 0 {
				return b.ub, true
			}
			frac := float64(threshold-lowerCount) / band
			return lower + (b.ub-lower)*frac, true
		}
	}
	return merged[len(merged)-1].ub, true
}

// bucketSample is one (upper_bound, cumulative_count) pair from a
// histogram sample. Used by TaskReuseQuantile to merge per-pod
// histograms before computing the in-process median. spec: §5.2 line
// 569.
type bucketSample struct {
	ub    float64
	count uint64
}

// mergeBuckets aggregates per-pod task-reuse bucket samples by upper
// bound, returning a sorted-by-UB slice with cumulative counts (across
// all pods that share the upper bound). The summed cumulative counts
// match Prometheus' histogram_quantile aggregation across series of
// the same histogram. spec: §5.2 line 569.
func mergeBuckets(in []bucketSample) []bucketSample {
	by := map[float64]uint64{}
	for _, b := range in {
		by[b.ub] += b.count
	}
	ubs := make([]float64, 0, len(by))
	for ub := range by {
		ubs = append(ubs, ub)
	}
	for i := 1; i < len(ubs); i++ {
		for j := i; j > 0 && ubs[j-1] > ubs[j]; j-- {
			ubs[j-1], ubs[j] = ubs[j], ubs[j-1]
		}
	}
	out := make([]bucketSample, 0, len(ubs))
	for _, ub := range ubs {
		out = append(out, bucketSample{ub: ub, count: by[ub]})
	}
	return out
}

// IncCheckpointOrphanedObjects increments the §4.4 line 248
// `lenny_checkpoint_orphaned_objects_total` counter labeled by pool
// and trigger. Called from the adapter Checkpoint RPC when a
// CheckpointAborter.AbortPartial call failed to delete a partially
// uploaded chunk object.
// spec: §4.4 line 248.
func (m *Metrics) IncCheckpointOrphanedObjects(pool, trigger string) {
	if m == nil {
		return
	}
	m.checkpointOrphanedObjects.WithLabelValues(pool, trigger).Inc()
}

// IncCheckpointSizeExceeded increments the §4.4 line 254
// `lenny_checkpoint_size_exceeded_total` counter labeled by pool and
// level. Called from the adapter Checkpoint RPC when the
// pre-checkpoint workspace-size probe rejects the run.
// spec: §4.4 line 254.
func (m *Metrics) IncCheckpointSizeExceeded(pool, level string) {
	if m == nil {
		return
	}
	m.checkpointSizeExceeded.WithLabelValues(pool, level).Inc()
}

// IncSessionEvictionTotalLoss increments the §4.4 lines 283–289
// `lenny_session_eviction_total_loss_total` counter labeled by pool
// and had_prior_checkpoint. Called from the eviction-fallback writer
// when both MinIO and Postgres failed for an eviction checkpoint.
// spec: §4.4 line 286.
func (m *Metrics) IncSessionEvictionTotalLoss(pool string, hadPriorCheckpoint bool) {
	if m == nil {
		return
	}
	label := "false"
	if hadPriorCheckpoint {
		label = "true"
	}
	m.sessionEvictionTotalLoss.WithLabelValues(pool, label).Inc()
}

// IncCheckpointEvictionPartialKeysLogged increments the §4.4 line 279
// `lenny_checkpoint_eviction_partial_keys_logged_total` counter
// labeled by pool and keys_committed ("0" for total-MinIO-failure, "1+"
// for partial-upload scenarios). Called from the eviction-fallback
// writer's WARN-log path before the total-loss orchestration fires.
// spec: §4.4 line 279.
func (m *Metrics) IncCheckpointEvictionPartialKeysLogged(pool, keysCommitted string) {
	if m == nil {
		return
	}
	m.checkpointEvictionPartialKeysLogged.WithLabelValues(pool, keysCommitted).Inc()
}

// IncPartialManifestCleanup increments the §4.4 line 236
// `lenny_partial_manifest_cleanup_total` counter labeled by outcome
// (`success`, `failed_deleted`, or `gc_collected`).
// spec: §4.4 line 236.
func (m *Metrics) IncPartialManifestCleanup(outcome string) {
	if m == nil {
		return
	}
	m.partialManifestCleanup.WithLabelValues(outcome).Inc()
}

// IncCheckpointPartialManifestsSuperseded increments the §10.1
// `lenny_checkpoint_partial_manifests_superseded_total` counter
// labeled by pool. Called once per prior partial manifest soft-deleted
// inside a supersede-on-write transaction.
// spec: §10.1 supersede-on-write.
func (m *Metrics) IncCheckpointPartialManifestsSuperseded(pool string) {
	if m == nil {
		return
	}
	m.checkpointPartialManifestsSuperseded.WithLabelValues(pool).Inc()
}

// ObserveCheckpointDuration observes one §4.4 line 254
// `lenny_checkpoint_duration_seconds` histogram value labeled by
// pool, level, and trigger. Called from the checkpointer at the
// end of every snapshot regardless of outcome. The §16.5
// CheckpointDurationHigh alert reads the P95 of this histogram.
// spec: §4.4 line 254.
func (m *Metrics) ObserveCheckpointDuration(pool, level, trigger string, seconds float64) {
	if m == nil {
		return
	}
	m.checkpointDuration.WithLabelValues(pool, level, trigger).Observe(seconds)
}

// ObserveSessionStartupDuration records the §6.3 line 348 end-to-end
// pod-warm startup latency (pod claim through agent session ready,
// excluding upload and workspace materialization) for a successful
// session start. The StartupLatencyBurnRate and
// StartupLatencyGVisorBurnRate alerts read the P95 of this histogram
// through the lenny_session_startup_duration_slow_ratio recording rule.
// spec: §16.1 line 14, §6.3 line 348.
func (m *Metrics) ObserveSessionStartupDuration(pool, runtimeClass, isolationProfile string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionStartupDuration.WithLabelValues(pool, runtimeClass, isolationProfile).Observe(seconds)
}

// ObserveSessionStartupPhase records the §6.3 line 372 latency of one
// hot-path startup phase. phase is one of pod_claim,
// workspace_materialization, setup_commands, credential_assignment, or
// agent_session_start. Observed once per phase per successful start so
// the per-phase latency budget (§6.3 table) can be attributed.
// spec: §6.3 line 372.
func (m *Metrics) ObserveSessionStartupPhase(phase, runtimeClass string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionStartupPhaseDuration.WithLabelValues(phase, runtimeClass).Observe(seconds)
}

// ObserveSessionTimeToFirstToken records the §6.3 line 356 / §16.1
// line 15 end-to-end TTFT: wall-clock seconds from session start
// request (POST /v1/sessions admission, i.e. session.CreatedAt) to
// the first agent-streamed response event emitted to the SSE client.
// Observed once per session — the first qualifying response event
// records, all subsequent events for the same session are ignored.
// The TTFTBurnRate alert reads the P95 of this histogram via an
// inline expression against the le="10" bucket (the 10s SLO
// threshold). spec: §6.3 line 356, §16.1 line 15.
func (m *Metrics) ObserveSessionTimeToFirstToken(pool, runtimeClass, isolationProfile string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionTimeToFirstToken.WithLabelValues(pool, runtimeClass, isolationProfile).Observe(seconds)
}

// IncWarmpoolClaim increments the §6.3 line 352 / §16.1 line 122
// `lenny_warmpool_claims_total{pool,runtime_class}` counter on each
// idle→claimed transition in the §6.1 warm pool. This counter is the
// denominator of the §6.3 SDK-warm demotion-rate ratio
// (`lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total`)
// that deployers must track to verify SDK-warm net benefit. The
// numerator (`lenny_warmpool_sdk_demotions_total`) is gated on the
// SDK-warm demotion path itself; see F-6.1.1 for the §4.7 DemoteSDK
// stub. spec: §6.3 line 352, §16.1 line 122.
func (m *Metrics) IncWarmpoolClaim(pool, runtimeClass string) {
	if m == nil {
		return
	}
	m.warmpoolClaims.WithLabelValues(pool, runtimeClass).Inc()
}

// IncSessionRetry increments the §16.1 / §7.3
// `lenny_session_retry_total{failure_class}` counter for one retry of
// a logical session. Each successful pod recovery (the v1 retry path,
// fired by sessionserver.bumpRecoveryGeneration) bumps it once. The
// failure_class label echoes the session's §7.1 FailureClass at retry
// time; the caller supplies "unknown" for a session with no recorded
// class so the label cardinality stays bounded by the §7.1 closed
// enum. spec: §7.3, §16.1. F-7.3.10.
func (m *Metrics) IncSessionRetry(failureClass string) {
	if m == nil {
		return
	}
	m.sessionRetryTotal.WithLabelValues(failureClass).Inc()
}

// IncSessionResumeAttempt increments the §16.1 / §7.3
// `lenny_session_resume_attempts_total{pool, outcome}` counter for one
// POST /v1/sessions/{id}/resume call. Outcome is "success" when the
// row transitions to running or "failure" when the pod-claim step
// fails. The pool label echoes the session's §5.2 PoolRef at resume
// time (empty for a session whose pool was never resolved). spec:
// §7.3, §16.1. F-7.3.10.
func (m *Metrics) IncSessionResumeAttempt(pool, outcome string) {
	if m == nil {
		return
	}
	m.sessionResumeAttempts.WithLabelValues(pool, outcome).Inc()
}

// IncWarmpoolWarmupFailure increments the §16.1 line 124
// `lenny_warmpool_warmup_failure_total{error_type}` counter for one warm-
// pool startup failure. error_type is the §7.3 line 387 non-retryable
// failure category the gateway classified (`setup_command_failed`,
// `workspace_plan_invalid`, `runtime_image_pull_failed`,
// `network_policy_denied`). spec: §16.1 line 124, §7.3 line 387 — F-7.5.9.
func (m *Metrics) IncWarmpoolWarmupFailure(errorType string) {
	if m == nil {
		return
	}
	m.warmpoolWarmupFailure.WithLabelValues(errorType).Inc()
}

// ObserveWorkspaceSealDuration records the §7.1 line 112
// `lenny_workspace_seal_duration_seconds{pool,outcome}` histogram for one
// terminal session's seal-and-export. outcome is "success" when the seal
// completed or "timeout" when the retry window was exhausted. The §16.5
// WorkspaceSealStuck alert fires on a nonzero outcome="timeout" count.
// spec: §7.1 line 112.
func (m *Metrics) ObserveWorkspaceSealDuration(pool, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.workspaceSealDuration.WithLabelValues(pool, outcome).Observe(seconds)
}

// IncCheckpointStorageFailure increments the §4.4 line 262
// `lenny_checkpoint_storage_failure_total` counter labeled by pool,
// level, and trigger. Called from the non-eviction MinIO-upload path
// when all retries are exhausted and the failed checkpoint is
// discarded. The fourth (`reason`) label is stamped
// `retry_exhausted` so the wider counter rolls up consistently with
// the §12.5 ll. 303 kms_unavailable rejection counted by
// IncCheckpointKMSUnavailable below.
// spec: §4.4 line 262.
func (m *Metrics) IncCheckpointStorageFailure(pool, level, trigger string) {
	if m == nil {
		return
	}
	m.checkpointStorageFailure.WithLabelValues(pool, level, trigger, "retry_exhausted").Inc()
}

// IncCheckpointKMSUnavailable increments the §12.5 ll. 303
// `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`
// counter on a T4 fail-closed write rejection. The MinIO blobstore
// fires its SetOnKMSUnavailable callback whenever a Put rejects under
// ErrClassificationControlViolation; the gateway main wires the
// callback to this method so every rejection drives the
// CheckpointStorageUnavailable alert. The pool / level / trigger
// labels are left empty because the rejection happens before the
// adapter has the wider context.
//
// spec: §12.5 ll. 303.
func (m *Metrics) IncCheckpointKMSUnavailable() {
	if m == nil {
		return
	}
	m.checkpointStorageFailure.WithLabelValues("", "", "", "kms_unavailable").Inc()
}

// AddGCTombstonesPruned bumps the §12.5 ll. 341
// `lenny_gc_tombstones_pruned_total{table}` counter by n. The §12.5
// hard-prune sweep emits the count of rows it removed for each table it
// sweeps; passing it through the gateway-side accessor keeps the metric
// registration centralised. table is `artifact_store` or
// `partial_manifest`.
//
// spec: §12.5 ll. 341.
func (m *Metrics) AddGCTombstonesPruned(table string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.gcTombstonesPruned.WithLabelValues(table).Add(float64(n))
}

// IncGCRun bumps the §12.5 ll. 321 `lenny_gc_runs_total` counter once
// per retention-GC sweep. outcome is `success` or `error`.
//
// spec: §12.5 line 321.
func (m *Metrics) IncGCRun(outcome string) {
	if m == nil {
		return
	}
	m.gcRuns.WithLabelValues(outcome).Inc()
}

// AddGCArtifactsDeleted bumps the §12.5 ll. 321
// `lenny_gc_artifacts_deleted` counter by n for the named per-store
// adapter. Called once per sweep per per-store result.
//
// spec: §12.5 line 321.
func (m *Metrics) AddGCArtifactsDeleted(store string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.gcArtifactsDeleted.WithLabelValues(store).Add(float64(n))
}

// IncGCError bumps the §12.5 ll. 321 `lenny_gc_errors_total` counter
// once per per-store adapter error observed in a sweep.
//
// spec: §12.5 line 321.
func (m *Metrics) IncGCError(store string) {
	if m == nil {
		return
	}
	m.gcErrors.WithLabelValues(store).Inc()
}

// ObserveGCDuration records the §12.5 ll. 321 retention-GC sweep
// duration. Called once per Tick regardless of outcome.
//
// spec: §12.5 line 321.
func (m *Metrics) ObserveGCDuration(seconds float64) {
	if m == nil {
		return
	}
	m.gcDuration.Observe(seconds)
}

// IncDrainReadinessCheck bumps the §12.5 ll. 291
// `lenny_drain_readiness_checks_total` counter once per webhook
// decision. outcome is `allowed`, `blocked`, or `forced`.
//
// spec: §12.5 line 291.
func (m *Metrics) IncDrainReadinessCheck(outcome string) {
	if m == nil {
		return
	}
	m.drainReadinessChecks.WithLabelValues(outcome).Inc()
}

// IncLegalHoldCheckpointGap bumps the §12.8 ll. 739
// `lenny_legal_hold_checkpoint_gaps_total` counter once per held
// session whose checkpoint sequence carries a gap.
//
// spec: §12.8 line 739.
func (m *Metrics) IncLegalHoldCheckpointGap(tenantID string) {
	if m == nil {
		return
	}
	m.legalHoldCheckpointGaps.WithLabelValues(tenantID).Inc()
}

// IncArtifactUploadError bumps the §12.5 ll. 282
// `lenny_artifact_upload_error_total` counter on every
// ArtifactStore PUT that exhausts its retry budget (the storage
// backend's onArtifactUploadError callback). errorType is one of
// `minio_unreachable`, `auth`, `quota_exceeded`, `other`, matching
// the §16.5 alert's selector vocabulary; the
// `minio_unreachable` value also drives the
// `lenny_checkpoint_storage_failure_total{reason="minio_unreachable"}`
// rollup so the CheckpointStorageUnavailable alert fires from the
// same signal.
//
// spec: §12.5 line 282; §16.5 ArtifactUploadError.
func (m *Metrics) IncArtifactUploadError(tenantID, errorType string) {
	if m == nil {
		return
	}
	m.artifactUploadError.WithLabelValues(tenantID, errorType).Inc()
	// Roll the upload-error class into the checkpoint storage
	// failure rollup so a single counter feeds both alerts. The pool/
	// level/trigger labels are blank since the failure originates in
	// the blob store before the higher-level context is available.
	m.checkpointStorageFailure.WithLabelValues("", "", "", errorType).Inc()
}

// IncRateLimitRejected increments the §11.1 line 7
// `lenny_rate_limit_rejected_total` counter for one 429 admission
// rejection. scope is `global` or `user`. spec: §11.1 line 7.
func (m *Metrics) IncRateLimitRejected(scope string) {
	if m == nil {
		return
	}
	m.rateLimitRejected.WithLabelValues(scope).Inc()
}

// SetRateLimitFailopenActive flips the §16.5 RateLimitDegraded source
// gauge. The ratelimit middleware sets 1 once a counter error is
// observed and 0 once the next Incr succeeds, so the alert reflects
// the live degraded state. spec: §16.5 RateLimitDegraded; §11.1 line 7.
func (m *Metrics) SetRateLimitFailopenActive(active bool) {
	if m == nil {
		return
	}
	v := 0.0
	if active {
		v = 1
	}
	m.rateLimitFailopenActive.Set(v)
}

// IncRateLimitCounterFailure increments the §11.1
// `lenny_rate_limit_counter_failure_total` counter. The middleware
// calls this on every Incr error so an operator can rate-aggregate
// counter outages even when the failopen-active gauge is pinned to 1.
// spec: §11.1 line 7.
func (m *Metrics) IncRateLimitCounterFailure() {
	if m == nil {
		return
	}
	m.rateLimitCounterFailure.Inc()
}

// SetDualStoreUnavailable flips the §10.1 line 45 DualStoreUnavailable
// source gauge. The dual-store monitor sets 1 the moment it declares
// both Postgres and Redis unreachable and clears to 0 once at least one
// store recovers, so the §16.5 alert reflects the live degraded state.
// spec: §10.1 line 45.
func (m *Metrics) SetDualStoreUnavailable(unavailable bool) {
	if m == nil {
		return
	}
	v := 0.0
	if unavailable {
		v = 1
	}
	m.dualStoreUnavailable.Set(v)
}

// IncIdempotencyCacheWriteFailure increments
// `lenny_idempotency_cache_write_failures_total{tenant_id}` once per
// §11.5 cache Put failure. The middleware calls this when the inner
// handler already executed (the client already got the response) and
// the durable store rejected the cache row, so the next retry with the
// same key WILL re-execute the operation. spec: §11.5 line 277;
// F-11.5.4.
func (m *Metrics) IncIdempotencyCacheWriteFailure(tenantID string) {
	if m == nil {
		return
	}
	m.idempotencyCacheWriteFailures.WithLabelValues(tenantID).Inc()
}

// IncIdempotencyCacheSkipped increments
// `lenny_idempotency_cache_skipped_total{tenant_id,reason}` once per
// §11.5 cache write the middleware declined by policy. reason is one
// of: "server_error" (inner-handler 5xx). spec: §11.5 line 277;
// F-11.5.3.
func (m *Metrics) IncIdempotencyCacheSkipped(tenantID, reason string) {
	if m == nil {
		return
	}
	m.idempotencyCacheSkipped.WithLabelValues(tenantID, reason).Inc()
}

// ObserveDelegationDepth records a §8.2 delegation-admission depth
// observation onto the `lenny_delegation_depth` histogram labeled by
// `pool`. spec: §8.2; §16.1 line 27.
func (m *Metrics) ObserveDelegationDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.delegationDepth.WithLabelValues(pool).Observe(float64(depth))
}

// IncDelegationWouldHaveBlocked increments the §8.2 three-layer-gate
// `lenny_delegation_would_have_blocked_total` counter for one
// (`layer`, `mode`) attribution row. Callers emit one row per failing
// layer: under `mode: enforce` this attributes the rejection causes
// for a rejected hop; under `mode: warn` it records the
// would-have-blocked layers for an admitted hop. The counter is not
// emitted under `mode: permissive` (the caller decides; this helper
// is unconditional). spec: §8.2 line 70; §16.1 line 79.
func (m *Metrics) IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string) {
	if m == nil {
		return
	}
	m.delegationWouldHaveBlocked.WithLabelValues(pool, tenantID, layer, mode).Inc()
}

// IncDelegationTreeCycleDetected increments the §8.9
// `lenny_delegation_tree_cycle_detected_total` counter when a tree
// walker hits a cycle in the §8.2 ParentSessionID lineage. `source`
// is `rest` for the /v1/sessions/{id}/tree handler and `mcp` for the
// lenny/get_task_tree platform-tool and lenny/await_children walks.
// spec: §8.9 line 1003; F-8.9.10.
func (m *Metrics) IncDelegationTreeCycleDetected(tenantID, source string) {
	if m == nil {
		return
	}
	m.delegationTreeCycleDetected.WithLabelValues(tenantID, source).Inc()
}

// ObserveDelegationParallelChildrenHighWatermark records the §8.3 line
// 379 maximum simultaneous in-flight children for one delegation tree
// onto the `lenny_delegation_parallel_children_high_watermark`
// histogram, sampled once when the tree root settles. `pool` is the
// root session's assigned pool (empty when unresolved) and `tenant_id`
// scopes the observation. spec: §8.3 line 379; §16.1 line 73. F-8.9.6.
func (m *Metrics) ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64) {
	if m == nil {
		return
	}
	m.delegationParallelChildrenHWM.WithLabelValues(pool, tenantID).Observe(float64(value))
}

// IncCheckpointEvictionFallback increments the §4.4 line 263
// `lenny_checkpoint_eviction_fallback_total` counter labeled by pool
// and had_prior_checkpoint. Called from the eviction-fallback writer
// at the entry to the Postgres minimal-state write.
// spec: §4.4 line 263.
func (m *Metrics) IncCheckpointEvictionFallback(pool string, hadPriorCheckpoint bool) {
	if m == nil {
		return
	}
	label := "false"
	if hadPriorCheckpoint {
		label = "true"
	}
	m.checkpointEvictionFallback.WithLabelValues(pool, label).Inc()
}

// IncPodClaimFallbackSkipped increments the §4.6.1
// `lenny_pod_claim_fallback_skipped_total` counter for reason
// (`mirror_stale` or `apiserver_unreachable`). Called by the gateway
// pod binder when a fallback precondition fails.
// spec: §4.6.1 "Fallback preconditions (mirror freshness and admission
// reachability)".
func (m *Metrics) IncPodClaimFallbackSkipped(reason string) {
	if m == nil {
		return
	}
	m.podClaimFallbackSkipped.WithLabelValues(reason).Inc()
}

// IncSlotAssignmentConflict increments the §5.2 line 519
// `lenny_slot_assignment_conflict_total` counter for pool. Called by
// the gateway slot claimer when a concurrent-mode reservation found a
// candidate pod at its maxConcurrent bound.
// spec: §5.2 line 519 "atomic reservation failures due to slot contention".
func (m *Metrics) IncSlotAssignmentConflict(pool string) {
	if m == nil {
		return
	}
	m.slotAssignmentConflict.WithLabelValues(pool).Inc()
}

// IncCredentialPreclaimMismatch increments the §4.9 line 1220
// `lenny_credential_preclaim_mismatch_total` counter for the
// (pool, provider) pair. Called when the pre-claim credential
// availability check passed but the subsequent lease assignment failed,
// so the gateway released the claimed pod and returned
// CREDENTIAL_POOL_EXHAUSTED to the client.
// spec: §4.9 line 1220.
func (m *Metrics) IncCredentialPreclaimMismatch(pool, provider string) {
	if m == nil {
		return
	}
	m.credentialPreclaimMismatch.WithLabelValues(pool, provider).Inc()
}

// IncCredentialLeaseAssignment increments the §16.1
// `lenny_credential_lease_assignments_total` counter for the
// (provider, pool, source) tuple. Called by the credential-assignment
// service after each lease is minted and recorded; in v1 source is
// always `primary` (the §4.9 fallback chain is the source of `fallback`
// and the semantic cache the source of `cached`).
// spec: §16.1 line 51.
func (m *Metrics) IncCredentialLeaseAssignment(provider, pool, source string) {
	if m == nil {
		return
	}
	m.credentialLeaseAssignments.WithLabelValues(provider, pool, source).Inc()
}

// IncCredentialRotation increments the §16.1
// `lenny_credential_rotation_total` counter for errorType. Called by the
// §4.9 LLM-proxy Fallback Flow each time a faulted lease is rotated to
// the chain's next pool.
// spec: §16.1 line 118.
func (m *Metrics) IncCredentialRotation(errorType string) {
	if m == nil {
		return
	}
	m.credentialRotation.WithLabelValues(errorType).Inc()
}

// IncCredentialFallbackExhausted increments the §4.9
// `lenny_gateway_credential_fallback_exhausted_total` counter for the
// (pool, provider, errorType) tuple. Called when the fallback chain is
// exhausted and the session is terminated with
// CREDENTIAL_FALLBACK_EXHAUSTED.
// spec: §4.9 line 1395.
func (m *Metrics) IncCredentialFallbackExhausted(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.credentialFallbackExhausted.WithLabelValues(pool, provider, errorType).Inc()
}

// ObserveCredentialLeaseDuration records the §16.1
// `lenny_credential_lease_duration_seconds` histogram for a lease's
// wall-clock duration from assignment to release. Called by the
// credential-assignment service on Release.
// spec: §16.1 line 55.
func (m *Metrics) ObserveCredentialLeaseDuration(provider, pool string, seconds float64) {
	if m == nil {
		return
	}
	m.credentialLeaseDuration.WithLabelValues(provider, pool).Observe(seconds)
}

// SetCredentialPoolUtilization sets the §16.1
// `lenny_credential_pool_utilization` gauge for pool to ratio (in
// [0,1]). Called by the credential-assignment service after each assign
// or release recomputes in-use credentials over the pool size.
// spec: §16.1 line 53.
func (m *Metrics) SetCredentialPoolUtilization(pool string, ratio float64) {
	if m == nil {
		return
	}
	m.credentialPoolUtilization.WithLabelValues(pool).Set(ratio)
}

// IncLLMProxyConnections / DecLLMProxyConnections move the §16.1
// `lenny_gateway_llm_proxy_active_connections` gauge. The proxy handler
// increments on request entry and decrements on exit so the gauge
// reflects in-flight requests on the replica.
// spec: §16.1 line 97.
func (m *Metrics) IncLLMProxyConnections() {
	if m == nil {
		return
	}
	m.llmProxyActiveConnections.Inc()
}

// DecLLMProxyConnections decrements the §16.1 LLM-proxy active-connection
// gauge.
// spec: §16.1 line 97.
func (m *Metrics) DecLLMProxyConnections() {
	if m == nil {
		return
	}
	m.llmProxyActiveConnections.Dec()
}

// ObserveLLMTranslation records the §16.1
// `lenny_gateway_llm_translation_duration_seconds` histogram for one
// translator leg. direction is `request` or `response`. Called by the
// proxy handler around each TranslateRequest / TranslateResponse call.
// spec: §16.1 line 99.
func (m *Metrics) ObserveLLMTranslation(pool, provider, proxyDialect, direction string, seconds float64) {
	if m == nil {
		return
	}
	m.llmTranslationDuration.WithLabelValues(pool, provider, proxyDialect, direction).Observe(seconds)
}

// IncLLMTranslationError increments the §16.1
// `lenny_gateway_llm_translation_errors_total` counter for the
// (pool, provider, error_type) tuple. errorType is the §4.9 translator
// taxonomy value carried by a *llmproxy.TranslationError.
// spec: §16.1 line 100.
func (m *Metrics) IncLLMTranslationError(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.llmTranslationErrors.WithLabelValues(pool, provider, errorType).Inc()
}

// IncSlotFailure increments the §5.2 line 12 `lenny_slot_failure_total`
// counter for the (errorType, pool, podName) tuple. Called by the
// concurrent-mode slot binder when a slot bind stage failed after the
// slot was reserved.
// spec: §5.2 line 12.
func (m *Metrics) IncSlotFailure(errorType, pool, podName string) {
	if m == nil {
		return
	}
	m.slotFailure.WithLabelValues(errorType, pool, podName).Inc()
}

// IncSlotRehydration increments the §5.2 line 521
// `lenny_slot_rehydration_total` counter for the (pod, pool) pair.
// Called by the concurrent-mode slot claimer when a pod's slot counter
// was seeded from Postgres after a Redis restart — once per pod per
// Redis restart.
// spec: §5.2 line 521 ("lenny_slot_rehydration_total counter (labeled
// by pod, pool) is emitted on each rehydration event").
func (m *Metrics) IncSlotRehydration(pod, pool string) {
	if m == nil {
		return
	}
	m.slotRehydration.WithLabelValues(pod, pool).Inc()
}

// IncSlotPodReplacement increments the §5.2 `lenny_slot_pod_replacement_total`
// counter for pool. Called by the concurrent-workspace slot retry policy
// when a pod is marked unhealthy and drained because ceil(maxConcurrent/2)
// or more of its slots failed or leaked within the rolling 5-minute window.
// spec: §5.2 "whole-pod replacement trigger".
func (m *Metrics) IncSlotPodReplacement(pool string) {
	if m == nil {
		return
	}
	m.slotPodReplacement.WithLabelValues(pool).Inc()
}

// IncCheckpointPartial increments the §4.4 line 234
// `lenny_checkpoint_partial_total` counter labeled by pool. Called
// once per partial-manifest row written by the §10.1
// chunk-upload pipeline.
// spec: §4.4 line 234.
func (m *Metrics) IncCheckpointPartial(pool string) {
	if m == nil {
		return
	}
	m.checkpointPartialTotal.WithLabelValues(pool).Inc()
}

// IncPreStopCapSelection increments the §10.1
// `lenny_prestop_cap_selection_total` counter labeled by pool,
// service_instance_id, and source. Called once per preStop Stage 2
// tier selection.
// spec: §10.1 — preStop tier selection source.
func (m *Metrics) IncPreStopCapSelection(pool, serviceInstanceID, source string) {
	if m == nil {
		return
	}
	m.prestopCapSelection.WithLabelValues(pool, serviceInstanceID, source).Inc()
}

// IncSigkillStreams increments the §10.1 line 161
// `lenny_gateway_sigkill_streams_total` counter for one in-flight
// stream the kubelet SIGKILLs at the grace deadline because its
// eviction checkpoint did not finish in budget. Called once per
// deadline-exceeded session during the preStop staged drain.
// spec: §10.1 line 161 — SIGKILL-deadline stream counter.
func (m *Metrics) IncSigkillStreams(pool, serviceInstanceID string) {
	if m == nil {
		return
	}
	m.sigkillStreams.WithLabelValues(pool, serviceInstanceID).Inc()
}

// SetCheckpointStaleSessions sets the per-pool/level
// `lenny_checkpoint_stale_sessions` gauge value. The freshness reaper
// calls this once per sweep with the count of active sessions whose
// `last_successful_checkpoint_at` is older than
// `periodicCheckpointIntervalSeconds`. The §16.5 `CheckpointStale`
// alert keys on the per-label value.
// spec: §4.4 line 256.
func (m *Metrics) SetCheckpointStaleSessions(pool, level string, count int) {
	if m == nil {
		return
	}
	m.checkpointStaleSessions.WithLabelValues(pool, level).Set(float64(count))
}

// RecordKMSSigningError increments the §10.2 line 225
// `lenny_gateway_kms_signing_errors_total{reason}` counter. The
// `reason` label distinguishes a downstream KMS failure (`inner`) from
// a breaker short-circuit (`rejected`). F-10.2.6.
func (m *Metrics) RecordKMSSigningError(reason string) {
	if m == nil {
		return
	}
	m.kmsSigningErrors.WithLabelValues(reason).Inc()
}

// SetKMSSigningCircuitState publishes the §10.2 line 225 JWTSigner
// breaker state to `lenny_gateway_kms_signing_circuit_state`. The
// §16.5 KMSSigningUnavailable alert fires on the error-rate counter
// rather than the gauge, but the gauge is useful for dashboards.
// F-10.2.6.
func (m *Metrics) SetKMSSigningCircuitState(value int) {
	if m == nil {
		return
	}
	m.kmsSigningCircuitState.Set(float64(value))
}

// SetTokenServiceCircuitState updates the §4.3 / §4.1
// lenny_token_service_circuit_state gauge. The §16.5
// TokenServiceUnavailable alert fires when the value equals 2 (open).
// spec: §4.3 line 211.
func (m *Metrics) SetTokenServiceCircuitState(value int) {
	if m == nil {
		return
	}
	m.tokenServiceCircuitState.Set(float64(value))
}

// SetMaxOrphanTasksPerTenant publishes the deployer-configured §8.10
// line 1103 orphan-cap as the `lenny_max_orphan_tasks_per_tenant`
// scalar gauge so the §16.5 OrphanTasksPerTenantHigh alert
// (`lenny_orphan_tasks_active_per_tenant > 0.80 *
// scalar(lenny_max_orphan_tasks_per_tenant)`) reads the live value.
// Called once at gateway startup and again on a config-reload.
// F-8.10.13.
func (m *Metrics) SetMaxOrphanTasksPerTenant(value int) {
	if m == nil {
		return
	}
	m.maxOrphanTasksPerTenant.Set(float64(value))
}

// IncOrphanCleanupRun increments the §8.10 line 1091 / §16.1 line 146
// `lenny_orphan_cleanup_runs_total` counter — one tick per sweep
// invocation regardless of outcome. spec: §8.10 line 1091; F-8.10.7.
func (m *Metrics) IncOrphanCleanupRun() {
	if m == nil {
		return
	}
	m.orphanCleanupRuns.Inc()
}

// AddOrphanTasksTerminated bumps the §8.10 / §16.1 line 147
// `lenny_orphan_tasks_terminated` counter by the per-sweep terminated
// count (the cleanup tick's return value). A zero-count sweep is a no-op.
// spec: §8.10 / §16.1 line 147; F-8.10.7.
func (m *Metrics) AddOrphanTasksTerminated(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.orphanTasksTerminated.Add(float64(n))
}

// SetOrphanTasksActive publishes the fleet-wide active orphan count as
// `lenny_orphan_tasks_active` per §8.10 / §16.1 line 148. Operators
// alert when the gauge exceeds a deployment-specific threshold
// (suggested 50 per §8.10 line 1101). spec: §16.1 line 148; F-8.10.7.
func (m *Metrics) SetOrphanTasksActive(value int) {
	if m == nil {
		return
	}
	m.orphanTasksActive.Set(float64(value))
}

// SetOrphanTasksActivePerTenant publishes the per-tenant active orphan
// count as `lenny_orphan_tasks_active_per_tenant{tenant_id}`. The §16.5
// OrphanTasksPerTenantHigh alert reads `value > 0.80 *
// scalar(lenny_max_orphan_tasks_per_tenant)`. The cleanup sweep calls
// this for every tenant on every Tick so a tenant whose orphan count
// drops to zero re-publishes a zero value. spec: §8.10 line 1103;
// §16.1 line 149; F-8.10.7.
func (m *Metrics) SetOrphanTasksActivePerTenant(tenantID string, value int) {
	if m == nil {
		return
	}
	m.orphanTasksActivePerTenant.WithLabelValues(tenantID).Set(float64(value))
}

// ObserveTreeRecoveryDuration records one wall-clock duration on the
// §8.10 / §16.1 line 144 `lenny_delegation_tree_recovery_duration_seconds`
// histogram. `pool` is the root session's pool; `outcome` is one of
// `full_success`, `partial_failure`, or `total_timeout`. spec: §8.10 /
// §16.1 line 144; F-8.10.7.
func (m *Metrics) ObserveTreeRecoveryDuration(pool, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.treeRecoveryDuration.WithLabelValues(pool, outcome).Observe(seconds)
}

// IncTreeRecoveryTimeout increments the §8.10 / §16.1 line 145
// `lenny_delegation_tree_recovery_timeout_total{pool, timeout_type}`
// counter. `timeout_type` is `level` (one tree-level budget exhausted)
// or `tree` (the whole-tree budget exhausted). spec: §8.10 / §16.1 line
// 145; F-8.10.7.
func (m *Metrics) IncTreeRecoveryTimeout(pool, timeoutType string) {
	if m == nil {
		return
	}
	m.treeRecoveryTimeout.WithLabelValues(pool, timeoutType).Inc()
}

// RecordElicitationDrop increments the §9.1 lenny_elicitation_dropped_total
// counter for the given drop reason (for example `budget_exceeded`).
func (m *Metrics) RecordElicitationDrop(reason string) {
	m.elicitationDropped.WithLabelValues(reason).Inc()
}

// RecordElicitationContentTamperDetected increments the §9.2 /
// §16.5 lenny_elicitation_content_tamper_detected_total counter
// when the §9.2 chain walk catches a forwarding hop that mutated
// the elicitation payload. Labelled by origin_pod (the pod that
// legitimately originated the elicitation), tampering_pod (the
// forwarding pod whose re-emission diverged), and enforcement_mode
// so the §16.5 ElicitationContentTamperDetected alert (which matches
// enforcement_mode="enforce") fires only when a tamper caused a hard
// drop; detect-only catches still bump the metric for visibility
// without firing the critical alert. spec: §16.1 line 64; §9.2 line
// 60. F-9.2.4.
func (m *Metrics) RecordElicitationContentTamperDetected(originPod, tamperingPod, enforcementMode string) {
	m.elicitationTamperDetected.WithLabelValues(originPod, tamperingPod, enforcementMode).Inc()
}

// SetElicitationIntegrityWeakened publishes the §16.5 line 460
// standing-alert gauge: the count of active tenants whose §9.2
// effective elicitation content-integrity mode is weaker than
// enforce. The gateway reconciliation loop calls this on every poll
// so the ElicitationContentIntegrityWeakened alert fires while any
// tenant is weakened and resolves once the count returns to zero.
// spec: §16.5 line 460. F-9.2.5.
func (m *Metrics) SetElicitationIntegrityWeakened(weakenedTenants int) {
	m.elicitationIntegrityWeakened.Set(float64(weakenedTenants))
}

// IncElicitationPending increments the §16.1 line 61
// `lenny_elicitation_pending` in-flight gauge. The §16.5
// ElicitationBacklogHigh alert reads `> 50 for 30s`. spec: §16.1 line 61.
// F-9.2.14.
func (m *Metrics) IncElicitationPending() {
	if m == nil {
		return
	}
	m.elicitationPending.Inc()
}

// DecElicitationPending decrements the §16.1 line 61 gauge on every
// terminal phase (responded | dismissed | timeout). spec: §16.1
// line 61. F-9.2.14.
func (m *Metrics) DecElicitationPending() {
	if m == nil {
		return
	}
	m.elicitationPending.Dec()
}

// IncElicitationTimeout increments the §16.1 line 63
// `lenny_elicitation_timeout_total` counter when the dispatcher
// drops a pending elicitation on the §9.1 maxElicitationWait
// deadline. spec: §16.1 line 63. F-9.2.14.
func (m *Metrics) IncElicitationTimeout() {
	if m == nil {
		return
	}
	m.elicitationTimeout.Inc()
}

// IncElicitationSuppressed increments the §16.1 line 62
// `lenny_elicitation_suppressed_total` counter for a §9.2 depth-policy
// suppression or a §9.1 per-session budget rejection. spec: §16.1
// line 62. F-9.2.14.
func (m *Metrics) IncElicitationSuppressed() {
	if m == nil {
		return
	}
	m.elicitationSuppressed.Inc()
}

// ObserveElicitationRoundtrip records the §16.1 line 60
// `lenny_elicitation_roundtrip_seconds` admit-to-terminal latency.
// spec: §16.1 line 60. F-9.2.14.
func (m *Metrics) ObserveElicitationRoundtrip(d time.Duration) {
	if m == nil {
		return
	}
	m.elicitationRoundtripSeconds.Observe(d.Seconds())
}

// RecordExperimentIsolationRejection increments the §16.1
// lenny_experiment_isolation_rejections_total counter when the §10.7
// ExperimentRouter fails a session closed because the variant pool's
// isolation profile is weaker than the session's.
func (m *Metrics) RecordExperimentIsolationRejection(tenantID, experimentID, variantID string) {
	m.experimentIsoRej.WithLabelValues(tenantID, experimentID, variantID).Inc()
}

// ObserveExperimentTargetingDuration records one §10.7 external
// experiment targeting evaluation against the §16.1 line 156
// lenny_experiment_targeting_duration_seconds histogram. provider is the
// OpenFeature provider name, or the OFREP endpoint hostname when
// provider:ofrep.
func (m *Metrics) ObserveExperimentTargetingDuration(provider string, seconds float64) {
	m.experimentTargetingDur.WithLabelValues(provider).Observe(seconds)
}

// RecordExperimentTargetingError increments the §16.1 line 157
// lenny_experiment_targeting_error_total counter on a §10.7
// targeting_failed condition. errorType classifies the failure cause
// (timeout, transport, or the OFREP errorCode).
func (m *Metrics) RecordExperimentTargetingError(provider, errorType string) {
	m.experimentTargetingErr.WithLabelValues(provider, errorType).Inc()
}

// SetExperimentTargetingCircuitOpen sets the §10.7 SCL-023 targeting
// circuit-breaker gauge for (tenant, provider): true → 1 (open, the
// gateway skips the OpenFeature call), false → 0 (closed). spec: §16.1
// line 64, §10.7 line 838.
func (m *Metrics) SetExperimentTargetingCircuitOpen(tenantID, provider string, open bool) {
	v := 0.0
	if open {
		v = 1.0
	}
	m.experimentTargetingCircuit.WithLabelValues(tenantID, provider).Set(v)
}

// RecordSessionTerminal records the §16.1 lines 161-163 / §10.7
// rollback-trigger session metrics at a terminal session transition: it
// increments lenny_session_total, increments lenny_session_error_total when
// the terminal state is an error outcome, and observes the per-session
// wall-clock duration on lenny_session_duration_seconds. sessionType is the
// §5.2 ExecutionMode; variantID is the §10.7 enrollment ("" for
// control / un-enrolled).
func (m *Metrics) RecordSessionTerminal(tenantID, sessionType, variantID string, isError bool, seconds float64) {
	if sessionType == "" {
		sessionType = "session"
	}
	m.sessionTotal.WithLabelValues(tenantID, sessionType, variantID).Inc()
	if isError {
		m.sessionError.WithLabelValues(tenantID, sessionType, variantID).Inc()
	}
	m.sessionDuration.WithLabelValues(tenantID, sessionType, variantID).Observe(seconds)
}

// ObserveEvalScore records one §16.1 line 164 lenny_eval_score observation
// per submitted eval run. variantID is the §10.7 enrollment ("" when the
// scored session was not enrolled).
func (m *Metrics) ObserveEvalScore(tenantID, scorer, variantID string, score float64) {
	m.evalScore.WithLabelValues(tenantID, scorer, variantID).Observe(score)
}

// IncErasureJobFailed increments lenny_erasure_job_failed_total for a
// failed §12.8 user-level erasure job. failurePhase is the §12.8 CMP-026
// phase label (store_delete, pseudonymization, verification, or
// memory_store_preflight). The §16.5 ErasureJobFailed alert fires on any
// increase. spec: §12.8 CMP-026 / §16.1 line 262.
func (m *Metrics) IncErasureJobFailed(tenantID, failurePhase string) {
	m.erasureJobFailed.WithLabelValues(tenantID, failurePhase).Inc()
}

// RecordNoEnvironmentPolicyAllowAll increments the §10.6
// lenny_noenvironmentpolicy_allowall_total counter when a tenant's
// rbac-config is written with noEnvironmentPolicy set to allow-all.
func (m *Metrics) RecordNoEnvironmentPolicyAllowAll(tenantID string) {
	m.noEnvPolicyAllowAll.WithLabelValues(tenantID).Inc()
}

// SetCircuitBreakerOpen updates the §16.1 per-breaker open gauge: 1
// when the named breaker is open, 0 when closed.
func (m *Metrics) SetCircuitBreakerOpen(name string, open bool) {
	v := 0.0
	if open {
		v = 1
	}
	m.circuitBreakerOpen.WithLabelValues(name).Set(v)
}

// RecordCircuitBreakerRejection increments the §11.6
// lenny_circuit_breaker_rejections_total counter for one admission
// rejection caused by a tripped breaker. Every rejection is counted,
// including those whose audit row is elided by sampling.
func (m *Metrics) RecordCircuitBreakerRejection(tenantID, circuitName, limitTier string) {
	m.cbRejections.WithLabelValues(tenantID, circuitName, limitTier).Inc()
}

// RecordCircuitBreakerRejectionSuppressed increments the §11.6
// lenny_circuit_breaker_rejections_suppressed_total counter for a
// rejection whose audit row was elided by the per-(tenant_id,
// circuit_name, caller_sub) 10-second sampling window.
func (m *Metrics) RecordCircuitBreakerRejectionSuppressed(tenantID, circuitName, limitTier string) {
	m.cbRejectionsSuppressed.WithLabelValues(tenantID, circuitName, limitTier).Inc()
}

// SetCircuitBreakerCache updates the §16.1 circuit-breaker cache
// gauges: the seconds since the last refresh and whether the cache has
// completed its first refresh.
func (m *Metrics) SetCircuitBreakerCache(staleSeconds float64, initialized bool) {
	m.cbCacheStale.Set(staleSeconds)
	if initialized {
		m.cbCacheInitialized.Set(1)
	} else {
		m.cbCacheInitialized.Set(0)
	}
}

// SetStorageQuota updates the §16.1 per-tenant storage-quota gauges:
// the bytes currently reserved-plus-committed and the tenant's
// configured quota. These drive the §16.5 StorageQuotaHigh alert.
func (m *Metrics) SetStorageQuota(tenantID string, used, limit int64) {
	m.storageQuotaUsed.WithLabelValues(tenantID).Set(float64(used))
	m.storageQuotaLimit.WithLabelValues(tenantID).Set(float64(limit))
}

// Handler returns the Prometheus `/metrics` scrape handler over the
// gateway's private registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Registerer exposes the gateway's private metric registry so a
// gateway subsystem (for example the §27 playground) can register its
// own metric vectors against the same registry and have them surface
// on the shared `/metrics` scrape target.
func (m *Metrics) Registerer() prometheus.Registerer {
	return m.reg
}

// SetActiveSessions updates the active-session gauge. The gateway
// calls this from the watchdog sweep or a dedicated poller.
func (m *Metrics) SetActiveSessions(n int) {
	m.activeSessions.Set(float64(n))
}

// SetActiveStreams updates the §4.1 lenny_gateway_active_streams gauge,
// the secondary HPA metric (SCL-026): the count of in-flight streaming
// connections on this replica.
func (m *Metrics) SetActiveStreams(n int) {
	m.activeStreams.Set(float64(n))
}

// SetRequestQueueDepth updates the §4.1 lenny_gateway_request_queue_depth
// gauge, the primary HPA scale-out trigger (SCL-026): the number of
// requests queued on this replica awaiting a handler goroutine.
func (m *Metrics) SetRequestQueueDepth(n int) {
	m.requestQueueDepth.Set(float64(n))
}

// SetRejectionRate updates the §10.1 lenny_gateway_rejection_rate gauge,
// the second leading-indicator metric: requests rejected with 429/503
// per second on this replica.
func (m *Metrics) SetRejectionRate(perSecond float64) {
	m.rejectionRate.Set(perSecond)
}

// SetMaxSessionsPerReplica emits the §4.1 capacity ceiling on the
// lenny_gateway_max_sessions_per_replica gauge for the given
// delivery_mode ("proxy" or "direct"). The §16.5
// GatewaySessionBudgetNearExhaustion alert reads this value as the
// denominator of `lenny_gateway_active_sessions / value > 0.90`. The
// gateway calls this once at startup so the gauge is observable as
// soon as the /metrics endpoint serves the first scrape; the spec
// requires both delivery_mode values be reported per replica so a
// capacity-planning dashboard can compute the proxy/direct ratio.
func (m *Metrics) SetMaxSessionsPerReplica(deliveryMode string, value int) {
	m.maxSessionsPerReplica.WithLabelValues(deliveryMode).Set(float64(value))
}

// SetMinReplicas emits the §4.1 / §16.5 lenny_gateway_min_replicas
// gauge. The value is the configured HPA minReplicas floor (per the
// §17.8.2 SCL-036 burst-absorption formula); the
// `GatewayNoHealthyReplicas` alert in pkg/alerting/rules/rules.go
// reads it via scalar(lenny_gateway_min_replicas) and fires when the
// fleet-wide ready replica count drops below this floor.
func (m *Metrics) SetMinReplicas(value int) {
	m.minReplicas.Set(float64(value))
}

// SetStreamCeiling emits the §4.1 / §16.5 lenny_gateway_stream_ceiling
// gauge. The value is the per-replica configured ceiling on
// simultaneous streaming connections; the `GatewayActiveStreamsHigh`
// alert in pkg/alerting/rules/rules.go reads it via
// scalar(lenny_gateway_stream_ceiling) and fires when
// lenny_gateway_active_streams exceeds 80% of the ceiling on any
// replica.
func (m *Metrics) SetStreamCeiling(value int) {
	m.streamCeiling.Set(float64(value))
}

// SetReplicaCount emits the §4.1 / §16.1
// lenny_gateway_replica_count gauge. v1 sets the gauge to 1 per
// replica at startup; the Prometheus recording rule
// `sum(lenny_gateway_replica_count)` then yields the fleet-wide ready
// replica count used by the `GatewayNoHealthyReplicas` alert numerator.
// Production deployments may swap this constant for a controller-managed
// value sourced from kube-state-metrics; the gateway still emits the
// per-replica constant so the alert keeps a non-NaN reading on
// installs without kube-state-metrics.
func (m *Metrics) SetReplicaCount(value int) {
	m.replicaCount.Set(float64(value))
}

// SetBillingCorrectionRateThreshold emits the §11.2.1 / §16.5
// lenny_billing_correction_rate_threshold gauge. The value is the
// deployer-configurable percentage (default 0.05 = 5%) of total billing
// events allowed in a rolling 24h window before
// `BillingCorrectionRateHigh` fires. The alert reads it via
// scalar(lenny_billing_correction_rate_threshold). F-11.2.23.
func (m *Metrics) SetBillingCorrectionRateThreshold(value float64) {
	m.billingCorrectionRateThreshold.Set(value)
}

// SetGatewayQueueDepthThreshold emits the §25.13 line 4737 / §16.5
// GatewayQueueDepthHigh ceiling. The alert reads it via
// scalar(lenny_gateway_queue_depth_threshold); tier-2/3 presets tighten
// the value via monitoring.alertThresholds.gatewayQueueDepthHigh.value.
// F-25.13.2.
func (m *Metrics) SetGatewayQueueDepthThreshold(value float64) {
	m.gatewayQueueDepthThreshold.Set(value)
}

// SetGatewayLatencyThresholdSeconds emits the §25.13 line 4737 / §16.5
// GatewayLatencyHigh p95 ceiling, in seconds. The alert reads it via
// scalar(lenny_gateway_latency_threshold_seconds). F-25.13.2.
func (m *Metrics) SetGatewayLatencyThresholdSeconds(value float64) {
	m.gatewayLatencyThresholdSeconds.Set(value)
}

// SetCredentialPoolLowThreshold emits the §25.13 line 4737 / §16.5
// CredentialPoolLow utilisation fraction. The alert reads it via
// scalar(lenny_credential_pool_low_threshold). F-25.13.2.
func (m *Metrics) SetCredentialPoolLowThreshold(value float64) {
	m.credentialPoolLowThreshold.Set(value)
}

// IncBillingFlushPressure advances the §12.3 line 76
// billing_flush_pressure counter. The failover Pipeline invokes it
// (via its OnFlushPressure callback) each time an Append leaves the
// Tier 2 write-ahead buffer over billingFlushMaxPending. F-12.3.13.
func (m *Metrics) IncBillingFlushPressure() {
	if m == nil {
		return
	}
	m.billingFlushPressure.Inc()
}

// SetPostgresWriteIops sets the §12.3 lines 115-125 sustained Postgres
// write-IOPS gauge. The periodic sampler computes the rate from
// pg_stat_database row-write deltas and pushes it here so the §16.5
// PostgresWriteSaturation alert can evaluate. F-12.3.7.
func (m *Metrics) SetPostgresWriteIops(iops float64) {
	if m == nil {
		return
	}
	m.postgresWriteIops.Set(iops)
}

// SetPostgresWriteCeilingIops emits the §12.3 line 123 configured
// postgres.writeCeilingIops ceiling so the PostgresWriteSaturation
// alert resolves scalar(lenny_postgres_write_ceiling_iops) to an
// operator-tunable denominator. F-12.3.8.
func (m *Metrics) SetPostgresWriteCeilingIops(iops float64) {
	if m == nil {
		return
	}
	m.postgresWriteCeilingIops.Set(iops)
}

// IncAuditChainIntegrity advances the §16.1
// lenny_audit_chain_integrity_total counter for one tenant's §11.7
// chain state. The §12.3 line 101 startup chain-continuity check calls
// it once per tenant; state="broken" drives the §16.5 AuditChainGap
// alert. F-12.3.9.
func (m *Metrics) IncAuditChainIntegrity(state string) {
	if m == nil {
		return
	}
	m.auditChainIntegrity.WithLabelValues(state).Inc()
}

// IncAuditGrantDrift advances the §11.7 item 2
// lenny_audit_grant_drift_total counter. The periodic background
// integrity check calls it when it detects a grant / trigger /
// erasure-guard drift after startup; the §16.5 AuditGrantDrift alert
// fires on `lenny_audit_grant_drift_total > 0`. F-11.7.3.
func (m *Metrics) IncAuditGrantDrift() {
	if m == nil {
		return
	}
	m.auditGrantDrift.Inc()
}

// IncAuditOCSFTranslationFailed advances the §16.1
// lenny_audit_ocsf_translation_failed_total counter for one per-row
// OCSF translation failure. The §11.7 translator's Metrics adapter
// calls it as the state machine advances the row toward retry_pending /
// dead_lettered; the (event_type, error_class) labels let operators
// localize an event-schema gap. F-11.7.1 / F-11.7.15.
func (m *Metrics) IncAuditOCSFTranslationFailed(eventType, errorClass string) {
	if m == nil {
		return
	}
	m.auditOCSFTranslationFailed.WithLabelValues(eventType, errorClass).Inc()
}

// SetExtractionThreshold emits the §4.1 configured per-subsystem
// extraction threshold on the lenny_gateway_extraction_threshold
// gauge. The subsystem label must be one of `stream_proxy`,
// `upload_handler`, `mcp_fabric`, `llm_proxy`; the metric label
// matches the `gateway.extractionThresholds.<subsystem>.<metric>`
// Helm key in snake_case form (e.g., `queue_depth`,
// `active_concurrent`). The gateway calls this once per configured
// threshold at startup so the values used for an extraction decision
// are auditable against /metrics.
func (m *Metrics) SetExtractionThreshold(subsystem, metric string, value float64) {
	m.extractionThreshold.WithLabelValues(subsystem, metric).Set(value)
}

// SetGCPauseP99Ms updates the §4.1 process-level GC pause p99 gauge
// (milliseconds). The periodic collector in cmd/lenny-gateway/main.go
// reads runtime/debug.ReadGCStats, computes the p99 over a sliding
// window, and pushes the value here so the §16.5 Tier3GCPressureHigh
// alert can evaluate.
func (m *Metrics) SetGCPauseP99Ms(value float64) {
	m.gcPauseP99Ms.Set(value)
}

// SetReplayBufferUtilization updates the §16 catalog
// lenny_event_bus_replay_buffer_utilization gauge. The caller samples
// the worst per-session ratio across the session SSE replay buffer
// periodically; ratio is in [0,1]. spec: §10.4 line 389. F-10.4.11.
func (m *Metrics) SetReplayBufferUtilization(ratio float64) {
	if m == nil {
		return
	}
	m.replayBufferUtilization.Set(ratio)
}

// IncPDBBlockedEvictions advances the §16 catalog
// lenny_pdb_blocked_evictions_total counter for a PDB observed as
// blocking by the periodic poller. spec: §10.4 / §16.5
// PDBBlockedEvictions. F-10.4.4.
func (m *Metrics) IncPDBBlockedEvictions(pdb, controller string) {
	if m == nil {
		return
	}
	m.pdbBlockedEvictions.WithLabelValues(pdb, controller).Inc()
}

// ObserveMemoryStoreOperation records one observation on the §9.4 /
// §16.1 line 151 lenny_memory_store_operation_duration_seconds
// histogram. The operation label is one of write, query, delete, list,
// delete_by_user, delete_by_tenant; backend is the implementation tag.
// F-9.4.1.
func (m *Metrics) ObserveMemoryStoreOperation(operation, backend string, seconds float64) {
	if m == nil {
		return
	}
	m.memoryStoreOperationDuration.WithLabelValues(operation, backend).Observe(seconds)
}

// IncMemoryStoreError advances the §9.4 / §16.1 line 152
// lenny_memory_store_errors_total counter. error_type is the caller's
// error classification. F-9.4.1.
func (m *Metrics) IncMemoryStoreError(operation, backend, errorType string) {
	if m == nil {
		return
	}
	m.memoryStoreErrors.WithLabelValues(operation, backend, errorType).Inc()
}

// SetMemoryStoreRecordCount updates the §9.4 / §16.1 line 153
// lenny_memory_store_record_count gauge for tenantID. The caller is the
// periodic sampler. F-9.4.1.
func (m *Metrics) SetMemoryStoreRecordCount(tenantID string, count int) {
	if m == nil {
		return
	}
	m.memoryStoreRecordCount.WithLabelValues(tenantID).Set(float64(count))
}

// IncMemoryStoreUserOverThreshold advances the §9.4 / §16.1 line 154
// lenny_memory_store_user_count_over_threshold_total counter. The
// MemoryStore.Write path increments it once per commit that leaves the
// writing user at >= 80% of memory.maxMemoriesPerUser. F-9.4.6.
func (m *Metrics) IncMemoryStoreUserOverThreshold(tenantID, backend string) {
	if m == nil {
		return
	}
	m.memoryStoreUserOverThreshold.WithLabelValues(tenantID, backend).Inc()
}

// SetTimeDrift publishes the §13.3 line 595 lenny_time_drift_seconds
// gauge. Driven by the pkg/driftmonitor sampler. Value is the signed
// offset in seconds (positive = ahead of NTP reference, negative =
// behind). The §16.5 GatewayClockDrift alert keys on
// `abs(lenny_time_drift_seconds) > 0.5`. F-13.3.5.
func (m *Metrics) SetTimeDrift(seconds float64) {
	if m == nil {
		return
	}
	m.timeDrift.Set(seconds)
}

// Middleware returns an http.Handler that records the §16.1 request
// metrics for every request to inner. The route label is taken from
// the supplied routeOf function so high-cardinality path segments
// (session ids, blob refs) collapse to a stable route template.
//
// The middleware also tracks the in-flight request count exposed via
// InflightRequests so the §16.1 lenny_gateway_request_queue_depth
// gauge — the §4.1 SCL-026 primary HPA scale-out trigger — reflects
// the instantaneous concurrent-request count on this replica.
func (m *Metrics) Middleware(inner http.Handler, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeOf(r)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		atomic.AddInt64(&m.inflight, 1)
		defer atomic.AddInt64(&m.inflight, -1)
		inner.ServeHTTP(rec, r)
		elapsed := time.Since(start).Seconds()

		m.requestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		m.requestDuration.WithLabelValues(r.Method, route).Observe(elapsed)
	})
}

// InflightRequests returns the number of HTTP requests currently
// being handled by the metrics Middleware. The watchdog poller in
// cmd/lenny-gateway/main.go reads this value and pushes it through
// SetRequestQueueDepth to surface it on the
// lenny_gateway_request_queue_depth gauge (§4.1 SCL-026).
func (m *Metrics) InflightRequests() int {
	return int(atomic.LoadInt64(&m.inflight))
}

// statusRecorder captures the response status code so the metrics
// middleware can label by status class.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying http.Flusher when the embedded
// ResponseWriter supports streaming. The §15.1 SSE event stream at
// GET /v1/sessions/{id}/events and the §4.9 LLM-proxy streaming
// translators rely on http.Flusher to push partial bytes to the
// client; without this forwarder the metrics middleware hides the
// Flusher and the SSE handler returns 500
// ("response writer does not support streaming"). The §16.1
// request-metrics accounting is unaffected because Write already
// passes through.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if !s.wroteHeader {
			s.wroteHeader = true
		}
		f.Flush()
	}
}

// statusClass collapses an HTTP status to its §16.1.1 low-cardinality
// class label (2xx, 3xx, 4xx, 5xx) so the metric does not explode
// into one series per status code.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code/100) + "xx"
	}
}
