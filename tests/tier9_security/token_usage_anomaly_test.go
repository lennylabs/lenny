// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §11.2 direct-mode token-usage integrity control
// (proposal 0024, F-11.2.20 / F-15.3.7). In direct mode the pod egresses to the
// LLM provider directly and never reaches the §4.9 gateway proxy, so the
// gateway has no independent view of a direct-mode session's token
// consumption. A malicious or broken runtime can therefore under-report token
// usage. §11.2 accepts this as a residual risk for direct mode and mandates it
// be monitored via `lenny_gateway_token_usage_anomaly_total`.
//
// This test drives the genuine gateway detector as the security control it is:
//
//   - A runtime that reports zero tokens across a session (an under-reporting
//     attack) is detected once the consecutive-zero run exceeds the window, and
//     the counter increments. Before proposal 0024 the metric was emitted
//     nowhere (F-11.2.20), so a zero-token attack raised no signal at all.
//   - A runtime that reports a trickle of tokens across many calls
//     (implausibly small relative to call frequency) is detected on the ratio
//     branch, catching an attacker who avoids an outright zero to dodge the
//     zero-delta window.
//   - The emitted counter carries only the §16.1.1-compliant {tenant_id,
//     reason} label set. §16.1.1 forbids `session_id` as a Prometheus label;
//     the pre-fix §11.2 prose labeled the metric by `session_id`, which this
//     test proves would fail the CI-enforced validator.
//   - Per-tenant isolation: one tenant's under-reporting does not raise
//     another tenant's series.
//
// spec: §11.2 (direct-mode usage integrity; monitored residual risk), §16.1.1
// (forbidden high-cardinality labels; per-session attribution via structured
// logs).

package tier9_security_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

const tokenAnomalyMetric = "lenny_gateway_token_usage_anomaly_total"

// anomalyCount reads the {tenant_id, reason} series value from reg.
func anomalyCount(t *testing.T, reg *prometheus.Registry, tenantID, reason string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != tokenAnomalyMetric {
			continue
		}
		for _, m := range mf.GetMetric() {
			if anomalyLabel(m, "tenant_id") == tenantID && anomalyLabel(m, "reason") == reason {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func anomalyLabel(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// diagnosis: the §11.2 direct-mode integrity control did not detect a
// zero-token under-reporting attack. Either the detector no longer fires on a
// sustained zero-token stream or its counter is not registered, so a malicious
// direct-mode runtime can hide its token consumption with no observable signal
// (regression of F-11.2.20).
//
// spec: §11.2 (direct-mode zero_delta under-reporting).
func TestDirectModeZeroTokenUnderReportingIsDetected_spec_11_2(t *testing.T) {
	reg := prometheus.NewRegistry()
	det, err := tokenanomaly.New(reg, tokenanomaly.Config{}, nil)
	if err != nil {
		t.Fatalf("construct detector: %v", err)
	}

	// A malicious runtime reports zero tokens on every pull. Three zeros sit at
	// the window boundary; the fourth exceeds it and raises the anomaly.
	for i := 0; i < 3; i++ {
		det.Observe("acme", "s_attack", 0, 0)
	}
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("anomaly fired inside the zero-token window (3 pulls, default window 3): got %v, want 0", got)
	}
	det.Observe("acme", "s_attack", 0, 0)
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("zero-token under-reporting attack was not detected: got %v zero_delta anomalies, want 1", got)
	}
}

// diagnosis: the §11.2 ratio branch did not catch a runtime reporting a trickle
// of tokens across many LLM calls (implausibly small relative to call
// frequency), so an attacker can evade the zero-delta window by reporting a
// single token per call and still under-report at scale.
//
// spec: §11.2 (direct-mode implausibly_small under-reporting).
func TestDirectModeImplausiblySmallUnderReportingIsDetected_spec_11_2(t *testing.T) {
	reg := prometheus.NewRegistry()
	det, err := tokenanomaly.New(reg, tokenanomaly.Config{ImplausiblySmallRatio: 10}, nil)
	if err != nil {
		t.Fatalf("construct detector: %v", err)
	}

	// One token per call, four calls: a 1-token/call average, far below the
	// 10-token threshold, with no zero pull to trip the zero-delta branch.
	for i := 0; i < 4; i++ {
		det.Observe("acme", "s_trickle", 1, 0)
	}
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 1 {
		t.Fatalf("implausibly-small under-reporting was not detected: got %v implausibly_small anomalies, want 1", got)
	}
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("a non-zero-but-small stream was misclassified as zero_delta: got %v, want 0", got)
	}
}

// diagnosis: the §11.2 anomaly counter carries a §16.1.1-forbidden label. The
// counter must be labeled by {tenant_id, reason} only; a `session_id` label
// would explode Prometheus cardinality and is CI-forbidden. A failure means the
// metric ships with an unbounded label (regression of the proposal 0024
// forbidden-label correction).
//
// spec: §16.1.1 (forbidden high-cardinality Prometheus labels).
func TestAnomalyCounterLabelSetIsCompliant_spec_16_1_1(t *testing.T) {
	// The compliant label set the detector uses passes the CI-enforced
	// validator.
	if err := metrics.Validate(tokenAnomalyMetric, []string{"tenant_id", "reason"}); err != nil {
		t.Fatalf("the {tenant_id, reason} label set failed §16.1.1 Validate: %v", err)
	}
	// The pre-fix §11.2 prose labeled the metric by session_id, which §16.1.1
	// forbids: assert that label set is rejected so a regression re-adding it is
	// caught here.
	if err := metrics.Validate(tokenAnomalyMetric, []string{"session_id", "tenant_id"}); err == nil {
		t.Fatal("a session_id label passed §16.1.1 Validate; the forbidden-label rule is not enforced")
	}
}

// diagnosis: the §11.2 detector leaked one tenant's under-reporting into
// another tenant's anomaly series, breaking per-tenant attribution of the
// integrity signal.
//
// spec: §11.2 (per-tenant direct-mode integrity signal).
func TestAnomalyIsIsolatedPerTenant_spec_11_2(t *testing.T) {
	reg := prometheus.NewRegistry()
	det, err := tokenanomaly.New(reg, tokenanomaly.Config{}, nil)
	if err != nil {
		t.Fatalf("construct detector: %v", err)
	}

	// acme's runtime under-reports; globex's reports healthy token counts.
	for i := 0; i < 4; i++ {
		det.Observe("acme", "s_bad", 0, 0)
		det.Observe("globex", "s_good", 800, 200)
	}
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("acme under-reporting not detected: got %v, want 1", got)
	}
	if got := anomalyCount(t, reg, "globex", "zero_delta"); got != 0 {
		t.Fatalf("globex's healthy session raised an anomaly (cross-tenant leak): got %v, want 0", got)
	}
}
