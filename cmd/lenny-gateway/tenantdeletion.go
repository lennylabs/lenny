// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// This file hosts the §12.8 tenant-deletion lifecycle inside the gateway
// process. The gateway owns the stores in the §12.8 erasure scope, the
// per-tenant KMS lifecycle, the audit appender, and the
// DELETE /v1/admin/tenants/{id} endpoint, so the background reconciler
// runs here rather than in lenny-controller (which has no store access).
//
// The persisted §12.8 TenantState column is the durable lifecycle
// anchor: DELETE transitions a tenant from `active` into `disabling`, and
// the runner below reconstructs an in-memory deletion Job from any tenant
// observed in `disabling`/`deleting`, advances it one phase per pass,
// reflects the job's state back onto the tenant row, and tombstones the
// tenant (`deleted` + deletedAt) once Phase 6 completes. Because every
// phase is idempotent (§12.8 "Idempotency and resumption"), a job rebuilt
// from `disabling` after a restart re-runs the early phases harmlessly.
//
// spec: §12.8. F-12.8.1, F-24.10.3.

// tenantStateDisabler is the §12.8 Phase 1 SoftDisabler: it flips the
// tenant into `disabling`, which tenantstore.Tenant.AcceptsNewWork reads
// to reject new sessions, API keys, and sign-ups.
type tenantStateDisabler struct {
	tenants tenantstore.Store
	clock   func() time.Time
}

func (d tenantStateDisabler) SoftDisableTenant(ctx context.Context, tenantID string) error {
	_, err := d.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		if t.State == "" || t.State == tenantstore.TenantStateActive {
			t.State = tenantstore.TenantStateDisabling
			t.UpdatedAt = d.clock()
		}
		return nil
	})
	return err
}

// tenantSessionLister lists a tenant's sessions. sessionstore.Store
// satisfies it.
type tenantSessionLister interface {
	List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error)
}

// tenantCredentialRevoker is the §12.8 Phase 3 CredentialRevoker. It runs
// three steps in order and returns the first error, which keeps the job at
// Phase 3 for the next pass. Each step is idempotent.
//
//  1. It commits TenantState deleting on the tenant row unless the row is
//     already deleting or deleted. The runner's syncBack normally writes
//     the state in the pass that completes Phase 2; committing it here
//     keeps the gateway's tenant registry refusing the tenant's bearers
//     before the other Phase 3 actions, even when that write failed.
//  2. It lists the tenant's sessions from the primary, terminal ones
//     included, and returns every credential lease they hold through the
//     §11.4 lease step. credential_leases carries no tenant column, so the
//     sessions are the only path to the tenant's leases. A lease-store
//     error inside the step is silent, because LeasesBySession returns no
//     error: such a lease stays valid until it expires, the stale-slot
//     reaper reclaims it, or a later pass returns it. The step therefore
//     does not fail closed on a lease-store error.
//  3. It runs the tokens entry of the Phase 4 eraser list, which deletes
//     the tenant's TokenStore rows (connector refresh tokens and
//     registered credentials). A nil function means no TokenStore backend
//     is wired in this posture.
//
// The access-token cache flush that §12.8 Phase 3 names has no code here.
// The token service constructs that cache and never writes to it, so a
// flush would delete nothing; the tier-0 guard
// TestErasureExemptionsHaveNoProductionWriter_spec_12_8 fails when a
// writer appears.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3), §11.4 (User Invalidation).
type tenantCredentialRevoker struct {
	tenants  tenantstore.Store
	sessions tenantSessionLister
	leases   admin.UserLeaseRevoker
	tokens   tenantEraseFunc
	clock    func() time.Time
}

// RevokeTenantCredentials runs the Phase 3 steps for tenantID.
func (c tenantCredentialRevoker) RevokeTenantCredentials(ctx context.Context, tenantID string) error {
	if err := c.commitDeleting(ctx, tenantID); err != nil {
		return fmt.Errorf("tenant deletion phase 3: commit deleting: %w", err)
	}
	rows, err := c.sessions.List(ctx, tenantID, sessionstore.ListFilter{Primary: true})
	if err != nil {
		return fmt.Errorf("tenant deletion phase 3: list sessions: %w", err)
	}
	if len(rows) > 0 {
		ids := make([]string, len(rows))
		for i, s := range rows {
			ids[i] = s.ID
		}
		c.leases.RevokeUserLeases(tenantID, "", ids)
	}
	if c.tokens != nil {
		if _, err := c.tokens(ctx, tenantID); err != nil {
			return fmt.Errorf("tenant deletion phase 3: delete tokens: %w", err)
		}
	}
	return nil
}

