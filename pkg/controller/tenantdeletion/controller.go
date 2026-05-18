// SPDX-License-Identifier: MIT

package tenantdeletion

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// TenantEraser is the §12.8 Phase 4 store-deletion seam: it runs
// DeleteByTenant across every store in the §12.8 erasure scope, in the
// dependency order the spec mandates (LeaseStore → ... → SessionStore →
// TokenStore → CredentialPoolStore → tenant configuration). It returns
// the per-store deleted-row tally.
//
// The seam keeps the controller decoupled from the concrete store
// adapters: the gateway wires a TenantEraser over its real Postgres,
// Redis, and MinIO stores, and a test wires a fake. DeleteByTenant
// must be idempotent — §12.8 Phase 4 is re-run on a controller restart.
type TenantEraser interface {
	// DeleteByTenant erases every store's data for tenantID and
	// returns a store-name → deleted-count map.
	DeleteByTenant(ctx context.Context, tenantID string) (map[string]int, error)
}

// SessionTerminator is the §12.8 Phase 2 seam: it sends
// graceful-shutdown signals to the tenant's active sessions and waits
// for them to reach a terminal state, force-terminating any that miss
// the timeout.
type SessionTerminator interface {
	// TerminateTenantSessions drains every active session for
	// tenantID. It is idempotent: a tenant with no active sessions is
	// a no-op.
	TerminateTenantSessions(ctx context.Context, tenantID string) error
}

// CredentialRevoker is the §12.8 Phase 3 seam: it revokes the
// tenant's OAuth tokens and refresh tokens and invalidates its
// credential pool leases.
type CredentialRevoker interface {
	// RevokeTenantCredentials revokes every token and lease for
	// tenantID. It is idempotent.
	RevokeTenantCredentials(ctx context.Context, tenantID string) error
}

// CRDCleaner is the §12.8 Phase 5 seam: it removes the tenant-scoped
// Kubernetes CRD instances (SandboxClaim, pool annotations,
// NetworkPolicy labels).
type CRDCleaner interface {
	// CleanTenantCRDs removes every tenant-scoped CRD instance for
	// tenantID. It is idempotent.
	CleanTenantCRDs(ctx context.Context, tenantID string) error
}

// ReceiptSink is the §12.8 Phase 6 seam: it writes the erasure
// receipt to the audit trail (a gdpr.* audit event under the
// lenny_erasure role).
type ReceiptSink interface {
	// WriteReceipt persists the §12.8 erasure receipt.
	WriteReceipt(ctx context.Context, r Receipt) error
}

// SoftDisabler is the §12.8 Phase 1 seam: it flips the tenant into the
// state that rejects new session creation, API key issuance, and user
// sign-ups. It is optional — when nil, the controller relies on the
// persisted TenantState transition alone to gate new work.
type SoftDisabler interface {
	// SoftDisableTenant rejects new work for tenantID. It is
	// idempotent.
	SoftDisableTenant(ctx context.Context, tenantID string) error
}

// Reconciler drives the §12.8 tenant-deletion lifecycle. Each call to
// ReconcileTenant advances one job by one phase; the manager adapter
// (runnable.go) reconciles every active job on a timer.
//
// The phase actions are injected as the seams above so the controller
// is decoupled from the concrete stores and is unit-testable with
// fakes. The TenantKMS lifecycle is used for the §12.8 Phase 4a
// cryptographic-erasure step on T4 tenants.
type Reconciler struct {
	// Jobs is the §12.8 job registry the controller resumes from.
	Jobs Store
	// KMS drives the §12.9 per-tenant KMS key lifecycle. Phase 4a
	// destroys the tenant key through it. Required.
	KMS *tenantkms.Lifecycle

	// Phase action seams. Eraser, Revoker, Cleaner, and Receipts are
	// required; Disabler and Terminator are optional (a nil value
	// makes the corresponding phase a no-op beyond the state
	// transition).
	Disabler   SoftDisabler
	Terminator SessionTerminator
	Revoker    CredentialRevoker
	Eraser     TenantEraser
	Cleaner    CRDCleaner
	Receipts   ReceiptSink

	// Clock supplies the current time. Nil defaults to time.Now in UTC.
	Clock func() time.Time
}

// now returns the current time via the injected Clock, defaulting to
// time.Now in UTC.
func (r *Reconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now().UTC()
}

