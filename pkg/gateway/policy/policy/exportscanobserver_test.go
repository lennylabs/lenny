// SPDX-License-Identifier: MIT

package policy_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// recordingExportAppender captures every appended audit row so a test
// can assert which §11.7 events the export-scan observer emitted.
type recordingExportAppender struct {
	rows []recordedRow
}

type recordedRow struct {
	tenant  string
	event   string
	payload map[string]any
}

func (a *recordingExportAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	row := recordedRow{tenant: tenantID, event: eventType, payload: map[string]any{}}
	_ = json.Unmarshal(payload, &row.payload)
	a.rows = append(a.rows, row)
	return audit.Row{}, nil
}

// fakeExportMetrics records the §16.1 metric calls.
type fakeExportMetrics struct {
	scans     []string // "pool|tenant|policy|ref|outcome"
	durations int
}

func (m *fakeExportMetrics) IncExportFileScan(pool, tenantID, policyName, interceptorRef, outcome string) {
	m.scans = append(m.scans, pool+"|"+tenantID+"|"+policyName+"|"+interceptorRef+"|"+outcome)
}

func (m *fakeExportMetrics) ObserveExportFileScanDuration(_, _, _ string, _ float64) {
	m.durations++
}

func baseEvent(outcome interceptor.ExportScanOutcome, reason string) interceptor.ExportScanEvent {
	return interceptor.ExportScanEvent{
		Pool:           "pool-a",
		TenantID:       "acme",
		SessionID:      "sess-1",
		PolicyName:     "orchestrator-policy",
		InterceptorRef: "export-scanner",
		FilePath:       "src/x.go",
		FileSize:       42,
		Outcome:        outcome,
		Reason:         reason,
		Duration:       7 * time.Millisecond,
	}
}

// spec: §11.7 line 69 — a `rejected` outcome emits
// delegation.export_file_scan_rejected with the §11.7 lines 119-122
// payload, and records the metric. F-8.7.9; F-8.7.10.
func TestExportScanObserverEmitsRejected_spec_11_7_69(t *testing.T) {
	app := &recordingExportAppender{}
	met := &fakeExportMetrics{}
	obs := policy.NewExportScanObserver(app, met, func() time.Time { return time.Unix(0, 0).UTC() })

	obs.ExportFileScanned(context.Background(), baseEvent(interceptor.OutcomeRejected, "blocked content"))

	if len(app.rows) != 1 {
		t.Fatalf("appended %d rows, want 1", len(app.rows))
	}
	row := app.rows[0]
	if row.event != policy.EventTypeDelegationExportFileScanRejected {
		t.Errorf("event = %q, want %q", row.event, policy.EventTypeDelegationExportFileScanRejected)
	}
	if row.tenant != "acme" {
		t.Errorf("tenant = %q, want acme", row.tenant)
	}
	if row.payload["policy_name"] != "orchestrator-policy" ||
		row.payload["interceptor_ref"] != "export-scanner" ||
		row.payload["file_path"] != "src/x.go" ||
		row.payload["reason"] != "blocked content" {
		t.Errorf("payload = %v, want the §11.7 export-reject fields", row.payload)
	}
	if fs, ok := row.payload["file_size"].(float64); !ok || int(fs) != 42 {
		t.Errorf("file_size = %v, want 42", row.payload["file_size"])
	}
	if len(met.scans) != 1 || met.scans[0] != "pool-a|acme|orchestrator-policy|export-scanner|rejected" {
		t.Errorf("metric scans = %v, want one rejected increment with full labels", met.scans)
	}
	if met.durations != 1 {
		t.Errorf("duration observations = %d, want 1", met.durations)
	}
}

// spec: §11.7 line 70 — a `failed_open` outcome emits
// delegation.export_scan_failed_open with the reason token. F-8.7.9.
func TestExportScanObserverEmitsFailedOpen_spec_11_7_70(t *testing.T) {
	app := &recordingExportAppender{}
	met := &fakeExportMetrics{}
	obs := policy.NewExportScanObserver(app, met, func() time.Time { return time.Unix(0, 0).UTC() })

	obs.ExportFileScanned(context.Background(), baseEvent(interceptor.OutcomeFailedOpen, "timeout"))

	if len(app.rows) != 1 || app.rows[0].event != policy.EventTypeDelegationExportScanFailedOpen {
		t.Fatalf("rows = %+v, want one export_scan_failed_open", app.rows)
	}
	if app.rows[0].payload["reason"] != "timeout" {
		t.Errorf("reason = %v, want timeout", app.rows[0].payload["reason"])
	}
	if len(met.scans) != 1 || met.scans[0] != "pool-a|acme|orchestrator-policy|export-scanner|failed_open" {
		t.Errorf("metric scans = %v, want one failed_open increment", met.scans)
	}
}

// spec: §11.7 lines 69-70 — admitted, modified, and failed_closed have
// no dedicated §11.7 audit event; the observer records the metric only.
// F-8.7.9; F-8.7.10.
func TestExportScanObserverNoAuditForNonRejectOutcomes_spec_11_7_69(t *testing.T) {
	for _, outcome := range []interceptor.ExportScanOutcome{
		interceptor.OutcomeAdmitted, interceptor.OutcomeModified, interceptor.OutcomeFailedClosed,
	} {
		app := &recordingExportAppender{}
		met := &fakeExportMetrics{}
		obs := policy.NewExportScanObserver(app, met, nil)
		obs.ExportFileScanned(context.Background(), baseEvent(outcome, ""))
		if len(app.rows) != 0 {
			t.Errorf("outcome %q: appended %d rows, want 0", outcome, len(app.rows))
		}
		if len(met.scans) != 1 {
			t.Errorf("outcome %q: metric scans = %v, want exactly one increment", outcome, met.scans)
		}
	}
}

// A nil appender and nil metrics recorder must not panic.
func TestExportScanObserverNilSinksDoNotPanic(t *testing.T) {
	obs := policy.NewExportScanObserver(nil, nil, nil)
	obs.ExportFileScanned(context.Background(), baseEvent(interceptor.OutcomeRejected, "x"))
	obs.ExportFileScanned(context.Background(), baseEvent(interceptor.OutcomeAdmitted, ""))
}
