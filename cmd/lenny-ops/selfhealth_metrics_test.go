// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §25.9 / §16.8 / F-16.8.4 — the §25.6 diagnostic rate limiter's
// Drop decision increments lenny_audit_rate_limited_total via the
// DiagnosticsAuditConfig.RateLimited callback buildDiagnosticsAudit wires.
// The counter is registered on the default registry and scraped by the
// §16.9 /metrics surface (F-16.8.1), so the Drop doc-comment's promise
// ("the caller increments lenny_audit_rate_limited_total") is fulfilled
// end to end.
func TestDiagnosticsAuditRateLimitedIncrementsCounter_spec_16_8_4(t *testing.T) {
	if diagnosticsAuditRateLimited == nil {
		t.Fatal("lenny_audit_rate_limited_total counter failed to register")
	}
	cfg := buildDiagnosticsAudit(60, opsaudit.New(nil))
	if cfg.RateLimited == nil {
		t.Fatal("buildDiagnosticsAudit did not wire the RateLimited callback")
	}
	before := testutil.ToFloat64(diagnosticsAuditRateLimited.WithLabelValues("pool_diagnosed", "sa-probe"))
	cfg.RateLimited("pool_diagnosed", "sa-probe")
	after := testutil.ToFloat64(diagnosticsAuditRateLimited.WithLabelValues("pool_diagnosed", "sa-probe"))
	if after != before+1 {
		t.Errorf("lenny_audit_rate_limited_total = %v, want %v after one Drop", after, before+1)
	}
}

// spec: §16.8 line 704 / §25.4 line 2507 — publishSelfHealthMetric maps
// each self-health check onto lenny_ops_self_health_status{check} with the
// 0=healthy, 1=degraded, 2=unhealthy encoding so OpsSelfHealthDegraded
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
