// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
