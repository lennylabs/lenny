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

// captureAppender records the last payload appended so a test can
// assert on the §16.7 interceptor.rejected row contents.
type captureAppender struct {
	tenant  string
	event   string
	payload map[string]any
}

func (a *captureAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	a.tenant = tenantID
	a.event = eventType
	a.payload = map[string]any{}
	_ = json.Unmarshal(payload, &a.payload)
	return audit.Row{}, nil
}

// spec: §4.8 line 981 — the audit row records the interceptor that
// actually rejected (Result.RejectedBy), not a fixed built-in.
func TestRecordRejectionUsesRejectedBy(t *testing.T) {
	app := &captureAppender{}
	sink := policy.NewAuditSink(app, func() time.Time { return time.Unix(0, 0).UTC() })
	err := sink.RecordRejection(context.Background(),
		policy.RejectionContext{TenantID: "acme", CallerSub: "alice", Phase: interceptor.PhasePreDelegation},
		interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked", RejectedBy: "content-scanner"})
	if err != nil {
		t.Fatalf("RecordRejection: %v", err)
	}
	if app.event != policy.EventTypeInterceptorRejected {
		t.Errorf("event = %q, want %q", app.event, policy.EventTypeInterceptorRejected)
	}
	if got := app.payload["interceptor_name"]; got != "content-scanner" {
		t.Errorf("interceptor_name = %v, want content-scanner", got)
	}
	if got := app.payload["interceptor_ref"]; got != "content-scanner" {
		t.Errorf("interceptor_ref = %v, want content-scanner", got)
	}
	if got := app.payload["phase"]; got != string(interceptor.PhasePreDelegation) {
		t.Errorf("phase = %v, want PreDelegation", got)
	}
	// A non-timeout REJECT carries no timeout_ms field.
	if _, ok := app.payload["timeout_ms"]; ok {
		t.Error("timeout_ms present on a deliberate REJECT")
	}
}

// A REJECT that does not name a rejector falls back to QuotaEvaluator,
// preserving the prior PostAuth behavior.
func TestRecordRejectionFallsBackToQuotaEvaluator(t *testing.T) {
	app := &captureAppender{}
	sink := policy.NewAuditSink(app, nil)
	if err := sink.RecordRejection(context.Background(),
		policy.RejectionContext{TenantID: "acme", Phase: interceptor.PhasePostAuth},
		interceptor.Result{Action: interceptor.ActionReject, Code: policy.CodeQuotaExceeded}); err != nil {
		t.Fatalf("RecordRejection: %v", err)
	}
	if got := app.payload["interceptor_name"]; got != policy.QuotaEvaluatorName {
		t.Errorf("interceptor_name = %v, want %q", got, policy.QuotaEvaluatorName)
	}
}

// spec: §4.8 line 1032 — a fail-closed timeout row carries timeout_ms.
func TestRecordRejectionTimeoutCarriesTimeoutMs(t *testing.T) {
	app := &captureAppender{}
	sink := policy.NewAuditSink(app, nil)
	if err := sink.RecordRejection(context.Background(),
		policy.RejectionContext{TenantID: "acme", Phase: interceptor.PhasePostAuth},
		interceptor.Result{
			Action:     interceptor.ActionReject,
			Code:       interceptor.CodeInterceptorTimeout,
			RejectedBy: "slow-classifier",
			TimeoutMs:  100,
		}); err != nil {
		t.Fatalf("RecordRejection: %v", err)
	}
	// JSON numbers unmarshal into float64.
	if got, ok := app.payload["timeout_ms"].(float64); !ok || got != 100 {
		t.Errorf("timeout_ms = %v (ok=%v), want 100", app.payload["timeout_ms"], ok)
	}
	if got := app.payload["interceptor_ref"]; got != "slow-classifier" {
		t.Errorf("interceptor_ref = %v, want slow-classifier", got)
	}
}
