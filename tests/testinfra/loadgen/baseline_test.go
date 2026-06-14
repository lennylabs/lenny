// SPDX-License-Identifier: MIT

package loadgen

import (
	"testing"
	"time"
)

func TestFileBaselineStoreRoundTrip(t *testing.T) {
	store := &FileBaselineStore{Dir: t.TempDir()}
	in := BaselineFromResult("slot_counter_race",
		Profile{Kind: ConstantVU, VUs: 50, Duration: 3 * time.Second},
		&Result{
			Iterations: 1000, Throughput: 333.0, ErrorRate: 0.001,
			Latency: HistogramSnapshot{P95: 0.012, P99: 0.020},
		})
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load("slot_counter_race")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load: not found")
	}
	if got.Scenario != "slot_counter_race" {
		t.Errorf("scenario=%q want slot_counter_race", got.Scenario)
	}
	if got.Throughput != 333.0 {
		t.Errorf("throughput=%v want 333", got.Throughput)
	}
}

func TestFileBaselineStoreMissingReturnsOkFalse(t *testing.T) {
	store := &FileBaselineStore{Dir: t.TempDir()}
	_, ok, err := store.Load("missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for missing baseline")
	}
}

func TestCompareToBaselineDetectsThroughputDrop(t *testing.T) {
	base := &Baseline{Throughput: 1000.0, Latency: HistogramSnapshot{P95: 0.010, P99: 0.020}}
	current := &Result{Throughput: 800.0, Latency: HistogramSnapshot{P95: 0.010, P99: 0.020}}
	t10 := Threshold{ThroughputDropPct: 15}
	regs := CompareToBaseline(current, base, t10)
	if len(regs) != 1 || regs[0].Metric != "throughput_per_sec" {
		t.Errorf("expected one throughput regression, got %+v", regs)
	}
}

func TestCompareToBaselineDetectsLatencyRise(t *testing.T) {
	base := &Baseline{Throughput: 1000.0, Latency: HistogramSnapshot{P95: 0.010, P99: 0.020}}
	current := &Result{Throughput: 1000.0, Latency: HistogramSnapshot{P95: 0.025, P99: 0.050}}
	t10 := Threshold{LatencyP95RisePct: 20, LatencyP99RisePct: 20}
	regs := CompareToBaseline(current, base, t10)
	if len(regs) != 2 {
		t.Errorf("expected 2 latency regressions, got %d: %+v", len(regs), regs)
	}
}

func TestCompareToBaselineDetectsErrorRateAbs(t *testing.T) {
	base := &Baseline{Throughput: 1000.0}
	current := &Result{Throughput: 1000.0, ErrorRate: 0.05}
	t10 := Threshold{ErrorRateAbs: 0.01}
	regs := CompareToBaseline(current, base, t10)
	if len(regs) != 1 || regs[0].Metric != "error_rate" {
		t.Errorf("expected error_rate regression, got %+v", regs)
	}
}

func TestCompareToBaselineEmptyWhenWithinBudget(t *testing.T) {
	base := &Baseline{Throughput: 1000.0, Latency: HistogramSnapshot{P95: 0.010, P99: 0.020}}
	current := &Result{Throughput: 950.0, ErrorRate: 0.001, Latency: HistogramSnapshot{P95: 0.011, P99: 0.021}}
	t10 := Threshold{ThroughputDropPct: 10, LatencyP95RisePct: 20, LatencyP99RisePct: 20, ErrorRateAbs: 0.01}
	regs := CompareToBaseline(current, base, t10)
	if len(regs) != 0 {
		t.Errorf("expected zero regressions, got %+v", regs)
	}
}

func TestCompareToBaselineNilSafe(t *testing.T) {
	if regs := CompareToBaseline(nil, nil, Threshold{}); regs != nil {
		t.Error("nil inputs should yield nil regressions")
	}
}
