// SPDX-License-Identifier: MIT

package loadgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileReporterRecordAndFlush(t *testing.T) {
	dir := t.TempDir()
	r := &FileReporter{Dir: dir}
	r.Record("scenario_a", "default",
		Profile{Kind: ConstantVU, VUs: 16, Duration: 2 * time.Second},
		&Result{
			Iterations: 1000, Errors: 5, ErrorRate: 0.005, Throughput: 500.0,
			Latency: HistogramSnapshot{P50: 0.001, P95: 0.005, P99: 0.010, Max: 0.020},
			Custom:  map[string]float64{"ops": 999},
		})
	r.Record("scenario_b", "capacity",
		Profile{Kind: ConstantArrivalRate, VUs: 32, Rate: 100, Duration: 5 * time.Second},
		&Result{Iterations: 500, Errors: 0, Throughput: 100.0})

	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify report.json contains both entries.
	jsonBody, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	var entries []ReportEntry
	if err := json.Unmarshal(jsonBody, &entries); err != nil {
		t.Fatalf("parse report.json: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("entries=%d want 2", len(entries))
	}

	// Verify report.md is human-readable.
	mdBody, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatalf("read report.md: %v", err)
	}
	if !strings.Contains(string(mdBody), "scenario_a") {
		t.Error("report.md missing scenario_a")
	}
	if !strings.Contains(string(mdBody), "scenario_b") {
		t.Error("report.md missing scenario_b")
	}
}

func TestFileReporterFlushEmpty(t *testing.T) {
	dir := t.TempDir()
	r := &FileReporter{Dir: dir}
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Errorf("empty Flush did not create report.json: %v", err)
	}
}

func TestFileReporterRecordNilResult(t *testing.T) {
	dir := t.TempDir()
	r := &FileReporter{Dir: dir}
	r.Record("nil_scenario", "default", Profile{}, nil)
	_ = r.Flush()
	body, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	var entries []ReportEntry
	_ = json.Unmarshal(body, &entries)
	if len(entries) != 0 {
		t.Errorf("nil result produced %d entries", len(entries))
	}
}
