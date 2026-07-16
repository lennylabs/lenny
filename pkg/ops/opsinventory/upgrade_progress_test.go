// SPDX-License-Identifier: MIT

package opsinventory_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// activeUpgradeInventory wires a platform-upgrade service holding st into
// the Operations Inventory the same way cmd/lenny-ops does: an
// UpgradeSource over the service, with the progress-baseline enrichment
// installed from baselines. now supplies the fixed clock both the service
// and the enrichment read.
func activeUpgradeInventory(t *testing.T, st upgradeservice.State, now time.Time, baselines operations.BaselineStore) (*operations.Inventory, *upgradeservice.Service) {
	t.Helper()
	store := upgradeservice.NewMemoryStore()
	if err := store.Save(context.Background(), st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	svc := upgradeservice.New(upgradeservice.Options{
		Store: store,
		Now:   func() time.Time { return now },
	})
	inv := operations.New(opsinventory.NewUpgradeSource(svc, ""))
	inv.SetProgressBaselines(func() time.Time { return now }, baselines)
	return inv, svc
}

// spec §25.8 line 3496: "GET /v1/admin/platform/upgrade/status and the
// Operations Inventory (Section 25.4) return the canonical progress
// envelope ... etaSeconds uses etaMethod: fixed_phase_durations (per-phase
// hard-coded durations) combined with historical_p50 when
// ops_operation_baselines has samples." An actively-progressing upgrade
// surfaced through the Operations Inventory (GET /v1/admin/operations and
// /{id}) must therefore report etaMethod fixed_phase_durations, never the
// generic linear_extrapolation the size/rate kinds use, and must match the
// direct GET /upgrade/status envelope the same service computes.
//
// diagnosis: the Operations Inventory ETA-enrichment picks the wrong
// method for platform_upgrade — a phase-index percent must not drive
// linear_extrapolation. A failure means the Inventory and the direct
// upgrade-status endpoint disagree on etaMethod for the same upgrade.
func TestUpgradeInventoryActiveUsesFixedPhaseDurations(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	// GatewayRoll's compiled-in phase duration is 4 minutes; started 90s
	// ago leaves a 150s fixed-phase ETA that counts down toward zero.
	st := upgradeservice.State{
		OperationID:   "upgrade-active-1",
		Phase:         upgrade.GatewayRoll,
		Paused:        false,
		TargetVersion: "1.6.0",
		StartedBy:     "sa-deploy",
		StartedAt:     now.Add(-90 * time.Second),
		UpdatedAt:     now.Add(-30 * time.Second),
	}
	inv, svc := activeUpgradeInventory(t, st, now, nil)
	ctx := context.Background()

	// The direct upgrade-status endpoint the Inventory must agree with.
	direct := svc.FullProgress(ctx, st)
	if direct.EtaMethod != conventions.EtaFixedPhaseDurations {
		t.Fatalf("direct FullProgress EtaMethod = %q, want fixed_phase_durations (test premise)", direct.EtaMethod)
	}

	assertActive := func(where string, pr *conventions.Progress) {
		t.Helper()
		if pr == nil {
			t.Fatalf("%s: progress is nil", where)
		}
		if pr.EtaMethod == conventions.EtaLinearExtrapolation {
			t.Errorf("%s: EtaMethod = linear_extrapolation, want fixed_phase_durations", where)
		}
		if pr.EtaMethod != conventions.EtaFixedPhaseDurations {
			t.Errorf("%s: EtaMethod = %q, want fixed_phase_durations", where, pr.EtaMethod)
		}
		if pr.EtaSeconds == nil || *pr.EtaSeconds != 150 {
			t.Errorf("%s: EtaSeconds = %v, want 150 (240s phase - 90s elapsed)", where, pr.EtaSeconds)
		}
		// The Inventory and the direct endpoint must produce the same ETA.
		if pr.EtaMethod != direct.EtaMethod ||
			(pr.EtaSeconds == nil) != (direct.EtaSeconds == nil) ||
			(pr.EtaSeconds != nil && direct.EtaSeconds != nil && *pr.EtaSeconds != *direct.EtaSeconds) {
			t.Errorf("%s: envelope %v/%v disagrees with direct %v/%v",
				where, pr.EtaMethod, pr.EtaSeconds, direct.EtaMethod, direct.EtaSeconds)
		}
	}

	// GET /v1/admin/operations
	page := inv.List(ctx, operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("List returned %d operations, want 1", len(page.Operations))
	}
	if got := page.Operations[0].Status; got != operations.StatusInProgress {
		t.Fatalf("status = %q, want in_progress", got)
	}
	assertActive("List", page.Operations[0].Progress)

	// GET /v1/admin/operations/{id}
	op, _, ok := inv.Get(ctx, "upgrade-active-1")
	if !ok {
		t.Fatalf("Get(upgrade-active-1) not found")
	}
	assertActive("Get", op.Progress)
}

// spec §25.8 line 3496: the same envelope is "combined with historical_p50
// when ops_operation_baselines has samples." Once the deployment has
// recorded enough completed upgrades, the Inventory's active-upgrade ETA
// switches to the historical_p50 method (§25.2 line 394 threshold), not
// linear_extrapolation.
//
// diagnosis: a failure means the Inventory does not consult the operation
// baselines for platform_upgrade, so it never reaches the highest-
// confidence ETA method the spec names for this endpoint.
func TestUpgradeInventoryActiveUsesHistoricalP50WithBaselines(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	st := upgradeservice.State{
		OperationID:   "upgrade-active-2",
		Phase:         upgrade.GatewayRoll,
		Paused:        false,
		TargetVersion: "1.6.0",
		StartedBy:     "sa-deploy",
		StartedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-30 * time.Second),
	}
	baselines := operations.NewMemoryBaselineStore()
	ctx := context.Background()
	for i := 0; i < operations.HistoricalP50MinSamples; i++ {
		if err := baselines.RecordCompletion(ctx, operations.KindPlatformUpgrade, 10*time.Minute); err != nil {
			t.Fatalf("RecordCompletion: %v", err)
		}
	}
	inv, _ := activeUpgradeInventory(t, st, now, baselines)

	page := inv.List(ctx, operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("List returned %d operations, want 1", len(page.Operations))
	}
	pr := page.Operations[0].Progress
	if pr == nil || pr.EtaMethod != conventions.EtaHistoricalP50 {
		t.Fatalf("EtaMethod = %v, want historical_p50", pr)
	}
	// 10 min p50 - 2 min elapsed = 8 min remaining.
	if pr.EtaSeconds == nil || *pr.EtaSeconds != 8*60 {
		t.Errorf("EtaSeconds = %v, want 480", pr.EtaSeconds)
	}
}

