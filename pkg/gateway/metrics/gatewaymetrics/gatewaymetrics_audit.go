// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// auditMetrics holds the §11.7 / §25.9 / §25.11 audit-chain, OCSF, residency, and replication-health metrics.
type auditMetrics struct {
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
	// auditRedactionReceiptMissing is the §16.1
	// lenny_audit_redaction_receipt_missing_total counter, labeled by
	// `tenant_id`. The periodic background integrity check increments it
	// when it finds an audit_log row carrying the §12.8 in-place GDPR
	// redaction marker but cannot locate a signature-bearing
	// RedactionReceipt for the same (tenant_id, sequence_number); the
	// §16.5 AuditRedactionReceiptMissing critical alert reads
	// `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0`.
	// F-11.7.15.
	auditRedactionReceiptMissing *prometheus.CounterVec
	// §25.9 audit-query observability surface. auditQueryDuration
	// observes one query latency per call, labeled by `endpoint`
	// (list/get/summary/verify) and `shards`. auditChainVerificationBroken
	// and auditChainRechainedPostOutage are unlabeled counters incremented
	// when a query surfaces a tampered or post-outage-rechained segment.
	// auditRateLimited counts audit events dropped by the §25.9 diagnostics
	// rate limiter, labeled by `event_type` and `service_account`.
	// auditScatterGatherShards observes the shard fan-out width per query
	// (1 in single-shard v1). spec: §25.9 metrics table. F-25.9.13.
	auditQueryDuration            *prometheus.HistogramVec
	auditChainVerificationBroken  prometheus.Counter
	auditChainRechainedPostOutage prometheus.Counter
	auditRateLimited              *prometheus.CounterVec
	auditScatterGatherShards      prometheus.Histogram
	// minioReplicationResidencyViolation is the §25.11 ArtifactStore
	// cross-region replication residency-violation counter, labeled by
	// source region. The replication Controller's residency preflight
	// increments it (via Metrics.ResidencyViolation) when a destination
	// bucket's jurisdiction tag does not match the source region's
	// dataResidencyRegion, the destination tag is missing, or the
	// destination resolves outside the allowed CIDRs. spec: §25.11;
	// §16.5 residency-violation alert. F-12.5.20 / F-16.7.2.
	minioReplicationResidencyViolation *prometheus.CounterVec
	// minioReplicationLagSeconds is the §17.3 / §25.11 ArtifactStore
	// off-cluster replication lag in seconds, labeled by source region.
	// The replication Controller's MeasureAll samples the source bucket's
	// replication queue each tick and sets this gauge (via
	// LagObserver.ReplicationLag); it drives MinIOArtifactReplicationLagHigh
	// (1× RPO) and MinIOArtifactReplicationLagCritical (4× RPO). F-17.3.7.
	minioReplicationLagSeconds *prometheus.GaugeVec
	// minioReplicationFailed is the §25.11 ArtifactStore object-level
	// replication-failure counter, labeled by source region. MeasureAll
	// reports the source cluster's cumulative failure total and the
	// reporter advances this counter by the observed delta; it drives
	// MinIOArtifactReplicationFailed. F-17.3.7.
	minioReplicationFailed *prometheus.CounterVec
	// dataResidencyViolation is the shared §16.1
	// lenny_data_residency_violation_total counter, labeled by the
	// operation that observed the violation (e.g. "artifact_replication").
	// The replication residency preflight bumps it alongside the
	// region-scoped counter above so the cross-operation §16.5 view has a
	// single series. F-12.5.20 / F-16.7.2.
	dataResidencyViolation *prometheus.CounterVec
	// platformAuditRegionUnresolvable is the §11.7 line 433
	// lenny_platform_audit_region_unresolvable_total counter, labeled by
	// the requested region and the failure_mode (missing_entry |
	// postgres_unreachable). The CMP-058 platform-tenant audit residency
	// gate bumps it (alongside dataResidencyViolation{operation=
	// "platform_audit_write"}) when a platform-tenant audit write
	// referencing a regulated target tenant cannot resolve that tenant's
	// regional platform-Postgres. It drives the PlatformAuditResidencyViolation
	// critical alert. F-11.7.9.
	platformAuditRegionUnresolvable *prometheus.CounterVec
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
}

