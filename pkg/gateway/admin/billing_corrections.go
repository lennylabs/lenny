// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// ApproverNotifier delivers a §11.2.1 dual-control approval
// notification (the billing.approverNotificationWebhook channel). The
// production implementation wraps the §11.2.1 webhook sink so the
// notification inherits HMAC signing, exponential-backoff retry, and
// dead-lettering. A nil notifier leaves the channel unconfigured; the
// dual-control workflow still records and audits the pending request.
//
// spec: §11.2.1 line 175. F-11.2.14.
type ApproverNotifier interface {
	NotifyApprovers(ctx context.Context, payload []byte) error
}

// WithApproverNotifier wires the §11.2.1 approver-notification channel.
func (r *Router) WithApproverNotifier(n ApproverNotifier) *Router {
	r.approverNotifier = n
	return r
}

// approverNotification is the JSON body posted to the
// billing.approverNotificationWebhook when a correction enters the
// dual-control pending state. It carries no replacement values that a
// notification channel does not need; an approver follows the
// approvalRequestId to the correction-queue API for the full detail.
type approverNotification struct {
	Type                 string `json:"type"`
	ApprovalRequestID    string `json:"approvalRequestId"`
	TenantID             string `json:"tenantId"`
	CorrectsSequence     uint64 `json:"correctsSequence"`
	CorrectionReasonCode string `json:"correctionReasonCode"`
	SubmittedBy          string `json:"submittedBy"`
	SubmittedAt          string `json:"submittedAt"`
}

// notifyApprovers fires the §11.2.1 approver notification for a pending
// dual-control correction. It is best-effort and runs detached from the
// request so webhook retry/backoff never blocks the HTTP response; the
// notifier owns its own dead-letter handling on exhaustion.
func (r *Router) notifyApprovers(p correctionstore.PendingCorrection) {
	if r.approverNotifier == nil {
		return
	}
	body, err := json.Marshal(approverNotification{
		Type:                 "billing.correction_approval_requested",
		ApprovalRequestID:    p.ID,
		TenantID:             p.TenantID,
		CorrectsSequence:     p.CorrectsSequence,
		CorrectionReasonCode: string(p.ReasonCode),
		SubmittedBy:          p.SubmittedBy,
		SubmittedAt:          rfc3339Nano(p.SubmittedAt),
	})
	if err != nil {
		return
	}
	go func() {
		// Detached from the request context, which ends when the 202 is
		// written; bound the delivery so a stuck webhook cannot leak a
		// goroutine indefinitely.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = r.approverNotifier.NotifyApprovers(ctx, body)
	}()
}

// WithBillingCorrections wires the §11.2.1 operator-initiated
// billing-correction workflow onto the Router. billing is the
// append-only billing ledger an approved correction is written to;
// corrections is the pending-correction registry that holds a request
// through the dual-control approval gate. dualControlThreshold is the
// §11.2.1 billing.dualControlThreshold: a correction whose absolute
// adjustment value exceeds it requires a second platform-admin's
// approval. The default of 0 means every operator-initiated correction
// is dual-control.
func (r *Router) WithBillingCorrections(billing billingstore.Store, corrections correctionstore.Store, dualControlThreshold float64) *Router {
	r.billing = billing
	r.corrections = corrections
	r.dualControlThresh = dualControlThreshold
	return r
}

// BillingCorrectionRequest is the §11.2.1 POST /v1/admin/billing-corrections
// body. The replacement values supersede the original event's
// corresponding fields when a consumer reconstructs the ledger.
type BillingCorrectionRequest struct {
	TenantID             string  `json:"tenantId"`
	CorrectsSequence     uint64  `json:"correctsSequence"`
	CorrectionReasonCode string  `json:"correctionReasonCode"`
	CorrectionDetail     string  `json:"correctionDetail,omitempty"`
	TokensInput          uint64  `json:"tokensInput"`
	TokensOutput         uint64  `json:"tokensOutput"`
	PodMinutes           float64 `json:"podMinutes,omitempty"`
}

