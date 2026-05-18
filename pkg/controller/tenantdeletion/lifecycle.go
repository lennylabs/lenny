// SPDX-License-Identifier: MIT

// Package tenantdeletion implements the §12.8 tenant-deletion /
// right-to-erasure controller: the background reconciler that drives a
// tenant marked for deletion through the §12.8 multi-phase lifecycle.
//
// The §12.8 lifecycle. Tenant deletion is a multi-phase process. Each
// tenant carries a TenantState enum (active, disabling, deleting,
// deleted) persisted in Postgres. The deletion controller advances a
// tenant through the §12.8 phases in order:
//
//	Phase 1  (disabling) — soft-disable: reject new sessions, API keys, sign-ups.
//	Phase 2  (disabling) — terminate: graceful-shutdown active sessions.
//	Phase 3  (deleting)  — revoke OAuth tokens and credential leases.
//	Phase 4  (deleting)  — DeleteByTenant across every store, in dependency order.
//	Phase 4a (deleting)  — T4 tenants only: destroy the per-tenant KMS key
//	                       for cryptographic erasure.
//	Phase 5  (deleting)  — clean CRDs: remove tenant-scoped Kubernetes resources.
//	Phase 6  (deleted)   — produce the erasure receipt in the audit trail.
//
// Idempotency and resumption (§12.8 "Idempotency and resumption"). The
// controller persists the current phase in the tenant record. If the
// process is interrupted — controller restart, transient failure — it
// resumes from the last incomplete phase. Each phase is individually
// idempotent, so re-running a phase that already completed is a no-op.
//
// This file defines the lifecycle state model — the TenantState and
// Phase enums, the Job record the controller reconciles, and the Store
// the Job is persisted to. The reconciler (controller.go) advances a
// Job one phase per pass; the manager adapter (runnable.go) registers
// it as a timer-driven manager.Runnable because Kubernetes cannot
// watch the Postgres-backed tenant registry.
package tenantdeletion

import (
	"context"
	"errors"
	"sync"
	"time"
)

// TenantState is the §12.8 tenant lifecycle enum persisted in the
// tenant record and exposed via the admin API.
type TenantState string

const (
	// TenantActive — the tenant is operational; deletion has not been
	// requested.
	TenantActive TenantState = "active"
	// TenantDisabling — Phase 1 and Phase 2: new work is rejected and
	// active sessions are being drained.
	TenantDisabling TenantState = "disabling"
	// TenantDeleting — Phases 3 through 5: credentials are revoked,
	// store data is deleted, the KMS key is destroyed, and CRDs are
	// cleaned.
	TenantDeleting TenantState = "deleting"
	// TenantDeleted — Phase 6 complete: the erasure receipt is written
	// and the tenant row is a tombstone.
	TenantDeleted TenantState = "deleted"
)

// Phase is the §12.8 tenant-deletion phase. It is finer-grained than
// TenantState — several phases share one TenantState — so the
// controller can resume from the exact step that was interrupted.
type Phase string

const (
	// PhaseSoftDisable — §12.8 Phase 1: reject new session creation,
	// API key issuance, and sign-ups for the tenant.
	PhaseSoftDisable Phase = "soft_disable"
	// PhaseTerminateSessions — §12.8 Phase 2: graceful-shutdown active
	// sessions, then force-terminate any that miss the timeout.
	PhaseTerminateSessions Phase = "terminate_sessions"
	// PhaseRevokeCredentials — §12.8 Phase 3: revoke OAuth tokens and
	// refresh tokens, invalidate credential pool leases.
	PhaseRevokeCredentials Phase = "revoke_credentials"
	// PhaseDeleteData — §12.8 Phase 4: DeleteByTenant on every store
	// in the erasure scope, in dependency order.
	PhaseDeleteData Phase = "delete_data"
	// PhaseDestroyKMSKey — §12.8 Phase 4a: T4 tenants only — destroy
	// the per-tenant KMS key for cryptographic erasure.
	PhaseDestroyKMSKey Phase = "destroy_kms_key"
	// PhaseCleanCRDs — §12.8 Phase 5: remove tenant-scoped Kubernetes
	// CRD instances.
	PhaseCleanCRDs Phase = "clean_crds"
	// PhaseProduceReceipt — §12.8 Phase 6: write the erasure receipt
	// to the audit trail.
	PhaseProduceReceipt Phase = "produce_receipt"
	// PhaseCompleted — the lifecycle is done; the tenant is a
	// tombstone. The controller does no further work.
	PhaseCompleted Phase = "completed"
	// PhaseFailed — a phase errored. Job.Failure carries the reason.
	// The controller retries a failed job from the recorded phase.
	PhaseFailed Phase = "failed"
)

