// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
)

// Runner executes §12.8 erasure jobs: it records a job, then drives it
// through its phases while the erasure.Orchestrator deletes the target
// user's data from every store.
//
// The runner drives the store-deletion core of the §12.8 sequence
// (initiated → store_deleting). When a BillingEraser is attached via
// WithBilling, it then drives the pseudonymizing and verifying phases:
// the target user's billing events are pseudonymized rather than
// deleted, and the §12.8 post-pseudonymization check confirms the
// erasure is effective. Without a BillingEraser the job completes at
// store deletion.
type Runner struct {
	jobs            Store
	erase           *erasure.Orchestrator
	billing         *BillingEraser
	memoryPreflight func(context.Context) error
	onFailure       func(tenantID, failurePhase string)
	clock           func() time.Time
}

// FailurePhase labels for the §12.8 CMP-026 lenny_erasure_job_failed_total
// counter. The MemoryStore erasure preflight (§12.8 lines 743-758) adds
// PhaseMemoryStorePreflight to the store_delete / pseudonymization /
// verification phases the spec enumerates.
const (
	FailurePhaseMemoryStorePreflight = "memory_store_preflight"
	FailurePhaseStoreDelete          = "store_delete"
	FailurePhasePseudonymization     = "pseudonymization"
	FailurePhaseVerification         = "verification"
)

// errMemoryStorePreflight is the §12.8 abort cause recorded when the
// per-job MemoryStore erasure preflight fails. The job aborts before any
// store deletion and the target user's processing_restricted flag is left
// set (the admin handler clears it only on PhaseCompleted).
var errMemoryStorePreflight = errors.New("memory_store_preflight_failed")

// NewRunner builds a Runner over the job registry and the erasure
// orchestrator. Pass nil for clock to default to time.Now.
func NewRunner(jobs Store, orch *erasure.Orchestrator, clock func() time.Time) *Runner {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{jobs: jobs, erase: orch, clock: clock}
}

// WithBilling attaches the §12.8 BillingEraser so Run drives the
// pseudonymizing and verifying phases after store deletion. It returns
// the Runner for call chaining.
func (r *Runner) WithBilling(b *BillingEraser) *Runner {
	r.billing = b
	return r
}

// WithMemoryPreflight attaches the §12.8 per-job MemoryStore erasure
// preflight (lines 743-758, defense-in-depth layer 3). The check re-runs
// the seeded-row write/erase/requery cycle at job start, before any store
// deletion; a backend that passed the startup preflight but later
// regressed (e.g., after a rolling upgrade of an external vector DB)
// aborts the job here rather than reporting a successful erasure while
// memories persist. Pass memorystore.ValidateMemoryStoreErasure bound to
// the wired store. A nil preflight disables the per-job check. Returns
// the Runner for call chaining.
func (r *Runner) WithMemoryPreflight(preflight func(context.Context) error) *Runner {
	r.memoryPreflight = preflight
	return r
}

// WithFailureObserver attaches the §12.8 CMP-026 failure counter hook,
// invoked with the tenant id and failure phase whenever a job transitions
// to PhaseFailed. Pass gatewaymetrics.Metrics.IncErasureJobFailed. A nil
// observer disables emission. Returns the Runner for call chaining.
func (r *Runner) WithFailureObserver(obs func(tenantID, failurePhase string)) *Runner {
	r.onFailure = obs
	return r
}

// errBillingVerification is the failure cause recorded when the §12.8
// post-pseudonymization check does not pass.
var errBillingVerification = errors.New(
	"billing erasure verification failed: the erasure salt or the original user id survived pseudonymization",
)

// setPhase returns a job mutator that sets the lifecycle phase.
func setPhase(p Phase) func(*Job) error {
	return func(j *Job) error {
		j.Phase = p
		return nil
	}
}

// recordBilling returns a job mutator that records the §12.8 billing
// erasure outcome on the job.
func recordBilling(o BillingErasureOutcome) func(*Job) {
	return func(j *Job) { j.Billing = o }
}

