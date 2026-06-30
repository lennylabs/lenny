// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// spec: §16.1 line 124, §7.3 line 387 — F-7.5.9.
//
// lenny_warmpool_warmup_failure_total{error_type} is exposed on /metrics
// with the spec-named error_type label. The §7.3 line 387 closed enum
// includes setup_command_failed which the gateway emits when a bind-time
// setup command exits non-zero.

func TestWarmpoolWarmupFailureExposes_F_7_5_9(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncWarmpoolWarmupFailure("setup_command_failed")
	m.IncWarmpoolWarmupFailure("setup_command_failed")
	m.IncWarmpoolWarmupFailure("workspace_plan_invalid")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_warmpool_warmup_failure_total{error_type="setup_command_failed"} 2`,
		`lenny_warmpool_warmup_failure_total{error_type="workspace_plan_invalid"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

// Nil receiver is a no-op so callers can wire the emitter unconditionally.
func TestWarmpoolWarmupFailureNilReceiverIsSafe_F_7_5_9(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncWarmpoolWarmupFailure("setup_command_failed") // must not panic
}
