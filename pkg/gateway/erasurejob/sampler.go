// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"time"
)

// AgeSink receives the §12.8 line 768 erasure-SLA gauges the Sampler
// publishes: the in-progress job age (per tenant + job), its removal on
// termination, and the platform deadline the §16.5 ErasureJobOverdue
// alert compares against. *gatewaymetrics.Metrics satisfies it via
// SetErasureJobAge / ClearErasureJobAge / SetErasureJobDeadlineSeconds.
type AgeSink interface {
	SetErasureJobAge(tenantID, jobID string, ageSeconds float64)
	ClearErasureJobAge(tenantID, jobID string)
	SetErasureJobDeadlineSeconds(seconds float64)
}

// Sampler publishes the §12.8 line 768 erasure-SLA gauges on a periodic
// tick. The Runner cannot advance a job's age while it is blocked inside
// a slow DeleteByUser, so a separate sampler reads the job registry and
// republishes each in-progress job's age. The §16.5 ErasureJobOverdue
// alert fires when a job's age exceeds the published deadline.
//
// spec: §12.8 line 768.
type Sampler struct {
	jobs            Store
	sink            AgeSink
	defaultDeadline time.Duration
	clock           func() time.Time
}

// NewSampler builds a Sampler over the job registry and the gauge sink.
// defaultDeadline is the platform SLA the §16.5 alert compares against
// (the §12.9 T3 72h bound is the common case; the per-job tier deadline
// is recorded on each Job for the receipt). A nil clock defaults to
// time.Now.
func NewSampler(jobs Store, sink AgeSink, defaultDeadline time.Duration, clock func() time.Time) *Sampler {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Sampler{jobs: jobs, sink: sink, defaultDeadline: defaultDeadline, clock: clock}
}

// Sample publishes one round of erasure-SLA gauges: the platform
// deadline, the age of every in-progress job, and a cleared age series
// for every terminal job (so a completed erasure no longer reads as
// in-progress). It is safe to call on a ticker.
func (s *Sampler) Sample(ctx context.Context) error {
	s.sink.SetErasureJobDeadlineSeconds(s.defaultDeadline.Seconds())
	jobs, err := s.jobs.List(ctx)
	if err != nil {
		return err
	}
	now := s.clock()
	for _, j := range jobs {
		if j.Phase.Terminal() {
			s.sink.ClearErasureJobAge(j.TenantID, j.ID)
			continue
		}
		age := now.Sub(j.StartedAt).Seconds()
		if age < 0 {
			age = 0
		}
		s.sink.SetErasureJobAge(j.TenantID, j.ID, age)
	}
	return nil
}

// Run drives Sample on the given interval until ctx is cancelled. The
// gateway runs it in a background goroutine for the process lifetime.
func (s *Sampler) Run(ctx context.Context, interval time.Duration) {
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
