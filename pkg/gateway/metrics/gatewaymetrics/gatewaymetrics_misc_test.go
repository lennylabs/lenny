// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

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

func TestInterceptorMTLSHandshakeMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveInterceptorMTLSHandshake("success", 1.0)
}
