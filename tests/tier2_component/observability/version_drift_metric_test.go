// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §25.8 version-drift metric and its
// PlatformVersionDrift alert. pkg/ops/upgradeservice's unit tests cover
// the pure Aggregate arithmetic (DriftCount, VersionDrift) and
// cmd/lenny-ops's unit tests cover the package-private
// lenny_platform_version_drift GaugeVec in isolation, but no test wires
// the two together the way lenny-ops does at runtime: the aggregator's
// DriftGauge callback setting a real Prometheus gauge that a scrape
// reads. This test builds that wiring with the production metrics
// package (the same NewGauge/MustRegister lenny-ops calls) over a test
// registry, runs the version aggregator with a drifted component, and
// gathers the registered series the way a Prometheus scrape would,
// closing the gap between "the aggregator computed drift" and "the
// gauge a scrape reads reflects it".

package observability_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// platformVersionDriftRule returns the shipped PlatformVersionDrift
// alert from the §16.5 catalog.
func platformVersionDriftRule(t *testing.T) rules.Rule {
	t.Helper()
	for _, r := range rules.Catalog() {
		if r.Name == "PlatformVersionDrift" {
			return r
		}
	}
	t.Fatalf("PlatformVersionDrift not present in the §16.5 alert catalog")
	return rules.Rule{}
}

// gatherVersionDriftGauge pulls the lenny_platform_version_drift gauge's
// sole (label-less) sample from reg, the way a Prometheus scrape of
// /metrics would, or fails if no such series was registered.
func gatherVersionDriftGauge(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != "lenny_platform_version_drift" {
			continue
		}
		if len(fam.GetMetric()) != 1 {
			t.Fatalf("lenny_platform_version_drift has %d series, want 1 (no labels)", len(fam.GetMetric()))
		}
		return fam.GetMetric()[0].GetGauge().GetValue()
	}
	t.Fatalf("lenny_platform_version_drift not found in the gathered families; the gauge was never registered or never scraped")
	return 0
}

// spec: §25.8 Version Aggregation (line 3374) — "When any component's
// current version does not match the compiled-in required version, the
// response includes \"versionDrift\": true"; §25.8 Metrics (line 3618)
// — "lenny_platform_version_drift | Gauge | | 1 if any component
// version drift, 0 otherwise"; §25.8 Alerting Rules (line 3627) —
// "PlatformVersionDrift | lenny_platform_version_drift == 1 for > 5m |
// Warning".
//
// diagnosis: a failure here means the aggregator-to-gauge wiring lenny-ops
// performs at runtime (upgradeservice.VersionAggregatorOptions.Gauge
// calling a registered prometheus.GaugeVec, see
// cmd/lenny-ops/deps.go's platformVersionDrift/setPlatformVersionDrift)
// is broken: either the gauge is never set after Aggregate, the value it
// carries does not track DriftCount, or the metric name/label shape has
// drifted from what the PlatformVersionDrift alert expression selects.
// Inspect pkg/ops/upgradeservice.VersionAggregator.Aggregate's call to
// a.gauge and the GaugeOpts/label set the production wiring registers.
func TestVersionDriftMetricScrapesAfterAggregation(t *testing.T) {
	rule := platformVersionDriftRule(t)
	if _, err := parser.ParseExpr(rule.Expr); err != nil {
		t.Fatalf("PlatformVersionDrift expr does not parse as PromQL: %v", err)
	}
	if rule.Expr != "lenny_platform_version_drift > 0" {
		t.Fatalf("PlatformVersionDrift expr = %q, want %q; the assertions below assume this threshold",
			rule.Expr, "lenny_platform_version_drift > 0")
	}

	// Build the gauge exactly as lenny-ops does: through the shared
	// metrics package (label-hygiene validated), registered on its own
	// registry so this test's scrape reads only what it wrote.
	reg := prometheus.NewRegistry()
	gauge, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_platform_version_drift",
		Help: "test double of the §25.8 lenny_platform_version_drift gauge",
	}, []string{})
	if err != nil {
		t.Fatalf("metrics.NewGauge: %v", err)
	}
	metrics.MustRegister(reg, gauge)

	setDriftGauge := func(count int) {
		gauge.WithLabelValues().Set(float64(count))
	}

	const buildVersion = "9.9.9-test"
	newAgg := func(driftedCurrent string) *upgradeservice.VersionAggregator {
		return upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
			PlatformVersion: buildVersion,
			Sources: []upgradeservice.VersionSource{
				upgradeservice.NewFuncVersionSource("ops", buildVersion, func(context.Context) (string, error) {
					return buildVersion, nil
				}),
				upgradeservice.NewFuncVersionSource("controllers", buildVersion, func(context.Context) (string, error) {
					return driftedCurrent, nil
				}),
			},
			Gauge: setDriftGauge,
		})
	}

	// Drifted: the controllers source reports a version that does not
	// match its required buildVersion.
	drifted := newAgg("1.0.0").Aggregate(context.Background())
	if !drifted.VersionDrift {
		t.Fatalf("VersionDrift = false, want true (controllers current 1.0.0 != required %s)", buildVersion)
	}
	if drifted.DriftCount != 1 {
		t.Fatalf("DriftCount = %d, want 1", drifted.DriftCount)
	}
	if got := gatherVersionDriftGauge(t, reg); got != 1 {
		t.Errorf("lenny_platform_version_drift scraped as %v after a drifted aggregation, want 1", got)
	} else if !(got > 0) {
		t.Errorf("scraped value %v does not satisfy the PlatformVersionDrift expr %q", got, rule.Expr)
	}

	// No drift: the controllers source now reports the required version.
	clean := newAgg(buildVersion).Aggregate(context.Background())
	if clean.VersionDrift {
		t.Fatalf("VersionDrift = true, want false (controllers current matches required %s)", buildVersion)
	}
	if clean.DriftCount != 0 {
		t.Fatalf("DriftCount = %d, want 0", clean.DriftCount)
	}
	if got := gatherVersionDriftGauge(t, reg); got != 0 {
		t.Errorf("lenny_platform_version_drift scraped as %v after a clean aggregation, want 0", got)
	} else if got > 0 {
		t.Errorf("scraped value %v would incorrectly satisfy the PlatformVersionDrift expr %q", got, rule.Expr)
	}
}
