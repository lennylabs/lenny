// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/upgrade"
)

func TestNextWalksTheFullSequence(t *testing.T) {
	want := []upgrade.Phase{
		upgrade.OpsRoll, upgrade.CRDUpdate, upgrade.SchemaMigration,
		upgrade.GatewayRoll, upgrade.ControllerRoll, upgrade.Verification,
		upgrade.Complete,
	}
	phase := upgrade.Preflight
	for i, expect := range want {
		next, err := upgrade.Next(phase)
		if err != nil {
			t.Fatalf("Next(%s): %v", phase, err)
		}
		if next != expect {
			t.Fatalf("step %d: Next(%s) = %s, want %s", i, phase, next, expect)
		}
		phase = next
	}
}

func TestNextRejectsTerminalAndUnknownPhases(t *testing.T) {
	for _, p := range []upgrade.Phase{upgrade.Complete, upgrade.RolledBack, "Bogus"} {
		if _, err := upgrade.Next(p); err == nil {
			t.Errorf("Next(%s) = nil error, want a rejection", p)
		}
	}
}

func TestCanRollBack(t *testing.T) {
	rollbackable := map[upgrade.Phase]bool{
		upgrade.Preflight: true, upgrade.OpsRoll: true, upgrade.CRDUpdate: true,
		upgrade.SchemaMigration: false, upgrade.GatewayRoll: false,
		upgrade.ControllerRoll: false, upgrade.Verification: false,
		upgrade.Complete: false, upgrade.RolledBack: false,
	}
	for phase, want := range rollbackable {
		if got := upgrade.CanRollBack(phase); got != want {
			t.Errorf("CanRollBack(%s) = %v, want %v", phase, got, want)
		}
	}
}

func TestRollback(t *testing.T) {
	got, err := upgrade.Rollback(upgrade.OpsRoll)
	if err != nil || got != upgrade.RolledBack {
		t.Errorf("Rollback(OpsRoll) = %s, %v; want RolledBack, nil", got, err)
	}
	if _, err := upgrade.Rollback(upgrade.SchemaMigration); err == nil {
		t.Error("Rollback(SchemaMigration) = nil error, want a rejection past the point of no return")
	}
}

func TestIsTerminal(t *testing.T) {
	if !upgrade.IsTerminal(upgrade.Complete) || !upgrade.IsTerminal(upgrade.RolledBack) {
		t.Error("Complete and RolledBack must be terminal")
	}
	if upgrade.IsTerminal(upgrade.Preflight) || upgrade.IsTerminal(upgrade.Verification) {
		t.Error("working phases must not be terminal")
	}
}

func TestStepNumber(t *testing.T) {
	steps := map[upgrade.Phase]int{
		upgrade.Preflight: 1, upgrade.OpsRoll: 2, upgrade.CRDUpdate: 3,
		upgrade.SchemaMigration: 4, upgrade.GatewayRoll: 5,
		upgrade.ControllerRoll: 6, upgrade.Verification: 7,
	}
	for phase, want := range steps {
		got, ok := upgrade.StepNumber(phase)
		if !ok || got != want {
			t.Errorf("StepNumber(%s) = %d, %v; want %d, true", phase, got, ok, want)
		}
	}
	if want := upgrade.TotalSteps; want != 7 {
		t.Errorf("TotalSteps = %d, want 7", want)
	}
	for _, p := range []upgrade.Phase{upgrade.Complete, upgrade.RolledBack} {
		if _, ok := upgrade.StepNumber(p); ok {
			t.Errorf("StepNumber(%s) reported a step, want none for a terminal phase", p)
		}
	}
}
