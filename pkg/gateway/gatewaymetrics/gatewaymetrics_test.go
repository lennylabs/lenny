// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

func TestMetricsHandlerExposesRegisteredMetrics(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Drive one request so the request-counter + duration vecs have
	// a child series (a label-vec with no observations emits nothing
	// per the Prometheus exposition model). The gauge is registered
	// with a child series at construction and shows immediately.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/healthz" })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_requests_total",
		"lenny_gateway_request_duration_seconds",
		"lenny_gateway_active_sessions",
		// §10.1 / §4.1 horizontal-scaling leading indicators are
		// registered with a child series at construction, so they
		// appear on /metrics immediately.
		"lenny_gateway_active_streams",
		"lenny_gateway_request_queue_depth",
		"lenny_gateway_rejection_rate",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

func TestHorizontalScalingGaugesExposeValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetActiveStreams(7)
	m.SetRequestQueueDepth(12)
	m.SetRejectionRate(3)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_active_streams 7",
		"lenny_gateway_request_queue_depth 12",
		"lenny_gateway_rejection_rate 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestSetStorageQuotaExposesGauges(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetStorageQuota("acme", 500, 1000)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_storage_quota_bytes_used{tenant_id="acme"} 500`,
		`lenny_tenant_storage_quota_bytes{tenant_id="acme"} 1000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestCircuitBreakerMetricsExposeGauges(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetCircuitBreakerOpen("rt-emergency", true)
	m.SetCircuitBreakerOpen("rt-calm", false)
	m.SetCircuitBreakerCache(0, true)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_circuit_breaker_open{circuit_name="rt-emergency"} 1`,
		`lenny_circuit_breaker_open{circuit_name="rt-calm"} 0`,
		`lenny_circuit_breaker_cache_stale_seconds 0`,
		`lenny_circuit_breaker_cache_initialized 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestAuditPartitionDropBlockedGauge covers the §16.4 line 378
// lenny_audit_partition_drop_blocked gauge the §16.5
// AuditPartitionDropBlocked alert reads: 1 when the SIEM delivery guard
// holds a partition past its retention TTL, 0 once the forwarder catches
// up, labelled by partition. F-16.4.6.
func TestAuditPartitionDropBlockedGauge(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetAuditPartitionDropBlocked("acme", true)
	m.SetAuditPartitionDropBlocked("globex", false)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_audit_partition_drop_blocked{partition="acme"} 1`,
		`lenny_audit_partition_drop_blocked{partition="globex"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestStorageWriteMetricsExposeValues covers the §12.3 write-pressure
// and billing-pressure metrics: the write-IOPS gauge, the configured
// ceiling, the billing_flush_pressure counter, and the audit chain
// integrity counter all surface on /metrics. F-12.3.7 / F-12.3.8 /
// F-12.3.9 / F-12.3.13.
func TestStorageWriteMetricsExposeValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetPostgresWriteIops(123)
	m.SetPostgresWriteCeilingIops(600)
	// §12.3 line 97 / §16.1 line 228 — the SIEM outbox forwarder sets the
	// delivery-lag gauge; the configured threshold is emitted at startup
	// so AuditSIEMDeliveryLag compares against an operator-tunable scalar.
	// F-12.3.6 / F-12.3.17.
	m.SetSIEMDeliveryLagSeconds(42)
	m.SetSIEMMaxDeliveryLagSeconds(45)
	// §12.3 line 99 — the AuditBatchingNoSIEM counter is incremented once
	// at startup when production batching has no SIEM. F-12.3.15.
	m.IncAuditBatchingNoSIEM()
	m.IncBillingFlushPressure()
	m.IncBillingFlushPressure()
	m.IncAuditChainIntegrity("verified")
	m.IncAuditChainIntegrity("broken")
	// §11.7 item 2 — the periodic integrity check increments grant-drift
	// on detection; the AuditGrantDrift alert reads `> 0`. F-11.7.3.
	m.IncAuditGrantDrift()
	m.IncAuditGrantDrift()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_postgres_write_iops 123`,
		`lenny_postgres_write_ceiling_iops 600`,
		`lenny_audit_siem_delivery_lag_seconds 42`,
		`lenny_audit_siem_max_delivery_lag_seconds 45`,
		`lenny_audit_batching_no_siem_total 1`,
		`lenny_billing_flush_pressure_total 2`,
		`lenny_audit_chain_integrity_total{state="verified"} 1`,
		`lenny_audit_chain_integrity_total{state="broken"} 1`,
		`lenny_audit_grant_drift_total 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestMiddlewareRecordsRequests(t *testing.T) {
	m, _ := gatewaymetrics.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions" })

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("inner status: %d", rr.Code)
		}
	}

	// The metrics endpoint should now report 3 requests with the
	// 2xx status class.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `lenny_gateway_requests_total{method="POST",route="/v1/sessions",status_class="2xx"} 3`) {
		t.Errorf("requests_total not recorded as 3 2xx; body:\n%s", body)
	}
}

func TestMiddlewareLabelsStatusClass(t *testing.T) {
	m, _ := gatewaymetrics.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions/{id}" })
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	mr := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mrr := httptest.NewRecorder()
	m.Handler().ServeHTTP(mrr, mr)
	if !strings.Contains(mrr.Body.String(), `status_class="4xx"`) {
		t.Errorf("404 should be labelled 4xx; body:\n%s", mrr.Body.String())
	}
}

func TestSetActiveSessions(t *testing.T) {
	m, _ := gatewaymetrics.New()
	m.SetActiveSessions(42)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "lenny_gateway_active_sessions 42") {
		t.Errorf("active sessions gauge not set; body:\n%s", rr.Body.String())
	}
}

// spec: §4.1 / §16.5 — the scalar configuration gauges are
// registered at construction so /metrics exposes them before the
// gateway main has called the setters. The
// `GatewayNoHealthyReplicas` and `GatewayActiveStreamsHigh` alert
// expressions read these via scalar(...); a missing child series
// resolves the scalar to NaN and the alert never fires.
func TestScalarGaugesRegisteredAtStartup(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas 0",
		"lenny_gateway_stream_ceiling 0",
		"lenny_gateway_replica_count 0",
		// spec: §11.2.1 line 187 / §16.5 BillingCorrectionRateHigh —
		// pre-materialize the threshold gauge with the spec default
		// (0.05 = 5%) so scalar(lenny_billing_correction_rate_threshold)
		// in the alert expression never evaluates to NaN even before
		// SetBillingCorrectionRateThreshold runs. F-11.2.23.
		"lenny_billing_correction_rate_threshold 0.05",
		// spec: §12.6 line 683 / §16.5 EventBusPublishDropped —
		// pre-materialize the drop-alert threshold gauge with the spec
		// default (10/min) so scalar(lenny_event_bus_drop_alert_threshold)
		// in the alert expression never evaluates to NaN before
		// SetEventBusDropAlertThreshold runs. F-12.6.23.
		"lenny_event_bus_drop_alert_threshold 10",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.6 line 683 / §16.5 — SetEventBusDropAlertThreshold drives the
// lenny_event_bus_drop_alert_threshold gauge that the EventBusPublishDropped
// alert reads via scalar(...). The deployer-configured eventBus.dropAlertThreshold
// must round-trip through /metrics so the alert evaluates against the
// chart-provided rate rather than the previously baked-in literal 10. F-12.6.23.
func TestSetEventBusDropAlertThreshold_spec_12_6_683(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetEventBusDropAlertThreshold(25)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_event_bus_drop_alert_threshold 25") {
		t.Fatalf("/metrics missing updated drop-alert threshold gauge\n---\n%s", rr.Body.String())
	}
}

// spec: §11.2.1 line 187 / §16.5 — SetBillingCorrectionRateThreshold
// drives the lenny_billing_correction_rate_threshold gauge that the
// §16.5 BillingCorrectionRateHigh alert reads via scalar(...). The
// deployer-configured value must round-trip through /metrics so the
// alert evaluates against the chart-provided threshold rather than a
// baked-in constant. F-11.2.23.
func TestSetBillingCorrectionRateThreshold_spec_11_2_1_187(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetBillingCorrectionRateThreshold(0.10)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_billing_correction_rate_threshold 0.1") {
		t.Fatalf("/metrics missing updated threshold gauge\n---\n%s", rr.Body.String())
	}
	// Zero is admissible (disable the alert), so the gauge must accept it.
	m.SetBillingCorrectionRateThreshold(0)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_billing_correction_rate_threshold 0") {
		t.Fatalf("/metrics missing zero threshold gauge\n---\n%s", rr.Body.String())
	}
}

// spec: §16.4 / §16.5 — SetAuditSIEMConfigured / SetAuditRetentionDays /
// SetEnvProduction drive the three scalar gauges the AuditSIEMNotConfigured
// and AuditRetentionLow alert expressions read. The fail-safe defaults
// (SIEM unconfigured, 365-day window, non-production) pre-materialize the
// series so a scrape before the gateway main has wired configuration still
// yields a finite reading, and the production gate keeps the alerts inert
// until envProduction is set. F-16.4.9; F-16.4.10.
func TestSetAuditAlertScalars_spec_16_4_9(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pre-Set defaults: SIEM unconfigured (0), default window (365),
	// non-production (0).
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_audit_siem_configured 0",
		"lenny_audit_retention_days 365",
		"lenny_env_production 0",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing default audit scalar %q\n---\n%s", want, rr.Body.String())
		}
	}
	// A production gateway with a SIEM and a tightened retention window
	// flows the operator configuration through to the gauges.
	m.SetAuditSIEMConfigured(true)
	m.SetAuditRetentionDays(2190)
	m.SetEnvProduction(true)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_audit_siem_configured 1",
		"lenny_audit_retention_days 2190",
		"lenny_env_production 1",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing updated audit scalar %q\n---\n%s", want, rr.Body.String())
		}
	}
	// The SIEM-configured term must accept a flip back to 0 (a SIEM
	// endpoint removed from Helm values) so AuditSIEMNotConfigured re-arms.
	m.SetAuditSIEMConfigured(false)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_audit_siem_configured 0") {
		t.Fatalf("/metrics missing re-armed siem-configured gauge\n---\n%s", rr.Body.String())
	}
}

// spec: §25.13 line 4737 / §16.5 — SetGatewayQueueDepthThreshold /
// SetGatewayLatencyThresholdSeconds / SetCredentialPoolLowThreshold
// drive the §25.13 tier-dependent threshold gauges the §16.5
// GatewayQueueDepthHigh / GatewayLatencyHigh / CredentialPoolLow
// alerts read via scalar(...). The base-Helm defaults pre-materialize
// the series so a scrape before the gateway main has called the
// setters still yields a non-NaN reading. F-25.13.2.
func TestSetAlertThresholds_spec_25_13_4737(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pre-Set baseline reading: the chart Tier 1 defaults are
	// materialized at registration time so scalar(...) is finite.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_gateway_queue_depth_threshold 20",
		"lenny_gateway_latency_threshold_seconds 3",
		"lenny_credential_pool_low_threshold 0.8",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing default threshold %q\n---\n%s", want, rr.Body.String())
		}
	}
	// Tier-2 / Tier-3 tightening flows through to the gauges via the
	// Set helpers.
	m.SetGatewayQueueDepthThreshold(5)
	m.SetGatewayLatencyThresholdSeconds(1.0)
	m.SetCredentialPoolLowThreshold(0.60)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_gateway_queue_depth_threshold 5",
		"lenny_gateway_latency_threshold_seconds 1",
		"lenny_credential_pool_low_threshold 0.6",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing tightened threshold %q\n---\n%s", want, rr.Body.String())
		}
	}
}

// TestSetSLOBurnRateMultipliers_spec_16_5_640 verifies the §16.5 line
// 640 operator-tunable burn-rate window multipliers: both gauges
// pre-materialize at their base-Helm defaults (14 / 3) so every
// burn-rate alert's scalar(lenny_slo_burn_rate_*_multiplier) lookup is
// finite before the gateway main has called the setter, and the setter
// flows an operator override through to /metrics. F-16.5.3.
func TestSetSLOBurnRateMultipliers_spec_16_5_640(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_slo_burn_rate_fast_multiplier 14",
		"lenny_slo_burn_rate_slow_multiplier 3",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing default burn-rate multiplier %q\n---\n%s", want, rr.Body.String())
		}
	}
	m.SetSLOBurnRateMultipliers(10, 2)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_slo_burn_rate_fast_multiplier 10",
		"lenny_slo_burn_rate_slow_multiplier 2",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing overridden burn-rate multiplier %q\n---\n%s", want, rr.Body.String())
		}
	}
}

// TestSetSessionUnavailabilityRatio_spec_16_5 verifies the §16.5
// SessionAvailabilityBurnRate SLI gauge: it materializes at 0 (an idle
// gateway is fully available) and the setter publishes the export-loop
// recovery/active fraction. F-16.5.3.
func TestSetSessionUnavailabilityRatio_spec_16_5(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_session_unavailability_ratio 0") {
		t.Errorf("/metrics missing default unavailability ratio 0\n---\n%s", rr.Body.String())
	}
	m.SetSessionUnavailabilityRatio(0.25)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_session_unavailability_ratio 0.25") {
		t.Errorf("/metrics missing set unavailability ratio 0.25\n---\n%s", rr.Body.String())
	}
}

// spec: §4.1 / §16.5 — SetMinReplicas / SetStreamCeiling /
// SetReplicaCount drive the three scalar gauges referenced from the
// §16.5 alert rules. Each value must round-trip through /metrics so
// the scalar(...) lookups in the alert rules resolve to the
// configured operator value.
// TestSetMaxOrphanTasksPerTenant_spec_8_10 verifies the §8.10 line
// 1103 orphan-cap gauge is exposed at startup as 0 and updates to the
// deployer-configured value once SetMaxOrphanTasksPerTenant fires. The
// §16.5 OrphanTasksPerTenantHigh alert reads it via
// `scalar(lenny_max_orphan_tasks_per_tenant)`. F-8.10.13.
// TestSetTimeDriftRoundTrips covers §13.3 line 595 / F-13.3.5: the
// lenny_time_drift_seconds gauge is materialized at startup as 0 and
// updates to the driftmonitor's sampled value via SetTimeDrift.
func TestSetTimeDriftRoundTrips(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_time_drift_seconds 0") {
		t.Fatalf("startup /metrics missing zero-init drift gauge\n---\n%s", rr.Body.String())
	}
	m.SetTimeDrift(2.5)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_time_drift_seconds 2.5") {
		t.Fatalf("/metrics missing updated drift gauge\n---\n%s", rr.Body.String())
	}
	// Negative offsets must round-trip too — the gauge is signed.
	m.SetTimeDrift(-3.2)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_time_drift_seconds -3.2") {
		t.Fatalf("/metrics missing negative drift gauge\n---\n%s", rr.Body.String())
	}
}

// TestSetTimeDriftNilSafe ensures the nil receiver short-circuits the
// gauge update, so a caller with a nil *Metrics handle does not crash.
func TestSetTimeDriftNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.SetTimeDrift(1.0) // must not panic
}

func TestSetMaxOrphanTasksPerTenant_spec_8_10(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_max_orphan_tasks_per_tenant 0") {
		t.Errorf("startup /metrics missing zero gauge\n---\n%s", rr.Body.String())
	}
	m.SetMaxOrphanTasksPerTenant(100)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_max_orphan_tasks_per_tenant 100") {
		t.Errorf("/metrics missing configured gauge after SetMaxOrphanTasksPerTenant\n---\n%s", rr.Body.String())
	}
}

// TestOrphanCleanupAndTreeRecoveryMetricsRegistered_spec_8_10_7 covers
// the §8.10 / §16.1 lines 144-149 metric surface — the orphan-cleanup
// counters, the per-tenant active gauge, and the tree-recovery duration
// histogram + timeout counter must be registered and visible on
// /metrics. F-8.10.7.
func TestOrphanCleanupAndTreeRecoveryMetricsRegistered_spec_8_10_7(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(3)
	m.SetOrphanTasksActive(2)
	m.SetOrphanTasksActivePerTenant("acme", 4)
	m.ObserveTreeRecoveryDuration("warm-pool-a", "full_success", 12.5)
	m.IncTreeRecoveryTimeout("warm-pool-a", "level")
	m.IncTreeRecoveryTimeout("warm-pool-a", "tree")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_orphan_cleanup_runs_total 1",
		"lenny_orphan_tasks_terminated 3",
		"lenny_orphan_tasks_active 2",
		`lenny_orphan_tasks_active_per_tenant{tenant_id="acme"} 4`,
		`lenny_delegation_tree_recovery_duration_seconds_count{outcome="full_success",pool="warm-pool-a"} 1`,
		`lenny_delegation_tree_recovery_timeout_total{pool="warm-pool-a",timeout_type="level"} 1`,
		`lenny_delegation_tree_recovery_timeout_total{pool="warm-pool-a",timeout_type="tree"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// TestInterceptorMTLSHandshakeMetric_spec_10_3_332 covers the §10.3
// NET-063 / §16.1 line 50 interceptor mTLS handshake histogram: each
// observed `result` registers a labeled series so the §16.5
// InterceptorMTLSHandshakeFailure alert evaluates on live data rather
// than an always-empty counter. F-10.3.3.
func TestInterceptorMTLSHandshakeMetric_spec_10_3_332(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveInterceptorMTLSHandshake("success", 0.012)
	m.ObserveInterceptorMTLSHandshake("san_mismatch", 0.004)
	m.ObserveInterceptorMTLSHandshake("cert_missing", 0.001)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="success"} 1`,
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="san_mismatch"} 1`,
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="cert_missing"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// TestInterceptorMTLSHandshakeMetricNilSafe pins the nil-receiver
// short-circuit so a dial without a metrics handle does not panic.
func TestInterceptorMTLSHandshakeMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveInterceptorMTLSHandshake("success", 1.0)
}

// TestOrphanMetricsNilSafe pins the nil-receiver short-circuits on the
// §8.10 setters so a caller without a metrics handle does not panic.
// F-8.10.7.
func TestOrphanMetricsNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(1)
	m.AddOrphanTasksTerminated(0) // no-op even with a non-nil receiver
	m.SetOrphanTasksActive(0)
	m.SetOrphanTasksActivePerTenant("acme", 0)
	m.ObserveTreeRecoveryDuration("pool", "outcome", 1.0)
	m.IncTreeRecoveryTimeout("pool", "level")
}

