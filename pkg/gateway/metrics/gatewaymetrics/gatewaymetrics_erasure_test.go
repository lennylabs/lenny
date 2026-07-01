// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

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
