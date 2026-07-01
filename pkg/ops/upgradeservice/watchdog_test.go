// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// mutClock is a settable clock so a test can stamp service transitions at
// one instant and evaluate the watchdog at another.
type mutClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mutClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mutClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// captureEmitter records every operational event emitted.
type captureEmitter struct {
	mu     sync.Mutex
	events []events.OperationalEvent
}

func (e *captureEmitter) Emit(_ context.Context, ev events.OperationalEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
	return nil
}

func (e *captureEmitter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

// stubObserver returns a fixed PodStatus for every phase.
type stubObserver struct {
	status upgradeservice.PodStatus
	calls  int
}

func (o *stubObserver) ObserveNewPod(context.Context, upgrade.Phase) (upgradeservice.PodStatus, error) {
	o.calls++
	return o.status, nil
}

// advanceTo proceeds the upgrade from a fresh start to the target phase,
// stamping each transition at the service clock's current time.
func advanceTo(t *testing.T, svc *upgradeservice.Service, target upgrade.Phase) {
	t.Helper()
	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 8; i++ {
		st, _, _ := svc.Status(context.Background())
		if st.Phase == target {
			return
		}
		if _, err := svc.Proceed(context.Background()); err != nil {
			t.Fatalf("Proceed to %s: %v", target, err)
		}
	}
	t.Fatalf("never reached %s", target)
}

// spec: §25.8 line 3509 — OpsRoll past the timeout with no heartbeat
// auto-rolls-back with OPS_ROLL_TIMEOUT.
func TestWatchdog_OpsRollTimeout_RollsBack_spec_25_8(t *testing.T) {
	t0 := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	svcClk := &mutClock{t: t0}
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore(), Now: svcClk.now})
	advanceTo(t, svc, upgrade.OpsRoll)

	wdClk := &mutClock{t: t0.Add(11 * time.Minute)} // > 600s default
	wd := upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{Service: svc, Now: wdClk.now})
	res, err := wd.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.RolledBack {
		t.Fatalf("res = %+v, want RolledBack", res)
	}
	st, _, _ := svc.Status(context.Background())
	if st.Phase != upgrade.RolledBack || st.Error != upgradeservice.CodeOpsRollTimeout {
		t.Fatalf("state phase=%s error=%q", st.Phase, st.Error)
	}
}

// spec: §25.8 line 3511 — a fresh ops_healthy heartbeat suppresses the
// OpsRoll timeout rollback (the new pod is alive).
func TestWatchdog_FreshHeartbeat_SuppressesRollback_spec_25_8(t *testing.T) {
	t0 := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	svcClk := &mutClock{t: t0}
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore(), Now: svcClk.now})
	advanceTo(t, svc, upgrade.OpsRoll)

	evalAt := t0.Add(11 * time.Minute)
	// Stamp the heartbeat 10s before the watchdog evaluation (within the
	// 60s observation window) and after the OpsRoll entry.
	svcClk.set(evalAt.Add(-10 * time.Second))
	if _, err := svc.RecordOpsHeartbeat(context.Background()); err != nil {
		t.Fatalf("RecordOpsHeartbeat: %v", err)
	}
	wdClk := &mutClock{t: evalAt}
	wd := upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{Service: svc, Now: wdClk.now})
	res, err := wd.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.HeartbeatSuppressed || res.RolledBack {
		t.Fatalf("res = %+v, want HeartbeatSuppressed and no rollback", res)
	}
	st, _, _ := svc.Status(context.Background())
	if st.Phase != upgrade.OpsRoll {
		t.Fatalf("phase = %s, want OpsRoll (suppressed)", st.Phase)
	}
}

// spec: §25.8 line 3510 — a stuck new pod emits
// platform_upgrade_image_pull_failed once per operation+phase, and the
// image-pull-check latency is recorded.
func TestWatchdog_ImagePullFailed_EmitsOnce_spec_25_8(t *testing.T) {
	t0 := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	svcClk := &mutClock{t: t0}
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore(), Now: svcClk.now})
	advanceTo(t, svc, upgrade.OpsRoll)

	em := &captureEmitter{}
	obs := &stubObserver{status: upgradeservice.PodStatus{Stuck: true, Reason: "ImagePullBackOff", ImageRef: "ghcr.io/lennylabs/lenny-ops:1.6.0"}}
	var recorded []string
	wdClk := &mutClock{t: t0.Add(30 * time.Second)} // within timeout
	wd := upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{
		Service:  svc,
		Observer: obs,
		Emitter:  em,
		Record:   func(c string, _ time.Duration) { recorded = append(recorded, c) },
		Now:      wdClk.now,
	})
	res, _ := wd.Evaluate(context.Background())
	if !res.ImagePullFailed || em.count() != 1 {
		t.Fatalf("first eval res=%+v emitted=%d", res, em.count())
	}
	// Second evaluation dedups the event.
	res2, _ := wd.Evaluate(context.Background())
	if res2.ImagePullFailed || em.count() != 1 {
		t.Fatalf("second eval res=%+v emitted=%d (should dedup)", res2, em.count())
	}
	if len(recorded) != 2 || recorded[0] != "ops" {
		t.Fatalf("image-pull-check recordings = %v", recorded)
	}
	if got := em.events[0].Type; got != events.EventPlatformUpgradeImagePullFailed.CloudEventsType() {
		t.Fatalf("event type = %q", got)
	}
}

// spec: §25.8 — the watchdog is a no-op outside a roll phase.
func TestWatchdog_NoOpOutsideRollPhase_spec_25_8(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	// No upgrade started.
	wd := upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{Service: svc})
	res, err := wd.Evaluate(context.Background())
	if err != nil || res.Active {
		t.Fatalf("no-upgrade eval res=%+v err=%v", res, err)
	}
	// Started but in Preflight (not a roll phase).
	_, _ = svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.6.0"})
	res, _ = wd.Evaluate(context.Background())
	if res.Active {
		t.Fatalf("Preflight eval should be inactive: %+v", res)
	}
}

// spec: §25.8 lines 3487, 3549 — a post-SchemaMigration roll phase
// (GatewayRoll) is past the point of no return; a timeout there does not
// auto-rollback (it surfaces the stuck event and leaves rollback to the
// operator).
func TestWatchdog_PostMigrationNoAutoRollback_spec_25_8(t *testing.T) {
	t0 := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	svcClk := &mutClock{t: t0}
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore(), Now: svcClk.now})
	advanceTo(t, svc, upgrade.GatewayRoll)

	wdClk := &mutClock{t: t0.Add(25 * time.Minute)} // > 1200s gateway timeout
	wd := upgradeservice.NewWatchdog(upgradeservice.WatchdogOptions{Service: svc, Now: wdClk.now})
	res, err := wd.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.RolledBack {
		t.Fatalf("GatewayRoll timeout must not auto-rollback: %+v", res)
	}
	st, _, _ := svc.Status(context.Background())
	if st.Phase != upgrade.GatewayRoll {
		t.Fatalf("phase = %s, want GatewayRoll preserved", st.Phase)
	}
}

// spec: §25.8 line 3511 — RecordOpsHeartbeat is a no-op outside OpsRoll.
func TestRecordOpsHeartbeat_OnlyOpsRoll_spec_25_8(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	advanceTo(t, svc, upgrade.CRDUpdate)
	st, err := svc.RecordOpsHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("RecordOpsHeartbeat: %v", err)
	}
	if !st.OpsHeartbeat.IsZero() {
		t.Fatalf("heartbeat stamped outside OpsRoll: %v", st.OpsHeartbeat)
	}
}
