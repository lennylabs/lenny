// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the platform tools' own elicitation metric set. The
// composition root registers it on the gateway's metric registry, so the
// platform tools seam holds no value of the replica's gateway metrics,
// which a seam may not import. It implements ElicitationDropRecorder,
// ElicitationLifecycleRecorder, and ElicitationTamperRecorder. The metric
// names, help text, labels, and buckets are the ones the gateway metrics
// registered before the platform tools owned them, and the unlabelled
// series are exposed at zero from registration, as they were there. The
// content-integrity weakened gauge, which the gateway's tenant
// reconciliation loop sets, stays in the gateway metrics.
type PromMetrics struct {
	dropped    *prometheus.CounterVec
	tamper     *prometheus.CounterVec
	pending    prometheus.Gauge
	timeout    prometheus.Counter
	suppressed prometheus.Counter
	roundtrip  prometheus.Observer
	treeCycle  *prometheus.CounterVec
}

var (
	_ ElicitationDropRecorder      = (*PromMetrics)(nil)
	_ ElicitationLifecycleRecorder = (*PromMetrics)(nil)
	_ ElicitationTamperRecorder    = (*PromMetrics)(nil)
	_ TreeCycleObserver            = (*PromMetrics)(nil)
)

// NewPromMetrics builds the elicitation metric set and registers it on
// reg. It fails when a collector cannot be built or registered, such as
// when reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	dropped, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_dropped_total",
		Help: "Total elicitations the gateway dropped, labelled by drop reason (§9.1).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	// §16.1: tamper detections by originating pod, tampering pod, and the
	// effective enforcement mode (enforce | detect-only). Both pod labels
	// are bounded by the delegation tree's depth.
	tamper, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_content_tamper_detected_total",
		Help: "Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by origin_pod, tampering_pod, and enforcement_mode (enforce | detect-only) per §16.1 so the §16.5 alert can fire on the enforce-mode stream only.",
	}, []string{"origin_pod", "tampering_pod", "enforcement_mode"})
	if err != nil {
		return nil, err
	}
	// §16.1: the in-flight elicitation gauge that the ElicitationBacklogHigh
	// alert reads.
	pending, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_pending",
		Help: "In-flight §9.2 elicitations awaiting human or intercepting-parent resolution (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	timeout, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_timeout_total",
		Help: "§9.1 elicitation drops on the maxElicitationWait deadline (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	suppressed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_elicitation_suppressed_total",
		Help: "§9.2 elicitations dropped by depth policy or per-session budget (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §16.1: admit-to-terminal latency; the buckets cover the default
	// elicitation timeout of 600s.
	roundtrip, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_elicitation_roundtrip_seconds",
		Help:    "§9.2 elicitation admit-to-terminal wall-clock latency (§16.1).",
		Buckets: []float64{0.5, 1, 5, 15, 30, 60, 120, 300, 600},
	}, nil)
	if err != nil {
		return nil, err
	}
	// §16.1 / §8.9: the tree walkers' defensive cycle hits. The REST tree
	// read and the platform tools' tree walks both count here, by the
	// source that walked the tree (`rest` or `mcp`).
	treeCycle, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_tree_cycle_detected_total",
		Help: "Tree walker hit a cycle in the §8.2 ParentSessionID lineage (corrupt store).",
	}, []string{"tenant_id", "source"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{dropped, tamper, pending, timeout, suppressed, roundtrip, treeCycle} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register elicitation metric: %w", err)
		}
	}
	return &PromMetrics{
		dropped:    dropped,
		tamper:     tamper,
		pending:    pending.WithLabelValues(),
		timeout:    timeout.WithLabelValues(),
		suppressed: suppressed.WithLabelValues(),
		roundtrip:  roundtrip.WithLabelValues(),
		treeCycle:  treeCycle,
	}, nil
}

// RecordElicitationDrop counts a dropped elicitation by reason.
func (m *PromMetrics) RecordElicitationDrop(reason string) {
	if m == nil {
		return
	}
	m.dropped.WithLabelValues(reason).Inc()
}

// RecordElicitationContentTamperDetected counts a chain walk that caught a
// forwarding hop altering the elicitation.
func (m *PromMetrics) RecordElicitationContentTamperDetected(originPod, tamperingPod, enforcementMode string) {
	if m == nil {
		return
	}
	m.tamper.WithLabelValues(originPod, tamperingPod, enforcementMode).Inc()
}

// IncElicitationPending raises the in-flight gauge when an elicitation is
// admitted.
func (m *PromMetrics) IncElicitationPending() {
	if m == nil {
		return
	}
	m.pending.Inc()
}

// DecElicitationPending lowers the in-flight gauge on a terminal phase.
func (m *PromMetrics) DecElicitationPending() {
	if m == nil {
		return
	}
	m.pending.Dec()
}

// IncElicitationTimeout counts a drop on the maxElicitationWait deadline.
func (m *PromMetrics) IncElicitationTimeout() {
	if m == nil {
		return
	}
	m.timeout.Inc()
}

// IncElicitationSuppressed counts a depth-policy suppression or a
// per-session budget rejection.
func (m *PromMetrics) IncElicitationSuppressed() {
	if m == nil {
		return
	}
	m.suppressed.Inc()
}

// ObserveElicitationRoundtrip records the admit-to-terminal latency.
func (m *PromMetrics) ObserveElicitationRoundtrip(d time.Duration) {
	if m == nil {
		return
	}
	m.roundtrip.Observe(d.Seconds())
}

// OnTreeCycle counts a cycle a tree walker met in the ParentSessionID
// lineage, by tenant and by the source that walked the tree. The walker
// truncates the cycle itself; the counter is the signal that the store
// holds a corrupt lineage. spec: §8.9; F-8.9.10.
func (m *PromMetrics) OnTreeCycle(_ context.Context, ev TreeCycleEvent) {
	if m == nil {
		return
	}
	m.treeCycle.WithLabelValues(ev.TenantID, ev.Source).Inc()
}
