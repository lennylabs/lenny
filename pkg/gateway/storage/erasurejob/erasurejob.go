// SPDX-License-Identifier: MIT

// Package erasurejob tracks §12.8 GDPR erasure jobs: the DeleteByUser
// deletion job initiated by POST /v1/admin/users/{userId}/erase and
// queried via GET /v1/admin/erasure-jobs/{jobId}.
//
// A Job records the §12.8 phase field so a controller restart resumes
// from the persisted safe point rather than re-running completed
// steps. The Runner (runner.go) drives a job through its phases while
// the erasure.Orchestrator deletes the user's data from every store.
package erasurejob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Phase is the §12.8 erasure-job lifecycle state. It is persisted with
// every step transition so a controller restart resumes from the safe
// point: a job in store_deleting re-runs the idempotent DeleteByUser
// sequence, and a job in a terminal phase does no further work.
type Phase string

const (
	// PhaseInitiated — the job record exists; no store deletion has run.
	PhaseInitiated Phase = "initiated"
	// PhaseStoreDeleting — the dependency-ordered store deletion is in
	// progress.
	PhaseStoreDeleting Phase = "store_deleting"
	// PhasePseudonymizing — billing events are being pseudonymized.
	PhasePseudonymizing Phase = "pseudonymizing"
	// PhaseVerifying — the post-deletion salt-removal check is running.
	PhaseVerifying Phase = "verifying"
	// PhaseCompleted — every step finished and the erasure receipt is
	// written.
	PhaseCompleted Phase = "completed"
	// PhaseFailed — a step errored; Job.Failure carries the reason and
	// the job may be retried.
	PhaseFailed Phase = "failed"
)

// Terminal reports whether p is an end state. A job in a terminal
// phase does no further work, so re-running it is a no-op.
func (p Phase) Terminal() bool {
	return p == PhaseCompleted || p == PhaseFailed
}

// PhaseTransition is one entry in a Job's PhaseLog: the lifecycle phase
// the job entered and the instant it entered it. spec: §12.8
// (each erasure job record persists its phase).
type PhaseTransition struct {
	Phase Phase
	At    time.Time
}

// Job is one §12.8 DeleteByUser erasure job.
type Job struct {
	// ID is the opaque job identifier returned to the admin caller.
	ID string
	// TenantID + UserID identify the erasure target.
	TenantID string
	UserID   string
	// Phase is the current §12.8 lifecycle state.
	Phase Phase
	// StartedAt is the instant the job record was created. A restart
	// keeps it, so the overdue sampler measures from the original start.
	StartedAt time.Time
	// Deadline is the §12.8 tier-specific SLA window the job
	// must complete within (T3 72h, T4 1h). Recorded at Start so the
	// overdue sampler and the completion receipt can present it. Zero
	// when no deadline resolver is wired into the Runner, in which case
	// the sampler measures against its default deadline.
	Deadline time.Duration
	// CompletedAt is set when Phase becomes completed or failed.
	CompletedAt time.Time
	// Deleted maps each store's name to its deleted-row count, for the
	// stores the job has erased so far.
	Deleted map[string]int
	// Total is the sum of Deleted across stores.
	Total int
	// Billing records the §12.8 billing-pseudonymization outcome: the
	// disposition, the count of events rewritten, and whether the
	// post-erasure verification passed. Zero value when the job has
	// not reached the pseudonymizing phase or no BillingEraser is
	// wired into the Runner.
	Billing BillingErasureOutcome
	// DeadLetterRedacted is the number of dead-lettered audit_log rows the
	// §12.8 step-14 OCSF dead-letter PII redaction scrubbed for the target
	// user. Zero when no dead-lettered rows named the user or no redaction
	// hook is wired into the Runner. spec: §12.8.
	DeadLetterRedacted int
	// PhaseLog records every §12.8 lifecycle transition in order with
	// its timestamp, so the completion receipt presents the per-phase
	// timeline a compliance auditor uses to reconstruct the erasure
	// sequence. Its length also identifies the attempt a writer read
	// (SameAttempt). spec: §12.8.
	PhaseLog []PhaseTransition
	// Failure carries the error reason when Phase is failed.
	Failure string
	// InitiatedBy is the subject of the principal that requested the
	// erasure. The completion receipt attributes the erasure to it.
	InitiatedBy string
	// OverrideReceipt carries the §12.8 legal-hold override fields the
	// erase request recorded, or is nil when no override was exercised.
	// It is persisted with the job so the completion receipt, written on
	// whichever replica finishes the job, still records the override.
	OverrideReceipt map[string]any
	// ErasedAt is the instant the job recorded the success of its last
	// erasure step. A job with ErasedAt set has only its receipt and
	// completion left.
	ErasedAt time.Time
	// ReceiptAt is the completion instant chosen once before the
	// completion receipt is written, so a receipt written twice is
	// byte-identical.
	ReceiptAt time.Time
}

// AllTenants is the tenant argument of Get that reads a job in any
// tenant. It is the §4.2 all-tenants sentinel; a background worker or a
// platform-admin path passes it, and every other caller passes its own
// tenant so another tenant's job reads as ErrNotFound.
const AllTenants = pgtenant.AllTenantsSentinel

