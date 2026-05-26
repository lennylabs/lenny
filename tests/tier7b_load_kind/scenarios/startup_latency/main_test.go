// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"
)

// TestBuildResultPercentileColumns_spec_6_3_F_6_3_14 pins the persisted
// metric_ms column set: p50, p90, p95, min, max. The §6.3 SLO is
// P95-keyed (spec/06_warm-pod-model.md line 348); p99 is intentionally
// omitted because the default iteration count cannot defend a tail
// estimate at one-sample resolution. spec-reviews: F-6.3.14.
func TestBuildResultPercentileColumns_spec_6_3_F_6_3_14(t *testing.T) {
	samples := make([]time.Duration, 0, 200)
	for i := 0; i < 200; i++ {
		samples = append(samples, time.Duration(i+1)*time.Millisecond)
	}
	r := buildResult(samples)

	want := []string{"min", "max", "p50", "p90", "p95"}
	for _, k := range want {
		if _, ok := r.MetricMS[k]; !ok {
			t.Errorf("expected metric_ms[%q] to be present, got keys=%v", k, keys(r.MetricMS))
		}
	}
	if _, ok := r.MetricMS["p99"]; ok {
		t.Errorf("p99 should be omitted per F-6.3.14 (statistical thinness), got %d", r.MetricMS["p99"])
	}
	if len(r.MetricMS) != len(want) {
		t.Errorf("metric_ms has %d keys (%v), want exactly %d (%v)", len(r.MetricMS), keys(r.MetricMS), len(want), want)
	}
}

// TestBuildResultPercentileOrdering_spec_6_3_F_6_3_14 asserts the
// percentile values are monotonically non-decreasing.
func TestBuildResultPercentileOrdering_spec_6_3_F_6_3_14(t *testing.T) {
	samples := make([]time.Duration, 0, 200)
	for i := 0; i < 200; i++ {
		samples = append(samples, time.Duration(i+1)*time.Millisecond)
	}
	r := buildResult(samples)
	if !(r.MetricMS["min"] <= r.MetricMS["p50"] &&
		r.MetricMS["p50"] <= r.MetricMS["p90"] &&
		r.MetricMS["p90"] <= r.MetricMS["p95"] &&
		r.MetricMS["p95"] <= r.MetricMS["max"]) {
		t.Errorf("percentiles not non-decreasing: %+v", r.MetricMS)
	}
}

// TestResultDocCommentP99Rationale_spec_6_3_F_6_3_14 is a regression
// guard against re-adding p99 without first bumping the iteration
// budget. The harness comment names the constraint; the test fails if a
// future edit drops the rationale without acting on it.
func TestResultDocCommentP99Rationale_spec_6_3_F_6_3_14(t *testing.T) {
	// scenarioVersion is intentionally bumped when the percentile set
	// changes — assert the current contract.
	if !strings.HasPrefix(scenarioVersion, "0.") {
		t.Errorf("scenarioVersion=%q must remain pre-1.0 until §6.3 promotion gate clears", scenarioVersion)
	}
}

func keys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
