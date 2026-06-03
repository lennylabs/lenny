// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is a test clock the cron-evaluator tests advance manually,
// so a scheduled job's firing is deterministic rather than wall-clock
// dependent.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCronEvaluatorRejectsBadExpression(t *testing.T) {
	_, err := NewCronEvaluator(time.Now, ScheduledJob{
		Name: "bad", Expression: "* * *", Run: func(context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("NewCronEvaluator accepted a 3-field expression, want an error")
	}
}

// TestCronEvaluatorFiresDueJob is the §25.4 cron-evaluator contract: a
// scheduled job fires when its cron schedule elapses. The clock starts
// at 01:58 and the job is scheduled for 02:00 daily (the §25.4 backup
// default); advancing past 02:00 must fire it exactly once.
func TestCronEvaluatorFiresDueJob(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 5, 18, 1, 58, 0, 0, time.UTC)}
	var fired int
	ev, err := NewCronEvaluator(clk.Now, ScheduledJob{
		Name:       "backup-full",
		Expression: "0 2 * * *", // daily at 02:00 — the §25.4 backups.schedule.full default
		Run: func(context.Context) error {
			fired++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewCronEvaluator: %v", err)
	}

	// A tick before 02:00: the job is not due.
	clk.Advance(time.Minute) // 01:59
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired != 0 {
		t.Fatalf("job fired %d times before 02:00, want 0", fired)
	}

	// A tick that crosses 02:00: the job fires.
	clk.Advance(2 * time.Minute) // 02:01
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired != 1 {
		t.Fatalf("job fired %d times across 02:00, want 1", fired)
	}

	// Another tick the same day: the daily job does not re-fire.
	clk.Advance(2 * time.Minute) // 02:03
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired != 1 {
		t.Errorf("job fired %d times after firing once, want 1 (no double-fire)", fired)
	}
}

// TestCronEvaluatorFiresEachDistinctSchedule confirms the evaluator
// fires the right job when several are registered with different
// schedules — the §25.4 evaluator drives full and postgres backups
// plus the platform_upgrade_check from one loop.
func TestCronEvaluatorFiresEachDistinctSchedule(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 5, 18, 5, 59, 0, 0, time.UTC)}
	fired := map[string]int{}
	ev, err := NewCronEvaluator(
		clk.Now,
		ScheduledJob{
			Name: "backup-full", Expression: "0 2 * * *",
			Run: func(context.Context) error { fired["backup-full"]++; return nil },
		},
		ScheduledJob{
			Name: "backup-postgres", Expression: "0 */6 * * *", // every 6h: 00,06,12,18
			Run: func(context.Context) error { fired["backup-postgres"]++; return nil },
		},
	)
	if err != nil {
		t.Fatalf("NewCronEvaluator: %v", err)
	}

	// Cross 06:00 — only the 6-hourly postgres backup is due.
	clk.Advance(2 * time.Minute) // 06:01
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired["backup-postgres"] != 1 {
		t.Errorf("backup-postgres fired %d times, want 1", fired["backup-postgres"])
	}
	if fired["backup-full"] != 0 {
		t.Errorf("backup-full fired %d times crossing 06:00, want 0", fired["backup-full"])
	}
}

// TestCronEvaluatorReportsRunError confirms a failing scheduled job's
// error is surfaced from Tick so the loop runner logs it.
func TestCronEvaluatorReportsRunError(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 5, 18, 1, 59, 0, 0, time.UTC)}
	ev, err := NewCronEvaluator(clk.Now, ScheduledJob{
		Name: "backup-full", Expression: "0 2 * * *",
		Run: func(context.Context) error { return errors.New("backup job creation failed") },
	})
	if err != nil {
		t.Fatalf("NewCronEvaluator: %v", err)
	}
	clk.Advance(2 * time.Minute) // cross 02:00
	if err := ev.Tick(context.Background()); err == nil {
		t.Error("Tick returned nil, want the scheduled job's error")
	}
}

// TestCronEvaluatorHonoursRuntimeSchedule is the §25.11 line 4106
// contract: a job whose ExpressionFunc supplies a runtime-edited cron
// fires on the edited cadence rather than the compiled-in Expression,
// without a process restart. The compiled-in default is 02:00 daily;
// the operator re-schedules to 04:00, and the 02:00 window must not
// fire while the 04:00 window does. F-25.11.5.
func TestCronEvaluatorHonoursRuntimeSchedule(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 5, 18, 1, 58, 0, 0, time.UTC)}
	var fired int
	current := "0 4 * * *" // operator edited the schedule to 04:00
	ev, err := NewCronEvaluator(clk.Now, ScheduledJob{
		Name:           "backup-full",
		Expression:     "0 2 * * *", // compiled-in default
		ExpressionFunc: func() string { return current },
		Run:            func(context.Context) error { fired++; return nil },
	})
	if err != nil {
		t.Fatalf("NewCronEvaluator: %v", err)
	}

	// Cross 02:00 — the static default would fire here, but the runtime
	// schedule (04:00) must suppress it.
	clk.Advance(3 * time.Minute) // 02:01
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired != 0 {
		t.Fatalf("job fired %d times at the 02:00 default; the 04:00 runtime schedule must win", fired)
	}

	// Cross 04:00 — the runtime schedule fires.
	clk.Advance(2 * time.Hour) // 04:01
	if err := ev.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired != 1 {
		t.Fatalf("job fired %d times across the 04:00 runtime schedule, want 1", fired)
	}
}

// TestCronEvaluatorFallsBackOnBlankOrBadRuntimeSchedule confirms an
// empty ExpressionFunc return (a cleared or unreadable stored schedule)
// and an unparseable expression both fall back to the compiled-in
// Expression, so the job still fires on its default cadence. F-25.11.5.
func TestCronEvaluatorFallsBackOnBlankOrBadRuntimeSchedule(t *testing.T) {
	for _, tc := range []struct {
		name string
		runt string
	}{
		{"blank", ""},
		{"unparseable", "not a cron"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{now: time.Date(2026, 5, 18, 1, 58, 0, 0, time.UTC)}
			var fired int
			ev, err := NewCronEvaluator(clk.Now, ScheduledJob{
				Name:           "backup-full",
				Expression:     "0 2 * * *",
				ExpressionFunc: func() string { return tc.runt },
				Run:            func(context.Context) error { fired++; return nil },
			})
			if err != nil {
				t.Fatalf("NewCronEvaluator: %v", err)
			}
			clk.Advance(3 * time.Minute) // cross the 02:00 default
			if err := ev.Tick(context.Background()); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if fired != 1 {
				t.Fatalf("job fired %d times, want 1 (fallback to the static 02:00 default)", fired)
			}
		})
	}
}