// commitDeleting moves the tenant row to deleting unless it is already
// deleting or deleted.
func (c tenantCredentialRevoker) commitDeleting(ctx context.Context, tenantID string) error {
	_, err := c.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		if t.State != tenantstore.TenantStateDeleting && t.State != tenantstore.TenantStateDeleted {
			t.State = tenantstore.TenantStateDeleting
			t.UpdatedAt = c.clock()
		}
		return nil
	})
	return err
}

// tenantEraserFunc returns the function of the entry named name in
// erasers, or nil when the entry is absent or has no function.
func tenantEraserFunc(erasers []namedTenantEraser, name string) tenantEraseFunc {
	for _, e := range erasers {
		if e.name == name {
			return e.fn
		}
	}
	return nil
}

// tenantSessionTerminator is the §12.8 Phase 2 SessionTerminator. It
// runs the lifecycle drain over the whole tenant under a per-drain
// budget, so one tenant's drain delays the other tenants' deletion by at
// most one budget. A budget expiry ends the drain between sessions and
// is returned as a drain error, which keeps the job at Phase 2 for the
// next tick.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain).
type tenantSessionTerminator struct {
	sessions admin.DrainLister
	term     admin.DrainTerminator
	// budget bounds each drain. It is the operator-tunable
	// --lifecycle-tick-budget, which the startup gate requires positive.
	budget time.Duration
}

// TerminateTenantSessions drains every non-terminal session of tenantID
// and returns the drain's error.
func (t tenantSessionTerminator) TerminateTenantSessions(ctx context.Context, tenantID string) error {
	tctx, cancel := context.WithTimeout(ctx, t.budget)
	defer cancel()
	if _, err := admin.DrainSessions(tctx, t.sessions, t.term, tenantID, ""); err != nil {
		return fmt.Errorf("tenant deletion drain: %w", err)
	}
	return nil
}

// namedTenantEraser pairs a store name with its DeleteByTenant function.
// Both the (int, error) CountingEraser stores and the (error)-only
// StoreEraser stores adapt to this single signature. A nil fn lists the store
// by name for a posture whose backend is absent: ValidateTenantOrder counts
// the name as wired, and tenantEraser skips it.
type namedTenantEraser struct {
	name string
	fn   func(ctx context.Context, tenantID string) (int, error)
}

// tenantEraser is the §12.8 Phase 4 TenantEraser: it runs DeleteByTenant
// across the wired stores in dependency order and returns the per-store
// deleted-row tally for the erasure receipt. A store error aborts the
// phase (it is retried from Phase 4 on a later pass); each DeleteByTenant
// is idempotent so the retry re-deletes already-removed rows as a no-op.
// An entry with a nil function is skipped and left out of the tally, so the
// receipt does not report a store that did not run as having erased zero
// rows.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle).
type tenantEraser struct {
	erasers []namedTenantEraser
}

func (e *tenantEraser) DeleteByTenant(ctx context.Context, tenantID string) (map[string]int, error) {
	out := make(map[string]int, len(e.erasers))
	for _, ne := range e.erasers {
		if ne.fn == nil {
			continue
		}
		n, err := ne.fn(ctx, tenantID)
		if err != nil {
			return out, fmt.Errorf("tenant erase %s: %w", ne.name, err)
		}
		out[ne.name] = n
	}
	return out, nil
}

// auditReceiptSink is the §12.8 Phase 6 ReceiptSink. It writes the
// erasure receipt as a gdpr.* audit event (retained under
// audit.gdprRetentionDays and exempt from later erasure, per §12.8) so the receipt survives the tenant tombstone.
type auditReceiptSink struct {
	appender policy.AuditAppender
	clock    func() time.Time
}

