// SPDX-License-Identifier: MIT

package mtls

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// recordingObserver captures every CARotationEvent it receives so a
// test asserts on the audit-shaped record without reaching into the
// rotation's private state.
type recordingObserver struct {
	events []CARotationEvent
}

func (r *recordingObserver) OnCARotation(ev CARotationEvent) {
	r.events = append(r.events, ev)
}

// withClock builds a CARotation with a deterministic clock so the
// §10.3 overlap window can be traversed without sleeping.
func withClock(t *testing.T, current string, window time.Duration, clock *time.Time, obs CARotationObserver) *CARotation {
	t.Helper()
	r, err := NewCARotation(current, CARotationOptions{
		OverlapWindow: window,
		Now:           func() time.Time { return *clock },
		Observer:      obs,
	})
	if err != nil {
		t.Fatalf("NewCARotation: %v", err)
	}
	return r
}

// spec: 10.3
// diagnosis: NewCARotation admitted an empty current CA, leaving the
// rotation unable to name the trust anchor it starts from.
// Construction must reject an empty currentCAID with *RotationError.
func TestNewCARotationRejectsEmptyCurrent(t *testing.T) {
	t.Parallel()
	_, err := NewCARotation("", CARotationOptions{})
	var re *RotationError
	if !errors.As(err, &re) || re.Kind != "invalid_argument" {
		t.Fatalf("NewCARotation(\"\"): want *RotationError kind=invalid_argument, got %v", err)
	}
}

// spec: 10.3
// diagnosis: a fresh rotation reported a stage other than idle,
// or a trust bundle that did not contain exactly the current CA. The
// resting state must carry stage=idle with TrustedCAIDs=[currentCAID].
func TestCARotationIdleSnapshot(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)

	snap := r.Snapshot()
	if snap.Stage != CAStageIdle {
		t.Errorf("fresh stage = %q, want %q", snap.Stage, CAStageIdle)
	}
	if snap.CurrentCAID != "ca-1" {
		t.Errorf("CurrentCAID = %q, want ca-1", snap.CurrentCAID)
	}
	if !reflect.DeepEqual(snap.TrustedCAIDs, []string{"ca-1"}) {
		t.Errorf("TrustedCAIDs = %v, want [ca-1]", snap.TrustedCAIDs)
	}
	if !snap.OverlapStartedAt.IsZero() {
		t.Errorf("OverlapStartedAt before Begin = %v, want zero", snap.OverlapStartedAt)
	}
}

// spec: 10.3
// diagnosis: BeginNewCARotation did not record the overlap-start
// instant or did not add the new CA to the trust bundle. After
// Begin the gateway must trust both CAs and the overlap clock must
// be running so RetireOldCA can refuse before it closes.
func TestCARotationBeginAddsNewCAToTrustBundle(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)

	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	snap := r.Snapshot()
	if snap.Stage != CAStageNewCADeployed {
		t.Errorf("stage after Begin = %q, want %q", snap.Stage, CAStageNewCADeployed)
	}
	if snap.CurrentCAID != "ca-1" {
		t.Errorf("CurrentCAID after Begin = %q, want ca-1 (issuer unchanged at this stage)", snap.CurrentCAID)
	}
	if !reflect.DeepEqual(snap.TrustedCAIDs, []string{"ca-1", "ca-2"}) {
		t.Errorf("TrustedCAIDs after Begin = %v, want [ca-1 ca-2]", snap.TrustedCAIDs)
	}
	if !snap.OverlapStartedAt.Equal(clock) {
		t.Errorf("OverlapStartedAt = %v, want %v", snap.OverlapStartedAt, clock)
	}
	wantCloses := clock.Add(24 * time.Hour)
	if !snap.OverlapClosesAt.Equal(wantCloses) {
		t.Errorf("OverlapClosesAt = %v, want %v", snap.OverlapClosesAt, wantCloses)
	}
}