// Sentinel errors.
var (
	// ErrNotFound — no job with the requested id exists in the tenant
	// the caller may read.
	ErrNotFound = errors.New("erasurejob: job not found")
	// ErrAlreadyExists — a job with the same id already exists.
	ErrAlreadyExists = errors.New("erasurejob: job already exists")
	// ErrMissingID — Start was called with an empty job id.
	ErrMissingID = errors.New("erasurejob: job ID required")
	// ErrInvalidTenant — Start was called with an empty tenant or the
	// all-tenants sentinel. A job is created only under its own tenant.
	ErrInvalidTenant = errors.New("erasurejob: a concrete tenant ID is required")
	// ErrNotFailed — Restart or ClearRestriction found the job in a
	// phase other than failed. Only a failed job is retried or has its
	// restriction cleared by an operator. The stores return it wrapped in
	// *NotFailedError, which carries the phase read under the job's lock;
	// match it with errors.Is.
	ErrNotFailed = errors.New("erasurejob: job is not failed")
	// ErrJobMoved — the stored job is no longer the attempt the writer
	// read (SameAttempt failed), so the write was refused and nothing
	// changed.
	ErrJobMoved = errors.New("erasurejob: job moved since it was read")
	// ErrRestrictionWriterUnwired — a Memory store with no restriction
	// writer refused a write that sets or clears the user's
	// processing_restricted flag together with the job. The job and the
	// flag are one write, so the store writes neither.
	ErrRestrictionWriterUnwired = errors.New("erasurejob: no processing-restriction writer is wired")
)

// ErrActiveJob reports that the user already has an erasure job in a
// non-terminal phase. Job is that in-progress job. Start and Restart
// return it and write nothing, because §12.8 allows one in-progress job
// per user.
//
// spec: §12.8 (User-level erasure invocation).
type ErrActiveJob struct {
	Job Job
}

// Error implements error.
func (e *ErrActiveJob) Error() string {
	return "erasurejob: user " + e.Job.UserID + " already has erasure job " + e.Job.ID + " in progress"
}

// NotFailedError is the refusal Restart and ClearRestriction return when
// the job, read under the write's own lock, is not failed. Phase is that
// locked read, so a caller that reports the phase names the one the store
// refused on rather than an earlier read taken outside the lock. It
// satisfies errors.Is(err, ErrNotFailed).
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §15.1 (REST API).
type NotFailedError struct {
	Phase Phase
}

// Error implements error.
func (e *NotFailedError) Error() string {
	return ErrNotFailed.Error() + " (phase " + string(e.Phase) + ")"
}

// Unwrap returns ErrNotFailed so errors.Is matches the sentinel.
func (e *NotFailedError) Unwrap() error { return ErrNotFailed }

// SameAttempt reports whether stored is still the job read was taken
// from: equal Phase and equal len(PhaseLog). The PhaseLog length refuses a
// writer that read `initiated` before a `fail` and a `Restart`, which a
// Phase comparison alone admits.
//
// spec: §12.8 (Erasure job phase field (crash recovery)).
func SameAttempt(stored, read Job) bool {
	return stored.Phase == read.Phase && len(stored.PhaseLog) == len(read.PhaseLog)
}

// Store is the erasure-job registry contract. Every method is
// goroutine-safe. The methods that change the user's processing_restricted
// flag write the job and the flag in one transaction, so no job runs
// without its restriction and no restriction outlives its job unnoticed.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Erasure job phase field (crash recovery)).
type Store interface {
	// Start inserts j and sets the user's processing_restricted flag,
	// naming j, in one transaction. When the user already has a job in
	// progress it writes nothing and returns *ErrActiveJob carrying that
	// job. A missing user row returns userstore.ErrNotFound and writes
	// nothing.
	Start(ctx context.Context, j Job) (Job, error)
	// Get returns the job whose ID equals id. tenantID scopes the read:
	// AllTenants reads any tenant, and any other value answers
	// ErrNotFound for another tenant's job.
	Get(ctx context.Context, tenantID, id string) (Job, error)
	// Update applies mutate to the job as read inside the write's own
	// transaction (the row locked FOR UPDATE on Postgres) and writes
	// nothing when mutate returns an error. Returns ErrNotFound when the
	// job is missing.
	Update(ctx context.Context, id string, mutate func(*Job) error) (Job, error)
	// Restart moves a failed job back to initiated, clears Failure,
	// CompletedAt, ErasedAt, ReceiptAt, and Billing, appends initiated to
	// PhaseLog, and sets the user's flag naming the job, in one
	// transaction. It returns *NotFailedError when the job is not failed and
	// *ErrActiveJob when another job for the user is in progress.
	Restart(ctx context.Context, id string) (Job, error)
	// ClearRestriction is the operator clear of a failed job's
	// restriction. It returns *NotFailedError when the job is not failed and
	// userstore.ErrRestrictionHeldByOtherJob when the user's
	// erasure_job_id names another job, and writes nothing in either case.
	ClearRestriction(ctx context.Context, id string) (Job, error)
	// Complete marks the job completed at completedAt and clears the
	// user's restriction where erasure_job_id equals id, in one
	// transaction. A restriction already cleared or replaced by a newer
	// job is left as it is, and the job still completes. It returns
	// ErrJobMoved and writes nothing unless SameAttempt(stored, expect).
	Complete(ctx context.Context, id string, completedAt time.Time, expect Job) (Job, error)
	// ListActive returns every job in a non-terminal phase, oldest
	// StartedAt first.
	ListActive(ctx context.Context) ([]Job, error)
}

// RestrictionWriter is the processing_restricted write path the Memory
// store calls inside its own lock, so a job write and its flag write are
// one step. *userstore.Memory satisfies it. SetErasureRestriction has the
// semantics of the Postgres store's SetErasureRestrictionTx, and
// ClearProcessingRestrictionForJob those of ClearProcessingRestrictionTx.
type RestrictionWriter interface {
	SetErasureRestriction(ctx context.Context, tenantID, subject, jobID string, restricted bool, requireJobID string) (int64, error)
	ClearProcessingRestrictionForJob(ctx context.Context, tenantID, subject, jobID string) error
}

// NewID returns a fresh erasure-job identifier — 8 random bytes
// hex-encoded with an `erasure_` prefix.
func NewID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "erasure_" + hex.EncodeToString(buf[:])
}
