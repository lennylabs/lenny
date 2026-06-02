// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// ErasureRunner starts and executes §12.8 GDPR erasure jobs. Start
// records a job in the registry and returns its id; Run drives the
// recorded job through its phases. *erasurejob.Runner satisfies it.
type ErasureRunner interface {
	Start(ctx context.Context, tenantID, userID string) (string, error)
	Run(ctx context.Context, jobID string) error
}

// WithErasure wires the §12.8 user-erasure endpoints onto the Router:
// POST /v1/admin/users/{user_id}/erase initiates a job, and GET
// /v1/admin/erasure-jobs/{job_id} reports its status.
func (r *Router) WithErasure(runner ErasureRunner, jobs erasurejob.Store) *Router {
	r.erasureRunner = runner
	r.erasureJobs = jobs
	return r
}

// EraseUserRequest is the optional POST /v1/admin/users/{user_id}/erase
// body. tenantId scopes the erasure for a platform-admin caller; a
// tenant-admin's tenant is taken from the token.
type EraseUserRequest struct {
	TenantID string `json:"tenantId,omitempty"`

	// AcknowledgeHoldOverride, when true, bypasses the §12.8 step-0
	// legal-hold preflight. The override requires a non-empty
	// Justification and the platform-admin role; a tenant-admin cannot
	// self-override.
	AcknowledgeHoldOverride bool `json:"acknowledgeHoldOverride,omitempty"`

	// Justification is the free-text reason recorded with a legal-hold
	// override. Required when AcknowledgeHoldOverride is set.
	Justification string `json:"justification,omitempty"`
}

// summarizeHolds projects the §12.8 preflight hold set into the two
// shapes the erasure audit surface uses: the session-id list
// (heldSessions, retained for backward-compatible SIEM correlation) and
// the {resourceType, resourceId} tuples the §12.8 line 794 blocked event
// and override receipt carry.
func summarizeHolds(holds []heldResource) (sessions []string, tuples []map[string]any) {
	for _, h := range holds {
		tuples = append(tuples, map[string]any{
			"resourceType": h.ResourceType,
			"resourceId":   h.ResourceID,
		})
		if h.ResourceType == "session" {
			sessions = append(sessions, h.ResourceID)
		}
	}
	return sessions, tuples
}

