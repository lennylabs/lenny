// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

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
