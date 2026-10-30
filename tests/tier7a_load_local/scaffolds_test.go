// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local entry point. Each scenario under
// tests/tier7a_load_local/scenarios/<name>/scenario.go registers itself
// with loadgen.DefaultRegistry() via init(); TestScenarios iterates the
// registry and runs every entry under the load_local build tag with
// the race detector enabled.
//
// TESTING.md §12.7.a defines this tier's contract: per-scenario
// budget ≤ 15s, total tier ≤ 5 min.
//
// Optional modes — all opt-in via env vars:
//
//   LENNY_TIER7A_REPORT_DIR=<dir>
//     Emit a per-scenario report (report.json + report.md) summarising
//     throughput and latency for every scenario. Off by default.
//
//   LENNY_TIER7A_BASELINE_DIR=<dir>
//     Compare each scenario's result against <dir>/<scenario>.json.
//     Thresholds:
//       LENNY_TIER7A_THROUGHPUT_DROP_PCT (default 15)
//       LENNY_TIER7A_P95_RISE_PCT        (default 25)
//       LENNY_TIER7A_P99_RISE_PCT        (default 25)
//       LENNY_TIER7A_ERROR_RATE_ABS      (default 0.01)
//     Test fails when any threshold is exceeded.
//
//   LENNY_TIER7A_UPDATE_BASELINES=1 LENNY_TIER7A_BASELINE_DIR=<dir>
//     Write the current run's results as the new baseline.
//
//   LENNY_TIER7A_CAPACITY=1
//     For scenarios implementing loadgen.RampableScenario, ramp
//     through ascending profiles and record the knee (last profile
//     that passed). Other scenarios are skipped in this mode.

package tier7a_load_local_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	// Importing the scenarios package triggers every scenario
	// subpackage's init() through blank imports.
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios"
)

// perScenarioBudget bounds a single scenario's wall-clock per
// TESTING.md §12.7.a.
const perScenarioBudget = 15 * time.Second

// perRampBudget bounds the full capacity-discovery ramp for one
// scenario (each profile gets its own duration plus overhead).
const perRampBudget = 90 * time.Second

// spec: §12.7.a.
// diagnosis: a failure means one of the §12.7.a local load scenarios
// breaches its per-scenario or per-ramp wall-clock budget, or its
// concurrency/ordering/atomicity assertion does not hold under load.
func TestScenarios(t *testing.T) {
	// Scenarios run sequentially. Each scenario boots its own
	// in-process surfaces (miniredis, fakekube, the inproc gateway
	// HTTP listener) and many of them are network-bound on
	// loopback; running 18 in parallel under -race exhausts OS
	// resources on a developer laptop. Sequential execution still
	// fits within the §12.7.a 5-minute wall-clock budget.
	registry := loadgen.DefaultRegistry()
	if registry.Len() == 0 {
		t.Skip("no tier-7a scenarios registered; scenarios land in Wave 2 and Wave 3")
	}

	reporter, baselineStore, threshold, updateBaselines, capacityMode := resolveEnv(t)

	for _, name := range registry.Names() {
		name := name
		t.Run(name, func(t *testing.T) {
			scenario := registry.MustGet(name)

			if capacityMode {
				runCapacity(t, scenario, reporter)
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), perScenarioBudget)
			defer cancel()
			profile := scenario.DefaultProfile()
			result, err := loadgen.Run(ctx, scenario, profile)
			if err != nil {
				t.Fatalf("loadgen.Run %s: %v", name, err)
			}
			if err := scenario.Assert(result); err != nil {
				t.Fatalf("SLO assertion failed for %s:\n%v\n\nresult:\n%s", name, err, result.Summary())
			}

			if reporter != nil {
				reporter.Record(name, "default", profile, result)
			}
			if baselineStore != nil {
				maybeCompareOrUpdate(t, baselineStore, threshold, updateBaselines, name, profile, result)
			}

			t.Logf("\n%s", result.Summary())
		})
	}

	if reporter != nil {
		if err := reporter.Flush(); err != nil {
			t.Errorf("reporter Flush: %v", err)
		}
	}
}

