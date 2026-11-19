// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
)

// TestElicitationPromMetricsExposeTheirSeries checks the elicitation drop,
// tamper, and lifecycle metrics: the unlabelled series are exposed at zero
// before any elicitation, and each records with its label set. The tamper
// counter carries origin_pod, tampering_pod, and enforcement_mode, and no
// tenant_id.
//
// spec: §16.1 (Metrics), §9.2 (Elicitation Chain)
func TestElicitationPromMetricsExposeTheirSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := mcptools.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	atStart := `
# HELP lenny_elicitation_pending In-flight §9.2 elicitations awaiting human or intercepting-parent resolution (§16.1).
# TYPE lenny_elicitation_pending gauge
lenny_elicitation_pending 0
# HELP lenny_elicitation_suppressed_total §9.2 elicitations dropped by depth policy or per-session budget (§16.1).
# TYPE lenny_elicitation_suppressed_total counter
lenny_elicitation_suppressed_total 0
# HELP lenny_elicitation_timeout_total §9.1 elicitation drops on the maxElicitationWait deadline (§16.1).
# TYPE lenny_elicitation_timeout_total counter
lenny_elicitation_timeout_total 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(atStart),
		"lenny_elicitation_pending", "lenny_elicitation_suppressed_total", "lenny_elicitation_timeout_total",
	); err != nil {
		t.Fatal(err)
	}

	m.RecordElicitationDrop("budget_exceeded")
	m.RecordElicitationDrop("budget_exceeded")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "detect-only")
	m.IncElicitationPending()
	m.IncElicitationPending()
	m.DecElicitationPending()
	m.IncElicitationTimeout()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(45 * time.Second)

	want := `
# HELP lenny_elicitation_content_tamper_detected_total Total §9.2 elicitation chain walks that detected tampered content at a forwarding hop. Labelled by origin_pod, tampering_pod, and enforcement_mode (enforce | detect-only) per §16.1 so the §16.5 alert can fire on the enforce-mode stream only.
# TYPE lenny_elicitation_content_tamper_detected_total counter
lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only",origin_pod="pod-origin",tampering_pod="pod-middle"} 1
lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce",origin_pod="pod-origin",tampering_pod="pod-middle"} 2
# HELP lenny_elicitation_dropped_total Total elicitations the gateway dropped, labelled by drop reason (§9.1).
# TYPE lenny_elicitation_dropped_total counter
lenny_elicitation_dropped_total{reason="budget_exceeded"} 2
# HELP lenny_elicitation_pending In-flight §9.2 elicitations awaiting human or intercepting-parent resolution (§16.1).
# TYPE lenny_elicitation_pending gauge
lenny_elicitation_pending 1
# HELP lenny_elicitation_suppressed_total §9.2 elicitations dropped by depth policy or per-session budget (§16.1).
# TYPE lenny_elicitation_suppressed_total counter
lenny_elicitation_suppressed_total 1
# HELP lenny_elicitation_timeout_total §9.1 elicitation drops on the maxElicitationWait deadline (§16.1).
# TYPE lenny_elicitation_timeout_total counter
lenny_elicitation_timeout_total 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"lenny_elicitation_content_tamper_detected_total", "lenny_elicitation_dropped_total",
		"lenny_elicitation_pending", "lenny_elicitation_suppressed_total", "lenny_elicitation_timeout_total",
	); err != nil {
		t.Fatal(err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "lenny_elicitation_roundtrip_seconds" {
			continue
		}
		h := mf.GetMetric()[0].GetHistogram()
		if h.GetSampleCount() != 1 || h.GetSampleSum() != 45 {
			t.Fatalf("roundtrip histogram count=%d sum=%v, want 1 and 45", h.GetSampleCount(), h.GetSampleSum())
		}
		if got := len(h.GetBucket()); got != 9 {
			t.Fatalf("roundtrip histogram has %d buckets, want 9", got)
		}
		return
	}
	t.Fatal("lenny_elicitation_roundtrip_seconds not gathered")
}

func TestElicitationPromMetricsRefuseARegistryThatHoldsTheNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := mcptools.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := mcptools.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilElicitationPromMetricsIgnoreEveryCall(t *testing.T) {
	var m *mcptools.PromMetrics
	m.RecordElicitationDrop("budget_exceeded")
	m.RecordElicitationContentTamperDetected("a", "b", "enforce")
	m.IncElicitationPending()
	m.DecElicitationPending()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(time.Second)
}
