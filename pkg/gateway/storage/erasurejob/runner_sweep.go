// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RunPending lists every in-progress job and, oldest StartedAt first,
// starts a run in its own goroutine for each job the runner admits, then
// returns when those runs return. The runner admits a job only when it is
// not already running here, no other job of its tenant is running here, and
// fewer than the WithMaxConcurrentJobs limit are running across overlapping
// calls. A listed job the runner does not admit waits for a later call. One
// tenant's backlog therefore cannot hold every slot.
//
// After its all-tenants ListActive read RunPending records one
// cross_tenant_read event, including for an empty read; an emit error
// starts no run and is returned.
//
// spec: §12.8 (Erasure job phase field (crash recovery)), §12.3 (Postgres
// HA Requirements).
func (r *Runner) RunPending(ctx context.Context) error {
	jobs, err := r.jobs.ListActive(ctx)
	if err != nil {
		return err
	}
	if r.crossTenantRead != nil {
		if err := r.crossTenantRead(ctx, CrossTenantReadEndpointRunner, "", len(jobs)); err != nil {
			return err
		}
	}
	SortByStart(jobs)
	var wg sync.WaitGroup
	for _, j := range jobs {
		if !r.admit(j) {
			continue
		}
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			defer r.release(j.ID)
			r.runLogged(ctx, j)
		}(j)
	}
	wg.Wait()
	return nil
}

// runLogged runs one job and logs an error that left it in progress or
// failed it. A job another executor moved is not an error.
func (r *Runner) runLogged(ctx context.Context, j Job) {
	err := r.Run(ctx, j.ID)
	if err == nil || errors.Is(err, ErrJobMoved) {
		return
	}
	r.logger.WarnContext(ctx, "erasure job run did not complete",
		"job_id", j.ID, "tenant_id", j.TenantID, "error", err)
}

// admit records j in the in-flight map when the admission rules allow it.
func (r *Runner) admit(j Job) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.inFlight) >= r.maxConcurrent {
		return false
	}
	if _, running := r.inFlight[j.ID]; running {
		return false
	}
	for _, tenant := range r.inFlight {
		if tenant == j.TenantID {
			return false
		}
	}
	r.inFlight[j.ID] = j.TenantID
	return true
}

// release removes a job from the in-flight map once its run returned.
func (r *Runner) release(jobID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.inFlight, jobID)
}

// Poll starts RunPending in a new goroutine at once and then every
// interval until ctx ends. A tick does not wait for the previous
// RunPending, so a long-running job does not delay the start of another
// tenant's job; the admission rules keep a job from running twice. On the
// gateway-leader gate ctx is the leader context, which ends on lease loss
// or shutdown.
//
// spec: §12.5 (lenny-gateway-leader gate), §12.8 (Erasure job phase field
// (crash recovery)).
func (r *Runner) Poll(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	tick := func() {
		go func() {
			if err := r.RunPending(ctx); err != nil && ctx.Err() == nil {
				r.logger.WarnContext(ctx, "erasure job sweep failed", "error", err)
			}
		}()
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
