// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

type recordedCompletion struct {
	kind string
	dur  time.Duration
}

type fakeBaselineRecorder struct{ calls []recordedCompletion }

func (f *fakeBaselineRecorder) RecordCompletion(_ context.Context, kind string, dur time.Duration) error {
	f.calls = append(f.calls, recordedCompletion{kind: kind, dur: dur})
	return nil
}

// advancingClock returns a clock that advances one minute per call so a
// walk to Complete yields a positive wall-clock duration.
func advancingClock() func() time.Time {
	var n int64
	base := time.Unix(1700000000, 0).UTC()
	return func() time.Time {
		i := atomic.AddInt64(&n, 1)
		return base.Add(time.Duration(i) * time.Minute)
	}
}

// spec §25.2 line 393: the upgrade orchestrator folds the completed
// upgrade's wall-clock duration into the baseline table on the Complete
// transition, exactly once, under the platform_upgrade kind.
func TestUpgradeRecordsBaselineOnComplete(t *testing.T) {
	rec := &fakeBaselineRecorder{}
	svc := upgradeservice.New(upgradeservice.Options{
		Store:     upgradeservice.NewMemoryStore(),
		Now:       advancingClock(),
		Baselines: rec,
	})
	ctx := context.Background()
	if _, err := svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0", StartedBy: "alice"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < upgrade.TotalSteps; i++ {
		if _, err := svc.Proceed(ctx); err != nil {
			t.Fatalf("Proceed %d: %v", i, err)
		}
	}
	if len(rec.calls) != 1 {
		t.Fatalf("recorded %d completions, want exactly 1", len(rec.calls))
	}
	if rec.calls[0].kind != string(operations.KindPlatformUpgrade) {
		t.Errorf("kind = %q, want platform_upgrade", rec.calls[0].kind)
	}
	if rec.calls[0].dur <= 0 {
		t.Errorf("dur = %v, want > 0", rec.calls[0].dur)
	}
}

// spec §25.2 line 393: a rollback is not a successful completion and is
// not recorded as a baseline.
func TestUpgradeDoesNotRecordBaselineOnRollback(t *testing.T) {
	rec := &fakeBaselineRecorder{}
	svc := upgradeservice.New(upgradeservice.Options{
		Store:     upgradeservice.NewMemoryStore(),
		Now:       advancingClock(),
		Baselines: rec,
	})
	ctx := context.Background()
	if _, err := svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Advance one phase (Preflight -> OpsRoll) then roll back; OpsRoll is
	// before the §25.8 point of no return.
	if _, err := svc.Proceed(ctx); err != nil {
		t.Fatalf("Proceed: %v", err)
	}
	if _, err := svc.Rollback(ctx, "abort"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("recorded %d completions on rollback, want 0", len(rec.calls))
	}
}
