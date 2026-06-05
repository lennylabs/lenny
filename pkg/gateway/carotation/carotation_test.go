// SPDX-License-Identifier: MIT

package carotation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/carotationstore"
	"github.com/lennylabs/lenny/pkg/mtls"
)

type recordingObserver struct{ events []mtls.CARotationEvent }

func (r *recordingObserver) OnCARotation(ev mtls.CARotationEvent) { r.events = append(r.events, ev) }

func newManager(t *testing.T, now func() time.Time) (*Manager, *recordingObserver) {
	t.Helper()
	obs := &recordingObserver{}
	store := carotationstore.NewMemory().WithClock(now)
	m := NewManager(store,
		WithObserver(obs),
		WithOverlapWindow(24*time.Hour),
		WithClock(now),
	)
	return m, obs
}

// spec: §10.3 lines 344-350 — the full operator-driven rotation:
// idle -> begin -> promote -> retire, each stage audited exactly once
// after it durably commits, and the overlap guard enforced on retire.
func TestManager_fullLifecycle_spec_10_3(t *testing.T) {
	ctx := context.Background()
	clk := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	m, obs := newManager(t, now)

	if err := m.EnsureInitialized(ctx, "ca-old"); err != nil {
		t.Fatalf("init: %v", err)
	}
	snap, ok, err := m.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("status: ok=%v err=%v", ok, err)
	}
	if snap.Stage != mtls.CAStageIdle || snap.CurrentCAID != "ca-old" {
		t.Fatalf("idle snapshot = %+v", snap)
	}

	if _, err := m.Begin(ctx, "ca-new"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := m.Promote(ctx); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// Overlap still open -> retire refuses.
	if _, err := m.Retire(ctx); !mtls.IsOverlapOpen(err) {
		t.Fatalf("retire before overlap close: err = %v, want overlap_open", err)
	}
	// Advance past the overlap window.
	clk = clk.Add(48 * time.Hour)
	snap, err = m.Retire(ctx)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if snap.Stage != mtls.CAStageOldCARetired || len(snap.TrustedCAIDs) != 1 {
		t.Fatalf("retired snapshot = %+v", snap)
	}

	// Exactly three audited transitions: begin, promote, retire. The
	// refused retire emitted nothing (it never committed).
	if len(obs.events) != 3 {
		t.Fatalf("audited %d transitions, want 3: %+v", len(obs.events), obs.events)
	}
	wantTo := []mtls.CARotationStage{mtls.CAStageNewCADeployed, mtls.CAStagePromoted, mtls.CAStageOldCARetired}
	for i, ev := range obs.events {
		if ev.To != wantTo[i] {
			t.Errorf("event %d To = %q, want %q", i, ev.To, wantTo[i])
		}
	}
}

func TestManager_notInitialized(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Unix(0, 0).UTC() }
	m, _ := newManager(t, now)
	if _, _, err := func() (mtls.CARotationSnapshot, bool, error) { return m.Status(ctx) }(); err != nil {
		t.Fatalf("status before init should be ok=false, got err %v", err)
	}
	if _, err := m.Begin(ctx, "ca-new"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("begin before init err = %v, want ErrNotInitialized", err)
	}
}

func TestManager_rejectsOutOfOrderTransitions(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Unix(0, 0).UTC() }
	m, _ := newManager(t, now)
	if err := m.EnsureInitialized(ctx, "ca-old"); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Promote before Begin: wrong stage.
	if _, err := m.Promote(ctx); err == nil {
		t.Fatalf("promote from idle should fail")
	}
	// Retire before Begin: wrong stage.
	if _, err := m.Retire(ctx); err == nil {
		t.Fatalf("retire from idle should fail")
	}
	if _, err := m.Begin(ctx, "ca-new"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Second Begin clobbers an in-flight rotation: rejected.
	if _, err := m.Begin(ctx, "ca-newer"); err == nil {
		t.Fatalf("double begin should fail")
	}
}

// EnsureInitialized is idempotent and never resets a mid-rotation stage.
func TestManager_ensureInitializedIdempotent(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Unix(0, 0).UTC() }
	m, _ := newManager(t, now)
	if err := m.EnsureInitialized(ctx, "ca-old"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := m.Begin(ctx, "ca-new"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// A restart re-runs EnsureInitialized; it must not reset to idle.
	if err := m.EnsureInitialized(ctx, "ca-old"); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	snap, _, err := m.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if snap.Stage != mtls.CAStageNewCADeployed {
		t.Fatalf("stage after re-init = %q, want new_ca_deployed", snap.Stage)
	}
}
