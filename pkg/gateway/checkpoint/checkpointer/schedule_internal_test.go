// SPDX-License-Identifier: MIT

package checkpointer

import (
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

var scheduleEpoch = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func bindingFor(id string) *podsession.BindResult {
	return &podsession.BindResult{SessionID: id, TenantID: "acme"}
}

func dueIDs(due []*podsession.BindResult) []string {
	out := make([]string, 0, len(due))
	for _, b := range due {
		out = append(out, b.SessionID)
	}
	return out
}

// spec: 4.4 (Event / Checkpoint Store) — "each session's first periodic
// checkpoint is scheduled at periodicCheckpointIntervalSeconds + random(0,
// periodicCheckpointIntervalSeconds × periodicCheckpointJitterFraction)
// seconds after session start".
func TestScheduleDelaysFirstCheckpointWithinJitterBound_spec_4_4(t *testing.T) {
	const interval = 600 * time.Second
	const jitter = 0.2
	for i := range 50 {
		id := fmt.Sprintf("acme-session-%d", i)
		var s periodicSchedule
		b := bindingFor(id)
		if due := s.collectDue(scheduleEpoch, []*podsession.BindResult{b}, interval, jitter); len(due) != 0 {
			t.Fatalf("%s: first observation returned due %v, want none", id, dueIDs(due))
		}
		at, ok := s.nextDue(id)
		if !ok {
			t.Fatalf("%s: no schedule entry after first observation", id)
		}
		offset := at.Sub(scheduleEpoch)
		if offset < interval || offset > interval+time.Duration(float64(interval)*jitter) {
			t.Fatalf("%s: first checkpoint offset %v outside [%v, %v]", id, offset, interval, interval+120*time.Second)
		}
		if due := s.collectDue(at.Add(-time.Nanosecond), []*podsession.BindResult{b}, interval, jitter); len(due) != 0 {
			t.Fatalf("%s: fired %v before its scheduled instant", id, at.Add(-time.Nanosecond))
		}
		if due := s.collectDue(at, []*podsession.BindResult{b}, interval, jitter); len(due) != 1 || due[0] != b {
			t.Fatalf("%s: did not fire at its scheduled instant, got %v", id, dueIDs(due))
		}
	}
}

// spec: 4.4 (Event / Checkpoint Store) — "Subsequent checkpoints are
// scheduled at the fixed interval from the previous checkpoint (no
// additional jitter)".
func TestScheduleKeepsFixedPeriodAfterFirstCheckpoint_spec_4_4(t *testing.T) {
	const interval = 600 * time.Second
	var s periodicSchedule
	b := bindingFor("acme-session-period")
	bs := []*podsession.BindResult{b}
	s.collectDue(scheduleEpoch, bs, interval, 1.0)
	fire, _ := s.nextDue(b.SessionID)
	for cycle := range 6 {
		if due := s.collectDue(fire, bs, interval, 1.0); len(due) != 1 {
			t.Fatalf("cycle %d: no checkpoint at %v", cycle, fire)
		}
		next, _ := s.nextDue(b.SessionID)
		if got := next.Sub(fire); got != interval {
			t.Fatalf("cycle %d: next checkpoint %v after the previous, want exactly %v", cycle, got, interval)
		}
		if due := s.collectDue(fire.Add(interval/2), bs, interval, 1.0); len(due) != 0 {
			t.Fatalf("cycle %d: fired mid-interval", cycle)
		}
		fire = next
	}
}

// spec: 4.4 (Event / Checkpoint Store) — the jitter "spreads the initial
// checkpoint uniformly across a 120s window (at the default 600s
// interval)", so a burst of sessions started together does not align.
func TestScheduleSpreadsFirstCheckpointsOfABurst_spec_4_4(t *testing.T) {
	const interval = 600 * time.Second
	const n = 2000
	const buckets = 12 // 10-second buckets across the 120-second window.
	var s periodicSchedule
	bs := make([]*podsession.BindResult, 0, n)
	for i := range n {
		bs = append(bs, bindingFor(fmt.Sprintf("burst-%04d", i)))
	}
	s.collectDue(scheduleEpoch, bs, interval, DefaultJitterFraction)
	counts := make([]int, buckets)
	for _, b := range bs {
		at, _ := s.nextDue(b.SessionID)
		extra := at.Sub(scheduleEpoch) - interval
		idx := int(extra / (10 * time.Second))
		if idx < 0 || idx >= buckets {
			t.Fatalf("%s: jitter %v outside the 120s window", b.SessionID, extra)
		}
		counts[idx]++
	}
	// A uniform spread puts about n/buckets (~167) in each bucket; any
	// bucket below half of that means the jitter clusters.
	for i, c := range counts {
		if c < n/buckets/2 {
			t.Errorf("bucket %d holds %d first checkpoints, want at least %d (counts %v)", i, c, n/buckets/2, counts)
		}
	}
}

// spec: 4.4 (Event / Checkpoint Store) — the first-checkpoint delay is
// measured from session start, and a resume rebinds the session.
func TestScheduleRestartsFirstDelayOnRebind_spec_4_4(t *testing.T) {
	const interval = 100 * time.Second
	var s periodicSchedule
	first := bindingFor("acme-rebind")
	s.collectDue(scheduleEpoch, []*podsession.BindResult{first}, interval, 0)
	resumedAt := scheduleEpoch.Add(90 * time.Second)
	second := bindingFor("acme-rebind")
	if due := s.collectDue(resumedAt, []*podsession.BindResult{second}, interval, 0); len(due) != 0 {
		t.Fatalf("rebind returned due %v, want none", dueIDs(due))
	}
	if at, _ := s.nextDue("acme-rebind"); !at.Equal(resumedAt.Add(interval)) {
		t.Fatalf("rebound session due at %v, want %v", at, resumedAt.Add(interval))
	}
	// The old schedule's instant must not fire the resumed session early.
	if due := s.collectDue(scheduleEpoch.Add(interval), []*podsession.BindResult{second}, interval, 0); len(due) != 0 {
		t.Fatal("resumed session fired on its pre-resume schedule")
	}
}

// spec: 4.4 (Event / Checkpoint Store) — only active sessions are
// scheduled; a released binding leaves the schedule.
func TestScheduleDropsReleasedSessions_spec_4_4(t *testing.T) {
	var s periodicSchedule
	a, b := bindingFor("a"), bindingFor("b")
	s.collectDue(scheduleEpoch, []*podsession.BindResult{a, b}, time.Minute, 0)
	s.collectDue(scheduleEpoch.Add(time.Second), []*podsession.BindResult{a}, time.Minute, 0)
	if _, ok := s.nextDue("b"); ok {
		t.Error("released session b still holds a schedule entry")
	}
	if _, ok := s.nextDue("a"); !ok {
		t.Error("bound session a lost its schedule entry")
	}
}

// spec: 4.4 (Event / Checkpoint Store) — the schedule cadence is never
// coarser than the periodic interval, so a short interval is honoured.
func TestScheduleTickDefaults_spec_4_4(t *testing.T) {
	if got := (&Checkpointer{}).scheduleTick(); got != DefaultScheduleTick {
		t.Errorf("default tick = %v, want %v", got, DefaultScheduleTick)
	}
	if got := (&Checkpointer{Interval: 10 * time.Millisecond}).scheduleTick(); got != 10*time.Millisecond {
		t.Errorf("tick with 10ms interval = %v, want 10ms", got)
	}
	if got := (&Checkpointer{ScheduleTick: 5 * time.Second}).scheduleTick(); got != 5*time.Second {
		t.Errorf("explicit tick = %v, want 5s", got)
	}
}