// handleEraseUser implements POST /v1/admin/users/{user_id}/erase —
// the §12.8 GDPR erasure initiation. It records an erasure job, starts
// the dependency-ordered DeleteByUser deletion in the background, and
// returns the job id immediately so the caller polls
// GET /v1/admin/erasure-jobs/{job_id} for completion.
//
// The §12.8 step-0 legal-hold preflight is enforced: a user with a
// legally-held session is rejected before the job initiates. The
// billing-event pseudonymization step is not yet enforced; the runner
// covers the store-deletion core of the erasure sequence.
func (r *Router) handleEraseUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("user_id")
	var body EraseUserRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// The erasure target must be a registered user.
	if _, err := r.users.Get(req.Context(), tenant, subject); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// §12.8 step 0: legal-hold preflight. A user with a legally-held
	// session or artifact is rejected synchronously before the job
	// initiates, so the processing restriction is never applied.
	// Destroying data under a preservation order would be spoliation.
	holds, err := r.heldResourcesForUser(req.Context(), tenant, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"legal-hold preflight failed: "+err.Error(), nil)
		return
	}
	heldSessions, holdTuples := summarizeHolds(holds)
	// overrideReceipt carries the §12.8 legal-hold override fields into
	// the completion receipt; it stays nil when no override occurred.
	var overrideReceipt map[string]any
	if len(holds) > 0 {
		if !body.AcknowledgeHoldOverride {
			// spec: §12.8 line 794 — the blocked event carries the
			// {resourceType, resourceId} tuples for every blocking hold.
			r.emit(req.Context(), principal, "gdpr.erasure_blocked_by_hold", subject, map[string]any{
				"tenantId":     tenant,
				"userId":       subject,
				"holdCount":    len(holds),
				"holds":        holdTuples,
				"heldSessions": heldSessions,
			})
			writeError(w, http.StatusConflict, "ERASURE_BLOCKED_BY_LEGAL_HOLD",
				"the user has one or more sessions or artifacts under a legal hold; erasure is blocked until the holds are released",
				map[string]any{"holds": holdTuples, "heldSessions": heldSessions})
			return
		}
		// §12.8: a platform-admin may override the preflight with a
		// recorded justification. A tenant-admin cannot self-override.
		if body.Justification == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"acknowledgeHoldOverride requires a non-empty justification", nil)
			return
		}
		if !principal.HasRole(auth.RolePlatformAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"a legal-hold override requires the platform-admin role", nil)
			return
		}
		// spec: §12.8 line 796 — the receipt records legal_hold_override,
		// override_by, override_justification, override_at, and the full
		// list of holds that would otherwise have blocked the erasure.
		// override_at is captured here, at the instant the override is
		// recorded, and flows into both the gdpr.legal_hold_overridden
		// event and the completion receipt so the two agree.
		overrideReceipt = map[string]any{
			"legalHoldOverride":     true,
			"overrideBy":            principal.Subject,
			"overrideJustification": body.Justification,
			"overrideAt":            rfc3339Nano(r.clock()),
			"overriddenHolds":       heldSessions,
			"holds":                 holdTuples,
		}
		// The underlying legal-hold rows are left set; the erasure
		// proceeds. The gdpr.legal_hold_overridden audit event is emitted
		// after the erasure job is created so the event carries jobId per
		// spec §12.8 line 796 ("carrying the same fields plus job_id").
	}
	jobID, err := r.erasureRunner.Start(req.Context(), tenant, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"erasure job could not be started: "+err.Error(), nil)
		return
	}
	if overrideReceipt != nil {
		// spec: §12.8 line 796 — emit gdpr.legal_hold_overridden after the
		// job exists so the event payload carries jobId for SIEM pivoting
		// from admin.user.erasure_initiated to the override decision. The
		// event carries the same fields as the receipt (legal_hold_override,
		// override_by, override_justification, override_at, overridden_holds)
		// plus job_id.
		overrideEvent := map[string]any{
			"tenantId":  tenant,
			"userId":    subject,
			"jobId":     jobID,
			"holdCount": len(holds),
		}
		for k, v := range overrideReceipt {
			overrideEvent[k] = v
		}
		r.emit(req.Context(), principal, "gdpr.legal_hold_overridden", subject, overrideEvent)
	}
	// §12.8 / GDPR Article 18: mark the user processing-restricted so
	// new session creation is rejected while erasure is in progress.
	_, _ = r.users.Update(req.Context(), tenant, subject, func(u *userstore.User) error {
		u.ProcessingRestricted = true
		u.ErasureJobID = jobID
		return nil
	})
	// §12.8: the job runs in the background and the API returns the job
	// id immediately. The job uses a detached context so it outlives
	// the request. On completion the processing restriction is lifted
	// and the §12.8 erasure receipt is written to the audit trail; a
	// failed job leaves the restriction set for an operator to retry.
	go r.runErasureToCompletion(principal, tenant, subject, jobID, overrideReceipt)

	r.emit(req.Context(), principal, "admin.user.erasure_initiated", subject, map[string]any{
		"tenantId": tenant,
		"jobId":    jobID,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":    jobID,
		"userId":   subject,
		"tenantId": tenant,
		"phase":    string(erasurejob.PhaseInitiated),
	})
}