// fail transitions the job to PhaseFailed with cause as the recorded
// failure reason and emits the §12.8 CMP-026 failure counter under
// failurePhase. extra, when non-nil, records any partial result first. It
// returns cause so a synchronous caller observes the failure; a
// registry-update error preempts it.
func (r *Runner) fail(ctx context.Context, jobID, failurePhase string, cause error, extra func(*Job)) error {
	job, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		if extra != nil {
			extra(j)
		}
		j.Phase = PhaseFailed
		j.Failure = cause.Error()
		j.CompletedAt = r.clock()
		return nil
	})
	if err != nil {
		return err
	}
	if r.onFailure != nil {
		r.onFailure(job.TenantID, failurePhase)
	}
	return cause
}

// Start records a fresh erasure job for the user in PhaseInitiated and
// returns its id. The job is not executed; the caller invokes Run —
// typically in a goroutine so the §12.8 admin API returns the job id
// immediately while erasure proceeds in the background.
func (r *Runner) Start(ctx context.Context, tenantID, userID string) (string, error) {
	id := NewID()
	if err := r.jobs.Create(ctx, Job{
		ID:        id,
		TenantID:  tenantID,
		UserID:    userID,
		Phase:     PhaseInitiated,
		StartedAt: r.clock(),
	}); err != nil {
		return "", err
	}
	return id, nil
}

// Run executes the erasure job to completion. It transitions the job
// initiated → store_deleting, recording the per-store deleted counts
// the orchestrator reports. When a BillingEraser is attached it then
// drives pseudonymizing → verifying — pseudonymizing the target user's
// billing events and confirming the §12.8 post-erasure check — before
// the job reaches completed.
//
// Run is fail-fast: a store error, a pseudonymization error, or a
// failed verification transitions the job to failed with the partial
// result preserved, matching the §12.8 contract so a retry resumes
// from a consistent point. A job already in a terminal phase is left
// untouched, so a crash-recovery re-run is idempotent.
//
// Run returns the failure cause (if any) for a synchronous caller; the
// job record is the authoritative outcome.
func (r *Runner) Run(ctx context.Context, jobID string) error {
	job, err := r.jobs.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Phase.Terminal() {
		return nil
	}

	// §12.8 per-job MemoryStore erasure preflight (defense-in-depth layer
	// 3): re-run the seeded-row check before step 8 (store deletion). On
	// failure the job aborts as memory_store_preflight_failed before any
	// data is touched, and the target user's processing_restricted flag is
	// left set (the admin handler clears it only on PhaseCompleted).
	if r.memoryPreflight != nil {
		if err := r.memoryPreflight(ctx); err != nil {
			return r.fail(ctx, jobID, FailurePhaseMemoryStorePreflight,
				fmt.Errorf("%w: %v", errMemoryStorePreflight, err), nil)
		}
	}

	// Store deletion.
	if _, err := r.jobs.Update(ctx, jobID, setPhase(PhaseStoreDeleting)); err != nil {
		return err
	}
	res, eraseErr := r.erase.DeleteByUser(ctx, job.TenantID, job.UserID)
	if _, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		j.Deleted = res.Deleted
		j.Total = res.Total
		return nil
	}); err != nil {
		return err
	}
	if eraseErr != nil {
		return r.fail(ctx, jobID, FailurePhaseStoreDelete, eraseErr, nil)
	}

	// §12.8 billing-event pseudonymization, when a BillingEraser is
	// attached: billing events are pseudonymized rather than deleted,
	// then the post-pseudonymization check confirms the erasure.
	billing := BillingErasureOutcome{}
	if r.billing != nil {
		if _, err := r.jobs.Update(ctx, jobID, setPhase(PhasePseudonymizing)); err != nil {
			return err
		}
		billing, err = r.billing.Pseudonymize(ctx, job.TenantID, job.UserID)
		if err != nil {
			return r.fail(ctx, jobID, FailurePhasePseudonymization, err, nil)
		}
		if billing.Disposition != billingExempt {
			if _, err := r.jobs.Update(ctx, jobID, setPhase(PhaseVerifying)); err != nil {
				return err
			}
			verified, err := r.billing.Verify(ctx, job.TenantID, job.UserID)
			if err != nil {
				return r.fail(ctx, jobID, FailurePhaseVerification, err, recordBilling(billing))
			}
			billing.Verified = verified
			if !verified {
				return r.fail(ctx, jobID, FailurePhaseVerification, errBillingVerification, recordBilling(billing))
			}
		}
	}

	// Completed.
	if _, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		j.Billing = billing
		j.Phase = PhaseCompleted
		j.CompletedAt = r.clock()
		return nil
	}); err != nil {
		return err
	}
	return nil
}
