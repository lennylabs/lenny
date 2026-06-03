// SPDX-License-Identifier: MIT

package operations_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// progressOp is an in-progress operation carrying a descriptive Progress
// envelope the enrichment recomputes ETA/stall fields onto.
func progressOp(id string, kind operations.Kind, status operations.Status, startedAt, lastProgressAt time.Time, completed, total int) operations.Operation {
	op := mustOp(id, kind, status, startedAt)
	op.Progress = &conventions.Progress{
		CompletedSteps: &completed,
		TotalSteps:     &total,
		CurrentStep:    "step",
	}
	if !lastProgressAt.IsZero() {
		op.Progress.LastProgressAt = lastProgressAt.UTC().Format(time.RFC3339)
	}
	return op
}

// spec §25.2 lines 393-394: with a baseline of sample_size >= 3, the
// Inventory enriches an in-progress operation's Progress with the
// historical_p50 ETA method.
func TestInventoryEnrichesHistoricalP50(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 5, 0, 0, time.UTC)
	src := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			progressOp("upgrade-1", operations.KindPlatformUpgrade, operations.StatusInProgress,
				now.Add(-2*time.Minute), now.Add(-30*time.Second), 3, 7),
		},
	}
	baselines := operations.NewMemoryBaselineStore()
	ctx := context.Background()
	for i := 0; i < operations.HistoricalP50MinSamples; i++ {
		_ = baselines.RecordCompletion(ctx, operations.KindPlatformUpgrade, 10*time.Minute)
	}

	inv := operations.New(src)
	inv.SetProgressBaselines(func() time.Time { return now }, baselines)
	page := inv.List(ctx, operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("got %d operations, want 1", len(page.Operations))
	}
	pr := page.Operations[0].Progress
	if pr == nil || pr.EtaMethod != conventions.EtaHistoricalP50 {
		t.Fatalf("EtaMethod = %v, want historical_p50", pr)
	}
	// 10 min p50 - 2 min elapsed = 8 min remaining.
	if pr.EtaSeconds == nil || *pr.EtaSeconds != 8*60 {
		t.Errorf("EtaSeconds = %v, want 480", pr.EtaSeconds)
	}
	// 30s since last progress is within the 2-min platform_upgrade cadence.
	if pr.StalledForSeconds != nil {
		t.Errorf("StalledForSeconds = %v, want nil within cadence", *pr.StalledForSeconds)
	}
}

// spec §25.2 lines 391/396: the Inventory derives the cadence-relative
// stalledForSeconds for an in-progress operation past its cadence.
func TestInventoryEnrichesStalled(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	src := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			// last progress 5 min ago, platform_upgrade cadence is 2 min ->
			// 3 min (180s) overrun.
			progressOp("upgrade-1", operations.KindPlatformUpgrade, operations.StatusInProgress,
				now.Add(-30*time.Minute), now.Add(-5*time.Minute), 2, 7),
		},
	}
	inv := operations.New(src)
	inv.SetProgressBaselines(func() time.Time { return now }, nil)
	page := inv.List(context.Background(), operations.Filter{}, 0)
	pr := page.Operations[0].Progress
	if pr.StalledForSeconds == nil || *pr.StalledForSeconds != 180 {
		t.Fatalf("StalledForSeconds = %v, want 180", pr.StalledForSeconds)
	}
}

// spec §25.2 line 391: a completed operation is never reported stalled —
// the enrichment leaves a non-in-progress operation's Progress untouched.
func TestInventoryDoesNotStallCompleted(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	src := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			progressOp("upgrade-1", operations.KindPlatformUpgrade, operations.StatusCompleted,
				now.Add(-30*time.Minute), now.Add(-20*time.Minute), 7, 7),
		},
	}
	inv := operations.New(src)
	inv.SetProgressBaselines(func() time.Time { return now }, nil)
	// A completed op is excluded from the default status filter, so query it
	// directly via Get to exercise the enrichment skip.
	op, _, ok := inv.Get(context.Background(), "upgrade-1")
	if !ok {
		t.Fatalf("Get(upgrade-1) not found")
	}
	if op.Progress.StalledForSeconds != nil {
		t.Errorf("completed op StalledForSeconds = %v, want nil", *op.Progress.StalledForSeconds)
	}
}
