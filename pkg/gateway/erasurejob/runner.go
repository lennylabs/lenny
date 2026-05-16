// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
)

// Runner executes §12.8 erasure jobs: it records a job, then drives it
// through its phases while the erasure.Orchestrator deletes the target
// user's data from every store.
//
// The v1 runner covers the store-deletion core of the §12.8 sequence
// (initiated → store_deleting → completed, or failed). The
// pseudonymizing and verifying phases belong to billing-event
// pseudonymization, which depends on the per-tenant erasure_salt and
// KMS unwrap path; the runner leaves those phases to the salt-aware
// erasure controller.
type Runner struct {
	jobs  Store
	erase *erasure.Orchestrator
	clock func() time.Time
}

// NewRunner builds a Runner over the job registry and the erasure
// orchestrator. Pass nil for clock to default to time.Now.
func NewRunner(jobs Store, orch *erasure.Orchestrator, clock func() time.Time) *Runner {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{jobs: jobs, erase: orch, clock: clock}
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
// initiated → store_deleting → completed, recording the per-store
// deleted counts the orchestrator reports. A store error transitions
// the job to failed with the orchestrator's partial result preserved,
// matching the §12.8 fail-fast contract so a retry resumes from a
// consistent point. A job already in a terminal phase is left
// untouched, so a crash-recovery re-run is idempotent.
//
// Run returns the erasure error (if any) for the benefit of a
// synchronous caller; the job record is the authoritative outcome.
func (r *Runner) Run(ctx context.Context, jobID string) error {
	job, err := r.jobs.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Phase.Terminal() {
		return nil
	}
	if _, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		j.Phase = PhaseStoreDeleting
		return nil
	}); err != nil {
		return err
	}

	res, eraseErr := r.erase.DeleteByUser(ctx, job.TenantID, job.UserID)

	if _, err := r.jobs.Update(ctx, jobID, func(j *Job) error {
		j.Deleted = res.Deleted
		j.Total = res.Total
		j.CompletedAt = r.clock()
		if eraseErr != nil {
			j.Phase = PhaseFailed
			j.Failure = eraseErr.Error()
			return nil
		}
		j.Phase = PhaseCompleted
		return nil
	}); err != nil {
		return err
	}
	return eraseErr
}
