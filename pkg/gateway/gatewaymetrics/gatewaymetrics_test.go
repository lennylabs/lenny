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
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
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