// runErasureToCompletion executes the erasure job and, on success,
// lifts the §12.8 / GDPR Article 18 processing restriction and writes
// the erasure receipt to the audit trail. overrideReceipt, when
// non-nil, merges the legal-hold override fields into the receipt. It
// runs on a detached context so it outlives the originating request,
// and is the shared completion path for both initial erasure
// (handleEraseUser) and operator retry (handleRetryErasureJob). A
// failed job leaves the restriction set so an operator can retry or
// clear it explicitly. spec: §12.8 lines 762, 764, 851.
func (r *Router) runErasureToCompletion(principal authmw.Principal, tenant, subject, jobID string, overrideReceipt map[string]any) {
	_ = r.erasureRunner.Run(context.Background(), jobID)
	job, err := r.erasureJobs.Get(context.Background(), jobID)
	if err != nil || job.Phase != erasurejob.PhaseCompleted {
		return
	}
	_, _ = r.users.Update(context.Background(), tenant, subject, func(u *userstore.User) error {
		u.ProcessingRestricted = false
		u.ErasureJobID = ""
		return nil
	})
	// §12.8 erasure receipt: the gdpr.* audit event is the
	// authoritative proof that the erasure was carried out. When a
	// legal-hold override was exercised, the receipt records it.
	receipt := map[string]any{
		"tenantId": tenant,
		"jobId":    jobID,
		"deleted":  job.Deleted,
		"total":    job.Total,
	}
	// §12.8: the receipt records which billing-erasure policy was
	// applied and whether billing events were pseudonymized or
	// exempted. Absent when no BillingEraser ran for this job.
	if job.Billing.Disposition != "" {
		receipt["billingErasure"] = map[string]any{
			"disposition":   job.Billing.Disposition,
			"pseudonymized": job.Billing.Pseudonymized,
			"verified":      job.Billing.Verified,
		}
	}
	// spec: §12.8 line 762 — the receipt carries the per-phase timeline
	// so a compliance auditor can reconstruct the erasure sequence from
	// a single event.
	if len(job.PhaseLog) > 0 {
		phaseLog := make([]map[string]any, 0, len(job.PhaseLog))
		for _, tr := range job.PhaseLog {
			phaseLog = append(phaseLog, map[string]any{
				"phase": string(tr.Phase),
				"at":    rfc3339Nano(tr.At),
			})
		}
		receipt["phaseLog"] = phaseLog
	}
	// spec: §12.8 line 851 — the salt-removal verification outcome is
	// recorded in the erasure receipt.
	receipt["verificationOutcome"] = job.Billing.VerificationOutcome()
	for k, v := range overrideReceipt {
		receipt[k] = v
	}
	r.emit(context.Background(), principal, "gdpr.erasure_completed", subject, receipt)
}

// handleRetryErasureJob implements POST
// /v1/admin/erasure-jobs/{job_id}/retry — the §24.12 / §12.8 line 766
// operator retry of a failed erasure job. The job must be in the
// `failed` phase; the handler clears the transient failure fields,
// resets the job to its first phase, and re-enqueues it on the runner.
//
// The runner re-runs the full dependency-ordered DeleteByUser sequence
// from the start; each step is idempotent (a second DeleteByUser
// deletes the already-removed rows as a no-op), so resetting to
// `initiated` is the safe resume point given the runner keeps no
// mid-phase checkpoint. On success the shared completion path lifts the
// processing restriction. spec: §12.8 line 766; §24.12 line 143.
func (r *Router) handleRetryErasureJob(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("job_id")
	job, err := r.erasureJobs.Get(req.Context(), jobID)
	if err != nil {
		if errors.Is(err, erasurejob.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// A job belonging to another tenant reads as not-found so its
	// existence is not leaked across the tenant boundary.
	if !r.callerMaySeeTenant(req, job.TenantID) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
		return
	}
	// spec: §24.12 line 143 — "The job must be in `failed` state."
	if job.Phase != erasurejob.PhaseFailed {
		writeError(w, http.StatusConflict, "ERASURE_JOB_NOT_FAILED",
			"erasure job is in phase "+string(job.Phase)+"; only a failed job can be retried", nil)
		return
	}
	// Clear the transient failure fields and reset to the first phase so
	// the runner (which short-circuits on a terminal phase) re-executes.
	if _, err := r.erasureJobs.Update(req.Context(), jobID, func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseInitiated
		j.Failure = ""
		j.CompletedAt = time.Time{}
		j.PhaseLog = append(j.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseInitiated, At: r.clock()})
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"erasure job could not be reset for retry: "+err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	go r.runErasureToCompletion(principal, job.TenantID, job.UserID, jobID, nil)
	// spec: §24.12 lines 143-144 — the retry is recorded in the audit
	// trail with the operator identity for the §11.7 chain.
	r.emit(req.Context(), principal, "gdpr.erasure_job_retried", job.UserID, map[string]any{
		"tenantId": job.TenantID,
		"userId":   job.UserID,
		"jobId":    jobID,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":    jobID,
		"userId":   job.UserID,
		"tenantId": job.TenantID,
		"phase":    string(erasurejob.PhaseInitiated),
	})
}

// clearRestrictionRequest is the POST
// /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction body.
type clearRestrictionRequest struct {
	// Justification is the operator's recorded reason for lifting the
	// restriction. Required (§24.12 line 144 / §12.8 line 764).
	Justification string `json:"justification,omitempty"`
}

// handleClearErasureRestriction implements POST
// /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction — the
// §24.12 / §12.8 line 764 manual clear of the GDPR Article 18
// processing-restriction flag after a failed erasure job. It requires a
// non-empty justification, records the operator identity and
// justification in the audit trail, and clears the flag through the
// privileged store path that bypasses the database-level Article 18
// trigger. spec: §12.8 line 764; §24.12 line 144.
func (r *Router) handleClearErasureRestriction(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("job_id")
	var body clearRestrictionRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}
	if body.Justification == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"clear-processing-restriction requires a non-empty justification", nil)
		return
	}
	job, err := r.erasureJobs.Get(req.Context(), jobID)
	if err != nil {
		if errors.Is(err, erasurejob.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !r.callerMaySeeTenant(req, job.TenantID) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
		return
	}
	if _, err := r.users.ClearProcessingRestriction(req.Context(), job.TenantID, job.UserID); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"processing restriction could not be cleared: "+err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// spec: §12.8 line 764 / §24.12 line 144 — the clear records the
	// operator identity and justification in the §11.7 audit chain.
	r.emit(req.Context(), principal, "gdpr.processing_restriction_cleared", job.UserID, map[string]any{
		"tenantId":      job.TenantID,
		"userId":        job.UserID,
		"jobId":         jobID,
		"justification": body.Justification,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":                jobID,
		"userId":               job.UserID,
		"tenantId":             job.TenantID,
		"processingRestricted": false,
	})
}

