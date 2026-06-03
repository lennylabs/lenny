// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §16.8 line 704 / §25.4 line 2507 — publishSelfHealthMetric maps
// each self-health check onto lenny_ops_self_health_status{check} with the
// 0=healthy, 1=degraded, 2=unhealthy encoding so LenniOpsSelfHealthDegraded
// has a per-check source on the §16.9 /metrics scrape.
func TestPublishSelfHealthMetric_PerCheckEncoding_spec_16_8(t *testing.T) {
	if opsSelfHealthStatus == nil {
		t.Fatal("lenny_ops_self_health_status gauge failed to register")
	}
	report := opsservice.SelfHealthReport{
		Status: opsservice.StatusUnhealthy,
		Checks: []opsservice.CheckResult{
			{Name: "postgres_pool", Status: opsservice.StatusHealthy},
			{Name: "redis_consumer_lag", Status: opsservice.StatusDegraded},
			{Name: "k8s_api", Status: opsservice.StatusUnhealthy},
		},
	}
	publishSelfHealthMetric(report)

	for check, want := range map[string]float64{
		"postgres_pool":      0,
		"redis_consumer_lag": 1,
		"k8s_api":            2,
	} {
		if got := testutil.ToFloat64(opsSelfHealthStatus.WithLabelValues(check)); got != want {
			t.Errorf("lenny_ops_self_health_status{check=%q} = %v, want %v", check, got, want)
		}
	}
}

// spec: §25.4 — a status recovery moves a check's gauge back down; the
// publisher overwrites the prior series value rather than leaving a stale
// unhealthy reading after the dependency recovers.
func TestPublishSelfHealthMetric_OverwritesOnRecovery_spec_16_8(t *testing.T) {
	if opsSelfHealthStatus == nil {
		t.Fatal("lenny_ops_self_health_status gauge failed to register")
	}
	degraded := opsservice.SelfHealthReport{Checks: []opsservice.CheckResult{
		{Name: "webhook_backlog", Status: opsservice.StatusUnhealthy},
	}}
	publishSelfHealthMetric(degraded)
	if got := testutil.ToFloat64(opsSelfHealthStatus.WithLabelValues("webhook_backlog")); got != 2 {
		t.Fatalf("pre-recovery gauge = %v, want 2", got)
	}
	recovered := opsservice.SelfHealthReport{Checks: []opsservice.CheckResult{
		{Name: "webhook_backlog", Status: opsservice.StatusHealthy},
	}}
	publishSelfHealthMetric(recovered)
	if got := testutil.ToFloat64(opsSelfHealthStatus.WithLabelValues("webhook_backlog")); got != 0 {
		t.Fatalf("post-recovery gauge = %v, want 0", got)
	}
}
