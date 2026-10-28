// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// DefaultScheduleTick is the cadence at which Run re-evaluates the
// per-session periodic-checkpoint schedule when ScheduleTick is unset. It
// bounds how late a due checkpoint can fire past its scheduled instant, and
// how long a newly bound session waits before the scheduler first observes
// it. Run applies the smaller of this value and the configured Interval, so
// a short test interval is not rounded up to a full second. The value is
// not fixed by the specification and is operator-tunable through
// ScheduleTick.
const DefaultScheduleTick = time.Second

// scheduleEntry records the next periodic-checkpoint instant for one
// session, keyed to the binding the scheduler first observed. A different
// binding pointer for the same session id means the session was rebound,
// which a resume or a coordinator handoff does, so the entry restarts the
// first-checkpoint delay.
type scheduleEntry struct {
	binding *podsession.BindResult
	due     time.Time
}

// periodicSchedule tracks the per-session §4.4 periodic-checkpoint
// deadlines. mu guards entries; the zero value is ready to use.
type periodicSchedule struct {
	mu      sync.Mutex
	entries map[string]scheduleEntry
}

// collectDue reconciles the schedule against the bindings the replica
// currently holds and returns the bindings whose periodic checkpoint is due
// at now.
//
// A binding the schedule has not seen, or a session whose binding pointer
// changed, is scheduled at now + FirstCheckpointDelay, which is the §4.4
// first-checkpoint jitter rule applied from the instant the session started
// or resumed on this replica. A due binding is rescheduled at now +
// interval with no further jitter. Entries for sessions that no longer hold
// a binding are dropped, so the schedule never outgrows the registry.
//
// spec: §4.4 (Periodic checkpoint scheduling) — "each session's first
// periodic checkpoint is scheduled at periodicCheckpointIntervalSeconds +
// random(0, periodicCheckpointIntervalSeconds ×
// periodicCheckpointJitterFraction) seconds after session start" and
// "Subsequent checkpoints are scheduled at the fixed interval from the
// previous checkpoint (no additional jitter)".
func (s *periodicSchedule) collectDue(now time.Time, bindings []*podsession.BindResult, interval time.Duration, jitterFraction float64) []*podsession.BindResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]scheduleEntry, len(bindings))
	}
	live := make(map[string]struct{}, len(bindings))
	var due []*podsession.BindResult
	for _, b := range bindings {
		live[b.SessionID] = struct{}{}
		e, ok := s.entries[b.SessionID]
		if !ok || e.binding != b {
			s.entries[b.SessionID] = scheduleEntry{
				binding: b,
				due:     now.Add(FirstCheckpointDelay(b.SessionID, interval, jitterFraction)),
			}
			continue
		}
		if now.Before(e.due) {
			continue
		}
		e.due = now.Add(interval)
		s.entries[b.SessionID] = e
		due = append(due, b)
	}
	for id := range s.entries {
		if _, ok := live[id]; !ok {
			delete(s.entries, id)
		}
	}
	return due
}

// nextDue reports the scheduled instant of sessionID's next periodic
// checkpoint, and false when the schedule holds no entry for it.
func (s *periodicSchedule) nextDue(sessionID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[sessionID]
	return e.due, ok
}

// SweepDue takes a §4.4 periodic checkpoint of every coordinated session
// whose scheduled instant has passed, and schedules the first checkpoint of
// any session it observes for the first time. It is the per-session
// scheduler Run drives; Sweep remains the unconditional all-sessions pass.
// A per-session failure is reported through OnError and does not stop the
// pass, and the failed session is retried at the next interval.
//
// spec: §4.4 (Periodic checkpoint scheduling) — "Failed periodic
// checkpoints are logged and retried on the next interval".
func (c *Checkpointer) SweepDue(ctx context.Context) {
	due := c.schedule.collectDue(c.now(), c.Registry.Snapshot(), c.interval(), c.JitterFraction)
	for _, b := range due {
		if err := c.Checkpoint(ctx, b.TenantID, b.SessionID); err != nil && c.OnError != nil {
			c.OnError(b.SessionID, err)
		}
	}
}

// scheduleTick returns the cadence Run re-evaluates the schedule on:
// ScheduleTick when set, else the smaller of DefaultScheduleTick and the
// periodic interval.
func (c *Checkpointer) scheduleTick() time.Duration {
	if c.ScheduleTick > 0 {
		return c.ScheduleTick
	}
	return min(DefaultScheduleTick, c.interval())
}
