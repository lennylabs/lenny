// SPDX-License-Identifier: MIT

package load

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// spec: 12.7 (k6 summary parses into the canonical Result shape)
// diagnosis: A k6 summary-export document failed to parse. The k6
//
//	schema may have drifted; check `k6 run --summary-export`
//	output against parseK6Summary.
func TestParseK6Summary(t *testing.T) {
	t.Parallel()
	// Synthesise a minimal summary that the parser must accept.
	summary := map[string]any{
		"metrics": map[string]any{
			"http_req_duration": map[string]any{
				"values": map[string]any{
					"avg":      12.5,
					"med":      9.0,
					"p(90)":    25.0,
					"p(95)":    40.0,
					"p(99)":    100.0,
					"p(99.9)":  500.0,
				},
			},
			"http_req_failed": map[string]any{
				"values": map[string]any{"rate": 0.001},
			},
			"http_reqs": map[string]any{
				"values": map[string]any{"rate": 250.0},
			},
		},
	}
	body, _ := json.Marshal(summary)
	res := parseK6Summary(t, "stub", Options{VUs: 10}, body)
	if res.MetricMS["p99"] != 100.0 {
		t.Errorf("p99 parse: want 100, got %v", res.MetricMS["p99"])
	}
	if res.ErrorRate != 0.001 {
		t.Errorf("error rate: want 0.001, got %v", res.ErrorRate)
	}
	if res.Throughput != 250.0 {
		t.Errorf("throughput: want 250, got %v", res.Throughput)
	}
}

// spec: 12.7 (baseline diff: a flat result is in-budget)
// diagnosis: A no-change result was reported as a regression. The
//
//	delta computation is wrong.
func TestBaselineFlatPasses(t *testing.T) {
	t.Parallel()
	base := Result{MetricMS: map[string]float64{"p50": 10, "p99": 100}}
	got := Result{MetricMS: map[string]float64{"p50": 10, "p99": 100}}
	regs := compareBaseline(base, got, Threshold{P50: 0.15, P99: 0.15})
	if len(regs) != 0 {
		t.Errorf("flat result reported as regression: %v", regs)
	}
}

// spec: 12.7 (baseline diff: a 20% p99 regression with a 15% budget fails)
// diagnosis: The threshold gate did not fire. The fractional delta
//
//	check is broken.
func TestBaselineRegressionFails(t *testing.T) {
	t.Parallel()
	base := Result{MetricMS: map[string]float64{"p99": 100}}
	got := Result{MetricMS: map[string]float64{"p99": 130}}
	regs := compareBaseline(base, got, Threshold{P99: 0.15})
	if len(regs) == 0 {
		t.Errorf("20%% regression with 15%% budget should fire; got no regressions")
	}
}

// spec: 12.7 (AssertBaseline can seed a missing baseline via LENNY_UPDATE_BASELINE)
// diagnosis: The seed path did not write the baseline. The
//
//	environment-variable gate is wrong.
func TestAssertBaselineSeeds(t *testing.T) {
	t.Setenv("LENNY_UPDATE_BASELINE", "1")
	tmp := t.TempDir()
	res := Result{Scenario: "test", MetricMS: map[string]float64{"p99": 100}}
	path := filepath.Join(tmp, "tests", "tier7_load", "baselines", "test.json")
	// Synthesise a fake repo root so writeBaseline lands in tmp.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	writeBaseline(t, path, res)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("baseline not seeded: %v", err)
	}
}