// TestOrphanSessionReconcilerMetrics_spec_10_1_51 covers the §10.1 line
// 51 orphan-session reconciler surface: the reconciliations counter and
// the per-pool agent_pod_state mirror-lag gauge must register and expose
// the emitted values on /metrics. F-10.1.5.
func TestOrphanSessionReconcilerMetrics_spec_10_1_51(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncOrphanSessionReconciliation()
	m.IncOrphanSessionReconciliation()
	m.SetAgentPodStateMirrorLag("warm-pool-a", 42)
	m.SetAgentPodStateMirrorLag("warm-pool-b", 7.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_orphan_session_reconciliations_total 2",
		`lenny_agent_pod_state_mirror_lag_seconds{pool="warm-pool-a"} 42`,
		`lenny_agent_pod_state_mirror_lag_seconds{pool="warm-pool-b"} 7.5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// TestOrphanSessionMetricsNilSafe pins the nil-receiver short-circuits on
// the §10.1 emitters. F-10.1.5.
func TestOrphanSessionMetricsNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncOrphanSessionReconciliation()
	m.SetAgentPodStateMirrorLag("pool", 1.0)
}

// TestExperimentTargetingMetricsRegistered_spec_10_7_833 covers the
// §10.7 line 833 / §16.1 lines 156-157 external-targeting observability
// surface: the per-provider duration histogram and the per-provider,
// per-error_type failure counter must be registered and visible on
// /metrics. F-10.7.14.
func TestExperimentTargetingMetricsRegistered_spec_10_7_833(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveExperimentTargetingDuration("flags.acme.com", 0.042)
	m.RecordExperimentTargetingError("flags.acme.com", "FLAG_NOT_FOUND")
	m.RecordExperimentTargetingError("flags.acme.com", "timeout")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_experiment_targeting_duration_seconds_count{provider="flags.acme.com"} 1`,
		`lenny_experiment_targeting_error_total{error_type="FLAG_NOT_FOUND",provider="flags.acme.com"} 1`,
		`lenny_experiment_targeting_error_total{error_type="timeout",provider="flags.acme.com"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §16.1 lines 161-163 / §10.7 lines 1120-1132 — the variant-labelled
// rollback-trigger session family. A terminal session always increments
// lenny_session_total and observes its duration; only a failed session
// increments lenny_session_error_total. session_type defaults to "session".
func TestRecordSessionTerminalMetrics_spec_16_1_161(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// One failed treatment session, one completed treatment session, one
	// completed un-enrolled session (empty variant, empty execution mode).
	m.RecordSessionTerminal("acme", "task", "treatment", true, 12.0)
	m.RecordSessionTerminal("acme", "task", "treatment", false, 30.0)
	m.RecordSessionTerminal("acme", "", "", false, 5.0)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_total{session_type="task",tenant_id="acme",variant_id="treatment"} 2`,
		`lenny_session_error_total{session_type="task",tenant_id="acme",variant_id="treatment"} 1`,
		`lenny_session_duration_seconds_count{session_type="task",tenant_id="acme",variant_id="treatment"} 2`,
		// empty execution mode falls back to session_type="session"
		`lenny_session_total{session_type="session",tenant_id="acme",variant_id=""} 1`,
		`lenny_session_duration_seconds_count{session_type="session",tenant_id="acme",variant_id=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
	// A non-error terminal must not have produced an error series for the
	// un-enrolled session.
	if strings.Contains(body, `lenny_session_error_total{session_type="session",tenant_id="acme",variant_id=""}`) {
		t.Errorf("/metrics unexpectedly recorded an error for a non-failed session\n%s", body)
	}
}

// spec: §16.1 line 164 / §10.7 line 1128 — one lenny_eval_score observation
// per submitted eval run, labelled by scorer and variant.
func TestObserveEvalScore_spec_16_1_164(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveEvalScore("acme", "safety", "treatment", 0.97)
	m.ObserveEvalScore("acme", "safety", "treatment", 0.93)
	m.ObserveEvalScore("acme", "helpfulness", "", 0.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_eval_score_count{scorer="safety",tenant_id="acme",variant_id="treatment"} 2`,
		`lenny_eval_score_sum{scorer="safety",tenant_id="acme",variant_id="treatment"} 1.9`,
		`lenny_eval_score_count{scorer="helpfulness",tenant_id="acme",variant_id=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.8 CMP-026 / §16.1 line 262 — lenny_erasure_job_failed_total is
// incremented per failed erasure job, labelled by tenant and failure
// phase; the memory_store_preflight phase covers the §12.8 MemoryStore
// erasure preflight.
func TestIncErasureJobFailed_spec_12_8_cmp_026(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncErasureJobFailed("acme", "memory_store_preflight")
	m.IncErasureJobFailed("acme", "memory_store_preflight")
	m.IncErasureJobFailed("acme", "store_delete")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_erasure_job_failed_total{failure_phase="memory_store_preflight",tenant_id="acme"} 2`,
		`lenny_erasure_job_failed_total{failure_phase="store_delete",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.8 line 768 — erasure throughput / SLA metrics. The
// in-progress gauge, the per-job duration histogram, and the overdue
// age/deadline gauges the §16.5 ErasureJobOverdue alert reads.
func TestErasureJobSLAMetrics_spec_12_8_768(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncErasureJobsActive()
	m.IncErasureJobsActive()
	m.DecErasureJobsActive()
	m.ObserveErasureJobDuration(42)
	m.SetErasureJobDeadlineSeconds((72 * 3600))
	m.SetErasureJobAge("acme", "erasure_abc", 1234)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_erasure_jobs_active 1`,
		`lenny_erasure_job_duration_seconds_count 1`,
		`lenny_erasure_job_deadline_seconds 259200`,
		`lenny_erasure_job_age_seconds{job_id="erasure_abc",tenant_id="acme"} 1234`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}

	// A cleared job age series disappears from /metrics.
	m.ClearErasureJobAge("acme", "erasure_abc")
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rr.Body.String(), `job_id="erasure_abc"`) {
		t.Error("ClearErasureJobAge must remove the age series")
	}
}