// handleGetErasureJob implements GET /v1/admin/erasure-jobs/{job_id} —
// the §12.8 erasure-job status query. It returns the current phase, an
// advisory completion percentage, elapsed time, the per-store deleted
// counts, and any failure reason.
func (r *Router) handleGetErasureJob(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("job_id")
	job, err := r.erasureJobs.Get(req.Context(), jobID)
	if err != nil {
		if errors.Is(err, erasurejob.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// A job belonging to another tenant reads as not-found so its
	// existence is not leaked across the tenant boundary.
	if !r.callerMaySeeTenant(req, job.TenantID) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(erasureJobPayload(job, r.clock()))
}

// callerMaySeeTenant reports whether the request's principal is
// authorized to read a resource scoped to tenantID: a platform-admin
// sees every tenant; a tenant-admin sees only its own.
func (r *Router) callerMaySeeTenant(req *http.Request, tenantID string) bool {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return false
	}
	if p.HasRole(auth.RolePlatformAdmin) {
		return true
	}
	return p.HasRole(auth.RoleTenantAdmin) && p.TenantID == tenantID
}

// erasureJobPayload is the §12.8 erasure-job status wire shape.
func erasureJobPayload(j erasurejob.Job, now time.Time) map[string]any {
	elapsed := now.Sub(j.StartedAt)
	if !j.CompletedAt.IsZero() {
		elapsed = j.CompletedAt.Sub(j.StartedAt)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	out := map[string]any{
		"jobId":             j.ID,
		"userId":            j.UserID,
		"tenantId":          j.TenantID,
		"phase":             string(j.Phase),
		"completionPercent": phaseCompletionPercent(j.Phase),
		"elapsedSeconds":    int64(elapsed.Seconds()),
		"total":             j.Total,
		"startedAt":         rfc3339Nano(j.StartedAt),
	}
	if j.Deleted != nil {
		out["deleted"] = j.Deleted
	}
	// spec: §12.8 line 762 — surface the per-phase timeline so the status
	// query exposes the same sequence the completion receipt records.
	if len(j.PhaseLog) > 0 {
		phaseLog := make([]map[string]any, 0, len(j.PhaseLog))
		for _, tr := range j.PhaseLog {
			phaseLog = append(phaseLog, map[string]any{
				"phase": string(tr.Phase),
				"at":    rfc3339Nano(tr.At),
			})
		}
		out["phaseLog"] = phaseLog
	}
	if !j.CompletedAt.IsZero() {
		out["completedAt"] = rfc3339Nano(j.CompletedAt)
	}
	if j.Failure != "" {
		out["error"] = j.Failure
	}
	return out
}

// phaseCompletionPercent maps a §12.8 job phase to an advisory
// completion percentage. The phase and error fields are the
// authoritative signals; this is a coarse progress indicator for
// dashboards.
func phaseCompletionPercent(p erasurejob.Phase) int {
	switch p {
	case erasurejob.PhaseStoreDeleting:
		return 25
	case erasurejob.PhasePseudonymizing:
		return 50
	case erasurejob.PhaseVerifying:
		return 75
	case erasurejob.PhaseCompleted:
		return 100
	default: // initiated, failed
		return 0
	}
}
