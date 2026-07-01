// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
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
// spec: §12.8 line 865, lines 872-889. F-12.8.1, F-24.10.3.

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

// namedTenantEraser pairs a store name with its DeleteByTenant function.
// Both the (int, error) CountingEraser stores and the (error)-only
// StoreEraser stores adapt to this single shape.
type namedTenantEraser struct {
	name string
	fn   func(ctx context.Context, tenantID string) (int, error)
}

// tenantEraser is the §12.8 Phase 4 TenantEraser: it runs DeleteByTenant
// across the wired stores in dependency order and returns the per-store
// deleted-row tally for the erasure receipt. A store error aborts the
// phase (it is retried from Phase 4 on a later pass); each DeleteByTenant
// is idempotent so the retry re-deletes already-removed rows as a no-op.
type tenantEraser struct {
	erasers []namedTenantEraser
}

func (e *tenantEraser) DeleteByTenant(ctx context.Context, tenantID string) (map[string]int, error) {
	out := make(map[string]int, len(e.erasers))
	for _, ne := range e.erasers {
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
// audit.gdprRetentionDays and exempt from later erasure, per §12.8 line
// 775) so the receipt survives the tenant tombstone.
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
// sessions). The audit_range / workspace_snapshot ledger holds (§12.8
// line 878(c)) are not yet enumerated here.
//
// spec: §12.8 line 878(a)(b).
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
// spec: §12.8 line 878.
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
// tenant once the lifecycle completes. A single-flight Postgres advisory
// lock keeps the destructive loop running on at most one gateway replica.
type tenantDeletionRunner struct {
	reconciler *tenantdeletion.Reconciler
	tenants    tenantstore.Store
	clock      func() time.Time
	interval   time.Duration
	// pool, when non-nil, gates each tick behind a non-blocking advisory
	// lock so only one replica runs the destructive loop. Nil runs the
	// loop unguarded (single-replica / in-memory posture).
	pool *pgxpool.Pool
}

// advisoryLockKey is the stable 64-bit key for the tenant-deletion
// single-flight lock, shared across replicas.
func advisoryLockKey() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("tenant_deletion_controller"))
	return int64(h.Sum64())
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

// tick advances every in-flight tenant deletion by one phase under the
// single-flight lock.
func (rn *tenantDeletionRunner) tick(ctx context.Context) {
	if rn.pool != nil {
		conn, err := rn.pool.Acquire(ctx)
		if err != nil {
			return
		}
		defer conn.Release()
		var locked bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey()).Scan(&locked); err != nil || !locked {
			return
		}
		defer func() {
			_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey())
		}()
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
// real progress; a failed or blocked job leaves the tenant in `deleting`
// for operator attention.
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
		// Leave the tenant where it is; an operator retries the job.
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
