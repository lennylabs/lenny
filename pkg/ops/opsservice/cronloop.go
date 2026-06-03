// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/cron"
)

// ScheduledJob is one §25.4 cron-driven operation the leader's cron
// evaluator fires: a scheduled backup (full or postgres), a backup
// verification, or the platform_upgrade_check. The expression is a
// standard five-field cron string parsed by pkg/cron.
type ScheduledJob struct {
	// Name identifies the job (e.g. "backup-full", "platform-upgrade-check").
	Name string
	// Expression is the five-field cron schedule.
	Expression string
	// Run executes the operation when the schedule is due. The cron
	// evaluator does the work inline on its own goroutine; a long job
	// should return promptly and continue asynchronously.
	Run func(ctx context.Context) error
	// ExpressionFunc, when non-nil, supplies the job's current cron
	// expression at evaluation time so a schedule edited at runtime takes
	// effect without a process restart. §25.11 line 4106 makes the backup
	// schedule "modifiable at runtime via PUT /v1/admin/backups/schedule";
	// the evaluator resolves this on each tick and uses it in place of the
	// static Expression. An empty return (an absent or cleared stored
	// schedule) or an unparseable expression falls back to Expression, so
	// the job still fires on its compiled-in cadence. spec: §25.11 line
	// 4106; F-25.11.5.
	ExpressionFunc func() string
}

// CronEvaluator is the §25.4 cron evaluator: the leader-only loop that
// drives pkg/cron. On each tick it asks every registered ScheduledJob
// whether its schedule has elapsed since the last evaluation and fires
// the ones that are due. Backup scheduling and the
// platform_upgrade_check cron are registered here.
type CronEvaluator struct {
	now func() time.Time

	mu       sync.Mutex
	jobs     []cronEntry
	lastEval time.Time
}

// cronEntry pairs a ScheduledJob with its parsed schedule.
type cronEntry struct {
	job      ScheduledJob
	schedule cron.Schedule
}

// NewCronEvaluator returns an evaluator over the given jobs. now
// supplies the current time; pass nil for time.Now. A job whose cron
// expression does not parse is rejected with an error so a malformed
// schedule fails at startup rather than silently never firing.
func NewCronEvaluator(now func() time.Time, jobs ...ScheduledJob) (*CronEvaluator, error) {
	if now == nil {
		now = time.Now
	}
	e := &CronEvaluator{now: now, lastEval: now()}
	for _, j := range jobs {
		sched, err := cron.Parse(j.Expression)
		if err != nil {
			return nil, fmt.Errorf("scheduled job %q: %w", j.Name, err)
		}
		e.jobs = append(e.jobs, cronEntry{job: j, schedule: sched})
	}
	return e, nil
}

// Tick is the §25.4 cron-evaluator loop body. It fires every job whose
// next scheduled time falls in the window since the previous tick. The
// window approach means a job is not missed when a tick is delayed and
// is not double-fired when ticks run faster than the cron resolution.
func (e *CronEvaluator) Tick(ctx context.Context) error {
	e.mu.Lock()
	from := e.lastEval
	to := e.now()
	e.lastEval = to
	entries := append([]cronEntry(nil), e.jobs...)
	e.mu.Unlock()

	var firstErr error
	for _, entry := range entries {
		schedule := entry.schedule
		// §25.11 line 4106: a job whose schedule is runtime-modifiable
		// resolves its current cron expression here so an edit applied via
		// PUT /v1/admin/backups/schedule changes the firing cadence on the
		// next tick rather than waiting for a lenny-ops restart. A blank or
		// unparseable expression keeps the compiled-in fallback parsed at
		// startup. F-25.11.5.
		if entry.job.ExpressionFunc != nil {
			if expr := entry.job.ExpressionFunc(); expr != "" {
				if parsed, err := cron.Parse(expr); err == nil {
					schedule = parsed
				}
			}
		}
		if !dueInWindow(schedule, from, to) {
			continue
		}
		if err := entry.job.Run(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("scheduled job %q: %w", entry.job.Name, err)
		}
	}
	return firstErr
}

// dueInWindow reports whether the schedule fires at some minute in the
// half-open interval (from, to]. cron.Next yields the earliest match
// strictly after its argument, so the first match after from that is
// not later than to means the job is due.
func dueInWindow(s cron.Schedule, from, to time.Time) bool {
	if !to.After(from) {
		return false
	}
	next, err := s.Next(from)
	if err != nil {
		return false
	}
	return !next.After(to)
}
