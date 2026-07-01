// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

func TestStorageWriteMetricsExposeValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetPostgresWriteIops(123)
	m.SetPostgresWriteCeilingIops(600)
	// §12.3 line 97 / §16.1 line 228 — the SIEM outbox forwarder sets the
	// delivery-lag gauge; the configured threshold is emitted at startup
	// so AuditSIEMDeliveryLag compares against an operator-tunable scalar.
	// F-12.3.6 / F-12.3.17.
	m.SetSIEMDeliveryLagSeconds(42)
	m.SetSIEMMaxDeliveryLagSeconds(45)
	// §12.3 line 99 — the AuditBatchingNoSIEM counter is incremented once
	// at startup when production batching has no SIEM. F-12.3.15.
	m.IncAuditBatchingNoSIEM()
	m.IncBillingFlushPressure()
	m.IncBillingFlushPressure()
	m.IncAuditChainIntegrity("verified")
	m.IncAuditChainIntegrity("broken")
	// §11.7 item 2 — the periodic integrity check increments grant-drift
	// on detection; the AuditGrantDrift alert reads `> 0`. F-11.7.3.
	m.IncAuditGrantDrift()
	m.IncAuditGrantDrift()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_postgres_write_iops 123`,
		`lenny_postgres_write_ceiling_iops 600`,
		`lenny_audit_siem_delivery_lag_seconds 42`,
		`lenny_audit_siem_max_delivery_lag_seconds 45`,
		`lenny_audit_batching_no_siem_total 1`,
		`lenny_billing_flush_pressure_total 2`,
		`lenny_audit_chain_integrity_total{state="verified"} 1`,
		`lenny_audit_chain_integrity_total{state="broken"} 1`,
		`lenny_audit_grant_drift_total 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}
