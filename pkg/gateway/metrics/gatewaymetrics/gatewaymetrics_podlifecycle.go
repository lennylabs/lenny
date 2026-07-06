// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// podLifecycleMetrics holds the §5.2 / §10.1 pod-scrub, retirement, preStop-cap, and coordinator-handoff metrics.
type podLifecycleMetrics struct {
	// podScrubFailureTotal is the §5.2 aggregate
	// `lenny_pod_scrub_failure_total` counter: incremented on every failed
	// whole-pod scrub of a recycling session-mode pod, independent of
	// whether the failure retires the pod. Labels: `pool`, `runtime_class`.
	// spec: §5.2 (warn-policy scrub failure increments the aggregate
	// counter alongside the per-pod gauge).
	podScrubFailureTotal *prometheus.CounterVec
	// podScrubFailureCount is the §16.1 per-pod
	// `lenny_pod_scrub_failure_count` gauge: the cumulative scrub-failure
	// count of a recycling session-mode pod, evaluated against
	// recycle.maxScrubFailures at the recycle disposition. Labels:
	// `k8s_pod_name`, `pool`, `runtime_class`. spec: §16.1.
	podScrubFailureCount *prometheus.GaugeVec
	// podRetirement is the §16.1 `lenny_gateway_pod_retirement_total`
	// counter: gateway-initiated retirements of a recycling session-mode
	// pod, by reason. Labels: `reason` (frozen to session_count_limit |
	// scrub_failure_limit — the controller-owned
	// `lenny_controller_pod_retirement_total` carries uptime_limit, and
	// applyDisposition suppresses the gateway's uptime_limit emission),
	// `pool`, `runtime_class`. `scrub_failure_limit` is counted at the
	// recycle disposition; `session_count_limit` is counted at the recycle
	// disposition on a single-session pool and at the per-release
	// maxSessionsPerPod drain on a concurrent non-`vm-restart` pool.
	// spec: §16.1, §16.1.1.
	podRetirement *prometheus.CounterVec
	// checkpointPartialTotal counts the §4.4 line 234 / §10.1 partial-
	// manifest row writes. Labels: `pool` (finite, sandbox-warm-pool
	// registry).
	checkpointPartialTotal *prometheus.CounterVec
	// prestopCapSelection counts the §10.1 preStop tiered-cap
	// selection by source. Labels: `pool`, `service_instance_id`,
	// and `source` (postgres | postgres_null | cache_hit |
	// cache_miss_max_tier).
	prestopCapSelection *prometheus.CounterVec
	// barrierTargetSource counts the §10.1 line 165 preStop barrier
	// target-set reads by source (postgres | cache_fallback) so
	// operators can detect how often the degraded in-memory lease-cache
	// path is exercised when the coordination-lease read fails.
	barrierTargetSource *prometheus.CounterVec
	// sigkillStreams counts the §10.1 line 161 in-flight streams the
	// kubelet SIGKILLs at the grace deadline because their eviction
	// checkpoint did not finish in budget. Labels: `pool`,
	// `service_instance_id`.
	sigkillStreams *prometheus.CounterVec
	// coordinatorHandoffStale counts the §10.1 line 61 generation-stale
	// coordinator-handoff rejections: a CoordinatorFence the gateway
	// issued was rejected because the pod had already been fenced to an
	// equal-or-higher generation by another coordinator.
	coordinatorHandoffStale prometheus.Counter
	// coordinatorFenceRetry counts the §11.3 line 209 CoordinatorFence
	// retries after a rejection or a transient transport fault, before
	// the coordinator relinquishes.
	coordinatorFenceRetry prometheus.Counter
	// coordinatorFenceRelinquished counts the §11.3 line 209 cases where
	// the coordinator gave up leadership of a session after exhausting
	// its fence retries, releasing the coordination lease.
	coordinatorFenceRelinquished prometheus.Counter
}

