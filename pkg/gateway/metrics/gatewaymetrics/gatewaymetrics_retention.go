// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// retentionMetrics holds the §12.5 / §12.8 retention-GC, drain-readiness, legal-hold, and artifact-upload metrics.
type retentionMetrics struct {
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
	// legalHoldEscrowRegionUnresolvable counts §12.8 line 883 Phase 3.5
	// escrow-region resolution failures on the force-delete override
	// path. The §16.5 LegalHoldEscrowResidencyViolation alert reads it.
	legalHoldEscrowRegionUnresolvable *prometheus.CounterVec
	// legalHoldOverriddenTenant counts §12.8 line 887 tenant-scope
	// legal-hold overrides (force-delete with acknowledgeHoldOverride).
	// The §16.5 LegalHoldOverrideUsedTenant alert reads it.
	legalHoldOverriddenTenant *prometheus.CounterVec
	// artifactUploadError counts §12.5 ll. 282 ArtifactStore PUT
	// failures after the retry budget. Labels: `tenant_id` (caller
	// tenant) and `error_type` (`minio_unreachable | auth |
	// quota_exceeded | other`). The §16.5 MinIOUnavailable alert reads
	// the `minio_unreachable` label.
	artifactUploadError *prometheus.CounterVec
}

// newRetentionMetrics constructs, registers, and materializes the retention metric subsystem
// against reg. spec: §16 observability metrics.
func newRetentionMetrics(reg *prometheus.Registry) (retentionMetrics, error) {
	var m retentionMetrics
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
		return m, err
	}
	gcArtifactsDeleted, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_artifacts_deleted",
		Help: "Artifacts removed by the retention GC sweep (§12.5 line 321). Labelled by per-store adapter name.",
	}, []string{"store"})
	if err != nil {
		return m, err
	}
	gcErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gc_errors_total",
		Help: "Retention GC errors observed per sweep (§12.5 line 321). Labelled by per-store adapter name.",
	}, []string{"store"})
	if err != nil {
		return m, err
	}
	gcDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gc_duration_seconds",
		Help:    "Retention GC sweep wall-clock duration in seconds (§12.5 line 321).",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12),
	}, nil)
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
	}
	// §12.8 line 883 — `lenny_legal_hold_escrow_region_unresolvable_total`
	// counts Phase 3.5 force-delete-override escrow-region resolution
	// failures (the resolved region has no legalHoldEscrow entry, or its
	// escrow KEK / bucket is unreachable). The §16.5
	// LegalHoldEscrowResidencyViolation alert reads it.
	legalHoldEscrowRegionUnresolvable, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_legal_hold_escrow_region_unresolvable_total",
		Help: "Phase 3.5 escrow-region resolution failures (§12.8 line 883).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	// §12.8 line 887 — `lenny_gdpr_legal_hold_overridden_tenant_total`
	// counts tenant-scope legal-hold overrides (force-delete with
	// acknowledgeHoldOverride). The §16.5 LegalHoldOverrideUsedTenant
	// warning alert reads it.
	legalHoldOverriddenTenant, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gdpr_legal_hold_overridden_tenant_total",
		Help: "Tenant-scope legal-hold overrides via force-delete (§12.8 line 887).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
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
		return m, err
	}
	reg.MustRegister(gcTombstonesPruned,
		gcRuns,
		gcArtifactsDeleted,
		gcErrors,
		gcDuration,
		drainReadinessChecks,
		legalHoldCheckpointGaps,
		legalHoldEscrowRegionUnresolvable,
		legalHoldOverriddenTenant,
		artifactUploadError)
	m.gcTombstonesPruned = gcTombstonesPruned
	m.gcRuns = gcRuns
	m.gcArtifactsDeleted = gcArtifactsDeleted
	m.gcErrors = gcErrors
	m.gcDuration = gcDuration.WithLabelValues()
	m.drainReadinessChecks = drainReadinessChecks
	m.legalHoldCheckpointGaps = legalHoldCheckpointGaps
	m.legalHoldEscrowRegionUnresolvable = legalHoldEscrowRegionUnresolvable
	m.legalHoldOverriddenTenant = legalHoldOverriddenTenant
	m.artifactUploadError = artifactUploadError
	return m, nil
}
