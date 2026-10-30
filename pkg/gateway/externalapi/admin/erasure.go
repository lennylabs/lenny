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
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// Route patterns of the erasure-job endpoints. The cross_tenant_read event
// of a sentinel read names the route it served.
const (
	routeGetErasureJob           = "GET /v1/admin/erasure-jobs/{jobId}"
	routeRetryErasureJob         = "POST /v1/admin/erasure-jobs/{jobId}/retry"
	routeClearErasureRestriction = "POST /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction"
)

// ErasureRunner records §12.8 GDPR erasure jobs. Start writes the job and
// the user's processing restriction in one transaction and returns the job,
// or *erasurejob.ErrActiveJob carrying the user's in-progress job. The
// gateway leader's sweep executes recorded jobs. *erasurejob.Runner
// satisfies it.
type ErasureRunner interface {
	Start(ctx context.Context, tenantID, userID, initiatedBy string, overrideReceipt map[string]any) (erasurejob.Job, error)
}

// WithErasure wires the §12.8 user-erasure endpoints onto the Router:
// POST /v1/admin/users/{userId}/erase initiates a job, and GET
// /v1/admin/erasure-jobs/{jobId} reports its status.
func (r *Router) WithErasure(runner ErasureRunner, jobs erasurejob.Store) *Router {
	r.erasureRunner = runner
	r.erasureJobs = jobs
	return r
}

// WithErasureCrossTenantReadAudit wires the §12.3 cross_tenant_read
// recorder the erasure-job handlers call once per read they make under the
// all-tenants sentinel: the platform-admin branch of the status query, the
// retry, and the restriction clear. An emit error answers 500 before any
// write. A nil function emits nothing.
//
// spec: §12.3 (Postgres HA Requirements).
func (r *Router) WithErasureCrossTenantReadAudit(f erasurejob.CrossTenantReadFunc) *Router {
	r.erasureCrossTenantRead = f
	return r
}

// EraseUserRequest is the optional POST /v1/admin/users/{userId}/erase
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

// summarizeHolds projects the §12.8 preflight hold set into the two lists
// the erasure audit surface uses: the session-id list (heldSessions) and
// the {resourceType, resourceId} tuples the §12.8 blocked event and
// override receipt carry.
func summarizeHolds(holds []HeldResource) (sessions []string, tuples []map[string]any) {
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

// writeHoldBlocked answers a request the §12.8 step-0 legal-hold preflight
// blocks: it emits gdpr.erasure_blocked_by_hold and answers
// 409 ERASURE_BLOCKED_BY_LEGAL_HOLD. jobID is the retried job on the retry
// path, where the event carries it, and empty on the erase path, where no
// job exists.
//
// spec: §12.8 (DeleteByUser dependency-ordered deletion sequence), §16.7.
func (r *Router) writeHoldBlocked(w http.ResponseWriter, req *http.Request, tenant, subject, jobID string, holds []HeldResource) {
	heldSessions, holdTuples := summarizeHolds(holds)
	principal, _ := authmw.FromContext(req.Context())
	event := map[string]any{
		"tenantId":     tenant,
		"userId":       subject,
		"holdCount":    len(holds),
		"holds":        holdTuples,
		"heldSessions": heldSessions,
	}
	if jobID != "" {
		event["jobId"] = jobID
	}
	r.emit(req.Context(), principal, "gdpr.erasure_blocked_by_hold", subject, event)
	writeError(w, http.StatusConflict, "ERASURE_BLOCKED_BY_LEGAL_HOLD",
		"the user has one or more sessions or artifacts under a legal hold; erasure is blocked until the holds are released",
		map[string]any{"holds": holdTuples, "heldSessions": heldSessions})
}

// handleEraseUser implements POST /v1/admin/users/{userId}/erase, the
// §12.8 GDPR erasure initiation. In order it checks that the user is
// registered, runs the step-0 legal-hold preflight, validates a legal-hold
// override, and calls Start, which writes the job and the user's
// processing restriction in one transaction. It answers 202 with the job
// id; the gateway leader's sweep executes the job. A user with a job
// already in progress gets that job's id, unless the request was admitted
// only through the override, which answers 409 INVALID_STATE_TRANSITION.
//
// spec: §12.8 (User-level erasure invocation), §12.8 (DeleteByUser
// dependency-ordered deletion sequence), §12.8 (Processing restriction
// during erasure (GDPR Article 18)), §15.1.
func (r *Router) handleEraseUser(w http.ResponseWriter, req *http.Request) {
	subject := req.PathValue("userId")
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
	// §12.8 step 0: legal-hold preflight. Destroying data under a
	// preservation order would be spoliation, so a hold blocks the request
	// before any write.
	holds, err := r.heldResourcesForUser(req.Context(), tenant, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"legal-hold preflight failed: "+err.Error(), nil)
		return
	}
	var overrideReceipt map[string]any
	if len(holds) > 0 {
		if !body.AcknowledgeHoldOverride {
			r.writeHoldBlocked(w, req, tenant, subject, "", holds)
			return
		}
		var ok bool
		if overrideReceipt, ok = r.holdOverrideReceipt(w, principal, body, holds); !ok {
			return
		}
	}
	job, err := r.erasureRunner.Start(req.Context(), tenant, subject, principal.Subject, overrideReceipt)
	var active *erasurejob.ErrActiveJob
	switch {
	case errors.As(err, &active) && overrideReceipt != nil:
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"erasure job "+active.Job.ID+" for the user is in progress; a legal-hold override applies only to a new job", nil)
		return
	case errors.As(err, &active):
		// §12.8: one in-progress job per user. The request is idempotent and
		// answers the existing job's id.
		writeErasureAccepted(w, active.Job)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"erasure job could not be started: "+err.Error(), nil)
		return
	}
	if overrideReceipt != nil {
		r.emitHoldOverridden(req.Context(), principal, job, len(holds), overrideReceipt)
	}
	r.emit(req.Context(), principal, "admin.user.erasure_initiated", subject, map[string]any{
		"tenantId": tenant,
		"jobId":    job.ID,
	})
	writeErasureAccepted(w, job)
}

