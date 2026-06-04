// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §11.2.1 operator-initiated billing-correction workflow —
// POST /v1/admin/billing-corrections and the approve/reject endpoints.

var correctionClock = func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) }

// secondAdmin authenticates as a distinct platform-admin, used to
// exercise the §11.2.1 four-eyes approval rule.
func withSecondAdminPrincipal(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "carol@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	return req.WithContext(ctx)
}

// newCorrectionAdmin builds a Router wired with the billing-correction
// workflow over an in-memory billing ledger pre-seeded with one
// session.created event (sequence 1, 100 input tokens) in acme.
func newCorrectionAdmin(t *testing.T, dualControlThreshold float64) (*admin.Router, billingstore.Store, correctionstore.Store, *recordingAudit) {
	t.Helper()
	billing := billingstore.NewMemory()
	if _, err := billing.Append(context.Background(), billingstore.Event{
		TenantID:    "acme",
		UserID:      "alice@acme.com",
		SessionID:   "sess-1",
		EventType:   billingstore.EventSessionCreated,
		TokensInput: 100,
	}); err != nil {
		t.Fatalf("seed billing event: %v", err)
	}
	corrections := correctionstore.NewMemoryWithClock(correctionClock)
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: correctionClock,
		Audit: audit,
	}).WithBillingCorrections(billing, corrections, dualControlThreshold)
	return router, billing, corrections, audit
}

func postCorrection(t *testing.T, h http.Handler, body admin.BillingCorrectionRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/billing-corrections", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeCorrection(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rr.Body.String())
	}
	return out
}

// TestCreateCorrectionRequiresPlatformAdmin covers §11.2.1 RBAC gating:
// the endpoint requires the issue_billing_corrections permission, which
// the §10.2 matrix grants to platform-admin only.
func TestCreateCorrectionRequiresPlatformAdmin(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	body := admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonTestSessionCleanup),
		TokensInput:          40,
	}
	// tenant-admin and user must be rejected with 403.
	for _, tc := range []struct {
		name string
		as   func(*http.Request) *http.Request
	}{
		{"tenant-admin", withTenantAdminPrincipal},
		{"user", withUserPrincipal},
		{"billing-viewer", withBillingViewerPrincipal},
	} {
		rr := postCorrection(t, router.Handler(), body, tc.as)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", tc.name, rr.Code)
		}
	}
}

// TestCreateCorrectionDualControlGoesPending covers §11.2.1: with the
// default threshold of 0, every operator-initiated correction is
// dual-control and lands in the pending state awaiting a second
// platform-admin.
func TestCreateCorrectionDualControlGoesPending(t *testing.T) {
	router, billing, corrections, audit := newCorrectionAdmin(t, 0)
	body := admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}
	rr := postCorrection(t, router.Handler(), body, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("dual-control submission: status %d, want 202 (body %s)", rr.Code, rr.Body.String())
	}
	resp := decodeCorrection(t, rr)
	if resp["state"] != "pending" {
		t.Errorf("state = %v, want pending", resp["state"])
	}
	if resp["dualControl"] != true {
		t.Errorf("dualControl = %v, want true", resp["dualControl"])
	}
	if resp["id"] == "" || resp["id"] == nil {
		t.Error("response is missing an approval_request_id")
	}
	// §11.7 / §11.2.1: nothing is written to the billing ledger before
	// approval — the original is the only event.
	events, _ := billing.Since(context.Background(), "acme", 0, 0)
	if len(events) != 1 {
		t.Errorf("no billing_correction should be written before approval, ledger has %d events", len(events))
	}
	pending, _ := corrections.List(context.Background(), correctionstore.Filter{State: correctionstore.StatePending})
	if len(pending) != 1 {
		t.Errorf("expected 1 pending correction, got %d", len(pending))
	}
	// The submission emits a billing.correction_issued audit event.
	if snap := audit.snapshot(); len(snap) == 0 || snap[0].Type != "billing.correction_issued" {
		t.Errorf("audit: first event should be billing.correction_issued, got %+v", snap)
	}
}

