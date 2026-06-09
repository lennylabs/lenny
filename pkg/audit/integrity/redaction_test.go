// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"errors"
	"testing"
)

// spec: §12.8 lines 810-827; §16.5 line 467 — a deployment with no
// redaction-marked orphans reports nothing, so the periodic check never
// touches lenny_audit_redaction_receipt_missing_total. F-11.7.15.
func TestCheckRedactionReceiptsClean(t *testing.T) {
	got, err := CheckRedactionReceipts(context.Background(), &scriptQuerier{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no orphaned redactions, got %v", got)
	}
}

// spec: §12.8 line 810 — a redaction-marked row without a
// signature-verifying receipt is a compliance incident. The detector
// returns the per-tenant orphan count, sorted by tenant, dropping any
// zero count the aggregate query might surface. F-11.7.15.
func TestCheckRedactionReceiptsOrphans(t *testing.T) {
	q := &scriptQuerier{
		redaction: [][]any{
			{"globex", int64(2)},
			{"acme", int64(1)},
			{"initech", int64(0)}, // zero rows must be dropped
		},
	}
	got, err := CheckRedactionReceipts(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tenants with orphans, got %d: %v", len(got), got)
	}
	if got[0].TenantID != "acme" || got[0].Missing != 1 {
		t.Fatalf("first result want {acme 1}, got %+v", got[0])
	}
	if got[1].TenantID != "globex" || got[1].Missing != 2 {
		t.Fatalf("second result want {globex 2}, got %+v", got[1])
	}
}

// spec: §11.7 line 370 — a scan error (for example the
// audit_redaction_receipts relation absent on a partial migration) is
// surfaced to the caller, which treats it as a non-fatal skipped cycle
// rather than drift. F-11.7.15.
func TestCheckRedactionReceiptsQueryError(t *testing.T) {
	q := &scriptQuerier{queryErr: errors.New("relation \"audit_redaction_receipts\" does not exist")}
	_, err := CheckRedactionReceipts(context.Background(), q)
	if err == nil {
		t.Fatal("expected an error when the receipts relation is unavailable")
	}
}

// spec: §16.5 line 467 — the periodic check turns each orphaned-redaction
// tenant into one OnRedactionReceiptMissing call carrying the count, the
// gateway adapter for lenny_audit_redaction_receipt_missing_total. An
// orphaned redaction is a compliance page, not a chain-tamper hard-fail,
// so CheckOnce does not report drift for it. F-11.7.15.
func TestPeriodicCheckRedactionReceiptMissing(t *testing.T) {
	var calls []RedactionReceiptResult
	p := &PeriodicCheck{
		DB: &scriptQuerier{
			redaction: [][]any{{"acme", int64(3)}},
		},
		Cfg: PeriodicConfig{ChainSampleN: 1000},
		OnRedactionReceiptMissing: func(tenantID string, n int) {
			calls = append(calls, RedactionReceiptResult{TenantID: tenantID, Missing: n})
		},
	}
	drift := p.CheckOnce(context.Background())
	if drift {
		t.Fatal("an orphaned redaction must not be reported as chain drift")
	}
	if len(calls) != 1 || calls[0].TenantID != "acme" || calls[0].Missing != 3 {
		t.Fatalf("OnRedactionReceiptMissing not invoked correctly: %v", calls)
	}
}

// spec: §11.7 line 370 — a clean redaction scan fires no observer, so the
// receipt-missing series stays at its steady-state zero. F-11.7.15.
func TestPeriodicCheckRedactionReceiptClean(t *testing.T) {
	fired := false
	p := &PeriodicCheck{
		DB:                        &scriptQuerier{},
		Cfg:                       PeriodicConfig{ChainSampleN: 1000},
		OnRedactionReceiptMissing: func(string, int) { fired = true },
	}
	if p.CheckOnce(context.Background()) {
		t.Fatal("clean cycle reported drift")
	}
	if fired {
		t.Fatal("OnRedactionReceiptMissing fired on a clean cycle")
	}
}
