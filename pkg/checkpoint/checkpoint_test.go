// SPDX-License-Identifier: MIT

package checkpoint

import (
	"errors"
	"testing"
	"time"
)

func TestAllLevelsIsExhaustive(t *testing.T) {
	if got := len(AllLevels()); got != 4 {
		t.Errorf("AllLevels() returned %d, want 4 per §4.4", got)
	}
	for _, l := range AllLevels() {
		if !l.IsValid() {
			t.Errorf("AllLevels() returned invalid %q", l)
		}
	}
}

func TestConsistencyForLevel(t *testing.T) {
	cases := map[Level]ConsistencyTag{
		LevelBasic:    ConsistencyBestEffort,
		LevelStandard: ConsistencyBestEffort,
		LevelFull:     ConsistencyConsistent,
		LevelEmbedded: ConsistencyConsistent,
	}
	for l, want := range cases {
		if got := ConsistencyForLevel(l); got != want {
			t.Errorf("ConsistencyForLevel(%q) = %q, want %q", l, got, want)
		}
	}
}

func TestAllTriggersIsExhaustive(t *testing.T) {
	got := AllTriggers()
	if len(got) != 3 {
		t.Errorf("AllTriggers() returned %d, want 3 per §4.4", len(got))
	}
}

func TestTriggerIsEviction(t *testing.T) {
	if !TriggerEviction.IsEviction() {
		t.Errorf("eviction trigger must report IsEviction")
	}
	for _, tr := range []Trigger{TriggerPeriodic, TriggerPreScaleDown} {
		if tr.IsEviction() {
			t.Errorf("%q must not report IsEviction", tr)
		}
	}
}

func TestRetryBudgetForTriggers(t *testing.T) {
	// §4.4 non-eviction: 200ms initial, ~5s total.
	rb := RetryBudgetFor(TriggerPeriodic)
	if rb.Initial != 200*time.Millisecond {
		t.Errorf("non-eviction initial: want 200ms, got %v", rb.Initial)
	}
	if rb.TotalBudget != 5*time.Second {
		t.Errorf("non-eviction total: want 5s, got %v", rb.TotalBudget)
	}
	// §4.4 eviction: 500ms initial, 5s cap, 30s total.
	rb = RetryBudgetFor(TriggerEviction)
	if rb.Initial != 500*time.Millisecond {
		t.Errorf("eviction initial: want 500ms, got %v", rb.Initial)
	}
	if rb.Cap != 5*time.Second {
		t.Errorf("eviction cap: want 5s, got %v", rb.Cap)
	}
	if rb.TotalBudget != 30*time.Second {
		t.Errorf("eviction total: want 30s, got %v", rb.TotalBudget)
	}
}

func TestCheckpointTimeoutIs60s(t *testing.T) {
	// §4.4 fixes the quiescence-to-completion window at 60s.
	if CheckpointTimeout != 60*time.Second {
		t.Errorf("CheckpointTimeout: want 60s, got %v", CheckpointTimeout)
	}
}

func TestAllOutcomesIsExhaustive(t *testing.T) {
	got := AllOutcomes()
	if len(got) != 4 {
		t.Errorf("AllOutcomes() returned %d, want 4 per §4.4", len(got))
	}
}

func TestAllResumeModesIsExhaustive(t *testing.T) {
	got := AllResumeModes()
	if len(got) != 4 {
		t.Errorf("AllResumeModes() returned %d, want 4", len(got))
	}
}

func TestResumeModeWorkspaceLost(t *testing.T) {
	if !ResumeConversationOnly.WorkspaceLost() {
		t.Errorf("conversation_only must report WorkspaceLost=true")
	}
	for _, m := range []ResumeMode{ResumeFull, ResumePartialWorkspace, ResumeCoordinatorHandoff} {
		if m.WorkspaceLost() {
			t.Errorf("%q must not report WorkspaceLost=true", m)
		}
	}
}

func TestWorkspaceSizePreCheckAdmitsWithin(t *testing.T) {
	cases := []struct {
		workspace, limit int64
	}{
		{0, 100},
		{50, 100},
		{99, 100},
		{100, 100}, // at limit is admitted; over limit rejected
	}
	for _, c := range cases {
		if err := WorkspaceSizePreCheck(c.workspace, c.limit); err != nil {
			t.Errorf("WorkspaceSizePreCheck(%d, %d) = %v, want nil", c.workspace, c.limit, err)
		}
	}
}

func TestWorkspaceSizePreCheckRejectsExceed(t *testing.T) {
	err := WorkspaceSizePreCheck(200, 100)
	if err == nil {
		t.Fatalf("expected workspace-size rejection")
	}
	var we *WorkspaceSizeExceededError
	if !errors.As(err, &we) {
		t.Errorf("expected *WorkspaceSizeExceededError, got %T", err)
	}
	if we.WorkspaceBytes != 200 || we.LimitBytes != 100 {
		t.Errorf("error fields: got %+v", we)
	}
}

func TestWorkspaceSizePreCheckUnconfigured(t *testing.T) {
	// Zero limit means no configured cap; admit anything.
	if err := WorkspaceSizePreCheck(1<<30, 0); err != nil {
		t.Errorf("zero limit should admit, got %v", err)
	}
}

func TestFreshnessCheckAdmitsWithinInterval(t *testing.T) {
	now := time.Now()
	for _, age := range []time.Duration{0, time.Minute, 9 * time.Minute, 10 * time.Minute} {
		if err := FreshnessCheck(now, now.Add(-age), 10*time.Minute); err != nil {
			t.Errorf("age %v within 10m interval: want nil, got %v", age, err)
		}
	}
}

func TestFreshnessCheckRejectsStale(t *testing.T) {
	now := time.Now()
	err := FreshnessCheck(now, now.Add(-11*time.Minute), 10*time.Minute)
	var se *StaleCheckpointError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StaleCheckpointError, got %v", err)
	}
	if se.IntervalSeconds != 600 {
		t.Errorf("IntervalSeconds: want 600, got %d", se.IntervalSeconds)
	}
}

func TestFreshnessCheckRejectsNeverCheckpointed(t *testing.T) {
	err := FreshnessCheck(time.Now(), time.Time{}, 10*time.Minute)
	var se *StaleCheckpointError
	if !errors.As(err, &se) {
		t.Fatalf("never-checkpointed session should be flagged, got %v", err)
	}
	if se.AgeSeconds != -1 {
		t.Errorf("never-checkpointed AgeSeconds: want -1, got %d", se.AgeSeconds)
	}
}

func TestFreshnessCheckTreatsZeroIntervalAsUnbounded(t *testing.T) {
	if err := FreshnessCheck(time.Now(), time.Time{}, 0); err != nil {
		t.Errorf("zero interval should be unbounded, got %v", err)
	}
}
