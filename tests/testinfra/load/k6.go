// SPDX-License-Identifier: MIT

// Package load wraps the §12.7 k6 load generator. Each scenario is
// a k6 script under tests/tier7_load/scenarios/<name>/main.js (or
// .ts); the Go wrapper invokes k6 with the canonical flag set and
// writes a baseline JSON document to tests/tier7_load/baselines/.
//
// Usage:
//
//	if !load.K6Available(t) {
//	    t.Skip("k6 not on PATH")
//	}
//	res := load.RunScenario(t, "streaming_reconnect", load.Options{
//	    Duration: 30 * time.Second,
//	    VUs:      50,
//	})
//	load.AssertBaseline(t, "streaming_reconnect", res, load.Threshold{P99: 0.15})
//
// The baseline file format mirrors the structure tests/tier7_load/
// baselines/startup_latency.json already uses: a JSON document with
// per-percentile latency, throughput, and error rate.
//
// # Environment variables
//
//	LENNY_UPDATE_BASELINE  When "1", AssertBaseline rewrites the
//	                       stored baseline JSON from the current
//	                       k6 run instead of comparing against it.
//	                       Use after a deliberate perf change has
//	                       been reviewed and approved.
package load

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Result is a scenario result.
type Result struct {
	Scenario   string             `json:"scenario"`
	Version    string             `json:"version"`
	RecordedAt string             `json:"recorded_at"`
	Duration   string             `json:"duration"`
	VUs        int                `json:"vus"`
	MetricMS   map[string]float64 `json:"metric_ms"`
	ErrorRate  float64            `json:"error_rate"`
	Throughput float64            `json:"throughput_per_sec"`
}

// Options configures a scenario run.
type Options struct {
	Duration time.Duration
	VUs      int
	BaseURL  string
	ExtraEnv map[string]string
}

// Threshold is the per-percentile regression budget.
type Threshold struct {
	P50  float64 // fractional regression budget for p50 (e.g. 0.15 = 15%)
	P90  float64
	P99  float64
	P999 float64
}

// K6Available reports whether the k6 binary is reachable on PATH.
func K6Available(t testing.TB) bool {
	t.Helper()
	_, err := exec.LookPath("k6")
	return err == nil
}

// SkipUnlessAvailable t.Skips when k6 is not on PATH. Matches the
// chaos.SkipUnlessAvailable / envtest.SkipUnlessAvailable
// convention. Callers that need conditional execution (a load
// scenario that has both a k6 and a fallback path) can still call
// K6Available directly.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !K6Available(t) {
		t.Skip("load.SkipUnlessAvailable: k6 not on PATH; install via `brew install k6` or per https://k6.io")
	}
}

// RunScenario invokes k6 against scenarios/<name>/main.js and parses
// the produced summary JSON.
func RunScenario(t testing.TB, scenario string, opts Options) Result {
	t.Helper()
	if !K6Available(t) {
		t.Skipf("RunScenario: k6 not on PATH; install via `brew install k6` or per https://k6.io")
	}
	scriptPath := filepath.Join(schematest.RepoRoot(t), "tests", "tier7_load", "scenarios", scenario, "main.js")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("RunScenario: scenario %s not present at %s", scenario, scriptPath)
	}

	outFile, err := os.CreateTemp("", scenario+"-*.json")
	if err != nil {
		t.Fatalf("k6 outfile: %v", err)
	}
	defer os.Remove(outFile.Name())
	_ = outFile.Close()

	args := []string{
		"run",
		"--summary-export", outFile.Name(),
	}
	if opts.Duration > 0 {
		args = append(args, "--duration", opts.Duration.String())
	}
	if opts.VUs > 0 {
		args = append(args, "--vus", fmt.Sprintf("%d", opts.VUs))
	}
	args = append(args, scriptPath)

	cmd := exec.Command("k6", args...)
	cmd.Env = append(
		os.Environ(),
		"K6_NO_USAGE_REPORT=true",
	)
	if opts.BaseURL != "" {
		cmd.Env = append(cmd.Env, "LENNY_BASE_URL="+opts.BaseURL)
	}
	for k, v := range opts.ExtraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	// k6 exits 99 when a threshold is crossed: the run completed and
	// the summary was written, the SLO was simply not met. A Tier-7
	// smoke run on a Kind cluster does not gate on the production SLO —
	// AssertBaseline's regression diff is the gate — so a threshold
	// cross is captured, not fatal. Any other non-zero exit is a real
	// k6 failure (a bad script, an unreachable target).
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 99 {
			t.Fatalf("k6 run %s: %v\n%s", scenario, err, out)
		}
		t.Logf("k6 run %s: a threshold was crossed (k6 exit 99); capturing the baseline regardless", scenario)
	}
	body, err := os.ReadFile(outFile.Name())
	if err != nil {
		t.Fatalf("read k6 summary: %v", err)
	}
	return parseK6Summary(t, scenario, opts, body)
}

