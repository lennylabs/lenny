// SPDX-License-Identifier: MIT

package tenantdeletion

import (
	"context"
	"errors"
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

// LegalHoldEnumerator is the §12.8 Phase 3.5 standard-path seam: it
// returns every active legal hold scoped to the tenant (sessions,
// artifacts, audit-range, workspace-snapshot). When nil, the controller
// treats the tenant as unheld and advances straight to Phase 4 — the
// fail-open posture is acceptable only for a deployment that has no
// legal-hold surface wired. spec: §12.8 line 878.
type LegalHoldEnumerator interface {
	// ActiveTenantHolds enumerates the tenant's active legal holds. An
	// empty result means the deletion may proceed into Phase 4.
	ActiveTenantHolds(ctx context.Context, tenantID string) ([]HeldResource, error)
}

// DeletionBlockedSink receives the §12.8 admin.tenant.deletion_blocked
// audit event the controller emits when Phase 3.5 pauses on an active
// legal hold. It is optional and called once per block transition (not
// on every re-evaluation pass). spec: §12.8 line 878.
type DeletionBlockedSink interface {
	// DeletionBlocked records that tenantID's deletion is paused on the
	// given active holds.
	DeletionBlocked(ctx context.Context, tenantID string, holds []HeldResource)
}

// EscrowRequest is the §12.8 Phase 3.5 override migration request the
// controller hands the EscrowMigrator once an operator force-deletes a
// held tenant.
type EscrowRequest struct {
	// TenantID is the tenant being force-deleted.
	TenantID string
	// JobID identifies the tenant-delete job for the ledger events.
	JobID string
	// Holds are the active tenant-scoped legal holds to segregate.
	Holds []HeldResource
}

// EscrowOutcome is the result of a successful §12.8 Phase 3.5 escrow
// migration, carried onto the job for the
// gdpr.legal_hold_overridden_tenant event and the receipt.
type EscrowOutcome struct {
	// ResolvedRegion is the region the evidence was escrowed to.
	ResolvedRegion string
	// EscrowKEKID is the region-scoped escrow KEK identifier.
	EscrowKEKID string
	// EscrowObjectKeys are the escrow bucket keys written.
	EscrowObjectKeys []string
}

// EscrowMigrator is the §12.8 Phase 3.5 override-path seam: it segregates
// the tenant's held evidence into the region-scoped legal-hold escrow
// (re-encrypt under platform:legal_hold_escrow:<region>, migrate to the
// escrow bucket, record the ledger) before the deletion proceeds. It is
// optional: a deployment with no escrow configured leaves it nil, in
// which case an override cannot be honored and Phase 3.5 falls back to
// the fail-closed standard-path block. EscrowHolds MUST return an error
// wrapping ErrEscrowRegionUnresolvable when the tenant's region has no
// escrow configuration, so the controller pauses rather than fails.
// spec: §12.8 lines 880-889.
type EscrowMigrator interface {
	EscrowHolds(ctx context.Context, req EscrowRequest) (EscrowOutcome, error)
}

// OverrideAppliedEvent is the §12.8 gdpr.legal_hold_overridden_tenant
// critical audit event the controller emits after the four override
// sub-steps complete.
type OverrideAppliedEvent struct {
	TenantID         string
	JobID            string
	OverrideBy       string
	Justification    string
	OverrideAt       time.Time
	OverriddenHolds  []HeldResource
	EscrowObjectKeys []string
}

// OverrideSink receives the §12.8 gdpr.legal_hold_overridden_tenant
// critical audit event and increments the per-tenant override counter
// once Phase 3.5's override sub-steps complete. Optional.
type OverrideSink interface {
	// OverrideApplied records the tenant-scope legal-hold override.
	OverrideApplied(ctx context.Context, ev OverrideAppliedEvent)
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
	// destroys the tenant key through it for a T4 tenant. Optional: a
	// reconciler hosted with only a probe-capable kms.Provider (e.g. the
	// gateway, whose provider is the §4 wrap/unwrap data path and cannot
	// run control-plane DestroyKey) passes nil, in which case Phase 4a is
	// a no-op and the T4 key is destroyed out-of-band against the cloud
	// KMS API per §12.5 line 301. A non-T4 tenant never touches KMS.
	KMS *tenantkms.Lifecycle

	// Phase action seams. Eraser and Receipts are required; Disabler,
	// Terminator, Revoker, Cleaner, and LegalHolds are optional. A nil
	// optional seam makes the corresponding phase a no-op beyond the
	// state transition: Phase 4 (Eraser) is the substantive erasure that
	// every deployment must wire, while graceful-shutdown (Phase 2),
	// credential revocation (Phase 3), and CRD cleanup (Phase 5) degrade
	// to the Phase 4 DeleteByTenant teardown when the host has no live
	// seam (e.g. a gateway-hosted reconciler with no Kubernetes client
	// for CRD cleanup). A nil LegalHolds advances Phase 3.5 unconditionally.
	Disabler   SoftDisabler
	Terminator SessionTerminator
	Revoker    CredentialRevoker
	Eraser     TenantEraser
	Cleaner    CRDCleaner
	Receipts   ReceiptSink
	LegalHolds LegalHoldEnumerator
	// Blocked receives the §12.8 admin.tenant.deletion_blocked event when
	// Phase 3.5 pauses on an active hold. Optional.
	Blocked DeletionBlockedSink

	// Escrow segregates held evidence into the region-scoped escrow on the
	// §12.8 force-delete override path. Optional: a nil migrator cannot
	// honor an override, so a held tenant with the override set still
	// blocks fail-closed. spec: §12.8 lines 880-889.
	Escrow EscrowMigrator
	// Override receives the §12.8 gdpr.legal_hold_overridden_tenant event
	// after the override sub-steps complete. Optional.
	Override OverrideSink

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
		// §12.8 Phase 3.5: an active legal hold pauses the deletion rather
		// than failing it. The job stays at PhaseLegalHoldSegregation in
		// state deleting; a later pass re-evaluates the ledger once the
		// holds clear (or the operator force-deletes).
		if errors.Is(err, ErrBlockedByLegalHold) {
			return r.block(ctx, &job)
		}
		// §12.8 Phase 3.5 override, sub-step 2 (line 883): an unresolvable
		// escrow region leaves the tenant paused at Phase 3.5 pending
		// operator remediation (fix the region config and re-enter), not
		// failed — the migrator already emitted DataResidencyViolationAttempt.
		if errors.Is(err, ErrEscrowRegionUnresolvable) {
			return r.pauseEscrowUnresolvable(ctx, &job)
		}
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
		// A phase advanced cleanly: clear any prior Phase 3.5 block so a
		// hold that was released no longer leaves a stale blocked marker.
		j.BlockedReason = ""
		j.BlockedHolds = nil
		// Carry forward the per-phase results accumulated on the local
		// copy by runPhase (deleted counts, KMS outcome, receipt, and the
		// Phase 3.5 override escrow outcome).
		j.DeletedCounts = job.DeletedCounts
		j.KMSKeyDestroyed = job.KMSKeyDestroyed
		j.Receipt = job.Receipt
		j.OverriddenHolds = job.OverriddenHolds
		j.EscrowRegion = job.EscrowRegion
		j.EscrowKEKID = job.EscrowKEKID
		j.EscrowObjectKeys = job.EscrowObjectKeys
		if next == PhaseCompleted {
			j.CompletedAt = now
		}
		return nil
	})
	return uerr
}

