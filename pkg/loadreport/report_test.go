// SPDX-License-Identifier: MIT

package loadreport

import (
	"strings"
	"testing"
	"time"
)

func TestRenderProducesHTML(t *testing.T) {
	run := &Run{
		ID:             "run-test-1",
		Branch:         "load-test-overhaul",
		Commit:         "abcdef1",
		ImageTag:       "v0",
		Scale:          "small",
		ClusterRelease: "lenny-load-small",
		StartedAt:      time.Now().Add(-10 * time.Minute),
		CompletedAt:    time.Now(),
		Scenarios: []ScenarioResult{
			{
				Name:       "session_throughput",
				Status:     "PASS",
				Throughput: 123.5,
				ErrorRate:  0.001,
				Latency: Latency{
					P50: 0.02, P95: 0.18, P99: 0.42, P999: 0.85, Max: 1.2,
				},
			},
		},
		Resources: ResourceSeries{
			GatewayCPU: []Point{
				{T: time.Now().Add(-1 * time.Minute), V: 0.4},
				{T: time.Now().Add(-30 * time.Second), V: 0.6},
				{T: time.Now(), V: 0.45},
			},
		},
	}
	body, err := RenderBytes(run)
	if err != nil {
		t.Fatalf("RenderBytes: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "Lenny load report") {
		t.Error("missing title in rendered HTML")
	}
	if !strings.Contains(s, "session_throughput") {
		t.Error("scenario row not rendered")
	}
	if !strings.Contains(s, "plotly") {
		t.Error("Plotly script reference missing")
	}
	if !strings.Contains(s, `"GatewayCPU"`) {
		t.Error("resource series not inlined as JSON")
	}
}

func TestRenderNilRun(t *testing.T) {
	if _, err := RenderBytes(nil); err == nil {
		t.Error("expected error on nil Run")
	}
}
