// SPDX-License-Identifier: MIT

package loadgen

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Result is the per-run aggregate. The format is intentionally close
// to k6's summary export so baselines remain comparable across
// tier-7a, tier-7b, and tier-12.
type Result struct {
	Scenario   string             `json:"scenario"`
	Profile    Profile            `json:"-"` // profile shape; not serialized
	StartedAt  time.Time          `json:"started_at"`
	Duration   time.Duration      `json:"duration_ns"`
	Iterations int64              `json:"iterations"`
	Errors     int64              `json:"errors"`
	Throughput float64            `json:"throughput_per_sec"`
	ErrorRate  float64            `json:"error_rate"`
	Latency    HistogramSnapshot  `json:"latency_seconds"`
	Custom     map[string]float64 `json:"custom,omitempty"`

	// errorSamples retains a bounded set of distinct error messages
	// from failed iterations. Used in Summary() for diagnosis.
	mu           sync.Mutex
	errorSamples []string
	errorSeen    map[string]bool
	errorSampleN int
}

// newResult returns a Result wired for a fresh run.
func newResult(scenarioName string, profile Profile) *Result {
	return &Result{
		Scenario:     scenarioName,
		Profile:      profile,
		Custom:       map[string]float64{},
		errorSeen:    map[string]bool{},
		errorSampleN: 5,
	}
}

// recordError stores up to errorSampleN distinct error strings for
// diagnosis. Safe to call concurrently.
func (r *Result) recordError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.errorSeen[msg] {
		return
	}
	if len(r.errorSamples) >= r.errorSampleN {
		return
	}
	r.errorSeen[msg] = true
	r.errorSamples = append(r.errorSamples, msg)
}

// AddCustom records a custom metric. Safe to call concurrently.
func (r *Result) AddCustom(name string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Custom[name] = value
}

// Summary returns a multi-line human-readable summary. Used in the
// test failure diagnosis path.
func (r *Result) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scenario=%s profile=%s\n", r.Scenario, profileSummary(r.Profile))
	fmt.Fprintf(&b, "  iterations=%d errors=%d (%.2f%%) throughput=%.1f/s\n",
		r.Iterations, r.Errors, r.ErrorRate*100, r.Throughput)
	fmt.Fprintf(&b, "  latency_s: avg=%.4f p50=%.4f p90=%.4f p95=%.4f p99=%.4f p99.9=%.4f max=%.4f\n",
		r.Latency.Avg, r.Latency.P50, r.Latency.P90, r.Latency.P95,
		r.Latency.P99, r.Latency.P999, r.Latency.Max)
	if len(r.Custom) > 0 {
		fmt.Fprintf(&b, "  custom: %s\n", customSummary(r.Custom))
	}
	r.mu.Lock()
	if len(r.errorSamples) > 0 {
		fmt.Fprintf(&b, "  error samples (%d distinct):\n", len(r.errorSamples))
		for _, m := range r.errorSamples {
			fmt.Fprintf(&b, "    - %s\n", m)
		}
	}
	r.mu.Unlock()
	return b.String()
}

// JSON returns the JSON-encoded form for baseline storage.
func (r *Result) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func profileSummary(p Profile) string {
	switch p.Kind {
	case ConstantVU:
		return fmt.Sprintf("ConstantVU(vu=%d, dur=%s)", p.VUs, p.Duration)
	case ConstantArrivalRate:
		return fmt.Sprintf("ConstantArrivalRate(rate=%d/s, vu=%d, dur=%s)", p.Rate, p.VUs, p.Duration)
	case RampingVU:
		return fmt.Sprintf("RampingVU(stages=%d, dur=%s)", len(p.RampStages), p.Duration)
	}
	return "(unknown)"
}

func customSummary(m map[string]float64) string {
	parts := []string{}
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%g", k, v))
	}
	return strings.Join(parts, " ")
}

// counter is a small wrapper for the per-run iteration and error
// counters. The driver uses sync/atomic on these.
type counter struct {
	n int64
}

func (c *counter) inc()       { atomic.AddInt64(&c.n, 1) }
func (c *counter) val() int64 { return atomic.LoadInt64(&c.n) }
