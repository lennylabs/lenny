// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
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
	jobs    Store
	erase   *erasure.Orchestrator
	billing *BillingEraser
	clock   func() time.Time
}

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
// failure reason. extra, when non-nil, records any partial result
// first. It returns cause so a synchronous caller observes the
// failure; a registry-update error preempts it.
func (r *Runner) fail(ctx context.Context, jobID string, cause error, extra func(*Job)) error {
	if _, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		if extra != nil {
			extra(j)
		}
		j.Phase = PhaseFailed
		j.Failure = cause.Error()
		j.CompletedAt = r.clock()
		return nil
	}); err != nil {
		return err
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
		return r.fail(ctx, jobID, eraseErr, nil)
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
			return r.fail(ctx, jobID, err, nil)
		}
		if billing.Disposition != billingExempt {
			if _, err := r.jobs.Update(ctx, jobID, setPhase(PhaseVerifying)); err != nil {
				return err
			}
			verified, err := r.billing.Verify(ctx, job.TenantID, job.UserID)
			if err != nil {
				return r.fail(ctx, jobID, err, recordBilling(billing))
			}
			billing.Verified = verified
			if !verified {
				return r.fail(ctx, jobID, errBillingVerification, recordBilling(billing))
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