// runCapacity executes the ramp for one scenario when LENNY_TIER7A_CAPACITY=1.
func runCapacity(t *testing.T, scenario loadgen.Scenario, reporter loadgen.Reporter) {
	ramper, ok := scenario.(loadgen.RampableScenario)
	if !ok {
		t.Skipf("scenario %s does not implement RampableScenario (no capacity profile)", scenario.Name())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), perRampBudget)
	defer cancel()
	res, err := loadgen.FindCapacityKnee(ctx, ramper)
	if err != nil {
		t.Fatalf("FindCapacityKnee %s: %v", scenario.Name(), err)
	}
	if !res.KneeFound {
		t.Errorf("scenario %s failed even the smallest ramp profile: %v", scenario.Name(), res.BreakingError)
	} else {
		t.Logf("scenario %s knee: VUs=%d rate=%d duration=%s; broke at VUs=%d (reason: %v)",
			scenario.Name(), res.Knee.VUs, res.Knee.Rate, res.Knee.Duration, res.Breaking.VUs, res.BreakingError)
	}
	if reporter != nil && res.KneeResult != nil {
		reporter.Record(scenario.Name(), "capacity-knee", res.Knee, res.KneeResult)
	}
	if reporter != nil && res.BreakingResult != nil {
		reporter.Record(scenario.Name(), "capacity-broke", res.Breaking, res.BreakingResult)
	}
}

// resolveEnv parses every optional-mode environment variable into a
// (reporter, baselineStore, threshold, updateBaselines, capacityMode) tuple.
func resolveEnv(t *testing.T) (loadgen.Reporter, loadgen.BaselineStore, loadgen.Threshold, bool, bool) {
	t.Helper()
	var reporter loadgen.Reporter
	if dir := os.Getenv("LENNY_TIER7A_REPORT_DIR"); dir != "" {
		reporter = &loadgen.FileReporter{Dir: dir}
		t.Logf("tier-7a report mode: writing to %s", dir)
	}
	var store loadgen.BaselineStore
	if dir := os.Getenv("LENNY_TIER7A_BASELINE_DIR"); dir != "" {
		store = &loadgen.FileBaselineStore{Dir: dir}
		t.Logf("tier-7a baseline mode: dir=%s", dir)
	}
	threshold := loadgen.Threshold{
		ThroughputDropPct: envFloat("LENNY_TIER7A_THROUGHPUT_DROP_PCT", 15),
		LatencyP95RisePct: envFloat("LENNY_TIER7A_P95_RISE_PCT", 25),
		LatencyP99RisePct: envFloat("LENNY_TIER7A_P99_RISE_PCT", 25),
		ErrorRateAbs:      envFloat("LENNY_TIER7A_ERROR_RATE_ABS", 0.01),
	}
	updateBaselines := os.Getenv("LENNY_TIER7A_UPDATE_BASELINES") == "1"
	capacityMode := os.Getenv("LENNY_TIER7A_CAPACITY") == "1"
	return reporter, store, threshold, updateBaselines, capacityMode
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func maybeCompareOrUpdate(t *testing.T, store loadgen.BaselineStore, threshold loadgen.Threshold, update bool, name string, profile loadgen.Profile, result *loadgen.Result) {
	if update {
		baseline := loadgen.BaselineFromResult(name, profile, result)
		if err := store.Save(baseline); err != nil {
			t.Errorf("baseline save %s: %v", name, err)
		}
		return
	}
	base, ok, err := store.Load(name)
	if err != nil {
		t.Errorf("baseline load %s: %v", name, err)
		return
	}
	if !ok {
		// Baseline missing is a soft skip; useful when adding new
		// scenarios that don't yet have a recorded baseline.
		return
	}
	regs := loadgen.CompareToBaseline(result, base, threshold)
	if len(regs) > 0 {
		var msg string
		for _, r := range regs {
			msg += "  - " + r.String() + "\n"
		}
		t.Fatalf("baseline regression for %s:\n%s\ncurrent result:\n%s", name, msg, result.Summary())
	}
}