// TestApproveCorrectionAppendsReversalWithoutMutatingOriginal is the
// core §11.2.1 / §11.7 assertion: an approved correction is appended to
// the ledger as a billing_correction event; the original event is never
// updated.
func TestApproveCorrectionAppendsReversalWithoutMutatingOriginal(t *testing.T) {
	router, billing, _, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()

	// Submit (dual-control, pending).
	rr := postCorrection(t, h, admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("submission: status %d", rr.Code)
	}
	id, _ := decodeCorrection(t, rr)["id"].(string)

	// A second, distinct platform-admin approves.
	approveReq := withSecondAdminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/billing-corrections/"+id+"/approve", nil,
	))
	approveRR := httptest.NewRecorder()
	h.ServeHTTP(approveRR, approveReq)
	if approveRR.Code != http.StatusOK {
		t.Fatalf("approve: status %d (body %s)", approveRR.Code, approveRR.Body.String())
	}
	approved := decodeCorrection(t, approveRR)
	if approved["state"] != "approved" {
		t.Errorf("state after approval = %v, want approved", approved["state"])
	}

	// §11.2.1: the billing_correction is now in the ledger as an
	// appended event; the original event is unchanged.
	events, _ := billing.Since(context.Background(), "acme", 0, 0)
	if len(events) != 2 {
		t.Fatalf("ledger should hold the original and the correction, has %d events", len(events))
	}
	if events[0].EventType != billingstore.EventSessionCreated || events[0].TokensInput != 100 {
		t.Errorf("the original event was mutated: %+v", events[0])
	}
	if !events[1].IsCorrection() {
		t.Errorf("the second event should be a billing_correction, got %q", events[1].EventType)
	}
	if events[1].CorrectsSequence != 1 || events[1].TokensInput != 40 {
		t.Errorf("the correction should reference seq 1 with 40 tokens, got %+v", events[1])
	}
	if events[1].CorrectionReasonCode != billingstore.ReasonOperatorManualAdjustment {
		t.Errorf("correction reason code = %q, want OPERATOR_MANUAL_ADJUSTMENT", events[1].CorrectionReasonCode)
	}
	// §11.2.1 ledger reconstruction: the correction supersedes the
	// original's token count.
	reconciled := billingstore.ReconcileLedger(events)
	if len(reconciled) != 1 || reconciled[0].TokensInput != 40 {
		t.Errorf("reconciled ledger should show 40 tokens, got %+v", reconciled)
	}
}

// TestSelfApprovalRejected covers the §11.2.1 four-eyes rule: the
// submitting admin cannot approve their own correction.
func TestSelfApprovalRejected(t *testing.T) {
	router, billing, _, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()

	rr := postCorrection(t, h, admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	id, _ := decodeCorrection(t, rr)["id"].(string)

	// The same admin who submitted attempts to approve.
	selfReq := withAdminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/billing-corrections/"+id+"/approve", nil,
	))
	selfRR := httptest.NewRecorder()
	h.ServeHTTP(selfRR, selfReq)
	if selfRR.Code != http.StatusForbidden {
		t.Fatalf("self-approval: status %d, want 403 (body %s)", selfRR.Code, selfRR.Body.String())
	}
	// The correction must not have been committed.
	events, _ := billing.Since(context.Background(), "acme", 0, 0)
	if len(events) != 1 {
		t.Errorf("a self-approved correction must not reach the ledger, has %d events", len(events))
	}
}

// TestRejectCorrectionDoesNotReachLedger covers §11.2.1: a rejected
// correction stays in the pending registry with the rejected outcome
// and is never promoted to the billing ledger.
func TestRejectCorrectionDoesNotReachLedger(t *testing.T) {
	router, billing, corrections, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()

	rr := postCorrection(t, h, admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	id, _ := decodeCorrection(t, rr)["id"].(string)

	rejectReq := withSecondAdminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/billing-corrections/"+id+"/reject", nil,
	))
	rejectRR := httptest.NewRecorder()
	h.ServeHTTP(rejectRR, rejectReq)
	if rejectRR.Code != http.StatusOK {
		t.Fatalf("reject: status %d (body %s)", rejectRR.Code, rejectRR.Body.String())
	}
	if decodeCorrection(t, rejectRR)["state"] != "rejected" {
		t.Errorf("state after reject should be rejected")
	}
	// §11.2.1: nothing reaches the billing ledger.
	events, _ := billing.Since(context.Background(), "acme", 0, 0)
	if len(events) != 1 {
		t.Errorf("a rejected correction must not reach the ledger, has %d events", len(events))
	}
	// The rejected request is retained for audit.
	got, err := corrections.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get rejected correction: %v", err)
	}
	if got.State != correctionstore.StateRejected {
		t.Errorf("rejected request state = %q, want rejected", got.State)
	}
}

// TestSingleControlPathCommitsImmediately covers §11.2.1: when the
// dual-control threshold is positive, a correction whose adjustment is
// at or below it is committed by the submitting admin without a second
// approval.
func TestSingleControlPathCommitsImmediately(t *testing.T) {
	// Threshold 1000: a 60-token adjustment (100 -> 40) is below it.
	router, billing, _, _ := newCorrectionAdmin(t, 1000)
	rr := postCorrection(t, router.Handler(), admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonTestSessionCleanup),
		TokensInput:          40,
	}, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("single-control submission: status %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	resp := decodeCorrection(t, rr)
	if resp["state"] != "approved" {
		t.Errorf("single-control correction should be approved immediately, got %v", resp["state"])
	}
	if resp["dualControl"] != false {
		t.Errorf("dualControl = %v, want false", resp["dualControl"])
	}
	// The correction is in the ledger.
	events, _ := billing.Since(context.Background(), "acme", 0, 0)
	if len(events) != 2 || !events[1].IsCorrection() {
		t.Errorf("single-control correction should be appended, ledger has %d events", len(events))
	}
}

