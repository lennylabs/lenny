// SPDX-License-Identifier: MIT

package promemit

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// spec: §16.1 — the Token Service registers the four declared metric
// vectors and surfaces them on /metrics. The catalog test in
// pkg/observability/metrics already proves the names are registered;
// here we prove the binary's metrics handler renders the catalog when
// the emitter records a sample.
func TestEmitterRegistersAndServesCatalog(t *testing.T) {
	emitter, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	emitter.RecordRequestDuration("exchange", 50*time.Millisecond)
	emitter.IncErrors("exchange", "invalid_grant")
	emitter.IncRateLimited("caller_per_second")
	emitter.IncRateLimitedSampled("caller_per_second")
	emitter.IncSecretReload("success")

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	emitter.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("metrics handler status=%d, want 200", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	bodyStr := string(body)
	mustContain := []string{
		"lenny_token_service_request_duration_seconds",
		"lenny_token_service_errors_total",
		"lenny_token_service_secret_reloads_total",
		"lenny_oauth_token_rate_limited_total",
		"lenny_oauth_token_rate_limited_sampled_total",
	}
	for _, want := range mustContain {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

// spec: §13.3 line 595 / §16.1 — the Token Service's
// lenny_time_drift_seconds gauge is registered, materialized at
// startup as 0, and updates via SetTimeDrift. F-13.3.5.
func TestEmitterSetTimeDriftRoundTrips(t *testing.T) {
	emitter, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	emitter.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "lenny_time_drift_seconds 0") {
		t.Fatalf("/metrics missing zero-init drift gauge:\n%s", body)
	}
	emitter.SetTimeDrift(1.75)
	w = httptest.NewRecorder()
	emitter.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body, _ = io.ReadAll(w.Body)
	if !strings.Contains(string(body), "lenny_time_drift_seconds 1.75") {
		t.Fatalf("/metrics missing updated drift gauge:\n%s", body)
	}
}
