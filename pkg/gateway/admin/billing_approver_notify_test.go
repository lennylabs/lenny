// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// capturingNotifier records the §11.2.1 approver-notification payload on
// a buffered channel so the test can assert delivery without racing the
// detached notification goroutine.
type capturingNotifier struct{ ch chan []byte }

func newCapturingNotifier() *capturingNotifier { return &capturingNotifier{ch: make(chan []byte, 1)} }

func (n *capturingNotifier) NotifyApprovers(_ context.Context, payload []byte) error {
	n.ch <- payload
	return nil
}

func (n *capturingNotifier) wait(t *testing.T) ([]byte, bool) {
	t.Helper()
	select {
	case p := <-n.ch:
		return p, true
	case <-time.After(2 * time.Second):
		return nil, false
	}
}

// spec: §11.2.1 line 175 — a correction entering the dual-control
// pending state notifies eligible approvers via the configured
// billing.approverNotificationWebhook channel.
func TestDualControlNotifiesApprovers_spec_11_2_1_175(t *testing.T) {
	router, _, _, _ := newCorrectionAdmin(t, 0)
	notifier := newCapturingNotifier()
	router = router.WithApproverNotifier(notifier)

	body := admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40,
	}
	rr := postCorrection(t, router.Handler(), body, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("dual-control submission: status %d, want 202 (body %s)", rr.Code, rr.Body.String())
	}
	payload, ok := notifier.wait(t)
	if !ok {
		t.Fatal("approver notification was not delivered on the dual-control path")
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("notification payload is not valid JSON: %v", err)
	}
	if got["type"] != "billing.correction_approval_requested" {
		t.Errorf("type = %v, want billing.correction_approval_requested", got["type"])
	}
	if got["tenantId"] != "acme" {
		t.Errorf("tenantId = %v, want acme", got["tenantId"])
	}
	if got["approvalRequestId"] == "" || got["approvalRequestId"] == nil {
		t.Error("notification is missing approvalRequestId")
	}
}

// spec: §11.2.1 — a single-control correction (committed immediately by
// the submitter) does not enter the pending state, so no approver
// notification is sent.
func TestSingleControlDoesNotNotifyApprovers_spec_11_2_1_175(t *testing.T) {
	// A positive threshold makes a small-adjustment correction
	// single-control: it commits without a second approval.
	router, _, _, _ := newCorrectionAdmin(t, 1000)
	notifier := newCapturingNotifier()
	router = router.WithApproverNotifier(notifier)

	body := admin.BillingCorrectionRequest{
		TenantID: "acme", CorrectsSequence: 1,
		CorrectionReasonCode: string(billingstore.ReasonOperatorManualAdjustment),
		TokensInput:          40, // adjustment below the threshold
	}
	rr := postCorrection(t, router.Handler(), body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("single-control submission: status %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	select {
	case <-notifier.ch:
		t.Fatal("approver notification fired on the single-control path")
	case <-time.After(200 * time.Millisecond):
		// expected: no notification
	}
}
