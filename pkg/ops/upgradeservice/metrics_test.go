// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// gatherMetric returns the value of the named gauge with the given
// target_version label from a registry, or (0, false) when absent.
func gatherMetric(t *testing.T, reg *prometheus.Registry, name, targetVersion string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "target_version" && lp.GetValue() == targetVersion {
					return m.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func collectorService(t *testing.T, clock *time.Time) *upgradeservice.Service {
	t.Helper()
	return upgradeservice.New(upgradeservice.Options{
		Store: upgradeservice.NewMemoryStore(),
		Now:   func() time.Time { return *clock },
		NewID: func() string { return "upgrade-metrics" },
	})
}

// spec: §25.8 Metrics — with no upgrade recorded the collector emits
// nothing (the gauges are absent, not zero).
func TestMetricsCollectorAbsentWhenNoUpgrade(t *testing.T) {
	clock := time.Unix(1700000000, 0).UTC()
	svc := collectorService(t, &clock)
	reg := prometheus.NewRegistry()
	reg.MustRegister(upgradeservice.NewMetricsCollector(svc))

	if _, ok := gatherMetric(t, reg, "lenny_platform_upgrade_phase", "1.5.0"); ok {
		t.Error("phase gauge present with no upgrade, want absent")
	}
	if _, ok := gatherMetric(t, reg, "lenny_platform_upgrade_duration_seconds", "1.5.0"); ok {
		t.Error("duration gauge present with no upgrade, want absent")
	}
}

// spec: §25.8 Metrics — the phase gauge reports the working-phase step
// and the duration gauge advances with the clock while active.
func TestMetricsCollectorPhaseAndDuration(t *testing.T) {
	clock := time.Unix(1700000000, 0).UTC()
	svc := collectorService(t, &clock)
	reg := prometheus.NewRegistry()
	reg.MustRegister(upgradeservice.NewMetricsCollector(svc))

	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Preflight is step 1.
	if v, ok := gatherMetric(t, reg, "lenny_platform_upgrade_phase", "1.5.0"); !ok || v != 1 {
		t.Errorf("phase = %v (present=%v), want 1", v, ok)
	}
	// Advance the clock 90s; duration tracks now-startedAt.
	clock = clock.Add(90 * time.Second)
	if v, ok := gatherMetric(t, reg, "lenny_platform_upgrade_duration_seconds", "1.5.0"); !ok || v != 90 {
		t.Errorf("duration = %v (present=%v), want 90", v, ok)
	}
	// Advance to OpsRoll (step 2).
	if _, err := svc.Proceed(context.Background()); err != nil {
		t.Fatalf("Proceed: %v", err)
	}
	if v, ok := gatherMetric(t, reg, "lenny_platform_upgrade_phase", "1.5.0"); !ok || v != 2 {
		t.Errorf("phase after proceed = %v, want 2", v)
	}
}

// spec: §25.8 — a terminal upgrade reports phase 0 so PlatformUpgradeStuck
// (phase > 0) does not fire, and the duration freezes at completion.
func TestMetricsCollectorTerminalPhaseZero(t *testing.T) {
	clock := time.Unix(1700000000, 0).UTC()
	svc := collectorService(t, &clock)
	reg := prometheus.NewRegistry()
	reg.MustRegister(upgradeservice.NewMetricsCollector(svc))

	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.5.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Drive through all seven phases to Complete.
	for i := 0; i < 7; i++ {
		clock = clock.Add(10 * time.Second)
		if _, err := svc.Proceed(context.Background()); err != nil {
			t.Fatalf("Proceed %d: %v", i, err)
		}
	}
	if v, ok := gatherMetric(t, reg, "lenny_platform_upgrade_phase", "1.5.0"); !ok || v != 0 {
		t.Errorf("terminal phase = %v (present=%v), want 0", v, ok)
	}
	// Duration freezes at the terminal transition (70s of advancement).
	froze, ok := gatherMetric(t, reg, "lenny_platform_upgrade_duration_seconds", "1.5.0")
	if !ok || froze != 70 {
		t.Errorf("frozen duration = %v, want 70", froze)
	}
	clock = clock.Add(1000 * time.Second)
	if again, _ := gatherMetric(t, reg, "lenny_platform_upgrade_duration_seconds", "1.5.0"); again != froze {
		t.Errorf("duration advanced after terminal: %v != %v", again, froze)
	}
}
