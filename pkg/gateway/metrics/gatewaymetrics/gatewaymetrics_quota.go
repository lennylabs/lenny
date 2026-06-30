// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// quotaMetrics holds the §11.1 / §11.5 / §12.3 / §12.4 rate-limit, quota fail-open, idempotency, and billing-flush metrics.
type quotaMetrics struct {
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
	// quotaFailopenCumulativeSeconds is the §12.4 line 224 cumulative
	// fail-open timer gauge: the total seconds this replica has spent in
	// fail-open mode within the rolling 1-hour window. The §16.5
	// QuotaFailOpenCumulativeThreshold alert fires at 80% of the
	// configured maximum. spec: §12.4 line 224; §16.1 / §16.5.
	quotaFailopenCumulativeSeconds prometheus.Gauge
	// quotaUserFailopenFraction exports the configured
	// quotaUserFailOpenFraction so the §16.5
	// QuotaFailOpenUserFractionInoperative warning fires for operators who
	// joined after startup. spec: §12.4 line 222; §16.1 / §16.5.
	quotaUserFailopenFraction prometheus.Gauge
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
	// auditBatchingNoSIEM is the §12.3 line 99 AuditBatchingNoSIEM
	// counter. The gateway increments it once at startup when
	// LENNY_ENV=production has audit.batchingEnabled set but no SIEM
	// endpoint configured: buffered T2 audit events would be lost on a
	// crash with no external durable copy to recover from. F-12.3.15.
	auditBatchingNoSIEM prometheus.Counter
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
	// siemDeliveryLag is the §16.1 line 228
	// lenny_audit_siem_delivery_lag_seconds gauge. The §12.3 outbox
	// forwarder sets it after each delivery checkpoint to the seconds
	// between the latest committed audit event in Postgres and the
	// latest SIEM-acknowledged event; the §16.5 AuditSIEMDeliveryLag
	// alert reads it against the configured max-lag scalar. F-12.3.6 /
	// F-12.3.17.
	siemDeliveryLag prometheus.Gauge
	// siemMaxDeliveryLag is the §12.3 line 97
	// audit.siem.maxDeliveryLagSeconds configured threshold (default
	// 30s), emitted unlabelled at startup so AuditSIEMDeliveryLag
	// resolves scalar(lenny_audit_siem_max_delivery_lag_seconds) to an
	// operator-tunable threshold rather than a literal. Not in the §16.1
	// catalog. F-12.3.17.
	siemMaxDeliveryLag prometheus.Gauge
}

