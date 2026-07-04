// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
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

func TestRecycleMetricsExposeSeries_spec_16_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncScrubFailureTotal("agents", "gvisor")
	m.SetScrubFailureCount("pod-1", "agents", "gvisor", 3)
	m.IncRetirement("scrub_failure_limit", "agents", "gvisor")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_pod_scrub_failure_total{pool="agents",runtime_class="gvisor"} 1`,
		`lenny_pod_scrub_failure_count{k8s_pod_name="pod-1",pool="agents",runtime_class="gvisor"} 3`,
		`lenny_pod_retirement_total{pool="agents",reason="scrub_failure_limit",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// TestResumeDeduplicatedCounterRemoved_spec_16_1 pins the removal of the
// lenny_coordinator_resume_deduplicated_total counter and its
// AddResumeDeduplicated increment site. Proposal 0026 reconciled the §10.1
// resume path to an adapter-assigned model, deleting the dead ResumeDedup
// consumer (its only increment site); §16.1 no longer defines this metric.
// The test asserts the concrete method is gone and the series never appears
// on /metrics. It fails against the pre-0026 code where both existed.
// spec: 16.1 (metric inventory; lenny_coordinator_resume_deduplicated_total removed)
func TestResumeDeduplicatedCounterRemoved_spec_16_1(t *testing.T) {
	if _, ok := reflect.TypeOf(&gatewaymetrics.Metrics{}).MethodByName("AddResumeDeduplicated"); ok {
		t.Error("Metrics.AddResumeDeduplicated must not exist: proposal 0026 removed its only increment site and dropped the §16.1 metric")
	}

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "lenny_coordinator_resume_deduplicated_total") {
		t.Errorf("/metrics still exposes the removed lenny_coordinator_resume_deduplicated_total counter\n---\n%s", rr.Body.String())
	}
}

func TestRecycleMetricsNilReceiverSafe_spec_16_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncScrubFailureTotal("agents", "gvisor")
	m.SetScrubFailureCount("pod-1", "agents", "gvisor", 3)
	m.IncRetirement("uptime_limit", "agents", "gvisor")
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

func TestAuditRedactionReceiptMissingMetric(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.AddAuditRedactionReceiptMissing("acme", 2)
	m.AddAuditRedactionReceiptMissing("acme", 1)
	m.AddAuditRedactionReceiptMissing("globex", 0)  // no-op: clean tenant
	m.AddAuditRedactionReceiptMissing("globex", -5) // no-op: defensive

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `lenny_audit_redaction_receipt_missing_total{tenant_id="acme"} 3`) {
		t.Errorf("/metrics missing acme receipt-missing count\n---\n%s", body)
	}
	if strings.Contains(body, `tenant_id="globex"`) {
		t.Errorf("/metrics surfaced a clean tenant on the receipt-missing series\n---\n%s", body)
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

func TestSetTimeDriftNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.SetTimeDrift(1.0) // must not panic
}

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

func TestIncAuditOCSFTranslationFailedNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncAuditOCSFTranslationFailed("session.created", "other")
}

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