// spec: §11.7 / §16.1 — the wired OCSF translator increments
// lenny_audit_ocsf_translation_failed_total labeled by event_type and
// error_class on each per-row translation failure. F-11.7.1 / F-11.7.15.
func TestIncAuditOCSFTranslationFailed_spec_11_7(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncAuditOCSFTranslationFailed("session.created", "class_mapping_missing")
	m.IncAuditOCSFTranslationFailed("session.created", "class_mapping_missing")
	m.IncAuditOCSFTranslationFailed("credential.leased", "schema_violation")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_audit_ocsf_translation_failed_total{error_class="class_mapping_missing",event_type="session.created"} 2`,
		`lenny_audit_ocsf_translation_failed_total{error_class="schema_violation",event_type="credential.leased"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

// IncAuditOCSFTranslationFailed must be nil-safe like the other emitters.
func TestIncAuditOCSFTranslationFailedNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncAuditOCSFTranslationFailed("session.created", "other")
}

func TestSetScalarGaugesEmitsConfiguredValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetMinReplicas(5)
	m.SetStreamCeiling(400)
	m.SetReplicaCount(1)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas 5",
		"lenny_gateway_stream_ceiling 400",
		"lenny_gateway_replica_count 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §4.1 / §16.5 — concurrent setter invocations from the
// startup wiring path and a watchdog poller must not race or panic.
// The scalar gauges are plain prometheus.Gauge values; this test
// pins the no-panic property under concurrent writes.
func TestScalarGaugesAreSafeUnderConcurrentSetters(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func(v int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				m.SetMinReplicas(v + j)
				m.SetStreamCeiling(v + j)
				m.SetReplicaCount(1)
			}
		}(i)
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	// One terminal read; assert the gauges still emit cleanly.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas",
		"lenny_gateway_stream_ceiling",
		"lenny_gateway_replica_count",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q after concurrent writes", want)
		}
	}
}

