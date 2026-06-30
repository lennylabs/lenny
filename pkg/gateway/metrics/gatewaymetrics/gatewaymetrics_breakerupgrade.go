// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// breakerUpgradeMetrics holds the §11.6 circuit-breaker, §10.5 runtime-upgrade, §11.2 storage-quota, and §15.1/§16.4 pool-drain and audit-partition gauges.
type breakerUpgradeMetrics struct {
	storageQuotaUsed               *prometheus.GaugeVec
	storageQuotaLimit              *prometheus.GaugeVec
	circuitBreakerOpen             *prometheus.GaugeVec
	poolDrainingSessions           *prometheus.GaugeVec
	auditPartitionDropBlocked      *prometheus.GaugeVec
	runtimeUpgradeState            *prometheus.GaugeVec
	runtimeUpgradePhaseDuration    *prometheus.GaugeVec
	runtimeUpgradeDrainingSessions *prometheus.GaugeVec
	cbRejections                   *prometheus.CounterVec
	cbRejectionsSuppressed         *prometheus.CounterVec
	cbCacheStale                   prometheus.Gauge
	cbCacheInitialized             prometheus.Gauge
	cbCacheStaleServes             *prometheus.CounterVec
}

// newBreakerUpgradeMetrics constructs, registers, and materializes the breakerupgrade metric subsystem
// against reg. spec: §16 observability metrics.
func newBreakerUpgradeMetrics(reg *prometheus.Registry) (breakerUpgradeMetrics, error) {
	var m breakerUpgradeMetrics
	storageQuotaUsed, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_storage_quota_bytes_used",
		Help: "Per-tenant artifact storage bytes reserved-plus-committed (§11.2).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	storageQuotaLimit, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_tenant_storage_quota_bytes",
		Help: "Per-tenant configured storage quota in bytes (§11.2 storageQuotaBytes).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	circuitBreakerOpen, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_open",
		Help: "1 when the named §11.6 circuit breaker is open, 0 when closed.",
	}, []string{"circuit_name"})
	if err != nil {
		return m, err
	}
	// spec: §15.1 line 797 — in-flight (non-terminal) sessions on a pool
	// while it drains, labelled by pool. Set when a pool enters the
	// `draining` phase and refreshed on each admin GET until the drain
	// converges to 0.
	poolDrainingSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_draining_sessions_total",
		Help: "In-flight sessions during a §15.1 pool drain, labelled by pool.",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// spec: §16.4 line 378 — 1 when the SIEM delivery guard is holding a
	// partition (audit chain) whose rows are past their retention TTL but
	// undelivered to the forwarder, so the §16.5 AuditPartitionDropBlocked
	// alert can fire. Labelled by partition; 0 once the forwarder catches
	// up. Set by the auditretention pruner each sweep.
	auditPartitionDropBlocked, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_audit_partition_drop_blocked",
		Help: "1 when an audit-partition drop is blocked by SIEM lag, labelled by partition (read by AuditPartitionDropBlocked).",
	}, []string{"partition"})
	if err != nil {
		return m, err
	}
	// spec: §10.5 lines 466-540 / §16.1 line 184 — current phase of the
	// 6-state RuntimeUpgrade machine per pool. Exactly one state row is 1
	// per pool; the others are 0. The §16.5 RuntimeUpgradeStuck alert fires
	// on state{state=~"expanding|draining|contracting"} == 1 held past
	// runtimeUpgrade.phaseTimeoutSeconds.
	runtimeUpgradeState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_runtime_upgrade_state",
		Help: "Current state of the 6-state runtime upgrade machine, labelled by pool and state.",
	}, []string{"pool", "state"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 185 — wall-clock time spent in the current upgrade
	// phase, labelled by pool and phase.
	runtimeUpgradePhaseDuration, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_runtime_upgrade_phase_duration_seconds",
		Help: "Wall-clock time in the current runtime upgrade phase, labelled by pool and phase.",
	}, []string{"pool", "phase"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 186 — sessions still draining on the old pool during
	// a runtime upgrade, labelled by pool.
	runtimeUpgradeDrainingSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_runtime_upgrade_draining_sessions",
		Help: "Sessions still draining during a runtime upgrade, labelled by pool.",
	}, []string{"pool"})
	if err != nil {
		return m, err
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
		return m, err
	}
	cbRejectionsSuppressed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_circuit_breaker_rejections_suppressed_total",
		Help: "§11.6 breaker rejections whose audit row was elided by per-(tenant_id, circuit_name, caller_sub) 10s sampling, labelled by tenant_id, circuit_name, and limit_tier.",
	}, []string{"tenant_id", "circuit_name", "limit_tier"})
	if err != nil {
		return m, err
	}
	cbCacheStale, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_stale_seconds",
		Help: "Wall seconds since the circuit-breaker cache last refreshed from Redis.",
	}, nil)
	if err != nil {
		return m, err
	}
	cbCacheInitialized, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_circuit_breaker_cache_initialized",
		Help: "1 once the circuit-breaker cache has completed its first refresh.",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 218 — every admission decision served against a
	// breaker cache that had not refreshed within the 5s poll interval,
	// labelled by outcome (rejected | admitted). outcome="admitted" is the
	// security-salient case: a breaker whose state the admission path could
	// not verify did not block the request.
	cbCacheStaleServes, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_circuit_breaker_cache_stale_serves_total",
		Help: "§11.6 admission decisions served against a stale (>5s unrefreshed) breaker cache, labelled by outcome (rejected | admitted).",
	}, []string{"outcome"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(storageQuotaUsed,
		storageQuotaLimit,
		circuitBreakerOpen,
		poolDrainingSessions,
		auditPartitionDropBlocked,
		runtimeUpgradeState,
		runtimeUpgradePhaseDuration,
		runtimeUpgradeDrainingSessions,
		cbRejections,
		cbRejectionsSuppressed,
		cbCacheStale,
		cbCacheStaleServes,
		cbCacheInitialized)
	cbStale := cbCacheStale.WithLabelValues()
	cbInit := cbCacheInitialized.WithLabelValues()
	m.storageQuotaUsed = storageQuotaUsed
	m.storageQuotaLimit = storageQuotaLimit
	m.circuitBreakerOpen = circuitBreakerOpen
	m.poolDrainingSessions = poolDrainingSessions
	m.auditPartitionDropBlocked = auditPartitionDropBlocked
	m.runtimeUpgradeState = runtimeUpgradeState
	m.runtimeUpgradePhaseDuration = runtimeUpgradePhaseDuration
	m.runtimeUpgradeDrainingSessions = runtimeUpgradeDrainingSessions
	m.cbRejections = cbRejections
	m.cbRejectionsSuppressed = cbRejectionsSuppressed
	m.cbCacheStale = cbStale
	m.cbCacheInitialized = cbInit
	m.cbCacheStaleServes = cbCacheStaleServes
	return m, nil
}
