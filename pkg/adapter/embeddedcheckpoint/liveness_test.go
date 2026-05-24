// SPDX-License-Identifier: MIT

package embeddedcheckpoint

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §4.4 line 250 — `/healthz` returns 200 when checkpointStuck is
// false.
func TestLivenessHandlerReturnsHealthyByDefault(t *testing.T) {
	flag := &StuckFlag{}
	h := LivenessHandler(flag)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"healthy"`) {
		t.Errorf("body = %q, want healthy status", rr.Body.String())
	}
}

// spec: §4.4 line 250 — `/healthz` returns 503 with reason
// checkpoint_stuck when the flag is set.
func TestLivenessHandlerReturns503WhenStuck(t *testing.T) {
	flag := &StuckFlag{}
	flag.Store(true)
	h := LivenessHandler(flag)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"unhealthy"`) {
		t.Errorf("body = %q, want unhealthy status", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"reason":"checkpoint_stuck"`) {
		t.Errorf("body = %q, want checkpoint_stuck reason", rr.Body.String())
	}
}

// A nil StuckFlag is treated as never-stuck so a misconfigured caller
// does not surface as 500.
func TestLivenessHandlerNilFlagIsHealthy(t *testing.T) {
	h := LivenessHandler(nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a nil flag", rr.Code)
	}
}
