// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
)

// spec: §12.8 line 768 — erasure throughput / SLA metrics.

// fakeLifecycle records the §12.8 line 768 lifecycle metric calls.
type fakeLifecycle struct {
	inc, dec  int
	durations []float64
}

func (f *fakeLifecycle) IncErasureJobsActive()               { f.inc++ }
func (f *fakeLifecycle) DecErasureJobsActive()               { f.dec++ }
func (f *fakeLifecycle) ObserveErasureJobDuration(s float64) { f.durations = append(f.durations, s) }

func TestRunnerLifecycleMetrics(t *testing.T) {
	jobs := erasurejob.NewMemory()
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
	jobs := erasurejob.NewMemory()
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
	jobs := erasurejob.NewMemory()
	at := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	r := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), fixedClock(at)).
		WithDeadlineResolver(func(_ context.Context, tenantID string) time.Duration {
			if tenantID == "regulated" {
				return time.Hour // T4
			}
			return 72 * time.Hour
		})

	id, _ := r.Start(context.Background(), "regulated", "alice")
	job, _ := jobs.Get(context.Background(), id)
	if job.Deadline != time.Hour {
		t.Errorf("Deadline = %v, want 1h (T4)", job.Deadline)
	}
}

// fakeAgeSink records the §12.8 line 768 SLA gauge calls.
type fakeAgeSink struct {
	ages     map[string]float64
	cleared  map[string]bool
	deadline float64
}

func newFakeAgeSink() *fakeAgeSink {
	return &fakeAgeSink{ages: map[string]float64{}, cleared: map[string]bool{}}
}
func (f *fakeAgeSink) SetErasureJobAge(_, jobID string, age float64) { f.ages[jobID] = age }
func (f *fakeAgeSink) ClearErasureJobAge(_, jobID string)            { f.cleared[jobID] = true }
func (f *fakeAgeSink) SetErasureJobDeadlineSeconds(s float64)        { f.deadline = s }

func TestSamplerEmitsAgeAndDeadline(t *testing.T) {
	jobs := erasurejob.NewMemory()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	// An in-progress job started 90s ago.
	_ = jobs.Create(context.Background(), erasurejob.Job{
		ID: "j_active", TenantID: "acme", UserID: "alice",
		Phase: erasurejob.PhaseStoreDeleting, StartedAt: now.Add(-90 * time.Second),
	})
	// A completed job: its age series must be cleared, not published.
	_ = jobs.Create(context.Background(), erasurejob.Job{
		ID: "j_done", TenantID: "acme", UserID: "bob",
		Phase: erasurejob.PhaseCompleted, StartedAt: now.Add(-10 * time.Minute),
	})

	sink := newFakeAgeSink()
	s := erasurejob.NewSampler(jobs, sink, 72*time.Hour, func() time.Time { return now })
	if err := s.Sample(context.Background()); err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if sink.deadline != (72 * time.Hour).Seconds() {
		t.Errorf("deadline = %v, want %v", sink.deadline, (72 * time.Hour).Seconds())
	}
	if sink.ages["j_active"] != 90 {
		t.Errorf("active job age = %v, want 90", sink.ages["j_active"])
	}
	if _, ok := sink.ages["j_done"]; ok {
		t.Error("a completed job must not publish an age series")
	}
	if !sink.cleared["j_done"] {
		t.Error("a completed job's age series must be cleared")
	}
}
