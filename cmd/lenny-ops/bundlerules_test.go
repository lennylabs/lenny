// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
)

// spec: §25.4 line 1339 / §25.13 lines 4833-4834 — the leader-only
// bundleRules reconciler re-stamps the bundled-rules observability
// gauges from the chart-supplied format set and override count. §25.13
// line 4816 forbids runtime rule mutation, so the reconciler only
// re-asserts the metrics; it never re-renders rules.
func TestBundleRulesReconciler_StampsGauges_spec_25_4_17(t *testing.T) {
	reg := prometheus.NewRegistry()
	mx, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("alertingmetrics.New: %v", err)
	}
	reconcile := bundleRulesReconciler(mx, []string{"prometheusrule", "configmap"}, 3)
	if err := reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	for format, want := range map[string]float64{"prometheusrule": 1, "configmap": 1} {
		got := testutil.ToFloat64(prometheusGauge(t, reg, "lenny_alerting_rules_bundled", format))
		if got != want {
			t.Errorf("lenny_alerting_rules_bundled{format=%q} = %v, want %v", format, got, want)
		}
	}
	if got := testutil.ToFloat64(prometheusGauge(t, reg, "lenny_alerting_rule_overrides", "")); got != 3 {
		t.Errorf("lenny_alerting_rule_overrides = %v, want 3", got)
	}

	// The reconciler is idempotent: a second tick re-asserts the same
	// values rather than accumulating.
	if err := reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile (2nd): %v", err)
	}
	if got := testutil.ToFloat64(prometheusGauge(t, reg, "lenny_alerting_rule_overrides", "")); got != 3 {
		t.Errorf("lenny_alerting_rule_overrides after re-tick = %v, want 3", got)
	}
}

// spec: §25.13 line 4705 — the format selector is closed-enum; a single
// rendered format leaves the other at 0 rather than absent.
func TestBundleRulesReconciler_SingleFormat_spec_25_4_17(t *testing.T) {
	reg := prometheus.NewRegistry()
	mx, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("alertingmetrics.New: %v", err)
	}
	if err := bundleRulesReconciler(mx, []string{"prometheusrule"}, 0)(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := testutil.ToFloat64(prometheusGauge(t, reg, "lenny_alerting_rules_bundled", "prometheusrule")); got != 1 {
		t.Errorf("lenny_alerting_rules_bundled{prometheusrule} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(prometheusGauge(t, reg, "lenny_alerting_rules_bundled", "configmap")); got != 0 {
		t.Errorf("lenny_alerting_rules_bundled{configmap} = %v, want 0 (unrendered)", got)
	}
}

// prometheusGauge fetches a single gauge series from a registry by metric
// name and optional format label.
func prometheusGauge(t *testing.T, reg *prometheus.Registry, name, format string) prometheus.Gauge {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "probe"})
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			matched := format == ""
			for _, l := range m.GetLabel() {
				if l.GetName() == "format" && l.GetValue() == format {
					matched = true
				}
			}
			if matched {
				g.Set(m.GetGauge().GetValue())
				return g
			}
		}
	}
	t.Fatalf("metric %q (format=%q) not found", name, format)
	return nil
}
