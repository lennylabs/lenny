// SPDX-License-Identifier: MIT

package loadgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reporter aggregates per-scenario Results into a unified tier-7a
// report. Default tier-7a runs use a nil Reporter (no I/O cost);
// opt in via the LENNY_TIER7A_REPORT_DIR env var which the test
// scaffolds resolve to a FileReporter.
type Reporter interface {
	// Record a scenario's result. Mode is "default", "capacity", or
	// any other label the caller wants. Safe to call concurrently.
	Record(scenario string, mode string, profile Profile, result *Result)
	// Flush writes the accumulated report and clears the buffer.
	Flush() error
}

// ReportEntry is one row in the unified report.
type ReportEntry struct {
	Scenario   string             `json:"scenario"`
	Mode       string             `json:"mode"`
	RecordedAt time.Time          `json:"recorded_at"`
	Profile    profileSummaryJSON `json:"profile"`
	Iterations int64              `json:"iterations"`
	Errors     int64              `json:"errors"`
	ErrorRate  float64            `json:"error_rate"`
	Throughput float64            `json:"throughput_per_sec"`
	Latency    HistogramSnapshot  `json:"latency_seconds"`
	Custom     map[string]float64 `json:"custom,omitempty"`
}

// profileSummaryJSON serialises a Profile in a stable shape.
type profileSummaryJSON struct {
	Kind       string `json:"kind"`
	VUs        int    `json:"vus"`
	Rate       int    `json:"rate,omitempty"`
	DurationNs int64  `json:"duration_ns"`
}

func encodeProfile(p Profile) profileSummaryJSON {
	kind := "unknown"
	switch p.Kind {
	case ConstantVU:
		kind = "constant_vu"
	case ConstantArrivalRate:
		kind = "constant_arrival_rate"
	case RampingVU:
		kind = "ramping_vu"
	}
	return profileSummaryJSON{
		Kind:       kind,
		VUs:        p.VUs,
		Rate:       p.Rate,
		DurationNs: p.Duration.Nanoseconds(),
	}
}

// FileReporter writes the report to a directory. The output is
// JSON (machine-readable) plus a Markdown summary (human-readable).
type FileReporter struct {
	Dir string

	mu      sync.Mutex
	entries []ReportEntry
}

// Record buffers an entry; the call is cheap and lock-protected.
func (r *FileReporter) Record(scenario, mode string, profile Profile, result *Result) {
	if result == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, ReportEntry{
		Scenario:   scenario,
		Mode:       mode,
		RecordedAt: time.Now().UTC(),
		Profile:    encodeProfile(profile),
		Iterations: result.Iterations,
		Errors:     result.Errors,
		ErrorRate:  result.ErrorRate,
		Throughput: result.Throughput,
		Latency:    result.Latency,
		Custom:     copyCustom(result.Custom),
	})
}

func copyCustom(m map[string]float64) map[string]float64 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Flush writes the buffered entries to Dir.
//
// The output is two files:
//
//   - report.json  — every recorded entry as a JSON array.
//   - report.md    — a one-line-per-scenario Markdown table.
//
// Both files are overwritten on each Flush. Callers run Flush once
// at the end of the tier-7a invocation.
func (r *FileReporter) Flush() error {
	if r.Dir == "" {
		return fmt.Errorf("loadgen.FileReporter: Dir is empty")
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return err
	}
	r.mu.Lock()
	entries := append([]ReportEntry{}, r.entries...)
	r.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Mode != entries[j].Mode {
			return entries[i].Mode < entries[j].Mode
		}
		return entries[i].Scenario < entries[j].Scenario
	})
	if err := writeJSON(filepath.Join(r.Dir, "report.json"), entries); err != nil {
		return err
	}
	if err := writeMarkdown(filepath.Join(r.Dir, "report.md"), entries); err != nil {
		return err
	}
	return nil
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func writeMarkdown(path string, entries []ReportEntry) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tier-7a load_local report\n\n")
	fmt.Fprintf(&b, "Recorded at %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "%d entries.\n\n", len(entries))
	if len(entries) == 0 {
		return os.WriteFile(path, []byte(b.String()), 0o644)
	}
	fmt.Fprintf(&b, "| Scenario | Mode | Profile | Iter | Errors | Rate (%%) | TPS | P50 (s) | P95 (s) | P99 (s) | Max (s) |\n")
	fmt.Fprintf(&b, "|:--|:--|:--|--:|--:|--:|--:|--:|--:|--:|--:|\n")
	for _, e := range entries {
		profile := fmt.Sprintf("%s vu=%d", e.Profile.Kind, e.Profile.VUs)
		if e.Profile.Rate > 0 {
			profile += fmt.Sprintf(" rate=%d", e.Profile.Rate)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | %.3f | %.1f | %.4f | %.4f | %.4f | %.4f |\n",
			e.Scenario, e.Mode, profile, e.Iterations, e.Errors,
			e.ErrorRate*100, e.Throughput,
			e.Latency.P50, e.Latency.P95, e.Latency.P99, e.Latency.Max)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