// billingCorrectionPayload is the §11.2.1 billing-correction wire shape
// returned by the create, get, list, approve, and reject endpoints.
type billingCorrectionPayload struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	CorrectsSequence     uint64  `json:"correctsSequence"`
	CorrectionReasonCode string  `json:"correctionReasonCode"`
	CorrectionDetail     string  `json:"correctionDetail,omitempty"`
	TokensInput          uint64  `json:"tokensInput"`
	TokensOutput         uint64  `json:"tokensOutput"`
	PodMinutes           float64 `json:"podMinutes,omitempty"`
	State                string  `json:"state"`
	SubmittedBy          string  `json:"submittedBy"`
	DecidedBy            string  `json:"decidedBy,omitempty"`
	DualControl          bool    `json:"dualControl"`
	CommittedSequence    uint64  `json:"committedSequence,omitempty"`
	SubmittedAt          string  `json:"submittedAt"`
	DecidedAt            string  `json:"decidedAt,omitempty"`
}

func correctionPayload(c correctionstore.PendingCorrection) billingCorrectionPayload {
	return billingCorrectionPayload{
		ID:                   c.ID,
		TenantID:             c.TenantID,
		CorrectsSequence:     c.CorrectsSequence,
		CorrectionReasonCode: string(c.ReasonCode),
		CorrectionDetail:     c.Detail,
		TokensInput:          c.TokensInput,
		TokensOutput:         c.TokensOutput,
		PodMinutes:           c.PodMinutes,
		State:                string(c.State),
		SubmittedBy:          c.SubmittedBy,
		DecidedBy:            c.DecidedBy,
		DualControl:          c.DualControl,
		CommittedSequence:    c.CommittedSequence,
		SubmittedAt:          rfc3339Nano(c.SubmittedAt),
		DecidedAt:            rfc3339Nano(c.DecidedAt),
	}
}

// handleCreateBillingCorrection implements POST /v1/admin/billing-corrections —
// the §11.2.1 operator-initiated (Category 2) billing-correction
// submission.
//
// The correction is never an UPDATE of the original billing event. It
// is recorded as a pending request and, once cleared, written to the
// ledger as an appended billing_correction event. This preserves the
// §11.7 append-only immutability control on the billing ledger.
//
// The §11.2.1 dual-control rule: a correction whose absolute adjustment
// value exceeds billing.dualControlThreshold must be approved by a
// second, distinct platform-admin before it is committed. A correction
// at or below the threshold (only possible when the threshold is
// positive) is single-control — it is committed immediately. The
// default threshold of 0 makes every correction dual-control.
func (r *Router) handleCreateBillingCorrection(w http.ResponseWriter, req *http.Request) {
	var body BillingCorrectionRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.TenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId is required",
			map[string]any{"field": "tenantId"})
		return
	}
	if body.CorrectsSequence == 0 {
		// §11.2.1: sequence numbers start at 1, so 0 never references a
		// real event.
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"correctsSequence is required and must reference an existing billing event",
			map[string]any{"field": "correctsSequence"})
		return
	}
	reason := billingstore.ReasonCode(body.CorrectionReasonCode)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"correctionReasonCode is required", map[string]any{"field": "correctionReasonCode"})
		return
	}
	// §11.2.1: a gateway-emitted (Category 1) reason code may not be used
	// on an operator-initiated correction — those corrections are written
	// by the gateway through the automated reconciliation path, not this
	// endpoint. A deployer-added code is Category 2 and is accepted.
	if billingstore.IsGatewayEmittedReason(reason) {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_CORRECTION_REASON",
			"reason code "+body.CorrectionReasonCode+" is gateway-emitted (automated reconciliation) and cannot be used for an operator-initiated correction",
			map[string]any{"field": "correctionReasonCode"})
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	// Resolve the original event so the correction's adjustment value
	// can be measured for the dual-control threshold and so a correction
	// against a non-existent event is rejected up front.
	original, err := r.findBillingEvent(req, body.TenantID, body.CorrectsSequence)
	if err != nil {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"no billing event with the given correctsSequence in this tenant", nil)
		return
	}
	adjustment := correctionAdjustment(original, body)
	dualControl := r.dualControlThresh <= 0 || adjustment > r.dualControlThresh

	pending, err := r.corrections.Create(req.Context(), correctionstore.PendingCorrection{
		TenantID:         body.TenantID,
		CorrectsSequence: body.CorrectsSequence,
		ReasonCode:       reason,
		Detail:           body.CorrectionDetail,
		TokensInput:      body.TokensInput,
		TokensOutput:     body.TokensOutput,
		PodMinutes:       body.PodMinutes,
		SubmittedBy:      principal.Subject,
		DualControl:      dualControl,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"the correction request could not be recorded: "+err.Error(), nil)
		return
	}

	// Single-control path: a correction at or below a positive threshold
	// is committed by the submitting admin without a second approval.
	if !dualControl {
		committed, err := r.commitCorrection(req, principal, pending)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"the correction could not be committed: "+err.Error(), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(correctionPayload(committed))
		return
	}

	// Dual-control path: the request waits in billing_correction_pending
	// for a second platform-admin's approval. §11.2.1: the submission
	// itself is audit-logged.
	r.emit(req.Context(), principal, "billing.correction_issued", body.TenantID, map[string]any{
		"tenantId":                body.TenantID,
		"approvalRequestId":       pending.ID,
		"correctsSequence":        body.CorrectsSequence,
		"correctionReasonCode":    body.CorrectionReasonCode,
		"replacementTokensInput":  body.TokensInput,
		"replacementTokensOutput": body.TokensOutput,
		"state":                   "pending",
		"dualControl":             true,
	})
	// §11.2.1 line 175: notify eligible approvers via the configured
	// billing.approverNotificationWebhook (best-effort, detached).
	r.notifyApprovers(pending)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(correctionPayload(pending))
}

