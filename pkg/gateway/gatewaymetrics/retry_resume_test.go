// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

// spec: §16.1 catalog — lenny_session_retry_total{failure_class} and
// lenny_session_resume_attempts_total{pool, outcome} are exposed on
// /metrics with the spec-named labels. F-7.3.10.

func TestSessionRetryTotalExposes_F_7_3_10(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSessionRetry("runtime_failure")
	m.IncSessionRetry("runtime_failure")
	m.IncSessionRetry("unknown")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_retry_total{failure_class="runtime_failure"} 2`,
		`lenny_session_retry_total{failure_class="unknown"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

func TestSessionResumeAttemptsTotalExposes_F_7_3_10(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSessionResumeAttempt("default", "success")
	m.IncSessionResumeAttempt("default", "success")
	m.IncSessionResumeAttempt("default", "failure")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_resume_attempts_total{outcome="success",pool="default"} 2`,
		`lenny_session_resume_attempts_total{outcome="failure",pool="default"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §16.1 (lenny_session_expiry_total{reason}); §16.1.1 (reason
// vocabulary) — the session-expiry counter is exposed on /metrics labelled by
// pool and the spec reason vocabulary (max_session_age | max_idle_time). The
// metric was declared in the §16.1 catalog with no emitter before S32; this
// pins the emitter to the catalog label set. F-11.3.7.
//
// diagnosis: a failure means the idle-termination counter is not scrapable or
// uses the wrong label set, so an operator cannot distinguish idle reclamation
// from age-cap expiry on the dashboards and burn-rate alerts.
func TestSessionExpiryTotalExposes_F_11_3_7(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSessionExpiry("default", "max_idle_time")
	m.IncSessionExpiry("default", "max_idle_time")
	m.IncSessionExpiry("default", "max_session_age")
	m.IncSessionExpiry("", "max_session_age")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_expiry_total{pool="default",reason="max_idle_time"} 2`,
		`lenny_session_expiry_total{pool="default",reason="max_session_age"} 1`,
		`lenny_session_expiry_total{pool="",reason="max_session_age"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n---\n%s", want, body)
		}
	}
}

// Nil receiver is a no-op so callers can wire the emitter unconditionally.
func TestSessionRetryNilReceiverIsSafe_F_7_3_10(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncSessionRetry("runtime_failure") // must not panic
	m.IncSessionResumeAttempt("p", "success")
	m.IncSessionExpiry("p", "max_idle_time")
}
