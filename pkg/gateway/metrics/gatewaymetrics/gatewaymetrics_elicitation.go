// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// elicitationMetrics holds the §9.1 / §9.2 / §16.1 elicitation observability.
type elicitationMetrics struct {
	elicitationDropped        *prometheus.CounterVec
	elicitationTamperDetected *prometheus.CounterVec
	// elicitationIntegrityWeakened is the §16.5 line 460 standing-alert
	// gauge: the count of active tenants whose §9.2 effective
	// elicitation content-integrity mode is weaker than enforce. A
	// gateway reconciliation loop refreshes it from the tenant store.
	// F-9.2.5.
	elicitationIntegrityWeakened prometheus.Gauge
	// elicitationPending is the §16.1 line 61 unlabelled gauge of
	// in-flight elicitations the §16.5 ElicitationBacklogHigh alert
	// keys on (`lenny_elicitation_pending > 50 for > 30s`). The
	// dispatcher's request_elicitation handler increments on admit
	// and decrements on terminal phase. spec: §16.1 line 61; §16.5
	// line 458. F-9.2.14.
	elicitationPending prometheus.Gauge
	// elicitationTimeout counts §9.1 line 103 elicitation drops on the
	// maxElicitationWait deadline. spec: §16.1 line 63. F-9.2.14.
	elicitationTimeout prometheus.Counter
	// elicitationSuppressed counts §9.2 depth-policy suppressions and
	// per-session budget exhaustions. spec: §16.1 line 62. F-9.2.14.
	elicitationSuppressed prometheus.Counter
	// elicitationRoundtripSeconds observes the wall-clock duration
	// from admit to resolve / dismiss / timeout per the §16.1 line 60
	// histogram contract. spec: §16.1 line 60. F-9.2.14.
	elicitationRoundtripSeconds prometheus.Observer
}

// newElicitationMetrics constructs, registers, and materializes the elicitation metric subsystem
// against reg. spec: §16 observability metrics.
func newElicitationMetrics(reg *prometheus.Registry) (elicitationMetrics, error) {
	var m elicitationMetrics
	elicitationDropped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_dropped_total",
		Help: "Total elicitations the gateway dropped, labelled by drop reason (§9.1).",
	}, []string{"reason"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 64; §9.2 line 60 — the tamper counter is labelled
	// by origin_pod, tampering_pod, and enforcement_mode. origin_pod is
	// the pod that legitimately originated the elicitation; tampering_pod
	// is the forwarding pod whose re-emission diverged. Both are bounded
	// by the active delegation-tree depth, so cardinality is safe under
	// the §16.1.1 attribute-naming rule. The enforcement_mode label is
	// one of enforce | detect-only only — the detector does not run under
	// effective mode off, so no stream is emitted there. F-9.2.4.
	elicitationTamperDetected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_content_tamper_detected_total",
		Help: "Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by origin_pod, tampering_pod, and enforcement_mode (enforce | detect-only) per §16.1 line 64 so the §16.5 alert can fire on the enforce-mode stream only.",
	}, []string{"origin_pod", "tampering_pod", "enforcement_mode"})
	if err != nil {
		return m, err
	}
	// spec: §16.5 line 460 — the standing ElicitationContentIntegrityWeakened
	// alert reads this gauge. It is a gateway-process count of active
	// tenants whose §9.2 effective elicitation content-integrity mode
	// (max(platformFloor, tenantStored)) is weaker than enforce. The
	// alert fires while the value is > 0 and resolves once every active
	// tenant's effective mode is enforce. Unlabelled to keep the gauge
	// cardinality-free; operators identify which tenants are weakened
	// from the paired tenant.elicitation_content_integrity_changed audit
	// events, per the §16.5 runbook. F-9.2.5.
	elicitationIntegrityWeakened, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce",
		Help: "Active tenants whose §9.2 effective elicitation content-integrity enforcement mode is weaker than enforce (§16.5 line 460). Standing-alert numerator; zero when every active tenant resolves to enforce.",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 61 — in-flight elicitation gauge the §16.5
	// ElicitationBacklogHigh alert reads. Unlabelled; the gauge is a
	// gateway-process count. F-9.2.14.
	elicitationPending, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_pending",
		Help: "In-flight §9.2 elicitations awaiting human or intercepting-parent resolution (§16.1 line 61).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 63 — elicitation-timeout counter the operator
	// dashboards read; the maxElicitationWait drop site increments it.
	// F-9.2.14.
	elicitationTimeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_timeout_total",
		Help: "§9.1 line 103 elicitation drops on the maxElicitationWait deadline (§16.1 line 63).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 62 — suppression / budget-exhaustion counter.
	// F-9.2.14.
	elicitationSuppressed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_suppressed_total",
		Help: "§9.2 elicitations dropped by depth policy or per-session budget (§16.1 line 62).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 60 — admit-to-terminal round-trip histogram.
	// Buckets cover the §9.1 default elicitationTimeout (600s) with
	// reasonable granularity on the typical-human-response range
	// (1s..10min). F-9.2.14.
	elicitationRoundtripSeconds, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_elicitation_roundtrip_seconds",
		Help:    "§9.2 elicitation admit-to-terminal wall-clock latency (§16.1 line 60).",
		Buckets: []float64{0.5, 1, 5, 15, 30, 60, 120, 300, 600},
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(elicitationDropped,
		elicitationTamperDetected,
		elicitationIntegrityWeakened,
		elicitationPending,
		elicitationTimeout,
		elicitationSuppressed,
		elicitationRoundtripSeconds)
	m.elicitationDropped = elicitationDropped
	m.elicitationTamperDetected = elicitationTamperDetected
	m.elicitationIntegrityWeakened = elicitationIntegrityWeakened.WithLabelValues()
	m.elicitationPending = elicitationPending.WithLabelValues()
	m.elicitationTimeout = elicitationTimeout.WithLabelValues()
	m.elicitationSuppressed = elicitationSuppressed.WithLabelValues()
	m.elicitationRoundtripSeconds = elicitationRoundtripSeconds.WithLabelValues()
	return m, nil
}
