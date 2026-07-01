// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// scrapeMetrics is defined in gatewaymetrics_test.go.

// spec: §25.11 / §16.7 — the ArtifactStore replication residency
// preflight increments lenny_minio_replication_residency_violation_total
// (by region) and the shared lenny_data_residency_violation_total (by
// operation). F-12.5.20 / F-16.7.2.
func TestIncMinioReplicationResidencyViolation(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncMinioReplicationResidencyViolation("eu-west-1")
	m.IncMinioReplicationResidencyViolation("eu-west-1")
	m.IncMinioReplicationResidencyViolation("us-east-1")

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_minio_replication_residency_violation_total{region="eu-west-1"} 2`,
		`lenny_minio_replication_residency_violation_total{region="us-east-1"} 1`,
		// Both region bumps roll into the shared artifact_replication operation.
		`lenny_data_residency_violation_total{operation="artifact_replication"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §16.1 — the shared data-residency-violation counter is also
// reachable directly for non-replication operations. F-12.5.20.
func TestIncDataResidencyViolation(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDataResidencyViolation("erasure")

	body := scrapeMetrics(t, m)
	if want := `lenny_data_residency_violation_total{operation="erasure"} 1`; !strings.Contains(body, want) {
		t.Errorf("/metrics output missing %q\n---\n%s", want, body)
	}
}

// A nil receiver must never panic, matching the other Inc accessors.
func TestReplicationMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncMinioReplicationResidencyViolation("eu-west-1")
	m.IncDataResidencyViolation("erasure")
	m.SetMinioReplicationLag("eu-west-1", 12)
	m.AddMinioReplicationFailed("eu-west-1", 3)
}

// spec: §17.3 line 130 / §25.11 line 4085 — MeasureAll sets the
// per-region replication-lag gauge and advances the per-region
// replication-failure counter. F-17.3.7.
func TestMinioReplicationLagAndFailed(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetMinioReplicationLag("eu-west-1", 42)
	m.SetMinioReplicationLag("eu-west-1", 30) // latest sample wins (gauge)
	m.AddMinioReplicationFailed("eu-west-1", 2)
	m.AddMinioReplicationFailed("eu-west-1", 5)
	m.AddMinioReplicationFailed("eu-west-1", 0)  // a zero delta is dropped
	m.AddMinioReplicationFailed("us-east-1", -3) // a negative delta is dropped

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_minio_replication_lag_seconds{region="eu-west-1"} 30`,
		`lenny_minio_replication_failed_total{region="eu-west-1"} 7`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, `lenny_minio_replication_failed_total{region="us-east-1"}`) {
		t.Error("a negative delta created a us-east-1 failure series")
	}
}
