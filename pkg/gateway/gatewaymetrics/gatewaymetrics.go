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

	requestsTotal             *prometheus.CounterVec
	requestDuration           *prometheus.HistogramVec
	activeSessions            prometheus.Gauge
	activeStreams             prometheus.Gauge
	requestQueueDepth         prometheus.Gauge
	rejectionRate             prometheus.Gauge
	maxSessionsPerReplica     *prometheus.GaugeVec
	minReplicas               prometheus.Gauge
	streamCeiling             prometheus.Gauge
	replicaCount              prometheus.Gauge
	extractionThreshold       *prometheus.GaugeVec
	storageQuotaUsed          *prometheus.GaugeVec
	storageQuotaLimit         *prometheus.GaugeVec
	circuitBreakerOpen        *prometheus.GaugeVec
	cbCacheStale              prometheus.Gauge
	cbCacheInitialized        prometheus.Gauge
	elicitationDropped        *prometheus.CounterVec
	elicitationTamperDetected *prometheus.CounterVec
	experimentIsoRej          *prometheus.CounterVec
	noEnvPolicyAllowAll       *prometheus.CounterVec
	gcPauseP99Ms              prometheus.Gauge
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
	// checkpointStorageFailure counts the §4.4 line 262 non-eviction
	// MinIO-upload failures (all retries exhausted, the failed
	// checkpoint is discarded). Labels: `pool`, `level`, and `trigger`.
	checkpointStorageFailure *prometheus.CounterVec
	// checkpointEvictionFallback counts the §4.4 line 263 eviction-
	// fallback writes to Postgres. Labels: `pool` and
	// `had_prior_checkpoint`.
	checkpointEvictionFallback *prometheus.CounterVec
	// checkpointPartialTotal counts the §4.4 line 234 / §10.1 partial-
	// manifest row writes. Labels: `pool` (finite, sandbox-warm-pool
	// registry).
	checkpointPartialTotal *prometheus.CounterVec
	// prestopCapSelection counts the §10.1 preStop tiered-cap
	// selection by source. Labels: `pool`, `service_instance_id`,
	// and `source` (postgres | postgres_null | cache_hit |
	// cache_miss_max_tier).
	prestopCapSelection *prometheus.CounterVec
	// gcTombstonesPruned counts the §12.5 ll. 341 hard-prune
	// removals: catalog rows whose tombstone deadline has elapsed and
	// were physically removed from the artifact_store table together
	// with the matching bucket object. No labels — the counter rolls up
	// across all tenants and artifact classes because the §12.5
	// monitoring contract treats hard-prune as a single rollup metric.
	gcTombstonesPruned prometheus.Counter
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
	elicitationTamperDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_content_tamper_detected_total",
		Help: "Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by tenant and enforcement_mode (off | detect-only | enforce) so the §16.5 alert can fire on enforce-mode catches only.",
	}, []string{"tenant_id", "enforcement_mode"})
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
	noEnvPolicyAllowAll, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_noenvironmentpolicy_allowall_total",
		Help: "Total tenant rbac-config writes that set noEnvironmentPolicy to allow-all (§10.6).",
	}, []string{"tenant_id"})
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
	// §12.5 ll. 341 — `lenny_gc_tombstones_pruned_total` counts the
	// soft-deleted catalog rows physically removed by the hard-prune
	// sweep once the tombstone retention window has elapsed. No
	// labels — the §12.5 monitoring contract treats hard-prune as a
	// single rollup metric across tenants and artifact classes.
	gcTombstonesPruned := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_tombstones_pruned_total",
		Help: "Soft-deleted artifact_store rows physically removed by the §12.5 hard-prune sweep.",
	})

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

	reg.MustRegister(requestsTotal, requestDuration, maxSessionsPerReplica,
		extractionThreshold,
		storageQuotaUsed, storageQuotaLimit, circuitBreakerOpen, elicitationDropped,
		elicitationTamperDetected, experimentIsoRej,
		noEnvPolicyAllowAll, tokenServiceCircuitState,
		checkpointStaleSessions,
		partialManifestCleanup, checkpointPartialManifestsSuperseded,
		checkpointOrphanedObjects, checkpointSizeExceeded, sessionEvictionTotalLoss,
		checkpointEvictionPartialKeysLogged,
		checkpointDuration, checkpointStorageFailure,
		checkpointEvictionFallback, checkpointPartialTotal, prestopCapSelection,
		gcTombstonesPruned,
		gcRuns, gcArtifactsDeleted, gcErrors, gcDuration,
		drainReadinessChecks, legalHoldCheckpointGaps)
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
	reg.MustRegister(activeSessions, activeStreams, requestQueueDepth,
		rejectionRate, cbCacheStale, cbCacheInitialized, gcPauseP99Ms,
		minReplicas, streamCeiling, replicaCount)

	tokenServiceCircuitChild := tokenServiceCircuitState.WithLabelValues()
	return &Metrics{
		reg:                       reg,
		requestsTotal:             requestsTotal,
		requestDuration:           requestDuration,
		activeSessions:            gauge,
		activeStreams:             streams,
		requestQueueDepth:         queueDepth,
		rejectionRate:             rejections,
		maxSessionsPerReplica:     maxSessionsPerReplica,
		minReplicas:               minReplicasChild,
		streamCeiling:             streamCeilingChild,
		replicaCount:              replicaCountChild,
		extractionThreshold:       extractionThreshold,
		storageQuotaUsed:          storageQuotaUsed,
		storageQuotaLimit:         storageQuotaLimit,
		circuitBreakerOpen:        circuitBreakerOpen,
		cbCacheStale:              cbStale,
		cbCacheInitialized:        cbInit,
		elicitationDropped:        elicitationDropped,
		elicitationTamperDetected: elicitationTamperDetected,
		experimentIsoRej:          experimentIsoRej,
		noEnvPolicyAllowAll:       noEnvPolicyAllowAll,
		gcPauseP99Ms:              gcPause,
		tokenServiceCircuitState:             tokenServiceCircuitChild,
		checkpointStaleSessions:              checkpointStaleSessions,
		partialManifestCleanup:               partialManifestCleanup,
		checkpointPartialManifestsSuperseded: checkpointPartialManifestsSuperseded,
		checkpointOrphanedObjects:            checkpointOrphanedObjects,
		checkpointSizeExceeded:               checkpointSizeExceeded,
		sessionEvictionTotalLoss:             sessionEvictionTotalLoss,
		checkpointEvictionPartialKeysLogged:  checkpointEvictionPartialKeysLogged,
		checkpointDuration:                   checkpointDuration,
		checkpointStorageFailure:             checkpointStorageFailure,
		checkpointEvictionFallback:           checkpointEvictionFallback,
		checkpointPartialTotal:               checkpointPartialTotal,
		prestopCapSelection:                  prestopCapSelection,
		gcTombstonesPruned:                   gcTombstonesPruned,
		gcRuns:                               gcRuns,
		gcArtifactsDeleted:                   gcArtifactsDeleted,
		gcErrors:                             gcErrors,
		gcDuration:                           gcDuration.WithLabelValues(),
		drainReadinessChecks:                 drainReadinessChecks,
		legalHoldCheckpointGaps:              legalHoldCheckpointGaps,
	}, nil
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
// `lenny_gc_tombstones_pruned_total` counter by n. The §12.5 hard-prune
// sweep emits the count of catalog rows it removed; passing it through
// the gateway-side accessor keeps the metric registration centralised.
//
// spec: §12.5 ll. 341.
func (m *Metrics) AddGCTombstonesPruned(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.gcTombstonesPruned.Add(float64(n))
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

// RecordElicitationDrop increments the §9.1 lenny_elicitation_dropped_total
// counter for the given drop reason (for example `budget_exceeded`).
func (m *Metrics) RecordElicitationDrop(reason string) {
	m.elicitationDropped.WithLabelValues(reason).Inc()
}

// RecordElicitationContentTamperDetected increments the §9.2 /
// §16.5 lenny_elicitation_content_tamper_detected_total counter
// when the §9.2 chain walk catches a forwarding hop that mutated
// the elicitation payload. Labelled by tenant and enforcement_mode
// so the §16.5 ElicitationContentTamperDetected alert (which
// matches enforcement_mode="enforce") fires only when a tamper
// caused a hard drop; detect-only catches still bump the metric
// for visibility without firing the alert.
func (m *Metrics) RecordElicitationContentTamperDetected(tenantID, enforcementMode string) {
	m.elicitationTamperDetected.WithLabelValues(tenantID, enforcementMode).Inc()
}

// RecordExperimentIsolationRejection increments the §16.1
// lenny_experiment_isolation_rejections_total counter when the §10.7
// ExperimentRouter fails a session closed because the variant pool's
// isolation profile is weaker than the session's.
func (m *Metrics) RecordExperimentIsolationRejection(tenantID, experimentID, variantID string) {
	m.experimentIsoRej.WithLabelValues(tenantID, experimentID, variantID).Inc()
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