// parseK6Summary extracts the canonical Result fields from a k6
// summary-export JSON document.
//
// The k6 `--summary-export` document places each metric's statistics
// directly on the metric object: `http_req_duration` carries `avg`,
// `med`, `p(90)`, `p(95)` (and any percentile named in the script's
// summaryTrendStats); `http_req_failed` carries `value` (the fail
// rate); `http_reqs` carries `rate`. Some k6 builds and the test
// fixtures wrap those statistics in a `values` sub-object instead.
// metricStat reads a named statistic under either layout so the parser
// tolerates both. If the k6 schema drifts further, this parser is the
// single point of update.
func parseK6Summary(t testing.TB, scenario string, opts Options, body []byte) Result {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("parse k6 summary: %v", err)
	}
	metrics, _ := raw["metrics"].(map[string]any)
	dur, _ := metrics["http_req_duration"].(map[string]any)

	pickMS := func(key string) float64 {
		v, _ := metricStat(dur, key)
		return v
	}

	res := Result{
		Scenario:   scenario,
		Version:    "0.1.0",
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Duration:   opts.Duration.String(),
		VUs:        opts.VUs,
		MetricMS: map[string]float64{
			"avg":  pickMS("avg"),
			"p50":  pickMS("med"),
			"p90":  pickMS("p(90)"),
			"p95":  pickMS("p(95)"),
			"p99":  pickMS("p(99)"),
			"p999": pickMS("p(99.9)"),
		},
	}
	if errs, ok := metrics["http_req_failed"].(map[string]any); ok {
		// The fail rate is `value` on the metric object; older fixtures
		// place it as `rate` under `values`.
		if rate, ok := metricStat(errs, "value"); ok {
			res.ErrorRate = rate
		} else if rate, ok := metricStat(errs, "rate"); ok {
			res.ErrorRate = rate
		}
	}
	if reqs, ok := metrics["http_reqs"].(map[string]any); ok {
		if rate, ok := metricStat(reqs, "rate"); ok {
			res.Throughput = rate
		}
	}
	return res
}

// metricStat reads a named statistic from a k6 metric object. k6's
// summary-export places statistics directly on the metric object; some
// builds and the test fixtures nest them under a `values` sub-object.
// metricStat checks the direct key first, then the `values` sub-object,
// and reports whether a float value was found.
func metricStat(metric map[string]any, key string) (float64, bool) {
	if metric == nil {
		return 0, false
	}
	if v, ok := metric[key].(float64); ok {
		return v, true
	}
	if values, ok := metric["values"].(map[string]any); ok {
		if v, ok := values[key].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

// AssertBaseline diffs res against the stored baseline at
// tests/tier7_load/baselines/<scenario>.json. Regressions beyond
// the threshold fail the test. When LENNY_UPDATE_BASELINE=1 the
// stored baseline is overwritten.
func AssertBaseline(t testing.TB, scenario string, res Result, threshold Threshold) {
	t.Helper()
	path := filepath.Join(schematest.RepoRoot(t), "tests", "tier7_load", "baselines", scenario+".json")
	if os.Getenv("LENNY_UPDATE_BASELINE") == "1" {
		writeBaseline(t, path, res)
		t.Logf("wrote baseline %s", path)
		return
	}
	stored, err := readBaseline(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("baseline %s does not exist. Run with LENNY_UPDATE_BASELINE=1 to seed it.", path)
		}
		t.Fatalf("read baseline: %v", err)
	}
	regressions := compareBaseline(stored, res, threshold)
	if len(regressions) > 0 {
		t.Errorf("baseline %s regressed:\n%s", scenario, strings.Join(regressions, "\n"))
	}
}

func readBaseline(path string) (Result, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(body, &r); err != nil {
		return Result{}, err
	}
	return r, nil
}

func writeBaseline(t testing.TB, path string, res Result) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir baseline dir: %v", err)
	}
	body, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
}

// compareBaseline returns one regression message per percentile that
// exceeds its threshold.
func compareBaseline(stored, got Result, t Threshold) []string {
	regs := []string{}
	check := func(label string, base, cur, bound float64) {
		if base == 0 {
			return
		}
		if bound <= 0 {
			return
		}
		delta := (cur - base) / base
		if delta > bound {
			regs = append(regs, fmt.Sprintf("  %s: %.2fms → %.2fms (%.1f%% > %.1f%%)",
				label, base, cur, delta*100, bound*100))
		}
	}
	check("p50", stored.MetricMS["p50"], got.MetricMS["p50"], t.P50)
	check("p90", stored.MetricMS["p90"], got.MetricMS["p90"], t.P90)
	check("p99", stored.MetricMS["p99"], got.MetricMS["p99"], t.P99)
	check("p999", stored.MetricMS["p999"], got.MetricMS["p999"], t.P999)
	return regs
}