// Start records a fresh §12.8 deletion job for the tenant, beginning
// at Phase 1 (soft-disable). It returns ErrAlreadyExists when a
// deletion job for the tenant is already in progress, so a repeated
// delete request does not restart the lifecycle.
//
// workspaceTier is the tenant's §12.9 tier; a T4 tenant runs the
// Phase 4a KMS-key destruction. Start does not run any phase — the
// caller's admin endpoint returns immediately while the controller's
// next pass advances the job.
func (r *Reconciler) Start(ctx context.Context, tenantID, workspaceTier string) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}
	now := r.now()
	return r.Jobs.Create(ctx, Job{
		TenantID:      tenantID,
		WorkspaceTier: workspaceTier,
		State:         TenantDisabling,
		Phase:         PhaseSoftDisable,
		StartedAt:     now,
		UpdatedAt:     now,
		PhaseLog:      map[Phase]time.Time{},
		DeletedCounts: map[string]int{},
	})
}

// ReconcileAll advances every active §12.8 deletion job by one phase.
// It is the entry point the timer-driven runnable calls each tick. A
// per-job failure is recorded on that job and does not abort the
// others; ReconcileAll returns the first error so the caller can log
// it.
func (r *Reconciler) ReconcileAll(ctx context.Context) error {
	jobs, err := r.Jobs.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("tenantdeletion: list active jobs: %w", err)
	}
	var firstErr error
	for _, j := range jobs {
		if err := r.ReconcileTenant(ctx, j.TenantID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ReconcileTenant advances the named tenant's §12.8 deletion job by
// exactly one phase. A job in a terminal phase (completed, failed) is
// left untouched, so a re-run after completion is a no-op and a
// crash-recovery re-run resumes from the persisted phase.
//
// Each phase is individually idempotent (§12.8 "Idempotency and
// resumption"), so re-running a phase whose action already committed —
// because the controller crashed after the action but before the
// phase advanced — does not double-apply it.
func (r *Reconciler) ReconcileTenant(ctx context.Context, tenantID string) error {
	job, err := r.Jobs.Get(ctx, tenantID)
	if err != nil {
		return err
	}
	if job.Phase.Terminal() {
		return nil
	}

	if err := r.runPhase(ctx, &job); err != nil {
		return r.fail(ctx, tenantID, job.Phase, err)
	}

	next, _ := NextPhase(job.Phase)
	completed := job.Phase
	_, uerr := r.Jobs.Update(ctx, tenantID, func(j *Job) error {
		now := r.now()
		if j.PhaseLog == nil {
			j.PhaseLog = map[Phase]time.Time{}
		}
		j.PhaseLog[completed] = now
		j.Phase = next
		j.State = stateForPhase(next)
		j.UpdatedAt = now
		// Carry forward the per-phase results accumulated on the local
		// copy by runPhase (deleted counts, KMS outcome, receipt).
		j.DeletedCounts = job.DeletedCounts
		j.KMSKeyDestroyed = job.KMSKeyDestroyed
		j.Receipt = job.Receipt
		if next == PhaseCompleted {
			j.CompletedAt = now
		}
		return nil
	})
	return uerr
}

// runPhase executes the action for job.Phase, mutating the in-memory
// job copy with the phase result. It does not advance the phase — the
// caller does that atomically after runPhase returns nil.
func (r *Reconciler) runPhase(ctx context.Context, job *Job) error {
	switch job.Phase {
	case PhaseSoftDisable:
		// §12.8 Phase 1: reject new session creation, API key
		// issuance, and sign-ups for the tenant.
		if r.Disabler != nil {
			return r.Disabler.SoftDisableTenant(ctx, job.TenantID)
		}
		return nil

	case PhaseTerminateSessions:
		// §12.8 Phase 2: graceful-shutdown active sessions.
		if r.Terminator != nil {
			return r.Terminator.TerminateTenantSessions(ctx, job.TenantID)
		}
		return nil

	case PhaseRevokeCredentials:
		// §12.8 Phase 3: revoke OAuth tokens and credential leases.
		if r.Revoker == nil {
			return fmt.Errorf("tenantdeletion: Phase 3 requires a CredentialRevoker")
		}
		return r.Revoker.RevokeTenantCredentials(ctx, job.TenantID)

	case PhaseDeleteData:
		// §12.8 Phase 4: DeleteByTenant across every store, in
		// dependency order.
		if r.Eraser == nil {
			return fmt.Errorf("tenantdeletion: Phase 4 requires a TenantEraser")
		}
		counts, err := r.Eraser.DeleteByTenant(ctx, job.TenantID)
		if err != nil {
			return err
		}
		if job.DeletedCounts == nil {
			job.DeletedCounts = map[string]int{}
		}
		for store, n := range counts {
			job.DeletedCounts[store] = n
		}
		return nil

	case PhaseDestroyKMSKey:
		// §12.8 Phase 4a: T4 tenants only — destroy the per-tenant KMS
		// key for cryptographic erasure. DestroyForTenant is a no-op
		// for a non-T4 tenant and idempotent for an already-destroyed
		// key. A failed KMS cleanup is surfaced in the receipt but
		// §12.8 Phase 4a does not let it block the remaining phases —
		// only a transport error from the KeyManager is returned.
		if r.KMS == nil {
			return fmt.Errorf("tenantdeletion: Phase 4a requires a TenantKMS lifecycle")
		}
		if job.WorkspaceTier != tenantkms.WorkspaceTierT4 {
			return nil
		}
		info, err := r.KMS.DestroyForTenant(ctx, job.TenantID)
		if err != nil {
			return err
		}
		job.KMSKeyDestroyed = info.State == tenantkms.KeyStateDestroyed
		return nil

	case PhaseCleanCRDs:
		// §12.8 Phase 5: remove tenant-scoped CRD instances.
		if r.Cleaner == nil {
			return fmt.Errorf("tenantdeletion: Phase 5 requires a CRDCleaner")
		}
		return r.Cleaner.CleanTenantCRDs(ctx, job.TenantID)

	case PhaseProduceReceipt:
		// §12.8 Phase 6: write the erasure receipt to the audit trail.
		if r.Receipts == nil {
			return fmt.Errorf("tenantdeletion: Phase 6 requires a ReceiptSink")
		}
		receipt := Receipt{
			TenantID:        job.TenantID,
			PhaseTimestamps: copyPhaseLog(job.PhaseLog),
			DeletedCounts:   copyCounts(job.DeletedCounts),
			KMSKeyDestroyed: job.KMSKeyDestroyed,
			CompletedAt:     r.now(),
		}
		if err := r.Receipts.WriteReceipt(ctx, receipt); err != nil {
			return err
		}
		job.Receipt = &receipt
		return nil

	default:
		return fmt.Errorf("tenantdeletion: unknown phase %q", job.Phase)
	}
}

// fail records a phase failure on the job and returns the cause. The
// controller retries a failed job from the recorded phase on a later
// pass, so the phase is left unchanged — only the failed marker and
// the reason are written.
func (r *Reconciler) fail(ctx context.Context, tenantID string, phase Phase, cause error) error {
	wrapped := fmt.Errorf("tenantdeletion: tenant %q phase %q: %w", tenantID, phase, cause)
	if _, err := r.Jobs.Update(ctx, tenantID, func(j *Job) error {
		j.Phase = PhaseFailed
		j.Failure = wrapped.Error()
		j.UpdatedAt = r.now()
		j.CompletedAt = r.now()
		return nil
	}); err != nil {
		return err
	}
	return wrapped
}

// Retry resets a failed §12.8 deletion job back to a runnable phase so
// the controller's next pass resumes it. The job is restored to the
// phase recorded before the failure — preserved in PhaseLog — or to
// the first incomplete phase when no phase had completed.
func (r *Reconciler) Retry(ctx context.Context, tenantID string) error {
	_, err := r.Jobs.Update(ctx, tenantID, func(j *Job) error {
		if j.Phase != PhaseFailed {
			return fmt.Errorf("tenantdeletion: tenant %q is not in a failed phase (%q)", tenantID, j.Phase)
		}
		resume := firstIncompletePhase(j.PhaseLog)
		j.Phase = resume
		j.State = stateForPhase(resume)
		j.Failure = ""
		j.CompletedAt = time.Time{}
		j.UpdatedAt = r.now()
		return nil
	})
	return err
}

// firstIncompletePhase returns the earliest §12.8 phase not recorded
// as completed in log — the safe resume point after a failure.
func firstIncompletePhase(log map[Phase]time.Time) Phase {
	for _, p := range phaseOrder {
		if _, done := log[p]; !done {
			return p
		}
	}
	return PhaseProduceReceipt
}

// copyPhaseLog deep-copies a phase-completion log.
func copyPhaseLog(in map[Phase]time.Time) map[Phase]time.Time {
	out := make(map[Phase]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// copyCounts deep-copies a store-deleted-count map.
func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
