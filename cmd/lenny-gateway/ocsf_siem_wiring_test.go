// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

// spec: §11.7 Wire Format — the gateway's ocsfMetricsAdapter bridges the
// OCSF translator's per-row TranslationFailed callback onto the
// Prometheus lenny_audit_ocsf_translation_failed_total counter, labeled
// by event type and ocsf.ErrorClass. The success and dead-letter
// callbacks are no-ops at the Prometheus layer (no dedicated §16.1
// series). F-11.7.1 / F-11.7.15.
func TestOCSFMetricsAdapterBridgesTranslationFailure(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	adapter := ocsfMetricsAdapter{metrics: m}
	adapter.TranslationFailed("session.created", ocsf.ErrClassMappingMissing)
	adapter.TranslationSucceeded("session.created")
	adapter.DeadLettered("session.created")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	want := `lenny_audit_ocsf_translation_failed_total{error_class="class_mapping_missing",event_type="session.created"} 1`
	if !strings.Contains(rr.Body.String(), want) {
		t.Errorf("/metrics missing %q", want)
	}
}