// block records the §12.8 Phase 3.5 standard-path pause on the job,
// leaving Phase unchanged so a later pass re-evaluates the ledger. The
// admin.tenant.deletion_blocked audit event is emitted once per block
// transition (when the job was not already blocked), not on every pass,
// so a long-held tenant does not flood the audit chain. block returns
// nil so ReconcileAll continues advancing the other tenants.
func (r *Reconciler) block(ctx context.Context, job *Job) error {
	firstBlock := job.BlockedReason == ""
	holds := append([]HeldResource(nil), job.BlockedHolds...)
	if _, err := r.Jobs.Update(ctx, job.TenantID, func(j *Job) error {
		j.BlockedReason = fmt.Sprintf("%d active legal hold(s) scoped to the tenant", len(holds))
		j.BlockedHolds = append([]HeldResource(nil), holds...)
		j.State = TenantDeleting
		j.UpdatedAt = r.now()
		return nil
	}); err != nil {
		return err
	}
	if firstBlock && r.Blocked != nil {
		r.Blocked.DeletionBlocked(ctx, job.TenantID, holds)
	}
	return nil
}

// pauseEscrowUnresolvable records the §12.8 Phase 3.5 override-path pause
// when the escrow region is unresolvable (line 883). Unlike block, it
// does not emit admin.tenant.deletion_blocked — the EscrowMigrator
// already emitted DataResidencyViolationAttempt and raised the
// LegalHoldEscrowResidencyViolation alert. The job stays at
// PhaseLegalHoldSegregation in state deleting so a later pass retries
// once the operator configures the region. pauseEscrowUnresolvable
// returns nil so ReconcileAll continues advancing other tenants.
func (r *Reconciler) pauseEscrowUnresolvable(ctx context.Context, job *Job) error {
	holds := append([]HeldResource(nil), job.BlockedHolds...)
	_, err := r.Jobs.Update(ctx, job.TenantID, func(j *Job) error {
		j.BlockedReason = "escrow region unresolvable (LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE)"
		j.BlockedHolds = append([]HeldResource(nil), holds...)
		j.State = TenantDeleting
		j.UpdatedAt = r.now()
		return nil
	})
	return err
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
		// §12.8 Phase 3: revoke OAuth tokens and credential leases. When
		// no revoker is wired, the tenant's token and credential-pool rows
		// are removed by Phase 4's DeleteByTenant a pass later, which also
		// invalidates them.
		if r.Revoker != nil {
			return r.Revoker.RevokeTenantCredentials(ctx, job.TenantID)
		}
		return nil

	case PhaseLegalHoldSegregation:
		// §12.8 Phase 3.5: before any destructive operation, enumerate
		// active tenant-scoped legal holds. A nil enumerator advances
		// unconditionally.
		if r.LegalHolds == nil {
			job.BlockedHolds = nil
			return nil
		}
		holds, err := r.LegalHolds.ActiveTenantHolds(ctx, job.TenantID)
		if err != nil {
			return err
		}
		if len(holds) == 0 {
			job.BlockedHolds = nil
			return nil
		}
		// Holds present. Standard path: pause (ErrBlockedByLegalHold) so
		// Phase 4 / 4a never destroy held evidence — that would be
		// spoliation. Override path (POST .../force-delete with
		// acknowledgeHoldOverride): segregate the evidence into the
		// region-scoped escrow, then proceed.
		if !job.OverrideHoldAck || r.Escrow == nil {
			// No override, or no escrow migrator wired to honor one — fail
			// closed by blocking. A force-delete on a deployment with no
			// escrow config cannot proceed (it would otherwise destroy
			// evidence with nowhere to segregate it to).
			job.BlockedHolds = holds
			return fmt.Errorf("%w: %d hold(s)", ErrBlockedByLegalHold, len(holds))
		}
		// §12.8 sub-steps 2-4: re-encrypt + migrate + ledger.
		outcome, err := r.Escrow.EscrowHolds(ctx, EscrowRequest{
			TenantID: job.TenantID,
			JobID:    job.TenantID, // the tenant id is the job key
			Holds:    holds,
		})
		if err != nil {
			if errors.Is(err, ErrEscrowRegionUnresolvable) {
				// The migrator emitted DataResidencyViolationAttempt and
				// raised the alert; pause here pending operator remediation.
				job.BlockedHolds = holds
				return ErrEscrowRegionUnresolvable
			}
			return err
		}
		job.OverriddenHolds = holds
		job.EscrowRegion = outcome.ResolvedRegion
		job.EscrowKEKID = outcome.EscrowKEKID
		job.EscrowObjectKeys = outcome.EscrowObjectKeys
		// §12.8: after the four sub-steps, emit gdpr.legal_hold_overridden_tenant.
		// The escrow is idempotent, so a crash-retry re-runs it and re-emits;
		// a duplicate override event is benign (compliance still sees it).
		if r.Override != nil {
			r.Override.OverrideApplied(ctx, OverrideAppliedEvent{
				TenantID:         job.TenantID,
				JobID:            job.TenantID,
				OverrideBy:       job.OverrideBy,
				Justification:    job.OverrideJustification,
				OverrideAt:       job.OverrideAt,
				OverriddenHolds:  holds,
				EscrowObjectKeys: outcome.EscrowObjectKeys,
			})
		}
		job.BlockedHolds = nil
		return nil

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
		// only a transport error from the KeyManager is returned. A nil
		// KMS (probe-only host) or a non-T4 tenant skips the step; the
		// receipt then records KMSKeyDestroyed=false.
		if r.KMS == nil || job.WorkspaceTier != tenantkms.WorkspaceTierT4 {
			return nil
		}
		info, err := r.KMS.DestroyForTenant(ctx, job.TenantID)
		if err != nil {
			return err
		}
		job.KMSKeyDestroyed = info.State == tenantkms.KeyStateDestroyed
		return nil

	case PhaseCleanCRDs:
		// §12.8 Phase 5: remove tenant-scoped CRD instances. Optional: a
		// reconciler hosted without a Kubernetes client (e.g. in the
		// gateway) relies on Phase 2 session termination and the warm-pool
		// GC to reap the tenant's SandboxClaims.
		if r.Cleaner != nil {
			return r.Cleaner.CleanTenantCRDs(ctx, job.TenantID)
		}
		return nil

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
