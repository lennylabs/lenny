// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

// spec: §5.1 (injection fail-closed), §15.1 (SERVICE_UNAVAILABLE) —
// F-5.1.20.
//
// lenny_injection_gate_failclosed_total{cause} is exposed on /metrics with
// the backing-store cause label so the granular runtime-store-versus-
// override-store distinction behind a coarse SERVICE_UNAVAILABLE stays
// observable as a metric, the "and metrics" half of the §15.1 observability
// contract for the §5.1 injection-gate fail-closed branch.
func TestInjectionGateFailClosedExposes_F_5_1_20(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncInjectionGateFailClosed("runtime_store")
	m.IncInjectionGateFailClosed("override_store")
	m.IncInjectionGateFailClosed("override_store")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_injection_gate_failclosed_total{cause="runtime_store"} 1`,
		`lenny_injection_gate_failclosed_total{cause="override_store"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

// Nil receiver is a no-op so callers can wire the emitter unconditionally.
// spec: §5.1 (injection fail-closed) — F-5.1.20.
func TestInjectionGateFailClosedNilReceiverIsSafe_F_5_1_20(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncInjectionGateFailClosed("runtime_store") // must not panic
}