// holdOverrideReceipt validates a §12.8 legal-hold override and returns
// the override fields the job persists for its completion receipt. A
// missing justification answers 400 and a caller without platform-admin
// answers 403; both return false.
func (r *Router) holdOverrideReceipt(w http.ResponseWriter, principal authmw.Principal, body EraseUserRequest, holds []HeldResource) (map[string]any, bool) {
	if body.Justification == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"acknowledgeHoldOverride requires a non-empty justification", nil)
		return nil, false
	}
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"a legal-hold override requires the platform-admin role", nil)
		return nil, false
	}
	heldSessions, holdTuples := summarizeHolds(holds)
	// spec: §12.8 — the receipt records legal_hold_override, override_by,
	// override_justification, override_at, and every hold that would
	// otherwise have blocked the erasure. override_at is captured once, so
	// the gdpr.legal_hold_overridden event and the receipt agree. The
	// legal-hold rows stay set.
	return map[string]any{
		"legalHoldOverride":     true,
		"overrideBy":            principal.Subject,
		"overrideJustification": body.Justification,
		"overrideAt":            rfc3339Nano(r.clock()),
		"overriddenHolds":       heldSessions,
		"holds":                 holdTuples,
	}, true
}

// emitHoldOverridden emits gdpr.legal_hold_overridden after the job
// exists, so the event carries jobId beside the override fields. spec:
// §12.8.
func (r *Router) emitHoldOverridden(ctx context.Context, principal authmw.Principal, job erasurejob.Job, holdCount int, overrideReceipt map[string]any) {
	event := map[string]any{
		"tenantId":  job.TenantID,
		"userId":    job.UserID,
		"jobId":     job.ID,
		"holdCount": holdCount,
	}
	for k, v := range overrideReceipt {
		event[k] = v
	}
	r.emit(ctx, principal, "gdpr.legal_hold_overridden", job.UserID, event)
}

// writeErasureAccepted answers 202 with the job's id, target, and phase.
func writeErasureAccepted(w http.ResponseWriter, job erasurejob.Job) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":    job.ID,
		"userId":   job.UserID,
		"tenantId": job.TenantID,
		"phase":    string(job.Phase),
	})
}

