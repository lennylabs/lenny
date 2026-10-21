// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// spec: §12.8 — erasure throughput / SLA metrics.

// fakeLifecycle records the §12.8 lifecycle metric calls.
type fakeLifecycle struct {
	inc, dec  int
	durations []float64
}

func (f *fakeLifecycle) IncErasureJobsActive()               { f.inc++ }
func (f *fakeLifecycle) DecErasureJobsActive()               { f.dec++ }
func (f *fakeLifecycle) ObserveErasureJobDuration(s float64) { f.durations = append(f.durations, s) }

func TestRunnerLifecycleMetrics(t *testing.T) {
	jobs := newJobStore(t)
	start := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	// Clock advances 12s between Start and the duration observation.
	end := start.Add(12 * time.Second)
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return start // Start
		}
		return end // every subsequent read (phase transitions + duration)
	}
	lc := &fakeLifecycle{}
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	r := erasurejob.NewRunner(jobs, orch, clock).WithLifecycleMetrics(lc)

	id, err := r.Start(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lc.inc != 1 || lc.dec != 1 {
		t.Errorf("active bracket = inc %d / dec %d, want 1/1", lc.inc, lc.dec)
	}
	if len(lc.durations) != 1 || lc.durations[0] != 12 {
		t.Errorf("duration observations = %v, want one observation of 12s", lc.durations)
	}
}

// A failed job still brackets the active gauge and observes a duration.
func TestRunnerLifecycleMetricsOnFailure(t *testing.T) {
	jobs := newJobStore(t)
	at := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	lc := &fakeLifecycle{}
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name:         "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) { return 0, context.DeadlineExceeded },
	}}})
	r := erasurejob.NewRunner(jobs, orch, fixedClock(at)).WithLifecycleMetrics(lc)

	id, _ := r.Start(context.Background(), "acme", "alice")
	_ = r.Run(context.Background(), id)
	if lc.inc != 1 || lc.dec != 1 || len(lc.durations) != 1 {
		t.Errorf("failed job metrics = inc %d / dec %d / durations %v, want 1/1/one", lc.inc, lc.dec, lc.durations)
	}
}

func TestRunnerRecordsDeadline(t *testing.T) {
	jobs := newJobStore(t)
	at := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), fixedClock(at)).
		WithDeadlineResolver(func(_ context.Context, tenantID string) time.Duration {
			if tenantID == "regulated" {
				return time.Hour // T4
			}
			return 72 * time.Hour
		})

	id, _ := r.Start(context.Background(), "regulated", "alice")
	job, _ := jobs.Get(context.Background(), erasurejob.AllTenants, id)
	if job.Deadline != time.Hour {
		t.Errorf("Deadline = %v, want 1h (T4)", job.Deadline)
	}
}

// fakeAgeSink records the §12.8 SLA gauge calls. ClearErasureJobAge
// deletes both of the job's series, as gatewaymetrics does.
type fakeAgeSink struct {
	mu       sync.Mutex
	ages     map[string]float64
	overdue  map[string]float64
	cleared  map[string]int
	active   int
	activeOK bool
}

func newFakeAgeSink() *fakeAgeSink {
	return &fakeAgeSink{ages: map[string]float64{}, overdue: map[string]float64{}, cleared: map[string]int{}}
}

func (f *fakeAgeSink) SetErasureJobAge(_, jobID string, age float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ages[jobID] = age
}

func (f *fakeAgeSink) SetErasureJobOverdue(_, jobID string, s float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overdue[jobID] = s
}

func (f *fakeAgeSink) ClearErasureJobAge(_, jobID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ages, jobID)
	delete(f.overdue, jobID)
	f.cleared[jobID]++
}

func (f *fakeAgeSink) SetErasureJobsActive(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active, f.activeOK = n, true
}

// series reports whether the job has an age or an overdue series.
func (f *fakeAgeSink) series(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, a := f.ages[jobID]
	_, o := f.overdue[jobID]
	return a || o
}

