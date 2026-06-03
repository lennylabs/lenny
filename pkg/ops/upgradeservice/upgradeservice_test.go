// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// newService wires a Service over a MemoryStore with a deterministic
// clock and id, capturing every audit event and operational event.
func newService(t *testing.T) (*upgradeservice.Service, *[]upgradeservice.AuditEvent, *events.EventBuffer) {
	t.Helper()
	var audits []upgradeservice.AuditEvent
	buf := events.NewEventBuffer(0)
	em := events.NewEmitter(buf, "test")
	n := 0
	svc := upgradeservice.New(upgradeservice.Options{
		Store:   upgradeservice.NewMemoryStore(),
		Emitter: em,
		Audit:   func(ev upgradeservice.AuditEvent) { audits = append(audits, ev) },
		Now:     func() time.Time { return time.Unix(1700000000, 0).UTC() },
		NewID:   func() string { n++; return "upgrade-test" },
	})
	return svc, &audits, buf
}

func auditTypes(evs []upgradeservice.AuditEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

// spec: §25.8 — Start creates the singleton at Preflight and emits
// platform.upgrade_started.
func TestStartEntersPreflightAndAuditsStarted(t *testing.T) {
	svc, audits, _ := newService(t)
	st, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.5.0", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.Phase != upgrade.Preflight {
		t.Errorf("phase = %s, want Preflight", st.Phase)
	}
	if !st.Paused {
		t.Error("a fresh upgrade should be paused awaiting the first proceed")
	}
	if st.OperationID != "upgrade-test" || st.StartedBy != "alice" {
		t.Errorf("operationID=%q startedBy=%q", st.OperationID, st.StartedBy)
	}
	if got := auditTypes(*audits); len(got) != 1 || got[0] != string(audit.EventPlatformUpgradeStarted) {
		t.Errorf("audit = %v, want [platform.upgrade_started]", got)
	}
}

// spec: §25.8 — a second start while an upgrade is active is rejected.
func TestStartRejectsSecondUpgrade(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != upgradeservice.ErrUpgradeInProgress {
		t.Errorf("second Start err = %v, want ErrUpgradeInProgress", err)
	}
}

// spec: §25.8 — a start without a target version is a validation error.
func TestStartRequiresVersion(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{}); err == nil {
		t.Error("Start without a version should error")
	}
}

// spec: §25.8 / §16.7 — Proceed walks every phase to Complete, emitting
// the past-tense audit event for each completed phase and one
// upgrade_progressed operational event per transition.
func TestProceedWalksToCompleteWithAudits(t *testing.T) {
	svc, audits, buf := newService(t)
	ctx := context.Background()
	if _, err := svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0", StartedBy: "alice", ImageDigest: "sha256:abc"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantPhases := []upgrade.Phase{
		upgrade.OpsRoll, upgrade.CRDUpdate, upgrade.SchemaMigration,
		upgrade.GatewayRoll, upgrade.ControllerRoll, upgrade.Verification, upgrade.Complete,
	}
	for i, want := range wantPhases {
		st, err := svc.Proceed(ctx)
		if err != nil {
			t.Fatalf("Proceed %d: %v", i, err)
		}
		if st.Phase != want {
			t.Fatalf("Proceed %d phase = %s, want %s", i, st.Phase, want)
		}
	}
	// Complete is the terminal phase; it must not be paused.
	st, _, _ := svc.Status(ctx)
	if st.Paused || st.Active() {
		t.Errorf("completed upgrade: paused=%v active=%v, want false,false", st.Paused, st.Active())
	}
	// The started event plus one per proceed transition.
	wantAudit := []string{
		string(audit.EventPlatformUpgradeStarted),
		string(audit.EventPlatformUpgradePhaseAdvanced),  // leaving Preflight
		string(audit.EventPlatformUpgradeOpsRolled),      // leaving OpsRoll
		string(audit.EventPlatformUpgradeCrdsUpdated),    // leaving CRDUpdate
		string(audit.EventPlatformUpgradeSchemaMigrated), // leaving SchemaMigration
		string(audit.EventPlatformUpgradeGatewayRolled),  // leaving GatewayRoll
		string(audit.EventPlatformUpgradeControllersRolled),
		string(audit.EventPlatformUpgradeCompleted), // leaving Verification → Complete
	}
	got := auditTypes(*audits)
	if len(got) != len(wantAudit) {
		t.Fatalf("audit count = %d (%v), want %d", len(got), got, len(wantAudit))
	}
	for i := range wantAudit {
		if got[i] != wantAudit[i] {
			t.Errorf("audit[%d] = %s, want %s", i, got[i], wantAudit[i])
		}
	}
	// One upgrade_progressed operational event per proceed.
	page := buf.Query(0, events.EventFilter{EventType: "upgrade_progressed"}, 100)
	if len(page.Events) != len(wantPhases) {
		t.Errorf("upgrade_progressed events = %d, want %d", len(page.Events), len(wantPhases))
	}
}