// spec: §4.1 — lenny_gateway_max_sessions_per_replica is emitted at
// startup with delivery_mode labels so the §16.5
// GatewaySessionBudgetNearExhaustion alert has a denominator gauge.
func TestSetMaxSessionsPerReplicaEmitsBothDeliveryModes(t *testing.T) {
	m, _ := gatewaymetrics.New()
	m.SetMaxSessionsPerReplica("direct", 50)
	m.SetMaxSessionsPerReplica("proxy", 50)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_gateway_max_sessions_per_replica{delivery_mode="direct"} 50`,
		`lenny_gateway_max_sessions_per_replica{delivery_mode="proxy"} 50`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §4.1 SCL-026 — the metrics Middleware tracks in-flight
// requests so the HPA gauge exporter can read it through
// InflightRequests and publish to lenny_gateway_request_queue_depth.
func TestMiddlewareTracksInflightRequests(t *testing.T) {
	m, _ := gatewaymetrics.New()
	// While no handler is running, in-flight is 0.
	if got := m.InflightRequests(); got != 0 {
		t.Fatalf("InflightRequests() = %d at rest, want 0", got)
	}
	// Hold the handler so we can observe the in-flight count from
	// outside. The handler signals when it has incremented; the test
	// closes a channel to release it.
	started := make(chan struct{})
	release := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/test" })

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}()
	<-started
	if got := m.InflightRequests(); got != 1 {
		t.Fatalf("InflightRequests() = %d while handler running, want 1", got)
	}
	close(release)
	<-done
	if got := m.InflightRequests(); got != 0 {
		t.Fatalf("InflightRequests() = %d after handler exit, want 0", got)
	}
}

func TestRecordElicitationDropExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordElicitationDrop("budget_exceeded")
	m.RecordElicitationDrop("budget_exceeded")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lenny_elicitation_dropped_total") {
		t.Fatalf("/metrics output missing lenny_elicitation_dropped_total:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_dropped_total{reason="budget_exceeded"} 2`) {
		t.Errorf("/metrics output missing the budget_exceeded count of 2:\n%s", body)
	}
}

// spec: §16.1 line 64; §9.2 line 60 — the tamper counter is labelled by
// origin_pod, tampering_pod, and enforcement_mode (no tenant_id). Two
// distinct (origin_pod, tampering_pod) pairs under enforce produce two
// independent series; a detect-only catch on the same pair is a third.
// F-9.2.4.
func TestRecordElicitationContentTamperDetectedExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "detect-only")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lenny_elicitation_content_tamper_detected_total") {
		t.Fatalf("/metrics output missing lenny_elicitation_content_tamper_detected_total:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce",origin_pod="pod-origin",tampering_pod="pod-middle"} 2`) {
		t.Errorf("/metrics output missing enforce-mode count of 2 with origin_pod/tampering_pod labels:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only",origin_pod="pod-origin",tampering_pod="pod-middle"} 1`) {
		t.Errorf("/metrics output missing detect-only count of 1 with origin_pod/tampering_pod labels:\n%s", body)
	}
	// §16.1 line 64 cardinality: tenant_id must NOT be a label on this
	// metric — the bounded labels are origin_pod and tampering_pod only.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "lenny_elicitation_content_tamper_detected_total{") && strings.Contains(line, "tenant_id") {
			t.Errorf("tamper counter must not carry a tenant_id label (§16.1 line 64): %s", line)
		}
	}
}