// phaseOrder is the §12.8 phase sequence. NextPhase walks it; the
// reconciler advances a job one entry per pass.
var phaseOrder = []Phase{
	PhaseSoftDisable,
	PhaseTerminateSessions,
	PhaseRevokeCredentials,
	PhaseDeleteData,
	PhaseDestroyKMSKey,
	PhaseCleanCRDs,
	PhaseProduceReceipt,
}

// NextPhase returns the phase that follows p in the §12.8 sequence,
// and true. For the last phase it returns PhaseCompleted, true. For a
// terminal phase (completed, failed) it returns p, false.
func NextPhase(p Phase) (Phase, bool) {
	if p == PhaseCompleted || p == PhaseFailed {
		return p, false
	}
	for i, ph := range phaseOrder {
		if ph == p {
			if i+1 < len(phaseOrder) {
				return phaseOrder[i+1], true
			}
			return PhaseCompleted, true
		}
	}
	// An unrecognized phase is treated as the start of the sequence.
	return phaseOrder[0], true
}

// stateForPhase maps a §12.8 phase to the TenantState the tenant
// record carries while that phase runs.
func stateForPhase(p Phase) TenantState {
	switch p {
	case PhaseSoftDisable, PhaseTerminateSessions:
		return TenantDisabling
	case PhaseRevokeCredentials, PhaseDeleteData, PhaseDestroyKMSKey, PhaseCleanCRDs:
		return TenantDeleting
	case PhaseProduceReceipt, PhaseCompleted:
		// Phase 6 transitions the tenant to deleted on completion.
		return TenantDeleted
	default:
		return TenantActive
	}
}

// Terminal reports whether p is an end state.
func (p Phase) Terminal() bool { return p == PhaseCompleted || p == PhaseFailed }

// Job is one §12.8 tenant-deletion lifecycle record. The controller
// persists it and resumes from Job.Phase after an interruption.
type Job struct {
	// TenantID is the tenant being deleted.
	TenantID string
	// WorkspaceTier is the tenant's §12.9 tier. A T4 tenant runs the
	// Phase 4a KMS-key destruction; any other tier skips it.
	WorkspaceTier string
	// State is the §12.8 TenantState the tenant currently carries.
	State TenantState
	// Phase is the §12.8 phase the controller will run next. After a
	// crash it is the resume point.
	Phase Phase
	// StartedAt is the instant Phase 1 began — the §12.8 deletion-SLA
	// clock start.
	StartedAt time.Time
	// UpdatedAt is the instant of the last phase transition.
	UpdatedAt time.Time
	// CompletedAt is set when Phase reaches completed or failed.
	CompletedAt time.Time
	// PhaseLog records the completion timestamp of each finished
	// phase, for the §12.8 Phase 6 erasure receipt.
	PhaseLog map[Phase]time.Time
	// DeletedCounts maps each store's name to its DeleteByTenant
	// deleted-row count, accumulated across Phase 4.
	DeletedCounts map[string]int
	// KMSKeyDestroyed records the §12.8 Phase 4a outcome: true once
	// the per-tenant KMS key has been destroyed (or confirmed absent).
	KMSKeyDestroyed bool
	// Receipt is the §12.8 Phase 6 erasure receipt, set once the
	// lifecycle completes.
	Receipt *Receipt
	// Failure carries the error reason when Phase is failed.
	Failure string
}

// Receipt is the §12.8 Phase 6 erasure receipt: the audit-trail proof
// that a tenant deletion was carried out.
type Receipt struct {
	// TenantID is the deleted tenant.
	TenantID string
	// PhaseTimestamps records each phase's completion instant.
	PhaseTimestamps map[Phase]time.Time
	// DeletedCounts is the per-store DeleteByTenant tally.
	DeletedCounts map[string]int
	// KMSKeyDestroyed records whether the §12.8 Phase 4a cryptographic
	// erasure ran.
	KMSKeyDestroyed bool
	// CompletedAt is the instant Phase 6 finished.
	CompletedAt time.Time
}

