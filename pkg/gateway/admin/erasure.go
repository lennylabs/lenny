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
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
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
	// session is rejected synchronously before the job initiates, so
	// the processing restriction is never applied. Destroying data
	// under a preservation order would be spoliation.
	held, err := r.heldSessions(req.Context(), tenant, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"legal-hold preflight failed: "+err.Error(), nil)
		return
	}
	// overrideReceipt carries the §12.8 legal-hold override fields into
	// the completion receipt; it stays nil when no override occurred.
	var overrideReceipt map[string]any
	if len(held) > 0 {
		if !body.AcknowledgeHoldOverride {
			r.emit(req.Context(), principal, "gdpr.erasure_blocked_by_hold", subject, map[string]any{
				"tenantId":     tenant,
				"userId":       subject,
				"holdCount":    len(held),
				"heldSessions": held,
			})
			writeError(w, http.StatusConflict, "ERASURE_BLOCKED_BY_LEGAL_HOLD",
				"the user has one or more sessions under a legal hold; erasure is blocked until the holds are released",
				map[string]any{"heldSessions": held})
			return
		}
		// §12.8: a platform-admin may override the preflight with a
		// recorded justification. A tenant-admin cannot self-override.
		if body.Justification == "" {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"acknowledgeHoldOverride requires a non-empty justification", nil)
			return
		}
		if !principal.HasRole(auth.RolePlatformAdmin) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"a legal-hold override requires the platform-admin role", nil)
			return
		}
		overrideReceipt = map[string]any{
			"legalHoldOverride":     true,
			"overrideBy":            principal.Subject,
			"overrideJustification": body.Justification,
			"overriddenHolds":       held,
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
		// from admin.user.erasure_initiated to the override decision.
		r.emit(req.Context(), principal, "gdpr.legal_hold_overridden", subject, map[string]any{
			"tenantId":      tenant,
			"userId":        subject,
			"jobId":         jobID,
			"overrideBy":    principal.Subject,
			"justification": body.Justification,
			"holdCount":     len(held),
			"heldSessions":  held,
		})
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
	go func() {
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
		for k, v := range overrideReceipt {
			receipt[k] = v
		}
		r.emit(context.Background(), principal, "gdpr.erasure_completed", subject, receipt)
	}()

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
