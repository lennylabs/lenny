// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/events"
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

// spec §4.0, §16.6: Pool upgrade state machine emits upgrade_progressed
// on each phase transition carrying pool, oldPhase, newPhase, and
// imageDigest.
func TestAdvanceEmitsUpgradeProgressedOnEveryTransition(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "test")
	phase := upgrade.Preflight
	digest := "sha256:abc123"
	want := []struct {
		from, to upgrade.Phase
	}{
		{upgrade.Preflight, upgrade.OpsRoll},
		{upgrade.OpsRoll, upgrade.CRDUpdate},
		{upgrade.CRDUpdate, upgrade.SchemaMigration},
		{upgrade.SchemaMigration, upgrade.GatewayRoll},
		{upgrade.GatewayRoll, upgrade.ControllerRoll},
		{upgrade.ControllerRoll, upgrade.Verification},
		{upgrade.Verification, upgrade.Complete},
	}
	for _, step := range want {
		next, err := upgrade.Advance(context.Background(), em, "default-gvisor", phase, digest)
		if err != nil {
			t.Fatalf("Advance(%s): %v", phase, err)
		}
		if next != step.to {
			t.Fatalf("Advance(%s) = %s, want %s", step.from, next, step.to)
		}
		phase = next
	}
	page := buf.Query(0, events.EventFilter{EventType: "upgrade_progressed"}, 100)
	if len(page.Events) != len(want) {
		t.Fatalf("emitted %d events, want %d", len(page.Events), len(want))
	}
	for i, ev := range page.Events {
		var data struct {
			Pool        string `json:"pool"`
			OldPhase    string `json:"oldPhase"`
			NewPhase    string `json:"newPhase"`
			ImageDigest string `json:"imageDigest"`
		}
		if err := json.Unmarshal(ev.Event.Data, &data); err != nil {
			t.Fatalf("event %d data: %v", i, err)
		}
		if data.Pool != "default-gvisor" {
			t.Errorf("event %d pool = %q, want default-gvisor", i, data.Pool)
		}
		if data.OldPhase != string(want[i].from) || data.NewPhase != string(want[i].to) {
			t.Errorf("event %d phases = %s→%s, want %s→%s",
				i, data.OldPhase, data.NewPhase, want[i].from, want[i].to)
		}
		if data.ImageDigest != digest {
			t.Errorf("event %d imageDigest = %q, want %q", i, data.ImageDigest, digest)
		}
		if ev.Event.Type != "dev.lenny."+"upgrade_progressed" {
			t.Errorf("event %d type = %q, want dev.lenny.upgrade_progressed", i, ev.Event.Type)
		}
	}
}

// spec §4.0: a nil emitter is a no-op; the phase still advances.
func TestAdvanceWithNilEmitterStillTransitions(t *testing.T) {
	next, err := upgrade.Advance(context.Background(), nil, "default", upgrade.Preflight, "sha256:x")
	if err != nil || next != upgrade.OpsRoll {
		t.Errorf("Advance(nil) = %s, %v; want OpsRoll, nil", next, err)
	}
}

// spec §25.8: Advance reports the Next error and emits nothing past the
// terminal phase.
func TestAdvanceRejectsTerminalPhases(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "test")
	for _, p := range []upgrade.Phase{upgrade.Complete, upgrade.RolledBack, "Bogus"} {
		if _, err := upgrade.Advance(context.Background(), em, "default", p, "sha256:x"); err == nil {
			t.Errorf("Advance(%s) = nil error, want a rejection", p)
		}
	}
	if got := buf.Query(0, events.EventFilter{EventType: "upgrade_progressed"}, 100); len(got.Events) != 0 {
		t.Errorf("emitted %d events on rejected transitions, want 0", len(got.Events))
	}
}

// spec §25.8: a rollbackable phase rolls back to RolledBack and the
// emit carries newPhase=RolledBack with severity=warning.
func TestAdvanceRollbackEmitsRolledBackPhase(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "test")
	next, err := upgrade.AdvanceRollback(context.Background(), em, "default", upgrade.OpsRoll, "sha256:abc")
	if err != nil || next != upgrade.RolledBack {
		t.Fatalf("AdvanceRollback(OpsRoll) = %s, %v; want RolledBack, nil", next, err)
	}
	page := buf.Query(0, events.EventFilter{EventType: "upgrade_progressed"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(page.Events))
	}
	if page.Events[0].Event.Severity != "warning" {
		t.Errorf("severity = %q, want warning for a rollback", page.Events[0].Event.Severity)
	}
}

// spec §25.8: AdvanceRollback rejects a phase past the point of no
// return and emits nothing.
func TestAdvanceRollbackRejectsLatePhases(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "test")
	if _, err := upgrade.AdvanceRollback(context.Background(), em, "default", upgrade.SchemaMigration, "sha256:x"); err == nil {
		t.Error("AdvanceRollback(SchemaMigration) = nil error, want a rejection past the point of no return")
	}
	if got := buf.Query(0, events.EventFilter{}, 100); len(got.Events) != 0 {
		t.Errorf("emitted %d events on rejected rollback, want 0", len(got.Events))
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