// spec: §16.5 line 460 — the weakened-mode gauge is the standing
// ElicitationContentIntegrityWeakened alert numerator. It is exposed
// unlabelled and reports the count of active tenants whose effective
// §9.2 mode is weaker than enforce. F-9.2.5.
func TestSetElicitationIntegrityWeakenedExposesGauge(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetElicitationIntegrityWeakened(3)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 3") {
		t.Errorf("/metrics output missing weakened gauge value of 3:\n%s", body)
	}
	// The gauge resolves to zero once every tenant is on enforce; the
	// alert must clear, so the series must report exactly 0 (not absent).
	m.SetElicitationIntegrityWeakened(0)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 0") {
		t.Errorf("/metrics output missing weakened gauge value of 0 after resolve:\n%s", body)
	}
}

// TestElicitationLifecycleMetricsExposeCounters proves the §16.1
// lines 60-63 elicitation lifecycle metrics are registered and
// observable on /metrics: the in-flight gauge, the timeout counter,
// the suppressed counter, and the round-trip histogram. F-9.2.14.
//
// spec: §16.1 lines 60–63; §16.5 line 458 ElicitationBacklogHigh
// alert.
func TestElicitationLifecycleMetricsExposeCounters_spec_16_1_F_9_2_14(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// One admit, three drops, one round-trip observation.
	m.IncElicitationPending()
	m.IncElicitationPending()
	m.DecElicitationPending() // net pending = 1
	m.IncElicitationTimeout()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(45 * time.Second)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()

	for _, want := range []string{
		"lenny_elicitation_pending 1",
		"lenny_elicitation_timeout_total 2",
		"lenny_elicitation_suppressed_total 1",
		// Histogram count and sum lines.
		"lenny_elicitation_roundtrip_seconds_count 1",
		"lenny_elicitation_roundtrip_seconds_sum 45",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestElicitationLifecycleMetricsNilSafe proves the helpers are
// nil-safe so an absent metrics dependency does not panic. F-9.2.14.
func TestElicitationLifecycleMetricsNilSafe_spec_16_1_F_9_2_14(t *testing.T) {
	var m *gatewaymetrics.Metrics // nil
	m.IncElicitationPending()
	m.DecElicitationPending()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(1 * time.Second)
}

func TestRecordExperimentIsolationRejectionExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordExperimentIsolationRejection("acme", "exp_1", "treatment")
	m.RecordExperimentIsolationRejection("acme", "exp_1", "treatment")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	want := `lenny_experiment_isolation_rejections_total{experiment_id="exp_1",tenant_id="acme",variant_id="treatment"} 2`
	if !strings.Contains(body, want) {
		t.Errorf("/metrics output missing %q\n---\n%s", want, body)
	}
}

// spec: §15.1 GET /v1/sessions/{id}/events (SSE event stream)
// diagnosis: the §16.1 metrics middleware wraps the response writer
// in statusRecorder. When the wrapper does not forward http.Flusher,
// the SSE handler at pkg/gateway/sessionserver/events.go:50 fails its
// http.Flusher type assertion and returns 500 "response writer does
// not support streaming", breaking every streaming surface that
// passes through the middleware (SSE events, the §4.9 LLM-proxy
// streaming translators).
func TestMiddlewareForwardsFlusher(t *testing.T) {
	m, _ := gatewaymetrics.New()
	flushed := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapper did not implement http.Flusher; SSE handlers will 500")
		}
		w.WriteHeader(http.StatusOK)
		f.Flush()
		flushed = true
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions/{id}/events" })

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/x/events", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !flushed {
		t.Fatal("inner handler did not reach Flush")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !rr.Flushed {
		t.Error("recorder reports the response was not flushed")
	}
}

// spec: §12.5 ll. 303 — the T4 fail-closed KMS-unavailable
// rejection emits to `lenny_checkpoint_storage_failure_total` with
// `reason="kms_unavailable"`. Existing retry-exhaustion calls stamp
// `reason="retry_exhausted"` so both flows aggregate into the same
// counter the `CheckpointStorageUnavailable` alert reads.
func TestCheckpointStorageFailureReasonLabel(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCheckpointStorageFailure("pool-a", "full", "periodic")
	m.IncCheckpointKMSUnavailable()
	m.IncCheckpointKMSUnavailable()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_checkpoint_storage_failure_total{level="full",pool="pool-a",reason="retry_exhausted",trigger="periodic"} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="kms_unavailable",trigger=""} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 ll. 341 — the hard-prune sweep increments the
// `lenny_gc_tombstones_pruned_total{table}` counter once per row
// removed, labeled by the GC-managed row class it swept
// (`artifact_store` or `partial_manifest`).
func TestGCTombstonesPrunedCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.AddGCTombstonesPruned("artifact_store", 0)  // no-op guard
	m.AddGCTombstonesPruned("artifact_store", -3) // no-op guard for negative input
	m.AddGCTombstonesPruned("artifact_store", 4)
	m.AddGCTombstonesPruned("artifact_store", 2)
	m.AddGCTombstonesPruned("partial_manifest", 5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	if !strings.Contains(body, `lenny_gc_tombstones_pruned_total{table="artifact_store"} 6`) {
		t.Errorf("/metrics missing the expected artifact_store counter value 6\n---\n%s", body)
	}
	if !strings.Contains(body, `lenny_gc_tombstones_pruned_total{table="partial_manifest"} 5`) {
		t.Errorf("/metrics missing the expected partial_manifest counter value 5\n---\n%s", body)
	}
}