// newAuditMetrics constructs, registers, and materializes the audit metric subsystem
// against reg. spec: §16 observability metrics.
func newAuditMetrics(reg *prometheus.Registry) (auditMetrics, error) {
	var m auditMetrics
	// §11.5 line 277 — `lenny_idempotency_cache_write_failures_total`
	// counts §11.5 idempotency-key Put failures (inner handler ran,
	// durable store rejected the cache row; next retry WILL
	// re-execute). spec: §11.5 line 277; F-11.5.4.
	idempotencyCacheWriteFailures, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_idempotency_cache_write_failures_total",
		Help: "§11.5 idempotency-key cache Put failures (silent re-execution risk).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
	}
	// §16.1 / §16.5 lenny_audit_redaction_receipt_missing_total — the
	// periodic background integrity check increments it per tenant when a
	// §12.8-redaction-marked audit_log row has no signature-verifying
	// RedactionReceipt; the §16.5 AuditRedactionReceiptMissing critical
	// alert distinguishes an orphaned GDPR redaction from a tamper that
	// cleared payload without a provenance receipt. F-11.7.15.
	auditRedactionReceiptMissing, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_redaction_receipt_missing_total",
		Help: "§16.1 redacted_gdpr rows with no signature-verifying RedactionReceipt, detected by the periodic background integrity check, by tenant_id.",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	// §25.9 audit-query observability surface. F-25.9.13.
	auditQueryDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_audit_query_duration_seconds",
		Help:    "§25.9 audit query latency by endpoint and shard count.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint", "shards"})
	if err != nil {
		return m, err
	}
	auditChainVerificationBroken := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_chain_verification_broken_total",
		Help: "§25.9 broken chain segments detected during query (tamper evidence).",
	})
	auditChainRechainedPostOutage := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_chain_rechained_post_outage_total",
		Help: "§25.9 chain segments rechained after a Postgres outage (not tamper evidence).",
	})
	auditRateLimited, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_rate_limited_total",
		Help: "§25.9 audit events dropped by rate limiting, by event_type and service_account.",
	}, []string{"event_type", "service_account"})
	if err != nil {
		return m, err
	}
	auditScatterGatherShards := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_audit_scatter_gather_shards_queried",
		Help:    "§25.9 shard count per scatter-gather audit query.",
		Buckets: []float64{1, 2, 4, 8, 16, 32},
	})
	// §25.11 ArtifactStore cross-region replication residency-violation
	// surface — the replication Controller's residency preflight wires
	// these via Metrics.ResidencyViolation. F-12.5.20 / F-16.7.2.
	minioReplicationResidencyViolation, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_minio_replication_residency_violation_total",
		Help: "§25.11 ArtifactStore replication residency violations by region.",
	}, []string{"region"})
	if err != nil {
		return m, err
	}
	dataResidencyViolation, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_data_residency_violation_total",
		Help: "§16.1 data residency violations by operation.",
	}, []string{"operation"})
	if err != nil {
		return m, err
	}
	platformAuditRegionUnresolvable, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_platform_audit_region_unresolvable_total",
		Help: "§11.7 CMP-058 platform-tenant audit residency resolution failures by region and failure_mode.",
	}, []string{"region", "failure_mode"})
	if err != nil {
		return m, err
	}
	// §17.3 line 130 / §25.11 line 4085 ArtifactStore replication-health
	// surface — the replication Controller's MeasureAll wires these via
	// LagObserver.ReplicationLag / ReplicationFailures. F-17.3.7.
	minioReplicationLagSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_minio_replication_lag_seconds",
		Help: "§25.11 ArtifactStore off-cluster replication lag in seconds by region.",
	}, []string{"region"})
	if err != nil {
		return m, err
	}
	minioReplicationFailed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_minio_replication_failed_total",
		Help: "§25.11 ArtifactStore object-level replication failures by region.",
	}, []string{"region"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(idempotencyCacheWriteFailures,
		idempotencyCacheSkipped,
		auditChainIntegrity,
		auditGrantDrift,
		auditOCSFTranslationFailed,
		auditRedactionReceiptMissing,
		auditQueryDuration,
		auditChainVerificationBroken,
		auditChainRechainedPostOutage,
		auditRateLimited,
		auditScatterGatherShards,
		minioReplicationResidencyViolation,
		dataResidencyViolation,
		platformAuditRegionUnresolvable,
		minioReplicationLagSeconds,
		minioReplicationFailed)
	m.auditChainIntegrity = auditChainIntegrity
	m.auditGrantDrift = auditGrantDrift
	m.auditOCSFTranslationFailed = auditOCSFTranslationFailed
	m.auditRedactionReceiptMissing = auditRedactionReceiptMissing
	m.auditQueryDuration = auditQueryDuration
	m.auditChainVerificationBroken = auditChainVerificationBroken
	m.auditChainRechainedPostOutage = auditChainRechainedPostOutage
	m.auditRateLimited = auditRateLimited
	m.auditScatterGatherShards = auditScatterGatherShards
	m.minioReplicationResidencyViolation = minioReplicationResidencyViolation
	m.minioReplicationLagSeconds = minioReplicationLagSeconds
	m.minioReplicationFailed = minioReplicationFailed
	m.dataResidencyViolation = dataResidencyViolation
	m.platformAuditRegionUnresolvable = platformAuditRegionUnresolvable
	m.idempotencyCacheWriteFailures = idempotencyCacheWriteFailures
	m.idempotencyCacheSkipped = idempotencyCacheSkipped
	return m, nil
}
