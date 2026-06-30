// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// checkpointMetrics holds the §4.4 / §10.1 checkpoint and session-eviction metrics.
type checkpointMetrics struct {
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
}

// newCheckpointMetrics constructs, registers, and materializes the checkpoint metric subsystem
// against reg. spec: §16 observability metrics.
func newCheckpointMetrics(reg *prometheus.Registry) (checkpointMetrics, error) {
	var m checkpointMetrics
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
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
	}
	reg.MustRegister(checkpointStaleSessions,
		partialManifestCleanup,
		checkpointPartialManifestsSuperseded,
		checkpointOrphanedObjects,
		checkpointSizeExceeded,
		sessionEvictionTotalLoss,
		checkpointEvictionPartialKeysLogged,
		checkpointDuration)
	m.checkpointStaleSessions = checkpointStaleSessions
	m.partialManifestCleanup = partialManifestCleanup
	m.checkpointPartialManifestsSuperseded = checkpointPartialManifestsSuperseded
	m.checkpointOrphanedObjects = checkpointOrphanedObjects
	m.checkpointSizeExceeded = checkpointSizeExceeded
	m.sessionEvictionTotalLoss = sessionEvictionTotalLoss
	m.checkpointEvictionPartialKeysLogged = checkpointEvictionPartialKeysLogged
	m.checkpointDuration = checkpointDuration
	return m, nil
}