// spec: §12.5 line 321 — `lenny_gc_runs_total`,
// `lenny_gc_artifacts_deleted`, `lenny_gc_errors_total`, and
// `lenny_gc_duration_seconds` are emitted by the retention-GC sweep.
func TestGCRetentionMetricsEmit(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncGCRun("success")
	m.IncGCRun("error")
	m.AddGCArtifactsDeleted("artifacts", 3)
	m.AddGCArtifactsDeleted("transcripts", 2)
	m.IncGCError("artifacts")
	m.ObserveGCDuration(0.5)
	m.ObserveGCDuration(1.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_gc_runs_total{outcome="success"} 1`,
		`lenny_gc_runs_total{outcome="error"} 1`,
		`lenny_gc_artifacts_deleted{store="artifacts"} 3`,
		`lenny_gc_artifacts_deleted{store="transcripts"} 2`,
		`lenny_gc_errors_total{store="artifacts"} 1`,
		"lenny_gc_duration_seconds_count 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 line 291 — `lenny_drain_readiness_checks_total` records
// the webhook decision by outcome (allowed|blocked|forced).
func TestDrainReadinessCheckCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDrainReadinessCheck("allowed")
	m.IncDrainReadinessCheck("blocked")
	m.IncDrainReadinessCheck("forced")
	m.IncDrainReadinessCheck("allowed")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_drain_readiness_checks_total{outcome="allowed"} 2`,
		`lenny_drain_readiness_checks_total{outcome="blocked"} 1`,
		`lenny_drain_readiness_checks_total{outcome="forced"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.8 line 739 — `lenny_legal_hold_checkpoint_gaps_total`
// counts held sessions where the reconciler detects a checkpoint gap.
func TestLegalHoldCheckpointGapCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncLegalHoldCheckpointGap("acme")
	m.IncLegalHoldCheckpointGap("acme")
	m.IncLegalHoldCheckpointGap("globex")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_legal_hold_checkpoint_gaps_total{tenant_id="acme"} 2`,
		`lenny_legal_hold_checkpoint_gaps_total{tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 line 282 — `lenny_artifact_upload_error_total` counts
// retry-exhausted PUT failures, labelled by tenant_id and error_type.
// The same call rolls into
// `lenny_checkpoint_storage_failure_total{reason=...}` so the
// MinIOUnavailable and CheckpointStorageUnavailable alerts fire from
// one source.
func TestArtifactUploadErrorCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncArtifactUploadError("acme", "minio_unreachable")
	m.IncArtifactUploadError("acme", "auth")
	m.IncArtifactUploadError("acme", "quota_exceeded")
	m.IncArtifactUploadError("globex", "minio_unreachable")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_artifact_upload_error_total{error_type="minio_unreachable",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="auth",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="quota_exceeded",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="minio_unreachable",tenant_id="globex"} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="minio_unreachable",trigger=""} 2`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="auth",trigger=""} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="quota_exceeded",trigger=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §5.2 line 519 — lenny_slot_assignment_conflict_total is a
// per-pool counter of concurrent-mode slot-contention reservation
// failures, exposed on /metrics for the pool-under-sizing signal.
func TestSlotAssignmentConflictCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSlotAssignmentConflict("acme-agents")
	m.IncSlotAssignmentConflict("acme-agents")
	m.IncSlotAssignmentConflict("globex-agents")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_slot_assignment_conflict_total{pool="acme-agents"} 2`,
		`lenny_slot_assignment_conflict_total{pool="globex-agents"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §5.2 line 521 — lenny_slot_rehydration_total counts post-recovery
// slot-counter rehydration events, labeled by pod and pool.
func TestSlotRehydrationCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSlotRehydration("sbx-1", "acme-agents")
	m.IncSlotRehydration("sbx-2", "acme-agents")
	m.IncSlotRehydration("sbx-1", "acme-agents")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_slot_rehydration_total{pod="sbx-1",pool="acme-agents"} 2`,
		`lenny_slot_rehydration_total{pod="sbx-2",pool="acme-agents"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics is a no-op for the rehydration counter (the §5.2 hook
// is nil-safe when metrics are unwired).
func TestSlotRehydrationCounterNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncSlotRehydration("sbx-1", "pool") // must not panic
}

// spec: §16 line 66 — lenny_delegation_lease_extension_total is the §8.6
// per-decision counter labelled by tenant_id and outcome
// (approved/capped/denied). F-8.6.13.
func TestDelegationLeaseExtensionCounter_spec_16_line_66(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "denied")
	m.IncDelegationLeaseExtension("globex", "capped")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_delegation_lease_extension_total{outcome="approved",tenant_id="acme"} 2`,
		`lenny_delegation_lease_extension_total{outcome="denied",tenant_id="acme"} 1`,
		`lenny_delegation_lease_extension_total{outcome="capped",tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics is a no-op for the delegation lease-extension counter so
// the leasecontrol path works even when metrics are unwired. F-8.6.13.
func TestDelegationLeaseExtensionCounterNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncDelegationLeaseExtension("acme", "approved") // must not panic
}

// spec: §4.9 line 1220 — lenny_credential_preclaim_mismatch_total is a
// per-(pool,provider) counter of races where the pre-claim availability
// check passed but the lease assignment failed.
func TestCredentialPreclaimMismatchCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCredentialPreclaimMismatch("claude-prod", "anthropic_direct")
	m.IncCredentialPreclaimMismatch("claude-prod", "anthropic_direct")
	m.IncCredentialPreclaimMismatch("bedrock-prod", "aws_bedrock")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_credential_preclaim_mismatch_total{pool="claude-prod",provider="anthropic_direct"} 2`,
		`lenny_credential_preclaim_mismatch_total{pool="bedrock-prod",provider="aws_bedrock"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics must no-op rather than panic, matching the other
// counter helpers (the minimal gateway leaves metrics unwired).
func TestCredentialPreclaimMismatchNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncCredentialPreclaimMismatch("p", "anthropic_direct") // must not panic
}

// spec: §16.1 lines 51, 53, 55, 97, 99, 100 and §5.2 line 12 — the
// credential, LLM-proxy, and slot-failure metrics register and emit
// through the gateway registry.
func TestCredentialAndLLMProxyAndSlotMetricsEmit(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCredentialLeaseAssignment("anthropic_direct", "claude-prod", "primary")
	m.IncCredentialLeaseAssignment("anthropic_direct", "claude-prod", "primary")
	m.ObserveCredentialLeaseDuration("anthropic_direct", "claude-prod", 42)
	m.SetCredentialPoolUtilization("claude-prod", 0.5)
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "request", 0.01)
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "response", 0.02)
	m.IncLLMTranslationError("claude-prod", "anthropic_direct", "upstream_5xx")
	m.IncSlotFailure("session_start", "pool-a", "sbx-1")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_credential_lease_assignments_total{pool="claude-prod",provider="anthropic_direct",source="primary"} 2`,
		`lenny_credential_lease_duration_seconds_count{pool="claude-prod",provider="anthropic_direct"} 1`,
		`lenny_credential_pool_utilization{pool="claude-prod"} 0.5`,
		// Registered with a child series at construction, so the net-zero
		// gauge still appears on /metrics.
		"lenny_gateway_llm_proxy_active_connections 0",
		`lenny_gateway_llm_translation_duration_seconds_count{direction="request",pool="claude-prod",provider="anthropic_direct",proxy_dialect="anthropic"} 1`,
		`lenny_gateway_llm_translation_errors_total{error_type="upstream_5xx",pool="claude-prod",provider="anthropic_direct"} 1`,
		`lenny_slot_failure_total{error_type="session_start",k8s_pod_name="sbx-1",pool="pool-a"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// A nil *Metrics no-ops on every new emitter rather than panicking.
func TestNewMetricsEmittersNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncCredentialLeaseAssignment("anthropic_direct", "p", "primary")
	m.ObserveCredentialLeaseDuration("anthropic_direct", "p", 1)
	m.SetCredentialPoolUtilization("p", 0.5)
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("p", "anthropic_direct", "anthropic", "request", 0.01)
	m.IncLLMTranslationError("p", "anthropic_direct", "upstream_5xx")
	m.IncSlotFailure("session_start", "p", "sbx-1")
	m.ObserveSessionStartupDuration("p", "runc", "standard", 1.0)
	m.ObserveSessionStartupPhase("pod_claim", "runc", 0.05)
}

// spec: §16.1 line 14 / §6.3 lines 348, 372 — the startup-latency
// histograms register and expose their series, the end-to-end metric
// carries the pool/runtime_class/isolation_profile labels, and the
// per-phase metric carries phase/runtime_class.
func TestSessionStartupMetricsExposed_spec_6_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionStartupDuration("pool-a", "runc", "standard", 1.3)
	m.ObserveSessionStartupDuration("pool-b", "gvisor", "sandboxed", 4.0)
	m.ObserveSessionStartupPhase("pod_claim", "runc", 0.05)
	m.ObserveSessionStartupPhase("agent_session_start", "gvisor", 4.2)

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_session_startup_duration_seconds_count{isolation_profile="standard",pool="pool-a",runtime_class="runc"} 1`,
		`lenny_session_startup_duration_seconds_count{isolation_profile="sandboxed",pool="pool-b",runtime_class="gvisor"} 1`,
		`lenny_session_startup_phase_duration_seconds_count{phase="pod_claim",runtime_class="runc"} 1`,
		`lenny_session_startup_phase_duration_seconds_count{phase="agent_session_start",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// spec: §16.5 lines 635-636 — the StartupLatency burn-rate alerts read
// the histogram's le="2" (runc, 2s SLO) and le="5" (gVisor, 5s SLO)
// bucket boundaries. The recorded buckets must carry exactly those le
// labels or the alert PromQL silently selects no series.
func TestSessionStartupDurationBucketBoundaries_spec_16_5(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionStartupDuration("pool-a", "runc", "standard", 0.5)

	body := scrapeMetrics(t, m)
	for _, le := range []string{`le="2"`, `le="5"`} {
		needle := `lenny_session_startup_duration_seconds_bucket{`
		found := false
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, needle) && strings.Contains(line, le) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("startup duration histogram has no bucket with %s; the StartupLatency alert expr would match no series", le)
		}
	}
}

// spec: §16.1 line 15 / §6.3 line 356 — the TTFT histogram registers
// and exposes its series under the
// pool/runtime_class/isolation_profile label triple.
func TestSessionTimeToFirstTokenExposed_spec_6_3_F_6_3_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionTimeToFirstToken("pool-a", "runc", "standard", 0.8)
	m.ObserveSessionTimeToFirstToken("pool-b", "gvisor", "sandboxed", 3.2)

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_session_time_to_first_token_seconds_count{isolation_profile="standard",pool="pool-a",runtime_class="runc"} 1`,
		`lenny_session_time_to_first_token_seconds_count{isolation_profile="sandboxed",pool="pool-b",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// spec: §16.5 line 637 / §6.3 line 356 — the TTFTBurnRate alert reads
// the histogram's le="10" (10s TTFT SLO) bucket boundary. The recorded
// buckets must carry exactly that le label or the alert PromQL silently
// selects no series.
func TestSessionTimeToFirstTokenBucketBoundary_spec_6_3_F_6_3_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionTimeToFirstToken("pool-a", "runc", "standard", 1.0)

	body := scrapeMetrics(t, m)
	needle := `lenny_session_time_to_first_token_seconds_bucket{`
	found := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, needle) && strings.Contains(line, `le="10"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("TTFT histogram has no bucket with le=\"10\"; the TTFTBurnRate alert expr would match no series")
	}
}

// spec: §6.3 line 352, §16.1 line 122 — lenny_warmpool_claims_total is
// emitted per pool/runtime_class so deployers can compute the §6.3
// SDK-warm demotion-rate ratio (denominator). The catalog declares the
// metric labels; the test confirms the production counter exposes the
// expected series.
func TestIncWarmpoolClaim_spec_6_3_F_6_3_6(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncWarmpoolClaim("pool-a", "runc")
	m.IncWarmpoolClaim("pool-a", "runc")
	m.IncWarmpoolClaim("pool-b", "gvisor")

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_warmpool_claims_total{pool="pool-a",runtime_class="runc"} 2`,
		`lenny_warmpool_claims_total{pool="pool-b",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// IncWarmpoolClaim is nil-safe so callers can pass a nil *Metrics
// without guarding (mirrors the pattern used by other emitters).
func TestIncWarmpoolClaimNilSafe_spec_6_3_F_6_3_6(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncWarmpoolClaim("pool-a", "runc") // must not panic
}

// spec: §8.2 / §16.1 line 27 — lenny_delegation_depth histogram
// observation labelled by `pool`.
func TestObserveDelegationDepth_spec_8_2(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveDelegationDepth("pool-a", 3)
	m.ObserveDelegationDepth("pool-a", 1)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_delegation_depth_count{pool="pool-a"} 2`) {
		t.Errorf("expected lenny_delegation_depth_count for pool-a = 2, body=%q", body)
	}
	if !strings.Contains(body, `lenny_delegation_depth_sum{pool="pool-a"} 4`) {
		t.Errorf("expected lenny_delegation_depth_sum for pool-a = 4, body=%q", body)
	}
}

