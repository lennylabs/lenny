// SPDX-License-Identifier: MIT

package checkpoint

import (
	"errors"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// testNow is the deterministic anchor every checkpoint test uses
// in place of time.Now(). FreshnessCheck accepts the caller's
// "now" as an explicit argument, so a fixed value disconnects the
// suite from wall-clock without losing semantics.
var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

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

func TestRetryBudgetForFallback(t *testing.T) {
	// spec: §4.4 line 277 — 500ms initial, 5s per-attempt cap, 60s total.
	rb := RetryBudgetForFallback()
	if rb.Initial != 500*time.Millisecond {
		t.Errorf("Postgres fallback initial: want 500ms, got %v", rb.Initial)
	}
	if rb.Cap != 5*time.Second {
		t.Errorf("Postgres fallback cap: want 5s, got %v", rb.Cap)
	}
	if rb.TotalBudget != 60*time.Second {
		t.Errorf("Postgres fallback total: want 60s, got %v", rb.TotalBudget)
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
	now := testNow
	for _, age := range []time.Duration{0, time.Minute, 9 * time.Minute, 10 * time.Minute} {
		if err := FreshnessCheck(now, now.Add(-age), 10*time.Minute); err != nil {
			t.Errorf("age %v within 10m interval: want nil, got %v", age, err)
		}
	}
}

func TestFreshnessCheckRejectsStale(t *testing.T) {
	now := testNow
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
	err := FreshnessCheck(testNow, time.Time{}, 10*time.Minute)
	var se *StaleCheckpointError
	if !errors.As(err, &se) {
		t.Fatalf("never-checkpointed session should be flagged, got %v", err)
	}
	if se.AgeSeconds != -1 {
		t.Errorf("never-checkpointed AgeSeconds: want -1, got %d", se.AgeSeconds)
	}
}

func TestFreshnessCheckTreatsZeroIntervalAsUnbounded(t *testing.T) {
	if err := FreshnessCheck(testNow, time.Time{}, 0); err != nil {
		t.Errorf("zero interval should be unbounded, got %v", err)
	}
}

// TestTriggerProtoMirrorsEveryTrigger asserts the CheckpointTrigger wire
// enum the gateway carries in the §10.1 CheckpointStart frame mirrors the
// §4.4 Trigger set: every trigger maps to a distinct, non-UNSPECIFIED
// wire value and round-trips back through TriggerFromProto. This pins the
// mirror the gateway's grant/confirm driver and the
// `lenny_checkpoint_duration_seconds` label depend on, so a Trigger added
// without a matching wire value fails here.
//
// spec: §10.1 line 130 — the gateway carries the typed trigger in the
// gateway-driven Checkpoint stream.
func TestTriggerProtoMirrorsEveryTrigger(t *testing.T) {
	seen := map[adapterv1.CheckpointTrigger]bool{}
	for _, tr := range AllTriggers() {
		pv := tr.Proto()
		if pv == adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_UNSPECIFIED {
			t.Errorf("Trigger %q maps to CHECKPOINT_TRIGGER_UNSPECIFIED, want a distinct wire value", tr)
		}
		if seen[pv] {
			t.Errorf("Trigger %q maps to wire value %v already claimed by another trigger", tr, pv)
		}
		seen[pv] = true
		if got := TriggerFromProto(pv); got != tr {
			t.Errorf("round-trip Trigger %q -> %v -> %q, want %q", tr, pv, got, tr)
		}
	}
	// The wire enum carries exactly the mirror set plus the zero value:
	// one production value per §4.4 trigger and nothing else.
	if len(seen) != len(AllTriggers()) {
		t.Errorf("mapped %d distinct wire values, want %d (one per trigger)", len(seen), len(AllTriggers()))
	}
	if want := len(AllTriggers()) + 1; len(adapterv1.CheckpointTrigger_name) != want {
		t.Errorf("CheckpointTrigger wire enum has %d values, want %d (UNSPECIFIED + one per §4.4 trigger)",
			len(adapterv1.CheckpointTrigger_name), want)
	}
}

// TestTriggerFromProtoUnspecifiedIsInvalid asserts the zero wire value
// and an out-of-range value map to the empty Trigger, which IsValid
// rejects, so the adapter fails closed on a CheckpointStart that carries
// no recognised trigger rather than silently selecting a retry budget.
func TestTriggerFromProtoUnspecifiedIsInvalid(t *testing.T) {
	for _, pv := range []adapterv1.CheckpointTrigger{
		adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_UNSPECIFIED,
		adapterv1.CheckpointTrigger(99),
	} {
		got := TriggerFromProto(pv)
		if got != "" || got.IsValid() {
			t.Errorf("TriggerFromProto(%v) = %q (valid=%v), want the empty invalid Trigger", pv, got, got.IsValid())
		}
	}
}

// TestUnknownTriggerProtoIsUnspecified asserts a Trigger outside the §4.4
// set maps to the wire zero value rather than aliasing a real one, so an
// unrecognised trigger cannot be smuggled onto the wire as a valid one.
func TestUnknownTriggerProtoIsUnspecified(t *testing.T) {
	if got := Trigger("bogus").Proto(); got != adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_UNSPECIFIED {
		t.Errorf("Trigger(\"bogus\").Proto() = %v, want CHECKPOINT_TRIGGER_UNSPECIFIED", got)
	}
}