// spec §25.4 (Operations Inventory response example, upgrade-550e...):
// a paused upgrade awaiting an operator proceed reports etaMethod "none",
// etaSeconds null, and stalledForSeconds null — a paused operation is
// awaiting an operator by design (§25.2 line 391), so it carries no ETA.
// This pins the guard that the fixed_phase_durations enrichment does not
// leak onto the paused case, matching the direct upgrade-status endpoint.
//
// diagnosis: a failure means the Inventory computes an ETA for a paused
// upgrade, contradicting the canonical §25.4 paused example.
func TestUpgradeInventoryPausedReportsNoEta(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	st := upgradeservice.State{
		OperationID:   "upgrade-paused-1",
		Phase:         upgrade.GatewayRoll,
		Paused:        true,
		TargetVersion: "1.6.0",
		StartedBy:     "sa-deploy",
		StartedAt:     now.Add(-90 * time.Second),
		UpdatedAt:     now.Add(-30 * time.Second),
	}
	inv, svc := activeUpgradeInventory(t, st, now, nil)
	ctx := context.Background()

	direct := svc.FullProgress(ctx, st)
	if direct.EtaMethod != conventions.EtaNone || direct.EtaSeconds != nil {
		t.Fatalf("direct FullProgress for paused = %v/%v, want none/nil (test premise)", direct.EtaMethod, direct.EtaSeconds)
	}

	page := inv.List(ctx, operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("List returned %d operations, want 1", len(page.Operations))
	}
	if got := page.Operations[0].Status; got != operations.StatusPaused {
		t.Fatalf("status = %q, want paused", got)
	}
	pr := page.Operations[0].Progress
	if pr == nil || pr.EtaMethod != conventions.EtaNone {
		t.Errorf("paused EtaMethod = %v, want none", pr)
	}
	if pr.EtaSeconds != nil {
		t.Errorf("paused EtaSeconds = %v, want nil", *pr.EtaSeconds)
	}
	if pr.StalledForSeconds != nil {
		t.Errorf("paused StalledForSeconds = %v, want nil", *pr.StalledForSeconds)
	}
}
