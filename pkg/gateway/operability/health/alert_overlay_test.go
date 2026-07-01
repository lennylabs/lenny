// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
)

// fakeAlertSource returns a fixed §25.3 alert-derived verdict per component.
type fakeAlertSource struct {
	byComponent map[string]alertVerdict
}

type alertVerdict struct {
	status health.Status
	firing []string
}

func (f fakeAlertSource) ComponentStatus(component string) (health.Status, []string, bool) {
	v, ok := f.byComponent[component]
	if !ok {
		return "", nil, false
	}
	return v.status, v.firing, true
}

// spec: §25.3 lines 443-451 — a firing critical alert mapped to a component
// reports it unhealthy even when the dependency probe is healthy, and the
// firing alert name surfaces on the Detail line.
func TestAlertOverlayCriticalMarksComponentUnhealthy_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(healthy("postgres"))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"postgres": {status: health.StatusUnhealthy, firing: []string{"SessionStoreUnavailable"}},
	}})

	comp, ok := agg.Component(context.Background(), "postgres")
	if !ok {
		t.Fatal("postgres component not found")
	}
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("status = %q, want unhealthy", comp.Status)
	}
	if !strings.Contains(comp.Detail, "SessionStoreUnavailable") {
		t.Errorf("Detail = %q, want it to name the firing alert", comp.Detail)
	}
}

// spec: §25.3 lines 443-451 — a firing warning alert reports degraded.
func TestAlertOverlayWarningMarksComponentDegraded_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(healthy("redis"))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"redis": {status: health.StatusDegraded, firing: []string{"RedisMemoryHigh"}},
	}})

	comp, _ := agg.Component(context.Background(), "redis")
	if comp.Status != health.StatusDegraded {
		t.Errorf("status = %q, want degraded", comp.Status)
	}
}

// spec: §25.3 lines 443-451 — the overlay only worsens a verdict; a degraded
// alert does not improve an already-unhealthy probe.
func TestAlertOverlayNeverImprovesStatus_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(failing("postgres", health.StatusUnhealthy))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"postgres": {status: health.StatusDegraded, firing: []string{"PostgresReplicationLagHigh"}},
	}})

	comp, _ := agg.Component(context.Background(), "postgres")
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("status = %q, want unhealthy (probe verdict preserved)", comp.Status)
	}
	if strings.Contains(comp.Detail, "PostgresReplicationLagHigh") {
		t.Errorf("Detail = %q, should not append a non-worsening alert", comp.Detail)
	}
}

// An unmapped component (ok=false) keeps its probe verdict.
func TestAlertOverlayUnmappedComponentUnchanged_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(healthy("executor"))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"postgres": {status: health.StatusUnhealthy, firing: []string{"SessionStoreUnavailable"}},
	}})

	comp, _ := agg.Component(context.Background(), "executor")
	if comp.Status != health.StatusHealthy {
		t.Errorf("status = %q, want healthy (no alert maps to executor)", comp.Status)
	}
}

// spec: §25.3 lines 443-451 — the aggregate Report reflects the worst
// alert-overlaid component verdict.
func TestReportAggregatesAlertOverlay_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(healthy("postgres"))
	agg.Register(healthy("redis"))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"postgres": {status: health.StatusUnhealthy, firing: []string{"SessionStoreUnavailable"}},
	}})

	rep := agg.Report(context.Background())
	if rep.Status != health.StatusUnhealthy {
		t.Errorf("aggregate status = %q, want unhealthy", rep.Status)
	}
}

// spec: §10.4 line 386 / §25.3 lines 443-451 — readiness reads raw probes,
// so an alert-only verdict (probe healthy) does not pull the replica out of
// the Service.
func TestHardDependencyStatusIgnoresAlertOverlay_spec_25_3_443(t *testing.T) {
	agg := health.NewAggregatorWithCache(0, nil)
	agg.Register(healthy("postgres"))
	agg.SetAlertSource(fakeAlertSource{byComponent: map[string]alertVerdict{
		"postgres": {status: health.StatusUnhealthy, firing: []string{"SessionStoreUnavailable"}},
	}})

	if got := agg.HardDependencyStatus(context.Background(), "postgres"); got != health.StatusHealthy {
		t.Errorf("readiness status = %q, want healthy (alert overlay must not affect readiness)", got)
	}
	// But the health endpoint view does reflect the alert.
	comp, _ := agg.Component(context.Background(), "postgres")
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("health view status = %q, want unhealthy", comp.Status)
	}
}
