// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// spec: §16.1, §7.3 — F-7.5.9, F-16.1.9.
//
// lenny_warmpool_warmup_failure_total{pool, runtime_class, error_type} is
// exposed on /metrics with the §16.1 label set, the same set the
// WarmPoolController's half carries. The gateway emits setup_command_failed
// when a bind-time setup command exits non-zero, labeled with the pool and
// runtime class of the pod it ran on.

func TestWarmpoolWarmupFailureExposes_F_7_5_9(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncWarmpoolWarmupFailure("acme-pool", "gvisor", "setup_command_failed")
	m.IncWarmpoolWarmupFailure("acme-pool", "gvisor", "setup_command_failed")
	m.IncWarmpoolWarmupFailure("globex-pool", "kata", "setup_command_failed")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_warmpool_warmup_failure_total{error_type="setup_command_failed",pool="acme-pool",runtime_class="gvisor"} 2`,
		`lenny_warmpool_warmup_failure_total{error_type="setup_command_failed",pool="globex-pool",runtime_class="kata"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

// Nil receiver is a no-op so callers can wire the emitter unconditionally.
func TestWarmpoolWarmupFailureNilReceiverIsSafe_F_7_5_9(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncWarmpoolWarmupFailure("acme-pool", "gvisor", "setup_command_failed") // must not panic
}