func (s auditReceiptSink) WriteReceipt(ctx context.Context, r tenantdeletion.Receipt) error {
	phases := make(map[string]string, len(r.PhaseTimestamps))
	for p, at := range r.PhaseTimestamps {
		phases[string(p)] = at.UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(map[string]any{
		"tenantId":        r.TenantID,
		"deletedCounts":   r.DeletedCounts,
		"kmsKeyDestroyed": r.KMSKeyDestroyed,
		"phaseTimestamps": phases,
		"completedAt":     r.CompletedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	_, err = s.appender.Append(ctx, r.TenantID, "gdpr.tenant_erased", payload, s.clock())
	return err
}

// artifactHoldChecker reports whether any artifact under a session
// carries a §12.8 legal hold. *artifactcatalog.Store satisfies it.
type artifactHoldChecker interface {
	IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// tenantHoldEnumerator is the §12.8 Phase 3.5 LegalHoldEnumerator. It
// reports the tenant's active session-level holds (sessions.legal_hold)
// and artifact-level holds (any artifact under one of the tenant's
// sessions). The audit_range / workspace_snapshot ledger holds (§12.8) are not yet enumerated here.
//
// spec: §12.8(a)(b).
type tenantHoldEnumerator struct {
	sessions  sessionstore.Store
	artifacts artifactHoldChecker
}

func (e tenantHoldEnumerator) ActiveTenantHolds(ctx context.Context, tenantID string) ([]tenantdeletion.HeldResource, error) {
	rows, err := e.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var holds []tenantdeletion.HeldResource
	for _, s := range rows {
		if s.LegalHold {
			holds = append(holds, tenantdeletion.HeldResource{ResourceType: "session", ResourceID: s.ID})
		}
		if e.artifacts != nil {
			held, herr := e.artifacts.IsLegalHeldAt(ctx, tenantID, s.ID)
			if herr != nil {
				return nil, herr
			}
			if held {
				holds = append(holds, tenantdeletion.HeldResource{ResourceType: "artifact", ResourceID: s.ID})
			}
		}
	}
	return holds, nil
}

// tenantDeletionBlockedSink is the §12.8 Phase 3.5 DeletionBlockedSink.
// It writes the admin.tenant.deletion_blocked audit event listing the
// held resource IDs when a deletion pauses on an active hold.
//
// spec: §12.8.
type tenantDeletionBlockedSink struct {
	appender policy.AuditAppender
	clock    func() time.Time
}

func (s tenantDeletionBlockedSink) DeletionBlocked(ctx context.Context, tenantID string, holds []tenantdeletion.HeldResource) {
	tuples := make([]map[string]string, 0, len(holds))
	for _, h := range holds {
		tuples = append(tuples, map[string]string{"resourceType": h.ResourceType, "resourceId": h.ResourceID})
	}
	payload, err := json.Marshal(map[string]any{
		"tenantId":  tenantID,
		"holdCount": len(holds),
		"holds":     tuples,
	})
	if err != nil {
		return
	}
	_, _ = s.appender.Append(ctx, tenantID, "admin.tenant.deletion_blocked", payload, s.clock())
}

// tenantDeletionRunner is the gateway-hosted §12.8 reconcile loop. Each
// tick it reconstructs a deletion Job for every tenant in `disabling` or
// `deleting`, advances every active job one phase via the Reconciler,
// reflects the job state back onto the tenant row, and tombstones the
// tenant once the lifecycle completes. A tick runs only while the §12.5
// lenny-gateway-leader elector reports this replica as leader, so the
// destructive loop runs on the same replica as the other gateway-singleton
// sweeps and holds no pooled connection across the tick.
//
// The runner is not registered on the leader gate: a lease loss cancels
// the leader context, which would fail the replica's in-memory deletion
// job mid-phase. It runs for the process lifetime and polls the elector
// instead.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.5 (lenny-gateway-leader
// gate).
type tenantDeletionRunner struct {
	reconciler *tenantdeletion.Reconciler
	tenants    tenantstore.Store
	clock      func() time.Time
	interval   time.Duration
	// elector gates each tick. startLeaderElectedSweeps sets it to the
	// gateway-leader elector before it launches Start. A nil elector runs
	// no tick, so an unwired runner never runs the destructive loop on
	// every replica.
	elector gatewayleader.Elector
}

// Start runs the reconcile loop until ctx is cancelled. It reconciles
// once immediately so a freshly-started gateway does not wait a full
// interval before advancing an in-flight deletion.
func (rn *tenantDeletionRunner) Start(ctx context.Context) {
	interval := rn.interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	rn.tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rn.tick(ctx)
		}
	}
}

// tick advances every in-flight tenant deletion by one phase when this
// replica is the gateway leader.
func (rn *tenantDeletionRunner) tick(ctx context.Context) {
	if rn.elector == nil || !rn.elector.IsLeader() {
		return
	}
	rn.reconcileOnce(ctx)
}

// reconcileOnce is the lock-free body of a tick, separated so a test can
// drive it deterministically.
func (rn *tenantDeletionRunner) reconcileOnce(ctx context.Context) {
	rows, err := rn.tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		log.Printf("lenny-gateway: tenant-deletion list: %v", err)
		return
	}
	var inFlight []tenantstore.Tenant
	for _, t := range rows {
		if t.State == tenantstore.TenantStateDisabling || t.State == tenantstore.TenantStateDeleting {
			inFlight = append(inFlight, t)
			rn.ensureJob(ctx, t)
		}
	}
	if err := rn.reconciler.ReconcileAll(ctx); err != nil {
		log.Printf("lenny-gateway: tenant-deletion reconcile: %v", err)
	}
	for _, t := range inFlight {
		rn.syncBack(ctx, t.ID)
	}
}

// ensureJob reconstructs the in-memory deletion Job for a tenant that is
// mid-lifecycle but has no live job (e.g. after a gateway restart, or on
// the replica that did not receive the DELETE). The resume phase is
// derived from the persisted TenantState; idempotency makes resuming from
// the start of the state's phase span safe.
func (rn *tenantDeletionRunner) ensureJob(ctx context.Context, t tenantstore.Tenant) {
	if existing, err := rn.reconciler.Jobs.Get(ctx, t.ID); err == nil {
		// §12.8 Phase 3.5 override: a force-delete authorized after the job
		// was already created (e.g. an operator first issued DELETE, then
		// force-delete) stamps the override on the tenant row. Reconcile it
		// onto the live job so Phase 3.5 escrows rather than re-blocks.
		if t.ForceDeleteHoldOverride && !existing.OverrideHoldAck {
			_, _ = rn.reconciler.Jobs.Update(ctx, t.ID, func(j *tenantdeletion.Job) error {
				j.OverrideHoldAck = true
				j.OverrideBy = t.ForceDeleteBy
				j.OverrideJustification = t.ForceDeleteJustification
				j.OverrideAt = t.ForceDeleteAt
				return nil
			})
		}
		return
	}
	now := rn.clock()
	phase := tenantdeletion.PhaseSoftDisable
	state := tenantdeletion.TenantDisabling
	if t.State == tenantstore.TenantStateDeleting {
		// Resume at the first `deleting`-state phase. Phase 3 (revoke) and
		// Phase 3.5 (legal-hold) are idempotent, so restarting there does
		// not double-apply Phase 4.
		phase = tenantdeletion.PhaseRevokeCredentials
		state = tenantdeletion.TenantDeleting
	}
	// §12.8 Phase 3.5 override: read the durable force-delete override off
	// the tenant row so a job reconstructed after a gateway restart still
	// segregates held evidence into the escrow.
	_ = rn.reconciler.Jobs.Create(ctx, tenantdeletion.Job{
		TenantID:              t.ID,
		WorkspaceTier:         t.WorkspaceTier,
		State:                 state,
		Phase:                 phase,
		StartedAt:             now,
		UpdatedAt:             now,
		PhaseLog:              map[tenantdeletion.Phase]time.Time{},
		DeletedCounts:         map[string]int{},
		OverrideHoldAck:       t.ForceDeleteHoldOverride,
		OverrideBy:            t.ForceDeleteBy,
		OverrideJustification: t.ForceDeleteJustification,
		OverrideAt:            t.ForceDeleteAt,
	})
}

// syncBack reflects a job's progress onto the tenant row: a completed
// lifecycle tombstones the tenant; an in-progress job advances the tenant
// state (disabling → deleting) so GET /v1/admin/tenants/{id} surfaces
// real progress. A job paused at Phase 2 on a drain that did not verify
// keeps state `disabling`, so a job rebuilt after a restart resumes at
// Phase 1 and re-runs the drain. A Phase 3.5 block leaves the tenant in
// `deleting`, and a failed job leaves it where it is for operator
// attention.
func (rn *tenantDeletionRunner) syncBack(ctx context.Context, tenantID string) {
	job, err := rn.reconciler.Jobs.Get(ctx, tenantID)
	if err != nil {
		return
	}
	switch job.Phase {
	case tenantdeletion.PhaseCompleted:
		// §12.8 Phase 6 / Phase 4 tombstone: state = deleted, deletedAt set.
		if err := rn.tenants.SoftDelete(ctx, tenantID, rn.clock()); err != nil {
			log.Printf("lenny-gateway: tenant-deletion tombstone %q: %v", tenantID, err)
		}
	case tenantdeletion.PhaseFailed:
		// Leave the tenant where it is. A failed job is terminal for the
		// controller and needs operator attention.
	default:
		// Reflect the job's coarse state onto the tenant row so a GET
		// surfaces progress. The job reaches state `deleted` already at the
		// produce-receipt phase, but the tenant must stay `deleting` until
		// the lifecycle actually completes and the tombstone is written;
		// otherwise a still-running job's tenant would read as deleted with
		// no DeletedAt and drop out of the reconcile sweep.
		want := string(job.State)
		if want == string(tenantdeletion.TenantDeleted) {
			want = tenantstore.TenantStateDeleting
		}
		_, err := rn.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
			if t.State != want && t.State != tenantstore.TenantStateDeleted {
				t.State = want
				t.UpdatedAt = rn.clock()
			}
			return nil
		})
		if err != nil {
			log.Printf("lenny-gateway: tenant-deletion state sync %q: %v", tenantID, err)
		}
	}
}