// TestSamplerPublishesAndClearsBothSeries_spec_12_8 pins the sampler: each
// in-progress job publishes its age and its overdue value (age minus its
// own deadline, or the default deadline when it recorded none), the
// active-jobs gauge is the ListActive count, and a job that leaves
// ListActive between rounds, whether this replica completed it or another
// finished it, has both series cleared on the next sample.
// spec: 12.8 (Compliance Interfaces: Erasure SLA enforcement), 16.5 (Alerting Rules and SLOs)
func TestSamplerPublishesAndClearsBothSeries_spec_12_8(t *testing.T) {
	ctx := context.Background()
	now := jobAt.Add(2 * time.Hour)
	jobs := newJobStore(t)
	t4 := newJob("j_t4", "acme", "alice")
	t4.Deadline = time.Hour
	t3 := newJob("j_t3", "globex", "alice")
	t3.Deadline = 72 * time.Hour
	nodeadline := newJob("j_default", "regulated", "alice")
	for _, j := range []erasurejob.Job{t4, t3, nodeadline} {
		startJob(t, jobs, j)
	}

	sink := newFakeAgeSink()
	s := erasurejob.NewSampler(jobs, sink, 3*time.Hour, func() time.Time { return now })
	if err := s.Sample(ctx); err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if sink.ages["j_t4"] != 7200 {
		t.Fatalf("age = %v, want 7200", sink.ages["j_t4"])
	}
	if sink.overdue["j_t4"] != 3600 {
		t.Fatalf("1h-deadline job at 2h overdue = %v, want +3600", sink.overdue["j_t4"])
	}
	if want := (2*time.Hour - 72*time.Hour).Seconds(); sink.overdue["j_t3"] != want {
		t.Fatalf("72h-deadline job at 2h overdue = %v, want %v", sink.overdue["j_t3"], want)
	}
	if sink.overdue["j_default"] != -3600 {
		t.Fatalf("default-deadline job overdue = %v, want -3600", sink.overdue["j_default"])
	}
	if !sink.activeOK || sink.active != 3 {
		t.Fatalf("active = %d (set %v), want 3", sink.active, sink.activeOK)
	}

	// j_t4 completes here; j_t3 is finished by another replica's runner,
	// which this sampler sees only as its absence from ListActive.
	j, _ := jobs.Get(ctx, erasurejob.AllTenants, "j_t4")
	if _, err := jobs.Complete(ctx, "j_t4", now, j); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	failJob(t, jobs, "j_t3")
	if err := s.Sample(ctx); err != nil {
		t.Fatalf("second Sample: %v", err)
	}
	for _, id := range []string{"j_t4", "j_t3"} {
		if sink.series(id) {
			t.Errorf("%s still publishes a series after leaving ListActive", id)
		}
		if sink.cleared[id] != 1 {
			t.Errorf("%s cleared %d times, want 1", id, sink.cleared[id])
		}
	}
	if !sink.series("j_default") || sink.active != 1 {
		t.Fatalf("remaining job series %v, active %d, want published and 1", sink.series("j_default"), sink.active)
	}
	if sink.cleared["j_default"] != 0 {
		t.Fatal("an in-progress job's series was cleared")
	}
}

// failingLister is a Store whose ListActive fails.
type failingLister struct{ erasurejob.Store }

func (failingLister) ListActive(context.Context) ([]erasurejob.Job, error) {
	return nil, errors.New("primary unavailable")
}

// TestSamplerListErrorPublishesNothing_spec_12_8 pins that a failed read
// publishes no series and no active count.
// spec: 12.8 (Compliance Interfaces: Erasure SLA enforcement)
func TestSamplerListErrorPublishesNothing_spec_12_8(t *testing.T) {
	sink := newFakeAgeSink()
	s := erasurejob.NewSampler(failingLister{}, sink, time.Hour, nil)
	if err := s.Sample(context.Background()); err == nil {
		t.Fatal("Sample over a failing ListActive returned nil")
	}
	if sink.activeOK || len(sink.ages) != 0 {
		t.Fatalf("a failed read published: active set %v, ages %v", sink.activeOK, sink.ages)
	}
}

// TestSamplerRunClearsOnContextEnd_spec_12_8 pins the lease-loss and
// shutdown outcome: when the context of Run ends, every series the sampler
// published is cleared and the active-jobs gauge reads 0, so a former
// leader leaves no stale series behind.
// spec: 12.8 (Compliance Interfaces: Erasure SLA enforcement), 16.5 (Alerting Rules and SLOs)
func TestSamplerRunClearsOnContextEnd_spec_12_8(t *testing.T) {
	jobs := newJobStore(t)
	startJob(t, jobs, newJob("j_a", "acme", "alice"))
	startJob(t, jobs, newJob("j_b", "globex", "alice"))
	sink := newFakeAgeSink()
	s := erasurejob.NewSampler(jobs, sink, time.Hour, func() time.Time { return jobAt.Add(time.Minute) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, time.Hour)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !sink.series("j_a") || !sink.series("j_b") {
		if time.Now().After(deadline) {
			t.Fatal("Run did not publish the first round")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if sink.series("j_a") || sink.series("j_b") {
		t.Fatal("Run left a series behind after its context ended")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.active != 0 {
		t.Fatalf("active = %d after Run ended, want 0", sink.active)
	}
}