// spec: 10.3
// diagnosis: BeginNewCARotation accepted an empty or duplicate new
// CA. Construction-time validation must reject both: an empty kid is
// unaddressable, and an identical CA would leave the rotation
// indistinguishable from a no-op.
func TestCARotationBeginRejectsBadNewCA(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)

	if err := r.BeginNewCARotation(""); err == nil {
		t.Error("BeginNewCARotation accepted an empty new CA")
	}
	if err := r.BeginNewCARotation("ca-1"); err == nil {
		t.Error("BeginNewCARotation accepted the current CA as the new CA")
	}
	// The rejected calls must not have advanced the stage.
	if r.Snapshot().Stage != CAStageIdle {
		t.Errorf("stage after rejected Begin = %q, want %q", r.Snapshot().Stage, CAStageIdle)
	}
}

// spec: 10.3
// diagnosis: a stage transition ran out of sequence (Promote before
// Begin, Retire before Promote, or a duplicate Begin). The state
// machine must reject every out-of-order transition with kind
// invalid_stage.
func TestCARotationRejectsOutOfOrderTransitions(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)

	if err := r.PromoteNewCA(); err == nil {
		t.Error("PromoteNewCA admitted from CAStageIdle")
	}
	if err := r.RetireOldCA(); err == nil {
		t.Error("RetireOldCA admitted from CAStageIdle")
	}
	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	if err := r.BeginNewCARotation("ca-3"); err == nil {
		t.Error("BeginNewCARotation admitted a duplicate call")
	}
	if err := r.RetireOldCA(); err == nil {
		t.Error("RetireOldCA admitted before Promote")
	}
}

// spec: 10.3
// diagnosis: PromoteNewCA did not flip the issuer to the new CA
// while keeping both CAs trusted. Promote must change CurrentCAID
// without shrinking the trust bundle.
func TestCARotationPromoteFlipsIssuerKeepsTrust(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)
	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	if err := r.PromoteNewCA(); err != nil {
		t.Fatalf("PromoteNewCA: %v", err)
	}
	snap := r.Snapshot()
	if snap.Stage != CAStagePromoted {
		t.Errorf("stage after Promote = %q, want %q", snap.Stage, CAStagePromoted)
	}
	if snap.CurrentCAID != "ca-2" {
		t.Errorf("CurrentCAID after Promote = %q, want ca-2", snap.CurrentCAID)
	}
	if !reflect.DeepEqual(snap.TrustedCAIDs, []string{"ca-1", "ca-2"}) {
		t.Errorf("TrustedCAIDs after Promote = %v, want [ca-1 ca-2]", snap.TrustedCAIDs)
	}
}

// spec: 10.3
// diagnosis: RetireOldCA admitted a call before the §10.3 overlap
// window had closed. Retire must refuse with kind overlap_open until
// now >= OverlapStartedAt + OverlapWindow so leaves still signed
// under the old CA keep verifying through the window.
func TestCARotationRetireGuardsOverlapWindow(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r := withClock(t, "ca-1", 24*time.Hour, &clock, nil)
	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	if err := r.PromoteNewCA(); err != nil {
		t.Fatalf("PromoteNewCA: %v", err)
	}
	// One second before the window closes: must refuse.
	clock = clock.Add(24*time.Hour - time.Second)
	err := r.RetireOldCA()
	if !IsOverlapOpen(err) {
		t.Fatalf("Retire 1s early: want overlap_open, got %v", err)
	}
	// At the close boundary: admitted.
	clock = clock.Add(time.Second)
	if err := r.RetireOldCA(); err != nil {
		t.Fatalf("RetireOldCA at window close: %v", err)
	}
	snap := r.Snapshot()
	if snap.Stage != CAStageOldCARetired {
		t.Errorf("stage after Retire = %q, want %q", snap.Stage, CAStageOldCARetired)
	}
	if !reflect.DeepEqual(snap.TrustedCAIDs, []string{"ca-2"}) {
		t.Errorf("TrustedCAIDs after Retire = %v, want [ca-2]", snap.TrustedCAIDs)
	}
}