// ErasureCompletionReceipt builds the §12.8 gdpr.erasure_completed receipt
// from a job whose ReceiptAt is set: the per-store counts, the billing
// outcome, the per-phase timeline ending in a completed entry at
// ReceiptAt, the verification outcome, the initiating principal, and the
// legal-hold override fields when an override was exercised. It reads only
// the job, so a receipt written again for the same job is byte-identical.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
func ErasureCompletionReceipt(job erasurejob.Job) map[string]any {
	receipt := map[string]any{
		"tenantId":    job.TenantID,
		"userId":      job.UserID,
		"jobId":       job.ID,
		"deleted":     job.Deleted,
		"total":       job.Total,
		"initiatedBy": job.InitiatedBy,
		"completedAt": rfc3339Nano(job.ReceiptAt),
	}
	// §12.8: the receipt records which billing-erasure policy was applied.
	// Absent when no BillingEraser ran for this job.
	if job.Billing.Disposition != "" {
		receipt["billingErasure"] = map[string]any{
			"disposition":   job.Billing.Disposition,
			"pseudonymized": job.Billing.Pseudonymized,
			"verified":      job.Billing.Verified,
		}
	}
	phaseLog := make([]map[string]any, 0, len(job.PhaseLog)+1)
	for _, tr := range job.PhaseLog {
		phaseLog = append(phaseLog, map[string]any{"phase": string(tr.Phase), "at": rfc3339Nano(tr.At)})
	}
	phaseLog = append(phaseLog, map[string]any{
		"phase": string(erasurejob.PhaseCompleted), "at": rfc3339Nano(job.ReceiptAt),
	})
	receipt["phaseLog"] = phaseLog
	receipt["verificationOutcome"] = job.Billing.VerificationOutcome()
	for k, v := range job.OverrideReceipt {
		receipt[k] = v
	}
	return receipt
}

// handleRetryErasureJob implements POST
// /v1/admin/erasure-jobs/{jobId}/retry, the §24.12 / §12.8 operator retry
// of a failed erasure job. In order it reads the job, records the
// cross_tenant_read event, checks tenant visibility and the failed phase,
// runs the legal-hold preflight with no override, and calls Restart, which
// moves the job to initiated and sets the user's restriction in one
// transaction. The leader's sweep then re-runs the job from initiated, so
// the drain reaches sessions admitted while an operator clear left the
// restriction lifted.
//
// spec: §12.8 (User-level erasure invocation), §12.8 (DeleteByUser
// dependency-ordered deletion sequence), §24.12.
func (r *Router) handleRetryErasureJob(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("jobId")
	job, ok := r.lookupErasureJob(w, req, jobID, routeRetryErasureJob)
	if !ok {
		return
	}
	// spec: §24.12 — "The job must be in `failed` state."
	if job.Phase != erasurejob.PhaseFailed {
		writeErasureJobNotFailed(w, job.Phase, "retried")
		return
	}
	holds, err := r.heldResourcesForUser(req.Context(), job.TenantID, job.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"legal-hold preflight failed: "+err.Error(), nil)
		return
	}
	if len(holds) > 0 {
		r.writeHoldBlocked(w, req, job.TenantID, job.UserID, jobID, holds)
		return
	}
	restarted, err := r.erasureJobs.Restart(req.Context(), jobID)
	if err != nil {
		var active *erasurejob.ErrActiveJob
		switch {
		case errors.Is(err, erasurejob.ErrNotFailed):
			writeErasureJobNotFailed(w, lockedPhase(err, job.Phase), "retried")
		case errors.As(err, &active):
			writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
				"erasure job "+active.Job.ID+" for the user is in progress", nil)
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"erasure job could not be reset for retry: "+err.Error(), nil)
		}
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// spec: §24.12 — the retry is recorded in the audit trail with the
	// operator identity for the §11.7 chain.
	r.emit(req.Context(), principal, "gdpr.erasure_job_retried", job.UserID, map[string]any{
		"tenantId": job.TenantID,
		"userId":   job.UserID,
		"jobId":    jobID,
	})
	writeErasureAccepted(w, restarted)
}

// clearRestrictionRequest is the POST
// /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction body.
type clearRestrictionRequest struct {
	// Justification is the operator's recorded reason for lifting the
	// restriction. Required (§24.12 / §12.8).
	Justification string `json:"justification,omitempty"`
}