// TestLargeAdjustmentForcesDualControlAboveThreshold covers §11.2.1:
// a correction whose absolute adjustment exceeds the threshold always
// takes the dual-control path even when the threshold is positive.
func TestLargeAdjustmentForcesDualControlAboveThreshold(t *testing.T) {
	// Threshold 10: a 60-token adjustment exceeds it.
	router, _, _, _ := newCorrectionAdmin(t, 10)
	rr := postCorrection(t, router.Handler(), admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40, // 100 -> 40 is a 60-token adjustment.
	}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("above-threshold correction: status %d, want 202 (dual-control)", rr.Code)
	}
	if decodeCorrection(t, rr)["dualControl"] != true {
		t.Error("an above-threshold correction must require dual-control")
	}
}

// TestCreateCorrectionRejectsGatewayEmittedReason covers §11.2.1: a
// Category 1 (gateway-emitted) reason code cannot be used on an
// operator-initiated correction.
func TestCreateCorrectionRejectsGatewayEmittedReason(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	rr := postCorrection(t, router.Handler(), admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonRetryOvercounting),
		TokensInput:          40,
	}, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("a gateway-emitted reason code: status %d, want 422", rr.Code)
	}
}

// TestCreateCorrectionRejectsUnknownOriginal covers §11.2.1: a
// correction must reference an existing billing event.
func TestCreateCorrectionRejectsUnknownOriginal(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	rr := postCorrection(t, router.Handler(), admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 999,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("a correction of a non-existent event: status %d, want 404", rr.Code)
	}
}

// TestCreateCorrectionValidatesBody covers the §11.2.1 required fields.
func TestCreateCorrectionValidatesBody(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()
	cases := []struct {
		name string
		body admin.BillingCorrectionRequest
	}{
		{"missing tenantId", admin.BillingCorrectionRequest{
			CorrectsSequence: 1, CorrectionReasonCode: "OPERATOR_MANUAL_ADJUSTMENT",
		}},
		{"missing correctsSequence", admin.BillingCorrectionRequest{
			TenantID: "acme", CorrectionReasonCode: "OPERATOR_MANUAL_ADJUSTMENT",
		}},
		{"missing reason code", admin.BillingCorrectionRequest{
			TenantID: "acme", CorrectsSequence: 1,
		}},
	}
	for _, tc := range cases {
		rr := postCorrection(t, h, tc.body, withAdminPrincipal)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, rr.Code)
		}
	}
}

// TestApproveAlreadyDecidedCorrectionConflicts covers §11.2.1: a
// correction cannot be approved twice.
func TestApproveAlreadyDecidedCorrectionConflicts(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()
	rr := postCorrection(t, h, admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	id, _ := decodeCorrection(t, rr)["id"].(string)

	approve := func() *httptest.ResponseRecorder {
		req := withSecondAdminPrincipal(httptest.NewRequest(
			http.MethodPost, "/v1/admin/billing-corrections/"+id+"/approve", nil,
		))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if first := approve(); first.Code != http.StatusOK {
		t.Fatalf("first approve: status %d", first.Code)
	}
	if second := approve(); second.Code != http.StatusConflict {
		t.Errorf("second approve: status %d, want 409", second.Code)
	}
}

// TestListAndGetCorrections covers the §11.2.1 correction-queue read
// endpoints.
func TestListAndGetCorrections(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	h := router.Handler()
	rr := postCorrection(t, h, admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}, withAdminPrincipal)
	id, _ := decodeCorrection(t, rr)["id"].(string)

	// List.
	listReq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/billing-corrections", nil))
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: status %d", listRR.Code)
	}
	var listResp struct {
		BillingCorrections []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.BillingCorrections) != 1 {
		t.Errorf("list: got %d corrections, want 1", len(listResp.BillingCorrections))
	}

	// Get by id.
	getReq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/billing-corrections/"+id, nil))
	getRR := httptest.NewRecorder()
	h.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get: status %d", getRR.Code)
	}
	if decodeCorrection(t, getRR)["id"] != id {
		t.Error("get returned the wrong correction")
	}

	// Get unknown id.
	missReq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/billing-corrections/ghost", nil))
	missRR := httptest.NewRecorder()
	h.ServeHTTP(missRR, missReq)
	if missRR.Code != http.StatusNotFound {
		t.Errorf("get unknown: status %d, want 404", missRR.Code)
	}
}