// spec: 10.3
// diagnosis: the observer did not receive one event per committed
// transition, or events carried the wrong from/to or trust bundle.
// Each Begin / Promote / Retire must emit exactly one event with the
// snapshot fields that match the post-transition state.
func TestCARotationObserverReceivesEachTransition(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	obs := &recordingObserver{}
	r := withClock(t, "ca-1", 1*time.Hour, &clock, obs)

	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	if err := r.PromoteNewCA(); err != nil {
		t.Fatalf("PromoteNewCA: %v", err)
	}
	clock = clock.Add(time.Hour)
	if err := r.RetireOldCA(); err != nil {
		t.Fatalf("RetireOldCA: %v", err)
	}

	if len(obs.events) != 3 {
		t.Fatalf("emitted %d events, want 3", len(obs.events))
	}
	want := []CARotationEvent{
		{From: CAStageIdle, To: CAStageNewCADeployed, CurrentCAID: "ca-1", TrustedCAIDs: []string{"ca-1", "ca-2"}},
		{From: CAStageNewCADeployed, To: CAStagePromoted, CurrentCAID: "ca-2", TrustedCAIDs: []string{"ca-1", "ca-2"}},
		{From: CAStagePromoted, To: CAStageOldCARetired, CurrentCAID: "ca-2", TrustedCAIDs: []string{"ca-2"}},
	}
	for i, w := range want {
		got := obs.events[i]
		if got.From != w.From || got.To != w.To {
			t.Errorf("event %d: from/to = %q→%q, want %q→%q",
				i, got.From, got.To, w.From, w.To)
		}
		if got.CurrentCAID != w.CurrentCAID {
			t.Errorf("event %d: CurrentCAID = %q, want %q", i, got.CurrentCAID, w.CurrentCAID)
		}
		if !reflect.DeepEqual(got.TrustedCAIDs, w.TrustedCAIDs) {
			t.Errorf("event %d: TrustedCAIDs = %v, want %v", i, got.TrustedCAIDs, w.TrustedCAIDs)
		}
	}
}

// spec: 10.3
// diagnosis: NewCARotation accepted a non-positive overlap window
// and the resulting rotation would close the window instantly.
// Construction must substitute DefaultCARotationOverlap for any
// non-positive input so a misconfigured operator does not collapse
// the §10.3 24h overlap guarantee.
func TestNewCARotationDefaultsOverlapWindow(t *testing.T) {
	t.Parallel()
	for _, w := range []time.Duration{0, -time.Hour} {
		r, err := NewCARotation("ca-1", CARotationOptions{OverlapWindow: w})
		if err != nil {
			t.Fatalf("NewCARotation(%v): %v", w, err)
		}
		if r.overlapWindow != DefaultCARotationOverlap {
			t.Errorf("overlapWindow for input %v: got %v, want %v",
				w, r.overlapWindow, DefaultCARotationOverlap)
		}
	}
}

// spec: 10.3
// diagnosis: the closed CAStage enum was checked outside its
// declared set. AllCARotationStages must enumerate every stage in
// order and IsValid must accept exactly that set.
func TestCARotationStageEnumIsClosed(t *testing.T) {
	t.Parallel()
	stages := AllCARotationStages()
	wantOrder := []CARotationStage{
		CAStageIdle, CAStageNewCADeployed, CAStagePromoted, CAStageOldCARetired,
	}
	if !reflect.DeepEqual(stages, wantOrder) {
		t.Errorf("AllCARotationStages = %v, want %v", stages, wantOrder)
	}
	for _, s := range wantOrder {
		if !s.IsValid() {
			t.Errorf("%q reported IsValid()=false", s)
		}
	}
	if CARotationStage("not_a_stage").IsValid() {
		t.Errorf("a stage outside the enum reported IsValid()=true")
	}
}