// handleClearErasureRestriction implements POST
// /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction — the
// §24.12 / §12.8 manual clear of the GDPR Article 18
// processing-restriction flag after a failed erasure job. It requires a
// non-empty justification, refuses a job that is not failed and a flag
// that names another job, records the operator identity and
// justification in the audit trail, and clears the flag through the
// privileged store path that bypasses the database-level Article 18
// trigger. spec: §12.8; §15.1; §24.12.
func (r *Router) handleClearErasureRestriction(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("jobId")
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
	job, ok := r.lookupErasureJob(w, req, jobID, routeClearErasureRestriction)
	if !ok {
		return
	}
	// spec: §12.8 (Processing restriction during erasure (GDPR Article
	// 18)), §15.1 (REST API) — the store locks the job row, admits only a
	// failed job, and clears the flag only when it names this job or no
	// job. The handler does not decide the phase from its own read, which
	// runs outside the clear's lock.
	if _, err := r.erasureJobs.ClearRestriction(req.Context(), jobID); err != nil {
		switch {
		case errors.Is(err, erasurejob.ErrNotFailed):
			writeErasureJobNotFailed(w, lockedPhase(err, job.Phase), "cleared")
		case errors.Is(err, userstore.ErrRestrictionHeldByOtherJob):
			writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
				"the user's processing restriction is held by another erasure job", nil)
		case errors.Is(err, erasurejob.ErrNotFound):
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
		case errors.Is(err, userstore.ErrNotFound):
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "user not found", nil)
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"processing restriction could not be cleared: "+err.Error(), nil)
		}
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	// spec: §12.8 / §24.12 — the clear records the
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

// handleGetErasureJob implements GET /v1/admin/erasure-jobs/{jobId} —
// the §12.8 erasure-job status query. It returns the current phase, an
// advisory completion percentage, elapsed time, the per-store deleted
// counts, and any failure reason.
func (r *Router) handleGetErasureJob(w http.ResponseWriter, req *http.Request) {
	jobID := req.PathValue("jobId")
	job, ok := r.lookupErasureJob(w, req, jobID, routeGetErasureJob)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(erasureJobPayload(job, r.clock()))
}

// lookupErasureJob reads the job the request names and writes the
// response when the caller may not see it. A platform-admin reads under
// the all-tenants sentinel and the read records one cross_tenant_read
// event naming route, with a row count of 1, or 0 when no job matched; an
// emit error answers 500 before any write. Any other caller reads under
// its own tenant and records no event, so another tenant's job reads as
// not found and its existence is not leaked across the tenant boundary.
//
// spec: §12.8 (User-level erasure invocation), §12.3 (Postgres HA
// Requirements).
func (r *Router) lookupErasureJob(w http.ResponseWriter, req *http.Request, jobID, route string) (erasurejob.Job, bool) {
	scope := erasurejob.AllTenants
	p, hasPrincipal := authmw.FromContext(req.Context())
	if hasPrincipal && !p.HasRole(auth.RolePlatformAdmin) {
		scope = p.TenantID
	}
	job, err := r.erasureJobs.Get(req.Context(), scope, jobID)
	if err != nil && !errors.Is(err, erasurejob.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return erasurejob.Job{}, false
	}
	if scope == erasurejob.AllTenants && r.erasureCrossTenantRead != nil {
		rows := 1
		if err != nil {
			rows = 0
		}
		if aerr := r.erasureCrossTenantRead(req.Context(), route, p.Subject, rows); aerr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"cross_tenant_read audit failed: "+aerr.Error(), nil)
			return erasurejob.Job{}, false
		}
	}
	if err != nil || !r.callerMaySeeTenant(req, job.TenantID) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "erasure job not found", nil)
		return erasurejob.Job{}, false
	}
	return job, true
}

// writeErasureJobNotFailed answers the §15.1 ERASURE_JOB_NOT_FAILED
// refusal of a retry or a restriction clear for a job that is not failed.
func writeErasureJobNotFailed(w http.ResponseWriter, phase erasurejob.Phase, action string) {
	writeError(w, http.StatusConflict, "ERASURE_JOB_NOT_FAILED",
		"erasure job is in phase "+string(phase)+"; only a failed job can be "+action, nil)
}

// lockedPhase returns the phase a store's ErrNotFailed refusal read under
// the job's lock. The handler's own read runs before the lock, so a retry
// or a completion that lands in between leaves that read at failed while
// the store refused on another phase; the response names the locked one.
// fallback is returned only for a refusal that carries no phase.
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §15.1 (REST API).
func lockedPhase(err error, fallback erasurejob.Phase) erasurejob.Phase {
	var nf *erasurejob.NotFailedError
	if errors.As(err, &nf) {
		return nf.Phase
	}
	return fallback
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
	// spec: §12.8 — surface the per-phase timeline so the status
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
