// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// podClaimMetrics holds the §4.6.1 / §5.2 pod-claim FIFO and concurrent-slot reservation metrics.
type podClaimMetrics struct {
	// podClaimFallbackSkipped counts the §4.6.1 Postgres-backed pod-claim
	// fallback skip events. Labels: `reason` (`mirror_stale` or
	// `apiserver_unreachable`).
	podClaimFallbackSkipped *prometheus.CounterVec
	// podClaimQueueDepth is the §4.6.1 / §16.1 per-pool claim-FIFO depth
	// gauge: the number of onPoolExhausted: queue requests waiting for a pod
	// to free in a pool. Labeled by `pool` (finite, the warm-pool registry).
	podClaimQueueDepth *prometheus.GaugeVec
	// podClaimQueueWait is the §4.6.1 / §16.1 per-pool claim-FIFO residency
	// histogram: the wall-clock a queued request spent in the FIFO before it
	// acquired a pod or timed out. Labeled by `pool`.
	podClaimQueueWait *prometheus.HistogramVec
	// podClaimTimeout counts the §4.6.1 / §16.1 onPoolExhausted: queue
	// requests that exhausted their maxQueueWaitSeconds bound and returned
	// WARM_POOL_EXHAUSTED. Labeled by `pool`. It backs the §16.5
	// PodClaimQueueSaturated alert alongside the depth gauge.
	podClaimTimeout *prometheus.CounterVec
	// slotAssignmentConflict counts the §5.2 line 519 concurrent-mode
	// slot reservation failures due to slot contention (a candidate pod
	// was at its maxConcurrent bound). Labeled by `pool` (finite, the
	// warm-pool registry), it lets operators detect pool under-sizing.
	slotAssignmentConflict *prometheus.CounterVec
}

// newPodClaimMetrics constructs, registers, and materializes the podclaim metric subsystem
// against reg. spec: §16 observability metrics.
func newPodClaimMetrics(reg *prometheus.Registry) (podClaimMetrics, error) {
	var m podClaimMetrics
	// §4.6.1 — `lenny_pod_claim_fallback_skipped_total` counts the
	// Postgres-backed fallback claim skips when a precondition fails.
	// Labels: `reason` (`mirror_stale` | `apiserver_unreachable`).
	podClaimFallbackSkipped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pod_claim_fallback_skipped_total",
		Help: "Postgres-backed pod-claim fallback skips by precondition (§4.6.1).",
	}, []string{"reason"})
	if err != nil {
		return m, err
	}
	// §4.6.1 / §16.1 — `lenny_pod_claim_queue_depth{pool}` is the per-pool
	// claim FIFO depth for onPoolExhausted: queue pools. The §16.5
	// PodClaimQueueSaturated alert reads it against the pool's minWarm.
	podClaimQueueDepth, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pod_claim_queue_depth",
		Help: "Pod claim queue depth by pool (§4.6.1 Pool exhaustion behavior, §16.1).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §4.6.1 / §16.1 — `lenny_pod_claim_queue_wait_seconds{pool}` is the
	// residency a queued request spent in the FIFO before acquiring a pod or
	// timing out. Buckets span the sub-second poll cadence through the 30s
	// default maxQueueWaitSeconds and a slow tail to a tuned 120s bound.
	podClaimQueueWait, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_pod_claim_queue_wait_seconds",
		Help:    "Pod claim queue wait time by pool (§4.6.1 Pool exhaustion behavior, §16.1).",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120},
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §4.6.1 / §16.1 — `lenny_pod_claim_timeout_total{pool}` counts the
	// onPoolExhausted: queue requests that exhausted maxQueueWaitSeconds and
	// returned WARM_POOL_EXHAUSTED.
	podClaimTimeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pod_claim_timeout_total",
		Help: "Pod claim queue-wait timeouts by pool (§4.6.1 Pool exhaustion behavior, §16.1).",
	}, []string{"pool"})
	if err != nil {
		return m, err
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
		return m, err
	}
	reg.MustRegister(podClaimFallbackSkipped,
		slotAssignmentConflict,
		podClaimQueueDepth,
		podClaimQueueWait,
		podClaimTimeout)
	m.podClaimFallbackSkipped = podClaimFallbackSkipped
	m.podClaimQueueDepth = podClaimQueueDepth
	m.podClaimQueueWait = podClaimQueueWait
	m.podClaimTimeout = podClaimTimeout
	m.slotAssignmentConflict = slotAssignmentConflict
	return m, nil
}
