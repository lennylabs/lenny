// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// credentialMetrics holds the §4.9 credential-lease, LLM-proxy, and §5.2 slot-binding metrics.
type credentialMetrics struct {
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
	// credentialLeasesSwept counts the §4.9 expired-lease rows the
	// bounded sweep worker removes from the credential_leases table
	// (lenny_gateway_credential_leases_swept_total). Unlabeled per
	// §16.1.1 (no high-cardinality attribute). spec: §4.9 line 1671.
	credentialLeasesSwept prometheus.Counter
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
	// adapterLeakedSlots is the §6.2 line 179 per-pod count of
	// concurrent-workspace slots whose cleanup timed out and are leaked
	// (not reclaimed until pod termination). Labels: `pod_id`, `pool`.
	adapterLeakedSlots *prometheus.GaugeVec
}

// newCredentialMetrics constructs, registers, and materializes the credential metric subsystem
// against reg. spec: §16 observability metrics.
func newCredentialMetrics(reg *prometheus.Registry) (credentialMetrics, error) {
	var m credentialMetrics
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
		return m, err
	}
	// §16.1 line 51 — `lenny_credential_lease_assignments_total` counts
	// the cumulative credential leases issued from a pool. `source` is
	// `primary` | `fallback` | `cached`.
	credentialLeaseAssignments, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_credential_lease_assignments_total",
		Help: "Credential leases issued from a pool by source (§16.1).",
	}, []string{"provider", "pool", "source"})
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
	}
	// §16.1 line 53 — `lenny_credential_pool_utilization` is the ratio of
	// in-use credentials to total pool credentials, in [0,1]. The
	// CredentialPoolLow alert fires above 0.80.
	credentialPoolUtilization, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_credential_pool_utilization",
		Help: "Ratio of in-use credentials to total pool credentials (§16.1).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §4.9 line 1671 — `lenny_gateway_credential_leases_swept_total` counts
	// the expired lease rows the bounded sweep worker removes from the
	// credential_leases table each tick. Unlabeled per §16.1.1.
	credentialLeasesSwept, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_credential_leases_swept_total",
		Help: "Expired credential-lease rows removed by the §4.9 sweep worker.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §16.1 line 97 — `lenny_gateway_llm_proxy_active_connections` is the
	// count of in-flight LLM proxy requests on a replica.
	llmProxyActiveConnections, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_llm_proxy_active_connections",
		Help: "LLM Proxy active connections (§16.1).",
	}, nil)
	if err != nil {
		return m, err
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
		return m, err
	}
	// §16.1 line 100 — `lenny_gateway_llm_translation_errors_total`
	// counts native-translator failures by the §4.9 error taxonomy.
	llmTranslationErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_llm_translation_errors_total",
		Help: "LLM translator failures by error type (§16.1).",
	}, []string{"pool", "provider", "error_type"})
	if err != nil {
		return m, err
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
		return m, err
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
		return m, err
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
		return m, err
	}
	// §6.2 line 179 — `lenny_adapter_leaked_slots` is the per-pod count of
	// concurrent-workspace slots whose cleanup timed out and remain counted
	// in active_slots until the pod terminates. Labels: `pod_id`, `pool`.
	adapterLeakedSlots, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_adapter_leaked_slots",
		Help: "Concurrent-workspace leaked slots per pod awaiting pod termination (§6.2 line 179).",
	}, []string{"pod_id", "pool"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(credentialPreclaimMismatch,
		credentialLeaseAssignments,
		credentialLeaseDuration,
		credentialPoolUtilization,
		llmTranslationDuration,
		llmTranslationErrors,
		slotFailure,
		slotRehydration,
		slotPodReplacement,
		adapterLeakedSlots,
		llmProxyActiveConnections,
		credentialLeasesSwept)
	llmProxyConns := llmProxyActiveConnections.WithLabelValues()
	m.credentialLeasesSwept = credentialLeasesSwept.WithLabelValues()
	m.credentialPreclaimMismatch = credentialPreclaimMismatch
	m.credentialLeaseAssignments = credentialLeaseAssignments
	m.credentialLeaseDuration = credentialLeaseDuration
	m.credentialRotation = credentialRotation
	m.credentialFallbackExhausted = credentialFallbackExhausted
	m.credentialPoolUtilization = credentialPoolUtilization
	m.llmProxyActiveConnections = llmProxyConns
	m.llmTranslationDuration = llmTranslationDuration
	m.llmTranslationErrors = llmTranslationErrors
	m.slotFailure = slotFailure
	m.slotPodReplacement = slotPodReplacement
	m.slotRehydration = slotRehydration
	m.adapterLeakedSlots = adapterLeakedSlots
	return m, nil
}
