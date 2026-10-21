// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"sync"
	"time"
)

// AgeSink receives the §12.8 erasure-SLA gauges the Sampler publishes:
// each in-progress job's age and overdue seconds (per tenant and job),
// the removal of both series for a job no longer in progress, and the
// count of in-progress jobs. *gatewaymetrics.Metrics satisfies it.
type AgeSink interface {
	SetErasureJobAge(tenantID, jobID string, ageSeconds float64)
	// SetErasureJobOverdue publishes the job's age minus its deadline, so
	// a positive value means the job is past its deadline.
	SetErasureJobOverdue(tenantID, jobID string, seconds float64)
	// ClearErasureJobAge deletes both the age and the overdue series of
	// the job.
	ClearErasureJobAge(tenantID, jobID string)
	SetErasureJobsActive(n int)
}

// jobKey names one published (tenant, job) series pair.
type jobKey struct{ tenantID, jobID string }

// Sampler publishes the §12.8 erasure-SLA gauges on a periodic tick. The
// Runner cannot advance a job's age while it is blocked inside a slow
// DeleteByUser, so a separate sampler reads the job registry and
// republishes each in-progress job's age and overdue seconds. The §16.5
// ErasureJobOverdue alert fires when a job's overdue value is positive.
//
// ListActive returns in-progress jobs only, so the sampler remembers the
// jobs it published in the previous round and clears the series of every
// job absent from the current one, whichever replica finished it.
//
// spec: §12.8 (Erasure SLA enforcement).
type Sampler struct {
	jobs            Store
	sink            AgeSink
	defaultDeadline time.Duration
	clock           func() time.Time

	// mu guards published, the set of jobs whose series the previous
	// round published, and serializes each round's sink writes with
	// clearAll so no series is published after the final clear.
	mu        sync.Mutex
	published map[jobKey]struct{}
}

// NewSampler builds a Sampler over the job registry and the gauge sink.
// defaultDeadline is the SLA the overdue value is measured against for a
// job that recorded no Deadline of its own. A nil clock defaults to
// time.Now.
func NewSampler(jobs Store, sink AgeSink, defaultDeadline time.Duration, clock func() time.Time) *Sampler {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Sampler{
		jobs: jobs, sink: sink, defaultDeadline: defaultDeadline, clock: clock,
		published: map[jobKey]struct{}{},
	}
}

// Sample publishes one round of erasure-SLA gauges: the age and overdue
// seconds of every in-progress job, the in-progress count, and a cleared
// pair of series for every job published in the previous round that is no
// longer in progress. A ListActive error publishes nothing and keeps the
// previous round's set. It is safe to call on a ticker.
func (s *Sampler) Sample(ctx context.Context) error {
	jobs, err := s.jobs.ListActive(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	current := make(map[jobKey]struct{}, len(jobs))
	for _, j := range jobs {
		age := now.Sub(j.StartedAt)
		if age < 0 {
			age = 0
		}
		deadline := j.Deadline
		if deadline == 0 {
			deadline = s.defaultDeadline
		}
		s.sink.SetErasureJobAge(j.TenantID, j.ID, age.Seconds())
		s.sink.SetErasureJobOverdue(j.TenantID, j.ID, (age - deadline).Seconds())
		current[jobKey{j.TenantID, j.ID}] = struct{}{}
	}
	s.sink.SetErasureJobsActive(len(jobs))
	for k := range s.published {
		if _, ok := current[k]; !ok {
			s.sink.ClearErasureJobAge(k.tenantID, k.jobID)
		}
	}
	s.published = current
	return nil
}

// clearAll deletes every series the sampler published and sets the
// in-progress count to 0.
func (s *Sampler) clearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.published {
		s.sink.ClearErasureJobAge(k.tenantID, k.jobID)
	}
	s.published = map[jobKey]struct{}{}
	s.sink.SetErasureJobsActive(0)
}

// Run drives Sample on the given interval until ctx ends. When ctx ends,
// which on the leader gate is a lease loss or shutdown, Run clears every
// series it published and sets the in-progress count to 0, so a former
// leader leaves no stale series behind.
func (s *Sampler) Run(ctx context.Context, interval time.Duration) {
	defer s.clearAll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	_ = s.Sample(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Sample(ctx)
		}
	}
}
