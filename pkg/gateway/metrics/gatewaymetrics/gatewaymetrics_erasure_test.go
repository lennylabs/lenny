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

// scrape returns the /metrics body.
func scrape(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	return rr.Body.String()
}

// TestErasureJobSLAMetrics_spec_12_8_768 pins the erasure SLA gauges the
// leader's sampler sets: the active-jobs count, the per-job age, and the
// per-job overdue value the ErasureJobOverdue alert reads, labelled by
// tenant and job. ClearErasureJobAge deletes both of the job's series.
// spec: 12.8 (Compliance Interfaces: Erasure SLA enforcement), 16.5 (Alerting Rules and SLOs)
func TestErasureJobSLAMetrics_spec_12_8_768(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetErasureJobsActive(3)
	m.SetErasureJobsActive(2)
	m.ObserveErasureJobDuration(42)
	m.SetErasureJobAge("acme", "erasure_abc", 7200)
	m.SetErasureJobOverdue("acme", "erasure_abc", 3600)
	m.SetErasureJobAge("globex", "erasure_def", 60)
	m.SetErasureJobOverdue("globex", "erasure_def", -3540)

	body := scrape(t, m)
	for _, want := range []string{
		`lenny_erasure_jobs_active 2`,
		`lenny_erasure_job_duration_seconds_count 1`,
		`lenny_erasure_job_age_seconds{job_id="erasure_abc",tenant_id="acme"} 7200`,
		`lenny_erasure_job_overdue_seconds{job_id="erasure_abc",tenant_id="acme"} 3600`,
		`lenny_erasure_job_overdue_seconds{job_id="erasure_def",tenant_id="globex"} -3540`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, "lenny_erasure_job_deadline_seconds") {
		t.Error("the unlabelled deadline gauge must not be exported")
	}

	// A cleared job loses both series; the other job keeps its own.
	m.ClearErasureJobAge("acme", "erasure_abc")
	body = scrape(t, m)
	if strings.Contains(body, `job_id="erasure_abc"`) {
		t.Error("ClearErasureJobAge must remove both the age and the overdue series")
	}
	if !strings.Contains(body, `lenny_erasure_job_overdue_seconds{job_id="erasure_def",tenant_id="globex"} -3540`) {
		t.Error("ClearErasureJobAge removed another job's series")
	}
}