// handleListBillingCorrections implements GET /v1/admin/billing-corrections —
// the §11.2.1 correction-queue listing. The ?tenantId= and ?state=
// query parameters narrow the result.
func (r *Router) handleListBillingCorrections(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	rows, err := r.corrections.List(req.Context(), correctionstore.Filter{
		TenantID: q.Get("tenantId"),
		State:    correctionstore.State(q.Get("state")),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]billingCorrectionPayload, 0, len(rows))
	for _, c := range rows {
		out = append(out, correctionPayload(c))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"billing_corrections": out})
}

// handleGetBillingCorrection implements GET /v1/admin/billing-corrections/{id}.
func (r *Router) handleGetBillingCorrection(w http.ResponseWriter, req *http.Request) {
	c, err := r.corrections.Get(req.Context(), req.PathValue("id"))
	if err != nil {
		if errors.Is(err, correctionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "billing correction not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(correctionPayload(c))
}

// handleApproveBillingCorrection implements POST
// /v1/admin/billing-corrections/{id}/approve — the §11.2.1 four-eyes
// approval. The approving platform-admin must be a different identity
// from the submitter; the gateway rejects self-approval. On approval
// the billing_correction event is written to the immutable ledger with
// a full monotonic sequence_number.
func (r *Router) handleApproveBillingCorrection(w http.ResponseWriter, req *http.Request) {
	r.decideBillingCorrection(w, req, true)
}

// handleRejectBillingCorrection implements POST
// /v1/admin/billing-corrections/{id}/reject. A rejected request stays
// in billing_correction_pending with the rejected outcome for audit and
// is never promoted to the billing ledger. Self-rejection is rejected,
// mirroring the four-eyes rule on approval.
func (r *Router) handleRejectBillingCorrection(w http.ResponseWriter, req *http.Request) {
	r.decideBillingCorrection(w, req, false)
}

// decideBillingCorrection is the shared body of the approve and reject
// endpoints. approve selects which transition to apply.
func (r *Router) decideBillingCorrection(w http.ResponseWriter, req *http.Request, approve bool) {
	id := req.PathValue("id")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	pending, err := r.corrections.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, correctionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "billing correction not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if pending.State != correctionstore.StatePending {
		writeError(w, http.StatusConflict, "CORRECTION_NOT_PENDING",
			"the correction is already "+string(pending.State)+" and cannot be decided again", nil)
		return
	}
	// §11.2.1 four-eyes rule: the approver (or rejecter) must be a
	// different platform-admin from the submitter. A self-decision is
	// rejected so a single operator cannot push a correction through.
	if pending.SubmittedBy == principal.Subject {
		action := "approve"
		if !approve {
			action = "reject"
		}
		writeError(w, http.StatusForbidden, "SELF_APPROVAL_FORBIDDEN",
			"the submitting admin cannot "+action+" their own billing correction; a second platform-admin must decide it", nil)
		return
	}

	if !approve {
		rejected, err := r.corrections.Transition(req.Context(), id, correctionstore.StateRejected,
			func(c *correctionstore.PendingCorrection) {
				c.DecidedBy = principal.Subject
				c.DecidedAt = r.clock()
			})
		if err != nil {
			r.writeCorrectionTransitionError(w, err)
			return
		}
		r.emit(req.Context(), principal, "billing.correction_approval_rejected", rejected.TenantID, map[string]any{
			"tenantId":          rejected.TenantID,
			"approvalRequestId": rejected.ID,
			"correctsSequence":  rejected.CorrectsSequence,
			"submittedBy":       rejected.SubmittedBy,
			"rejectedBy":        principal.Subject,
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(correctionPayload(rejected))
		return
	}

	// Approval: write the billing_correction event to the ledger, then
	// move the request to approved with the committed sequence number.
	committed, err := r.commitCorrection(req, principal, pending)
	if err != nil {
		if errors.Is(err, correctionstore.ErrNotPending) {
			writeError(w, http.StatusConflict, "CORRECTION_NOT_PENDING",
				"the correction is no longer pending", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"the correction could not be committed: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(correctionPayload(committed))
}

// commitCorrection writes the §11.2.1 billing_correction event to the
// append-only ledger and moves the pending request to approved with the
// committed sequence number. It is the single place a correction
// reaches the ledger, used by both the single-control create path and
// the dual-control approve path.
//
// The correction is an Append, never an UPDATE: the original billing
// event stays in the stream unchanged, and the correction carries its
// own monotonic sequence_number. decider is the platform-admin who
// authorized the commit — the submitter on the single-control path, the
// approver on the dual-control path.
func (r *Router) commitCorrection(req *http.Request, decider authmw.Principal, pending correctionstore.PendingCorrection) (correctionstore.PendingCorrection, error) {
	correctionEvent := billingstore.Event{
		TenantID:             pending.TenantID,
		UserID:               "",
		EventType:            billingstore.EventBillingCorrection,
		TokensInput:          pending.TokensInput,
		TokensOutput:         pending.TokensOutput,
		PodMinutes:           pending.PodMinutes,
		CorrectsSequence:     pending.CorrectsSequence,
		CorrectionReasonCode: pending.ReasonCode,
		CorrectionDetail:     pending.Detail,
	}
	committed, err := r.billing.Append(req.Context(), correctionEvent)
	if err != nil {
		return correctionstore.PendingCorrection{}, err
	}
	approved, err := r.corrections.Transition(req.Context(), pending.ID, correctionstore.StateApproved,
		func(c *correctionstore.PendingCorrection) {
			c.DecidedBy = decider.Subject
			c.DecidedAt = r.clock()
			c.CommittedSequence = committed.SequenceNumber
		})
	if err != nil {
		return correctionstore.PendingCorrection{}, err
	}
	// §11.2.1: every committed correction emits a billing.correction_issued
	// audit event recording the issuing admin, the approver (when the
	// dual-control path applied), the reason code, the corrected
	// sequence, and the replacement values. The audit event is distinct
	// from the billing event and cannot be suppressed.
	detail := map[string]any{
		"tenantId":                approved.TenantID,
		"approvalRequestId":       approved.ID,
		"correctsSequence":        approved.CorrectsSequence,
		"correctionSequence":      committed.SequenceNumber,
		"correctionReasonCode":    string(approved.ReasonCode),
		"replacementTokensInput":  approved.TokensInput,
		"replacementTokensOutput": approved.TokensOutput,
		"submittedBy":             approved.SubmittedBy,
		"dualControl":             approved.DualControl,
	}
	if approved.DualControl {
		detail["approvedBy"] = decider.Subject
	}
	r.emit(req.Context(), decider, "billing.correction_issued", approved.TenantID, detail)
	return approved, nil
}

// writeCorrectionTransitionError maps a correctionstore transition error
// to its HTTP envelope.
func (r *Router) writeCorrectionTransitionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, correctionstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "billing correction not found", nil)
	case errors.Is(err, correctionstore.ErrNotPending):
		writeError(w, http.StatusConflict, "CORRECTION_NOT_PENDING",
			"the correction is no longer pending", nil)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// findBillingEvent returns the billing event with the given sequence
// number in the tenant's ledger. §11.2.1: a correction must reference
// an existing original, so the create handler resolves it up front.
func (r *Router) findBillingEvent(req *http.Request, tenantID string, sequence uint64) (billingstore.Event, error) {
	// Since returns events with sequence_number > since, so reading from
	// sequence-1 yields the target event as the first row.
	events, err := r.billing.Since(req.Context(), tenantID, sequence-1, 1)
	if err != nil {
		return billingstore.Event{}, err
	}
	if len(events) == 0 || events[0].SequenceNumber != sequence {
		return billingstore.Event{}, errors.New("billing event not found")
	}
	return events[0], nil
}

// correctionAdjustment is the §11.2.1 "absolute adjustment value" of a
// correction: the magnitude of the change the correction makes to the
// original event's billable dimensions. It is the sum of the absolute
// deltas in tokens_input, tokens_output, and pod_minutes. The
// dual-control threshold is compared against this value.
func correctionAdjustment(original billingstore.Event, req BillingCorrectionRequest) float64 {
	delta := absDeltaUint(original.TokensInput, req.TokensInput) +
		absDeltaUint(original.TokensOutput, req.TokensOutput)
	return float64(delta) + math.Abs(original.PodMinutes-req.PodMinutes)
}

// absDeltaUint returns the absolute difference between two uint64s.
func absDeltaUint(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

// CorrectionExpiryReconciler retires §11.2.1 pending billing corrections
// that were not actioned within the approval timeout. timeout is
// billing.approvalTimeoutSeconds (default 86400 = 24h). The reconciler
// moves each timed-out request to the expired state and emits a
// billing.correction_approval_expired audit event. It returns the
// number of requests expired.
//
// This is invoked on a schedule by the gateway's background loops; it
// is exported so a test or an operator runbook can run a sweep
// directly.
func (r *Router) CorrectionExpiryReconciler(req *http.Request, timeout time.Duration) (int, error) {
	if r.corrections == nil {
		return 0, nil
	}
	pending, err := r.corrections.List(req.Context(), correctionstore.Filter{State: correctionstore.StatePending})
	if err != nil {
		return 0, err
	}
	now := r.clock()
	expired := 0
	for _, c := range pending {
		if now.Sub(c.SubmittedAt) < timeout {
			continue
		}
		retired, terr := r.corrections.Transition(req.Context(), c.ID, correctionstore.StateExpired,
			func(pc *correctionstore.PendingCorrection) {
				pc.DecidedAt = now
			})
		if terr != nil {
			// A request decided between the List and the Transition is
			// skipped; it is no longer pending.
			continue
		}
		r.emit(req.Context(), authmw.Principal{Subject: "lenny-gateway"},
			"billing.correction_approval_expired", retired.TenantID, map[string]any{
				"tenantId":          retired.TenantID,
				"approvalRequestId": retired.ID,
				"correctsSequence":  retired.CorrectsSequence,
				"submittedBy":       retired.SubmittedBy,
			})
		expired++
	}
	return expired, nil
}
