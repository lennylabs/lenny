// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// miscMetrics holds the §5.2 / §8.6 / §8.7 / §9.4 / §10.3 / §13.3 stateless-routing, lease-extension, export-scan, interceptor-mTLS, memory-store, and clock-drift metrics.
type miscMetrics struct {
	// statelessRequests is the §5.2 cumulative request count arriving at
	// the pool's Kubernetes Service in service mode. The
	// PoolScalingController reads
	// `rate(lenny_service_requests_total[5m])` as `base_demand_p95`
	// for service pools (service mode bypasses the gateway claim model).
	// Labeled by `pool` (the SandboxTemplate name) — the emitter lands
	// with the tenant-affinity routing layer (F-5.2.3), the metric is
	// registered here so the catalog test sees the declared surface and
	// operators can scrape it as soon as the producer exists.
	statelessRequests *prometheus.CounterVec
	// statelessConcurrentActive is the §5.2 instantaneous per-pod
	// concurrent active-slot count. The PoolScalingController reads
	// `max_over_time(lenny_service_concurrent_active[5m])` as
	// `burst_p99_claims` for service pools. Labeled by `pool` — the
	// per-pod dimension is intentionally dropped to keep the cardinality
	// bound and because the controller aggregates across pods anyway.
	// Emitter lands with F-5.2.3.
	statelessConcurrentActive *prometheus.GaugeVec
	// sessionReuseCount is the §5.2 / §16.1 histogram of sessions served by
	// a single pod under recycle.enabled. The PoolScalingController reads
	// `histogram_quantile(0.50, ...)` as the mode-adjusted `mode_factor`
	// for recycling session-mode pools so the scaling formula converges
	// on observed reuse. Labeled by `pool` and `k8s_pod_name` per §16.1.
	sessionReuseCount *prometheus.HistogramVec
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
	// interceptorMTLSHandshake is the §16.1 line 50
	// lenny_interceptor_mtls_handshake_duration_seconds histogram
	// labeled by `result` (success, san_mismatch, cert_expired,
	// cert_missing, tls_error) — the wall-clock duration and outcome of
	// the §10.3 NET-063 gateway→in-cluster-interceptor TLS handshake.
	// The §16.5 InterceptorMTLSHandshakeFailure alert reads it. F-10.3.3.
	interceptorMTLSHandshake *prometheus.HistogramVec
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
}

// newMiscMetrics constructs, registers, and materializes the misc metric subsystem
// against reg. spec: §16 observability metrics.
func newMiscMetrics(reg *prometheus.Registry) (miscMetrics, error) {
	var m miscMetrics
	// §5.2 — `lenny_service_requests_total` is the cumulative request
	// count arriving at a service-mode pool's Kubernetes Service; the
	// PoolScalingController reads `rate(lenny_service_requests_total[5m])`
	// for service pool `base_demand_p95`. The producer lands with the
	// tenant-affinity routing layer (F-5.2.3).
	statelessRequests, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_service_requests_total",
		Help: "Service-mode requests routed through the pool's Service (§5.2).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §5.2 — `lenny_service_concurrent_active` is the instantaneous
	// active-slot count per service-mode pool. The PoolScalingController
	// reads `max_over_time(...[5m])` for service pool `burst_p99_claims`.
	// Producer lands with F-5.2.3.
	statelessConcurrentActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_service_concurrent_active",
		Help: "Service-mode pool peak active slot count (§5.2).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §5.2 / §16.1 — `lenny_pod_session_reuse_count` is a per-pod
	// histogram of sessions served on a single pod under recycle.enabled.
	// The PoolScalingController reads the median over the rolling window
	// as the mode-adjusted `mode_factor` for recycling session-mode pools.
	sessionReuseCount, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_pod_session_reuse_count",
		Help:    "Sessions served by a single pod under recycle.enabled (§5.2 / §16.1).",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"pool", "k8s_pod_name"})
	if err != nil {
		return m, err
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
		return m, err
	}
	// §16.1 line 80 — per-file PreExportMaterialization scan outcomes on
	// the §8.7 delegation file-export path. F-8.7.10.
	exportFileScans, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_export_file_scans_total",
		Help: "§8.7 PreExportMaterialization per-file scan outcomes (§16.1 line 80).",
	}, []string{"pool", "tenant_id", "policy_name", "interceptor_ref", "outcome"})
	if err != nil {
		return m, err
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
		return m, err
	}
	// §10.3 NET-063 / §16.1 line 50 — gateway→in-cluster-interceptor
	// TLS 1.3 handshake latency and outcome. Buckets span sub-
	// millisecond to a few seconds so a slow or failing handshake is
	// observable; the `result` label distinguishes the spec's distinct
	// rejection paths. F-10.3.3.
	interceptorMTLSHandshake, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_interceptor_mtls_handshake_duration_seconds",
		Help:    "§10.3 NET-063 gateway→interceptor mTLS handshake latency and outcome (§16.1 line 50).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"result"})
	if err != nil {
		return m, err
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
		return m, err
	}
	// §9.4 line 200 / §16.1 line 152 — MemoryStore per-operation error
	// counter.
	memoryStoreErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_memory_store_errors_total",
		Help: "MemoryStore per-operation errors (§9.4 line 200 / §16.1 line 152).",
	}, []string{"operation", "backend", "error_type"})
	if err != nil {
		return m, err
	}
	// §9.4 line 202 / §16.1 line 153 — MemoryStore per-tenant
	// approximate record count gauge. tenant_id only; user_id is on the
	// §16.1.1 forbidden-label list as a cardinality hot-spot.
	memoryStoreRecordCount, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_memory_store_record_count",
		Help: "Approximate stored memory records per tenant (§9.4 line 202 / §16.1 line 153).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
	}
	reg.MustRegister(statelessRequests,
		statelessConcurrentActive,
		sessionReuseCount,
		delegationLeaseExtension,
		exportFileScans,
		exportFileScanDuration,
		interceptorMTLSHandshake,
		memoryStoreOperationDuration,
		memoryStoreErrors,
		memoryStoreRecordCount,
		memoryStoreUserOverThreshold,
		timeDriftGauge)
	timeDriftChild := timeDriftGauge.WithLabelValues()
	m.statelessRequests = statelessRequests
	m.statelessConcurrentActive = statelessConcurrentActive
	m.sessionReuseCount = sessionReuseCount
	m.delegationLeaseExtension = delegationLeaseExtension
	m.exportFileScans = exportFileScans
	m.exportFileScanDuration = exportFileScanDuration
	m.interceptorMTLSHandshake = interceptorMTLSHandshake
	m.memoryStoreOperationDuration = memoryStoreOperationDuration
	m.memoryStoreErrors = memoryStoreErrors
	m.memoryStoreRecordCount = memoryStoreRecordCount
	m.memoryStoreUserOverThreshold = memoryStoreUserOverThreshold
	m.timeDrift = timeDriftChild
	return m, nil
}
