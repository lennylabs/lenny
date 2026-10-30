// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

func TestExperimentTargetingMetricsRegistered_spec_10_7_833(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveExperimentTargetingDuration("flags.acme.com", 0.042)
	m.RecordExperimentTargetingError("flags.acme.com", "FLAG_NOT_FOUND")
	m.RecordExperimentTargetingError("flags.acme.com", "timeout")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_experiment_targeting_duration_seconds_count{provider="flags.acme.com"} 1`,
		`lenny_experiment_targeting_error_total{error_type="FLAG_NOT_FOUND",provider="flags.acme.com"} 1`,
		`lenny_experiment_targeting_error_total{error_type="timeout",provider="flags.acme.com"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestRecordSessionTerminalMetrics_spec_16_1_161(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// One failed treatment session, one completed treatment session, one
	// completed un-enrolled session (empty variant, empty execution mode).
	m.RecordSessionTerminal("acme", "task", "treatment", true, 12.0)
	m.RecordSessionTerminal("acme", "task", "treatment", false, 30.0)
	m.RecordSessionTerminal("acme", "", "", false, 5.0)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_total{session_type="task",tenant_id="acme",variant_id="treatment"} 2`,
		`lenny_session_error_total{session_type="task",tenant_id="acme",variant_id="treatment"} 1`,
		`lenny_session_duration_seconds_count{session_type="task",tenant_id="acme",variant_id="treatment"} 2`,
		// empty execution mode falls back to session_type="session"
		`lenny_session_total{session_type="session",tenant_id="acme",variant_id=""} 1`,
		`lenny_session_duration_seconds_count{session_type="session",tenant_id="acme",variant_id=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
	// A non-error terminal must not have produced an error series for the
	// un-enrolled session.
	if strings.Contains(body, `lenny_session_error_total{session_type="session",tenant_id="acme",variant_id=""}`) {
		t.Errorf("/metrics unexpectedly recorded an error for a non-failed session\n%s", body)
	}
}

func TestObserveEvalScore_spec_16_1_164(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveEvalScore("acme", "safety", "treatment", 0.97)
	m.ObserveEvalScore("acme", "safety", "treatment", 0.93)
	m.ObserveEvalScore("acme", "helpfulness", "", 0.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_eval_score_count{scorer="safety",tenant_id="acme",variant_id="treatment"} 2`,
		`lenny_eval_score_sum{scorer="safety",tenant_id="acme",variant_id="treatment"} 1.9`,
		`lenny_eval_score_count{scorer="helpfulness",tenant_id="acme",variant_id=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}