// Sentinel errors.
var (
	// ErrNotFound — no deletion job for the requested tenant.
	ErrNotFound = errors.New("tenantdeletion: job not found")
	// ErrAlreadyExists — a deletion job for the tenant already exists.
	ErrAlreadyExists = errors.New("tenantdeletion: job already exists")
	// ErrMissingTenantID — a job was created with an empty tenant id.
	ErrMissingTenantID = errors.New("tenantdeletion: tenant ID required")
)

// Store is the §12.8 tenant-deletion job registry. The controller
// persists the lifecycle phase here so a restart resumes from the
// recorded point. Every method is goroutine-safe.
type Store interface {
	// Create persists a fresh job. Returns ErrAlreadyExists when a job
	// for the tenant already exists.
	Create(ctx context.Context, j Job) error
	// Get returns the job for tenantID. Returns ErrNotFound when none
	// exists.
	Get(ctx context.Context, tenantID string) (Job, error)
	// Update applies mutate to a copy of the job and persists it.
	// Returns ErrNotFound when the job is missing.
	Update(ctx context.Context, tenantID string, mutate func(*Job) error) (Job, error)
	// ListActive returns every job that has not reached a terminal
	// phase, oldest first, so the controller reconciles in-progress
	// deletions FIFO.
	ListActive(ctx context.Context) ([]Job, error)
}

// Memory is the in-memory Store backing tests and the minimal
// deployment.
type Memory struct {
	mu   sync.RWMutex
	jobs map[string]Job
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{jobs: map[string]Job{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, j Job) error {
	if j.TenantID == "" {
		return ErrMissingTenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.TenantID]; ok {
		return ErrAlreadyExists
	}
	m.jobs[j.TenantID] = cloneJob(j)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID string) (Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[tenantID]
	if !ok {
		return Job{}, ErrNotFound
	}
	return cloneJob(j), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID string, mutate func(*Job) error) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[tenantID]
	if !ok {
		return Job{}, ErrNotFound
	}
	j = cloneJob(j)
	if err := mutate(&j); err != nil {
		return Job{}, err
	}
	m.jobs[tenantID] = cloneJob(j)
	return cloneJob(j), nil
}

// ListActive implements Store.
func (m *Memory) ListActive(_ context.Context) ([]Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Job
	for _, j := range m.jobs {
		if !j.Phase.Terminal() {
			out = append(out, cloneJob(j))
		}
	}
	// Oldest first so the controller reconciles deletions FIFO.
	sortJobsByStart(out)
	return out, nil
}

// cloneJob deep-copies the mutable maps and the Receipt pointer so a
// stored job and a returned copy never share state.
func cloneJob(j Job) Job {
	if j.PhaseLog != nil {
		pl := make(map[Phase]time.Time, len(j.PhaseLog))
		for k, v := range j.PhaseLog {
			pl[k] = v
		}
		j.PhaseLog = pl
	}
	if j.DeletedCounts != nil {
		dc := make(map[string]int, len(j.DeletedCounts))
		for k, v := range j.DeletedCounts {
			dc[k] = v
		}
		j.DeletedCounts = dc
	}
	if j.Receipt != nil {
		r := *j.Receipt
		if j.Receipt.PhaseTimestamps != nil {
			pt := make(map[Phase]time.Time, len(j.Receipt.PhaseTimestamps))
			for k, v := range j.Receipt.PhaseTimestamps {
				pt[k] = v
			}
			r.PhaseTimestamps = pt
		}
		if j.Receipt.DeletedCounts != nil {
			dc := make(map[string]int, len(j.Receipt.DeletedCounts))
			for k, v := range j.Receipt.DeletedCounts {
				dc[k] = v
			}
			r.DeletedCounts = dc
		}
		j.Receipt = &r
	}
	return j
}

// sortJobsByStart orders jobs oldest-StartedAt first. A simple
// insertion sort keeps the dependency surface minimal; an active job
// set is small.
func sortJobsByStart(jobs []Job) {
	for i := 1; i < len(jobs); i++ {
		for j := i; j > 0 && jobs[j].StartedAt.Before(jobs[j-1].StartedAt); j-- {
			jobs[j], jobs[j-1] = jobs[j-1], jobs[j]
		}
	}
}