// spec: §8.2 line 70 / §16.1 line 79 —
// lenny_delegation_would_have_blocked_total carries (pool, tenant_id,
// layer, mode) labels.
func TestIncDelegationWouldHaveBlocked_spec_8_2(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "platform", "enforce")
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "runtime", "warn")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_delegation_would_have_blocked_total{layer="platform",mode="enforce",pool="pool-a",tenant_id="acme"} 1`,
		`lenny_delegation_would_have_blocked_total{layer="runtime",mode="warn",pool="pool-a",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §8.2 / §16.1 — nil receivers are no-ops (caller-side guard).
func TestDelegationMetricsNilSafe_spec_8_2(t *testing.T) {
	var m *gatewaymetrics.Metrics
	// Must not panic.
	m.ObserveDelegationDepth("pool-a", 1)
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "policy", "enforce")
	// F-8.9.10 nil-safe coverage.
	m.IncDelegationTreeCycleDetected("acme", "rest")
}

// spec: §8.9 line 1003 / §16.1 — lenny_delegation_tree_cycle_detected_total
// carries (tenant_id, source) labels and increments once per repeated
// node hit by the tree walker. F-8.9.10.
func TestIncDelegationTreeCycleDetected_spec_8_9(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationTreeCycleDetected("acme", "rest")
	m.IncDelegationTreeCycleDetected("acme", "rest")
	m.IncDelegationTreeCycleDetected("acme", "mcp")
	m.IncDelegationTreeCycleDetected("globex", "rest")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_delegation_tree_cycle_detected_total{source="rest",tenant_id="acme"} 2`,
		`lenny_delegation_tree_cycle_detected_total{source="mcp",tenant_id="acme"} 1`,
		`lenny_delegation_tree_cycle_detected_total{source="rest",tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §11.1 line 7 — lenny_rate_limit_rejected_total{scope} carries
// the §11.1 admission scope and bumps once per 429 rejection.
func TestIncRateLimitRejected_spec_11_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncRateLimitRejected("global")
	m.IncRateLimitRejected("user")
	m.IncRateLimitRejected("user")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_rate_limit_rejected_total{scope="global"} 1`,
		`lenny_rate_limit_rejected_total{scope="user"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §16.5 RateLimitDegraded — the source gauge must flip 0→1 on
// SetRateLimitFailopenActive(true) and 1→0 on the recovery call so
// the alert resolves cleanly.
func TestSetRateLimitFailopenActive_spec_16_5(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 0") {
		t.Errorf("startup gauge sample = missing 0, body=%q", body)
	}
	m.SetRateLimitFailopenActive(true)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 1") {
		t.Errorf("degraded gauge sample = missing 1, body=%q", body)
	}
	m.SetRateLimitFailopenActive(false)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 0") {
		t.Errorf("recovery gauge sample = missing 0, body=%q", body)
	}
}

// spec: §11.1 line 7 — counter-failure counter is monotonic across
// the outage window so an operator can rate-aggregate even after the
// gauge edge has fired.
func TestIncRateLimitCounterFailure_spec_11_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncRateLimitCounterFailure()
	m.IncRateLimitCounterFailure()
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_counter_failure_total 2") {
		t.Errorf("expected counter_failure_total = 2, body=%q", body)
	}
}