// newPodLifecycleMetrics constructs, registers, and materializes the podlifecycle metric subsystem
// against reg. spec: §16 observability metrics.
func newPodLifecycleMetrics(reg *prometheus.Registry) (podLifecycleMetrics, error) {
	var m podLifecycleMetrics
	// §5.2 — `lenny_pod_scrub_failure_total` is the aggregate count of
	// failed whole-pod scrubs on a recycling session-mode pod, incremented
	// on every failure regardless of whether it retires the pod. Labels:
	// `pool`, `runtime_class` (both finite).
	podScrubFailureTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pod_scrub_failure_total",
		Help: "Aggregate failed whole-pod scrubs on recycling session-mode pods (§5.2).",
	}, []string{"pool", "runtime_class"})
	if err != nil {
		return m, err
	}
	// §16.1 — `lenny_pod_scrub_failure_count` is the per-pod cumulative
	// scrub-failure gauge, evaluated against recycle.maxScrubFailures at the
	// recycle disposition. `k8s_pod_name` is sanctioned for this metric by
	// §16.1; `pool` and `runtime_class` are finite.
	podScrubFailureCount, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pod_scrub_failure_count",
		Help: "Per-pod cumulative scrub-failure count on a recycling session-mode pod (§16.1).",
	}, []string{"k8s_pod_name", "pool", "runtime_class"})
	if err != nil {
		return m, err
	}
	// §16.1 — `lenny_gateway_pod_retirement_total` counts gateway-initiated
	// session-pool pod retirements by `reason`, frozen to the two
	// gateway-decided lifecycle-limit triggers (session_count_limit,
	// scrub_failure_limit). The controller-owned
	// `lenny_controller_pod_retirement_total` carries uptime_limit; an
	// operator sums the two via the §16.1 recording rule. Labels: `reason`,
	// `pool`, `runtime_class` (all finite). spec: §16.1, §16.1.1.
	podRetirement, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_pod_retirement_total",
		Help: "Gateway-initiated session-pool pod retirements by reason (§16.1).",
	}, []string{"reason", "pool", "runtime_class"})
	if err != nil {
		return m, err
	}
	// §4.4 line 234 — `lenny_checkpoint_partial_total` counts the
	// partial-manifest row writes. Labels: `pool` (finite).
	checkpointPartialTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_partial_total",
		Help: "Partial-manifest checkpoint writes (§4.4 line 234).",
	}, []string{"pool"})
	if err != nil {
		return m, err
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
		return m, err
	}
	// §10.1 line 165 — `lenny_prestop_barrier_target_source_total`
	// counts preStop CheckpointBarrier target-set reads by source
	// (postgres | cache_fallback) so operators can detect how often the
	// degraded in-memory lease-cache path is taken when the
	// coordination-lease read fails or exceeds its 2s deadline.
	barrierTargetSource, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_prestop_barrier_target_source_total",
		Help: "preStop CheckpointBarrier target-set source (postgres | cache_fallback) (§10.1).",
	}, []string{"source"})
	if err != nil {
		return m, err
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
		return m, err
	}
	// §10.1 line 61 — `lenny_coordinator_handoff_stale_total` counts the
	// generation-stale coordinator-handoff rejections: a CoordinatorFence
	// the gateway issued was refused because the pod had already been
	// fenced to an equal-or-higher generation by another coordinator.
	coordinatorHandoffStale, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_coordinator_handoff_stale_total",
		Help: "Generation-stale coordinator handoff rejections (§10.1 line 61).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §11.3 line 209 — `lenny_coordinator_fence_retry_total` counts the
	// CoordinatorFence retries after a stale rejection or a transient
	// transport fault, before the coordinator relinquishes leadership.
	coordinatorFenceRetry, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_coordinator_fence_retry_total",
		Help: "Coordinator retries after a fencing rejection (§11.3 line 209).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §11.3 line 209 — `lenny_coordinator_fence_relinquished_total` counts
	// the cases where the coordinator gave up leadership of a session
	// after exhausting its fence retries, releasing the coordination lease.
	coordinatorFenceRelinquished, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_coordinator_fence_relinquished_total",
		Help: "Coordinator leadership relinquished after fence retries (§11.3 line 209).",
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(podScrubFailureTotal,
		podScrubFailureCount,
		podRetirement,
		checkpointPartialTotal,
		prestopCapSelection,
		sigkillStreams,
		barrierTargetSource,
		coordinatorHandoffStale,
		coordinatorFenceRetry,
		coordinatorFenceRelinquished)
	m.podScrubFailureTotal = podScrubFailureTotal
	m.podScrubFailureCount = podScrubFailureCount
	m.podRetirement = podRetirement
	m.checkpointPartialTotal = checkpointPartialTotal
	m.prestopCapSelection = prestopCapSelection
	m.barrierTargetSource = barrierTargetSource
	m.sigkillStreams = sigkillStreams
	m.coordinatorHandoffStale = coordinatorHandoffStale.WithLabelValues()
	m.coordinatorFenceRetry = coordinatorFenceRetry.WithLabelValues()
	m.coordinatorFenceRelinquished = coordinatorFenceRelinquished.WithLabelValues()
	return m, nil
}
