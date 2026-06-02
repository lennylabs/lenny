// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
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
}