// spec: §25.8 — Proceed on a terminal upgrade is rejected.
func TestProceedRejectsTerminal(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	for i := 0; i < 7; i++ {
		if _, err := svc.Proceed(ctx); err != nil {
			t.Fatalf("proceed %d: %v", i, err)
		}
	}
	if _, err := svc.Proceed(ctx); err != upgradeservice.ErrUpgradeTerminal {
		t.Errorf("Proceed past Complete err = %v, want ErrUpgradeTerminal", err)
	}
}

// spec: §25.8 — proceed/pause/rollback with no active upgrade returns
// ErrNoUpgrade.
func TestTransitionsRequireActiveUpgrade(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	for _, fn := range []func() error{
		func() error { _, e := svc.Proceed(ctx); return e },
		func() error { _, e := svc.Pause(ctx, ""); return e },
		func() error { _, e := svc.Rollback(ctx, ""); return e },
		func() error { _, e := svc.Verify(ctx); return e },
	} {
		if err := fn(); err != upgradeservice.ErrNoUpgrade {
			t.Errorf("transition with no upgrade err = %v, want ErrNoUpgrade", err)
		}
	}
}

// spec: §25.8 / §10.5 — rollback is allowed through CRDUpdate and
// rejected from SchemaMigration onward.
func TestRollbackHonorsPointOfNoReturn(t *testing.T) {
	ctx := context.Background()
	// Rollbackable at CRDUpdate.
	svc, audits, _ := newService(t)
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	_, _ = svc.Proceed(ctx) // OpsRoll
	_, _ = svc.Proceed(ctx) // CRDUpdate
	st, err := svc.Rollback(ctx, "preflight regression")
	if err != nil {
		t.Fatalf("Rollback at CRDUpdate: %v", err)
	}
	if st.Phase != upgrade.RolledBack {
		t.Errorf("phase = %s, want RolledBack", st.Phase)
	}
	last := (*audits)[len(*audits)-1]
	if last.Type != string(audit.EventPlatformUpgradeRolledBack) || last.Detail != "preflight regression" {
		t.Errorf("rollback audit = %+v", last)
	}

	// Not rollbackable at SchemaMigration.
	svc2, _, _ := newService(t)
	_, _ = svc2.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	for i := 0; i < 3; i++ {
		_, _ = svc2.Proceed(ctx) // → OpsRoll → CRDUpdate → SchemaMigration
	}
	if _, err := svc2.Rollback(ctx, "too late"); err != upgradeservice.ErrNotRollbackable {
		t.Errorf("Rollback at SchemaMigration err = %v, want ErrNotRollbackable", err)
	}
}

// spec: §25.8 — pause marks the upgrade awaiting proceed and audits
// platform.upgrade_paused; the next proceed clears it.
func TestPauseAndResumeViaProceed(t *testing.T) {
	svc, audits, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	_, _ = svc.Proceed(ctx) // OpsRoll, paused again
	st, err := svc.Pause(ctx, "operator hold")
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !st.Paused || st.Reason != "operator hold" {
		t.Errorf("paused=%v reason=%q", st.Paused, st.Reason)
	}
	if last := (*audits)[len(*audits)-1]; last.Type != string(audit.EventPlatformUpgradePaused) {
		t.Errorf("last audit = %s, want platform.upgrade_paused", last.Type)
	}
	st, _ = svc.Proceed(ctx) // CRDUpdate
	if st.Paused != true {
		// Non-terminal phases pause again awaiting the next proceed.
		t.Errorf("after proceed paused=%v, want true (awaiting next proceed)", st.Paused)
	}
	if st.Phase != upgrade.CRDUpdate {
		t.Errorf("phase = %s, want CRDUpdate", st.Phase)
	}
}