// spec: §11.1 — nil receivers are no-ops.
func TestRateLimitMetricsNilSafe_spec_11_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncRateLimitRejected("global")
	m.SetRateLimitFailopenActive(true)
	m.IncRateLimitCounterFailure()
}

// TestStatelessMetricsRegistered_spec_5_2_573 covers the §5.2 line 573
// concurrent-stateless demand metrics — counter increment + gauge set,
// both labeled by pool, exposed on /metrics.
// spec: §16.1 lines 80-81 — lenny_export_file_scans_total (labelled
// pool, tenant_id, policy_name, interceptor_ref, outcome) and
// lenny_export_file_scan_duration_seconds (pool, tenant_id,
// interceptor_ref) are registered and emit. F-8.7.10.
func TestExportFileScanMetricsRegistered_spec_16_1_80(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncExportFileScan("orchestrator-pool", "acme", "orchestrator-policy", "export-scanner", "rejected")
	m.IncExportFileScan("orchestrator-pool", "acme", "orchestrator-policy", "export-scanner", "failed_open")
	m.ObserveExportFileScanDuration("orchestrator-pool", "acme", "export-scanner", 0.012)
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_export_file_scans_total{interceptor_ref="export-scanner",outcome="rejected",policy_name="orchestrator-policy",pool="orchestrator-pool",tenant_id="acme"} 1`,
		`lenny_export_file_scans_total{interceptor_ref="export-scanner",outcome="failed_open",policy_name="orchestrator-policy",pool="orchestrator-pool",tenant_id="acme"} 1`,
		`lenny_export_file_scan_duration_seconds_count{interceptor_ref="export-scanner",pool="orchestrator-pool",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

func TestStatelessMetricsRegistered_spec_5_2_573(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncStatelessRequest("stateless-pool")
	m.IncStatelessRequest("stateless-pool")
	m.SetStatelessConcurrentActive("stateless-pool", 5)
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_stateless_requests_total{pool="stateless-pool"} 2`,
		`lenny_stateless_concurrent_active{pool="stateless-pool"} 5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

// TestStatelessMetricsNilSafe_spec_5_2_573 confirms nil receivers do
// not panic for the stateless emitters — the producer (F-5.2.3) calls
// these from a hot path.
func TestStatelessMetricsNilSafe_spec_5_2_573(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncStatelessRequest("any")
	m.SetStatelessConcurrentActive("any", 7)
}

// spec: §6.2 line 179 — the lenny_adapter_leaked_slots gauge is per-pod
// (labeled pod_id, pool), set when a concurrent-workspace slot's cleanup
// does not reclaim it, and zeroed when the pod is drained for replacement.
func TestAdapterLeakedSlotsGauge_spec_6_2_179(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetAdapterLeakedSlots("pod-a", "default-gvisor", 2)
	body := scrapeMetrics(t, m)
	if want := `lenny_adapter_leaked_slots{pod_id="pod-a",pool="default-gvisor"} 2`; !strings.Contains(body, want) {
		t.Errorf("/metrics missing %q", want)
	}
	// The drain path zeroes the pod's series.
	m.SetAdapterLeakedSlots("pod-a", "default-gvisor", 0)
	body = scrapeMetrics(t, m)
	if want := `lenny_adapter_leaked_slots{pod_id="pod-a",pool="default-gvisor"} 0`; !strings.Contains(body, want) {
		t.Errorf("/metrics missing zeroed series %q", want)
	}
	// Nil receiver must not panic (the producer calls from the slot path).
	var nilM *gatewaymetrics.Metrics
	nilM.SetAdapterLeakedSlots("pod-a", "p", 1)
}

// TestTaskReuseHistogramRegistered_spec_5_2_569 covers the §5.2 line
// 569 / §16.1 line 124 lenny_task_reuse_count histogram registration +
// observation + the in-process TaskReuseQuantile helper the
// PoolScalingController consumes as mode_factor.
func TestTaskReuseHistogramRegistered_spec_5_2_569(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Two pods on the same pool: pod-1 retired after 4 tasks, pod-2 after
	// 16. The cross-series median should be ~10 (the linear-interpolation
	// midpoint between 8 and 16 buckets).
	m.ObserveTaskReuseCount("tp", "pod-1", 4)
	m.ObserveTaskReuseCount("tp", "pod-2", 16)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_task_reuse_count_count{k8s_pod_name="pod-1",pool="tp"} 1`) {
		t.Errorf("/metrics missing pod-1 sample: %s", body)
	}
	med, ok := m.TaskReuseQuantile("tp", 0.5)
	if !ok {
		t.Fatal("TaskReuseQuantile reported !ok with observations recorded")
	}
	// With ExponentialBuckets(1, 2, 10) the buckets are 1,2,4,8,16,32,...
	// Pod-1 (4) lands in [2,4]; pod-2 (16) lands in [8,16]. Combined
	// cumulative counts: 4→1, 8→1, 16→2. Median at threshold 1 sits at
	// the 8 upper bound after interpolation.
	if med <= 0 {
		t.Errorf("TaskReuseQuantile returned non-positive median: %v", med)
	}
}

// TestTaskReuseQuantileBeforeObservation_spec_5_2_569 confirms a
// PoolScalingController querying the histogram before any observation
// sees ok=false — the bootstrap-mode fallback path.
func TestTaskReuseQuantileBeforeObservation_spec_5_2_569(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := m.TaskReuseQuantile("never-observed", 0.5); ok {
		t.Error("expected ok=false before any observation")
	}
}

// TestTaskReuseNilSafe_spec_5_2_569 confirms nil receivers do not panic.
func TestTaskReuseNilSafe_spec_5_2_569(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveTaskReuseCount("any", "pod", 5)
	if v, ok := m.TaskReuseQuantile("any", 0.5); ok || v != 0 {
		t.Errorf("nil receiver: got (%v, %v), want (0, false)", v, ok)
	}
}

// spec: §10.4 line 389 / §16 catalog — the gauge series exists at
// startup so /metrics never returns a missing series for the alert
// query. F-10.4.11.
func TestReplayBufferUtilizationExposedAtStartup_spec_10_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_event_bus_replay_buffer_utilization 0") {
		t.Errorf("/metrics missing replay-buffer utilization series at startup: %s", body)
	}
	m.SetReplayBufferUtilization(0.42)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_event_bus_replay_buffer_utilization 0.42") {
		t.Errorf("/metrics did not reflect updated utilization: %s", body)
	}
}

// spec: §10.4 / §16.5 PDBBlockedEvictions — each increment surfaces on
// /metrics with the pdb and controller labels. F-10.4.4.
func TestIncPDBBlockedEvictions_spec_10_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncPDBBlockedEvictions("lenny-gateway", "poller")
	m.IncPDBBlockedEvictions("lenny-gateway", "poller")
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_pdb_blocked_evictions_total{controller="poller",pdb="lenny-gateway"} 2`) {
		t.Errorf("/metrics missing labelled PDB counter sample: %s", body)
	}
}

// Nil-receiver safety so a missing Metrics does not crash the watcher
// or the periodic poller. F-10.4.4 / F-10.4.11.
func TestReplayBufferAndPDBNilSafe_spec_10_4(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.SetReplayBufferUtilization(0.9)
	m.IncPDBBlockedEvictions("any", "poller")
}

func scrapeMetrics(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rr.Code)
	}
	return rr.Body.String()
}