// newQuotaMetrics constructs, registers, and materializes the quota metric subsystem
// against reg. spec: §16 observability metrics.
func newQuotaMetrics(reg *prometheus.Registry) (quotaMetrics, error) {
	var m quotaMetrics
	// §11.1 line 7 — `lenny_rate_limit_rejected_total` counts ratelimit
	// middleware 429 rejections, labelled by `scope` (`global` | `user`)
	// so operators can attribute rejection volume per enforcement axis.
	rateLimitRejected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_rate_limit_rejected_total",
		Help: "§11.1 ratelimit middleware admission rejections by scope (global | user).",
	}, []string{"scope"})
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
	}
	rateLimitCounterFailure := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lenny_rate_limit_counter_failure_total",
		Help: "§11.1 ratelimit counter errors observed by the middleware.",
	})
	// §12.4 line 224 — `lenny_quota_failopen_cumulative_seconds` is the
	// cumulative fail-open timer: total seconds spent fail-open within the
	// rolling 1h window. Drives the QuotaFailOpenCumulativeThreshold alert.
	quotaFailopenCumulativeSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_quota_failopen_cumulative_seconds",
		Help: "§12.4 cumulative quota fail-open seconds per replica (rolling 1h window).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.4 line 222 — `lenny_quota_user_failopen_fraction` exports the
	// configured quotaUserFailOpenFraction so the
	// QuotaFailOpenUserFractionInoperative warning reflects a weakened
	// (>= 0.5) per-user fail-open cap.
	quotaUserFailopenFraction, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_quota_user_failopen_fraction",
		Help: "§12.4 configured quotaUserFailOpenFraction value.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.3 line 76 — billing_flush_pressure: emitted when the failover
	// Tier 2 write-ahead buffer crosses billingFlushMaxPending. F-12.3.13.
	billingFlushPressure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_billing_flush_pressure_total",
		Help: "§12.3 billing_flush_pressure: billing write-ahead buffer crossed billingFlushMaxPending and was force-flushed.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.3 line 99 AuditBatchingNoSIEM — incremented once at startup
	// when production has audit.batchingEnabled but no SIEM endpoint.
	// F-12.3.15.
	auditBatchingNoSIEM, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_audit_batching_no_siem_total",
		Help: "§12.3 line 99 AuditBatchingNoSIEM: production audit.batchingEnabled is set with no SIEM endpoint, so buffered T2 events are lost on crash with no external copy.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.3 lines 115-125 — sustained Postgres write IOPS, sampled from
	// pg_stat_database row-write deltas. Numerator of the §16.5
	// PostgresWriteSaturation ratio. F-12.3.7.
	postgresWriteIops, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_postgres_write_iops",
		Help: "§12.3 sustained Postgres write IOPS sampled from pg_stat_database row-write deltas.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.3 line 123 — configured postgres.writeCeilingIops, emitted at
	// startup so PostgresWriteSaturation reads an operator-tunable
	// scalar() denominator. F-12.3.8.
	postgresWriteCeilingIops, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_postgres_write_ceiling_iops",
		Help: "§12.3 line 123 configured Postgres sustained write-IOPS ceiling (postgres.writeCeilingIops).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §16.1 line 228 lenny_audit_siem_delivery_lag_seconds — the §12.3
	// outbox forwarder sets it after each delivery checkpoint. F-12.3.6.
	siemDeliveryLag, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_audit_siem_delivery_lag_seconds",
		Help: "§16.1 seconds between the latest committed audit event in Postgres and the latest SIEM-acknowledged event, set by the §12.3 outbox forwarder.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §12.3 line 97 audit.siem.maxDeliveryLagSeconds — emitted at
	// startup so AuditSIEMDeliveryLag reads an operator-tunable
	// scalar() threshold. F-12.3.17.
	siemMaxDeliveryLag, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_audit_siem_max_delivery_lag_seconds",
		Help: "§12.3 line 97 configured SIEM delivery-lag threshold (audit.siem.maxDeliveryLagSeconds).",
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(rateLimitRejected,
		rateLimitFailopenActive,
		rateLimitCounterFailure,
		quotaFailopenCumulativeSeconds,
		quotaUserFailopenFraction,
		dualStoreUnavailable,
		billingFlushPressure,
		auditBatchingNoSIEM,
		postgresWriteIops,
		postgresWriteCeilingIops,
		siemDeliveryLag,
		siemMaxDeliveryLag)
	siemMaxDeliveryLagChild := siemMaxDeliveryLag.WithLabelValues()
	siemMaxDeliveryLagChild.Set(30)
	m.rateLimitRejected = rateLimitRejected
	m.rateLimitFailopenActive = rateLimitFailopenActive.WithLabelValues()
	m.rateLimitCounterFailure = rateLimitCounterFailure
	m.quotaFailopenCumulativeSeconds = quotaFailopenCumulativeSeconds.WithLabelValues()
	m.quotaUserFailopenFraction = quotaUserFailopenFraction.WithLabelValues()
	m.dualStoreUnavailable = dualStoreUnavailable.WithLabelValues()
	m.billingFlushPressure = billingFlushPressure.WithLabelValues()
	m.auditBatchingNoSIEM = auditBatchingNoSIEM.WithLabelValues()
	m.postgresWriteIops = postgresWriteIops.WithLabelValues()
	m.postgresWriteCeilingIops = postgresWriteCeilingIops.WithLabelValues()
	m.siemDeliveryLag = siemDeliveryLag.WithLabelValues()
	m.siemMaxDeliveryLag = siemMaxDeliveryLagChild
	return m, nil
}