// spec: §25.8 — verify is valid only at the Verification phase and
// emits platform.upgrade_verified without changing the phase.
func TestVerifyOnlyAtVerification(t *testing.T) {
	svc, audits, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	if _, err := svc.Verify(ctx); err != upgradeservice.ErrNotVerifiable {
		t.Errorf("Verify at Preflight err = %v, want ErrNotVerifiable", err)
	}
	for i := 0; i < 6; i++ {
		_, _ = svc.Proceed(ctx) // → Verification
	}
	st, _, _ := svc.Status(ctx)
	if st.Phase != upgrade.Verification {
		t.Fatalf("phase = %s, want Verification", st.Phase)
	}
	st, err := svc.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !st.Verified || st.Phase != upgrade.Verification {
		t.Errorf("verified=%v phase=%s", st.Verified, st.Phase)
	}
	if last := (*audits)[len(*audits)-1]; last.Type != string(audit.EventPlatformUpgradeVerified) {
		t.Errorf("last audit = %s, want platform.upgrade_verified", last.Type)
	}
}

// spec: §25.8 line 357 — the progress object reports the 1-based step,
// the total, and an operator-facing detail.
func TestProgressObject(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"})
	st, _, _ := svc.Status(ctx)
	p := st.Progress()
	if p["currentStep"] != 1 || p["totalSteps"] != upgrade.TotalSteps {
		t.Errorf("progress = %v", p)
	}
	if p["currentStepDetail"] != "Waiting for operator to call /upgrade/proceed" {
		t.Errorf("detail = %v", p["currentStepDetail"])
	}
}

// spec: §16.7 — every audit event the orchestrator emits is catalogued.
func TestEmittedAuditEventsAreCatalogued(t *testing.T) {
	catalogued := map[string]bool{}
	for _, e := range audit.Catalog() {
		catalogued[e.String()] = true
	}
	for _, ev := range upgradeservice.AuditEventTypes() {
		if !catalogued[ev] {
			t.Errorf("emitted audit event %q is not in the §16.7 catalog", ev)
		}
	}
}

// MemoryStore round-trips the singleton and overwrites a terminal record.
func TestMemoryStoreRoundTrip(t *testing.T) {
	st := upgradeservice.NewMemoryStore()
	if _, ok, _ := st.Load(context.Background()); ok {
		t.Error("empty store reported a record")
	}
	_ = st.Save(context.Background(), upgradeservice.State{OperationID: "u1", Phase: upgrade.Complete})
	got, ok, _ := st.Load(context.Background())
	if !ok || got.OperationID != "u1" {
		t.Errorf("loaded %+v ok=%v", got, ok)
	}
}

// A nil emitter and nil audit sink are no-ops; the phase still advances.
func TestNilEmitterAndAuditAreNoOps(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	ctx := context.Background()
	if _, err := svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st, err := svc.Proceed(ctx); err != nil || st.Phase != upgrade.OpsRoll {
		t.Errorf("Proceed = %s, %v; want OpsRoll, nil", st.Phase, err)
	}
}

// the upgrade_progressed event carries the platform scope as the pool.
func TestUpgradeProgressedCarriesPlatformScope(t *testing.T) {
	svc, _, buf := newService(t)
	ctx := context.Background()
	_, _ = svc.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.5.0", ImageDigest: "sha256:d"})
	_, _ = svc.Proceed(ctx)
	page := buf.Query(0, events.EventFilter{EventType: "upgrade_progressed"}, 10)
	if len(page.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(page.Events))
	}
	var data struct {
		Pool        string `json:"pool"`
		ImageDigest string `json:"imageDigest"`
	}
	_ = json.Unmarshal(page.Events[0].Event.Data, &data)
	if data.Pool != upgradeservice.PlatformScope || data.ImageDigest != "sha256:d" {
		t.Errorf("event data = %+v", data)
	}
}
