// SPDX-License-Identifier: MIT

// Package scenarios contains executable Tier 7 load and latency
// scenarios. startup_latency.go is the Phase 2 baseline: it measures
// the wall-clock from process start to first JSONL output for the
// Basic-level echo runtime, repeats the measurement N times, and
// writes a baseline JSON document that subsequent runs compare
// against.
//
// The scenario is invoked by `lenny-test --tier load --scenario startup_latency`
// or directly as `go run ./tests/tier7b_load_kind/scenarios/startup_latency.go`.
// See TESTING.md §12.7 and §13.3.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const scenarioVersion = "0.2.0-phase2"

// Result is the persisted baseline document. The percentile set reports
// p50, p90, and p95 only: the §6.3 SLO targets are P95-keyed
// (spec/06_warm-pod-model.md line 348, 2s runc / 5s gVisor), and p99
// requires roughly 1000+ samples to estimate with one-sample resolution
// — the default iteration count cannot defend a tail-latency claim, so
// the harness omits the p99 column rather than persist a misleading
// number. spec-reviews: F-6.3.14.
type Result struct {
	Scenario string `json:"scenario"`
	Version  string `json:"version"`
	// The measured binary is rebuilt into a fresh temp directory on
	// every run, so its absolute path is recording-host trivia rather
	// than a reproducibility input. It is deliberately not persisted to
	// the baseline (it leaked a developer's private /var/folders path
	// into version control). spec-reviews: F-6.3.13.
	Iterations     int              `json:"iterations"`
	RecordedAt     string           `json:"recorded_at"`
	MetricMS       map[string]int64 `json:"metric_ms"`
	Iterations_raw []int64          `json:"iterations_raw_ms"`
	Notes          string           `json:"notes,omitempty"`
}

func main() {
	var (
		binary = flag.String("binary", "", "adapter binary to measure (built into a temp dir if empty)")
		// 200 iterations are roughly the conventional minimum for a
		// stable p95 estimate at one-sample resolution; below that the
		// p95 column is not a defensible reference. p99 is not reported
		// at all because it needs ~1000+ samples (see Result doc-comment).
		// spec-reviews: F-6.3.14.
		iters   = flag.Int("iterations", 200, "number of samples (≥ ~200 recommended for a stable p95)")
		output  = flag.String("output", "", "write JSON to this path (default: tests/tier7b_load_kind/baselines/startup_latency.json)")
		stdout  = flag.Bool("stdout", false, "also emit JSON to stdout")
		warmup  = flag.Int("warmup", 3, "warm-up iterations excluded from the metric (the OS file cache cools at process start)")
		timeout = flag.Duration("timeout", 10*time.Second, "per-iteration timeout")
	)
	flag.Parse()

	if *iters < 1 {
		fmt.Fprintln(os.Stderr, "iterations must be >= 1")
		os.Exit(2)
	}
	if *warmup < 0 {
		fmt.Fprintln(os.Stderr, "warmup must be >= 0")
		os.Exit(2)
	}

	bin := *binary
	if bin == "" {
		var cleanup func()
		var err error
		bin, cleanup, err = buildEcho()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build echo: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()
	}

	for i := 0; i < *warmup; i++ {
		_, _ = measureOnce(bin, *timeout)
	}

	samples := make([]time.Duration, 0, *iters)
	for i := 0; i < *iters; i++ {
		d, err := measureOnce(bin, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "iteration %d: %v\n", i, err)
			os.Exit(1)
		}
		samples = append(samples, d)
	}

	r := buildResult(samples)
	r.Notes = fmt.Sprintf("warmup=%d, timeout=%s", *warmup, *timeout)

	outPath := *output
	if outPath == "" {
		root, err := repoRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "repo root: %v\n", err)
			os.Exit(1)
		}
		outPath = filepath.Join(root, "tests", "tier7b_load_kind", "baselines", "startup_latency.json")
	}
	if err := writeJSON(outPath, r); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("startup_latency baseline written: %s\n", outPath)
	fmt.Printf("  iterations: %d (after %d warm-up samples discarded)\n", r.Iterations, *warmup)
	fmt.Printf("  p50:  %d ms\n", r.MetricMS["p50"])
	fmt.Printf("  p90:  %d ms\n", r.MetricMS["p90"])
	fmt.Printf("  p95:  %d ms\n", r.MetricMS["p95"])
	fmt.Printf("  max:  %d ms\n", r.MetricMS["max"])

	if *stdout {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	}
}

// measureOnce launches the adapter, waits for the heartbeat_ack on
// stdout (the first byte the adapter writes after receiving a heartbeat),
// and returns the elapsed time. The heartbeat round-trip is a tighter
// proxy for "the adapter is alive" than process-exit timing.
func measureOnce(bin string, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(stdin, `{"type":"heartbeat","ts":0}`+"\n"); err != nil {
		return 0, err
	}

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		return 0, fmt.Errorf("no stdout before EOF")
	}
	elapsed := time.Since(start)

	_ = stdin.Close()
	_ = cmd.Wait()
	return elapsed, nil
}

func buildResult(samples []time.Duration) Result {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	r := Result{
		Scenario:   "startup_latency",
		Version:    scenarioVersion,
		Iterations: len(samples),
		RecordedAt: time.Now().UTC().Format(time.RFC3339Nano),
		MetricMS:   map[string]int64{},
	}
	r.MetricMS["min"] = samples[0].Milliseconds()
	r.MetricMS["max"] = samples[len(samples)-1].Milliseconds()
	r.MetricMS["p50"] = pct(samples, 0.50).Milliseconds()
	r.MetricMS["p90"] = pct(samples, 0.90).Milliseconds()
	// §6.3 SLO is P95-keyed (spec/06_warm-pod-model.md line 348). p99
	// is intentionally omitted — see Result doc-comment.
	// spec-reviews: F-6.3.14.
	r.MetricMS["p95"] = pct(samples, 0.95).Milliseconds()

	r.Iterations_raw = make([]int64, len(samples))
	for i, s := range samples {
		r.Iterations_raw[i] = s.Milliseconds()
	}
	return r
}

// pct returns the requested quantile from a sorted sample slice.
// p in [0,1]. For an out-of-range p, returns the boundary value.
func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

// buildEcho compiles cmd/runtimes/echo into a temp dir and returns the
// path plus a cleanup function.
func buildEcho() (string, func(), error) {
	tmp, err := os.MkdirTemp("", "echo-load-*")
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(tmp, "echo")
	root, err := repoRoot()
	if err != nil {
		os.RemoveAll(tmp)
		return "", nil, err
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/runtimes/echo")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmp)
		return "", nil, err
	}
	return bin, func() { os.RemoveAll(tmp) }, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("repoRoot: no go.mod above %s", wd)
}

var _ = strings.TrimSpace
