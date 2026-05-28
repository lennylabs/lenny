// SPDX-License-Identifier: MIT

package alertingmetrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestNewRegistersSurface_spec_25_13_4833 asserts the three §25.13
// alerting-observability metrics land on the supplied registerer with
// the §25.13 line 4833-4835 names. The two gauges expose their
// closed-enum series at boot; the histogram only emits once observed,
// so an observation is recorded to flush its descriptor.
func TestNewRegistersSurface_spec_25_13_4833(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveRuleEvalDuration("WarmPoolExhausted", time.Millisecond)
	wantNames := []string{
		"lenny_alerting_rules_bundled",
		"lenny_alerting_rule_overrides",
		"lenny_alerting_rule_eval_duration_seconds",
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := map[string]bool{}
	for _, f := range families {
		got[f.GetName()] = true
	}
	for _, name := range wantNames {
		if !got[name] {
			t.Errorf("registry missing §25.13 metric %q", name)
		}
	}
}

// TestSetBundledFormatsStampsClosedEnum_spec_25_13_4833 asserts every
// closed-enum format gets a series so an unrendered format reads as 0
// rather than absent.
func TestSetBundledFormatsStampsClosedEnum_spec_25_13_4833(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetBundledFormats(alertingmetrics.FormatPrometheusRule)
	if got := gaugeValue(t, reg, "lenny_alerting_rules_bundled", "format", "prometheusrule"); got != 1.0 {
		t.Errorf("prometheusrule = %v, want 1", got)
	}
	if got := gaugeValue(t, reg, "lenny_alerting_rules_bundled", "format", "configmap"); got != 0.0 {
		t.Errorf("configmap (unrendered) = %v, want 0", got)
	}
	m.SetBundledFormats(alertingmetrics.FormatPrometheusRule, alertingmetrics.FormatConfigMap)
	if got := gaugeValue(t, reg, "lenny_alerting_rules_bundled", "format", "configmap"); got != 1.0 {
		t.Errorf("configmap after both = %v, want 1", got)
	}
	// Unknown labels are silently dropped — never registered.
	m.SetBundledFormats("yaml-cron-tab")
	if got := gaugeValue(t, reg, "lenny_alerting_rules_bundled", "format", "configmap"); got != 0.0 {
		t.Errorf("configmap after dropping unknown = %v, want 0 (reset by re-stamp)", got)
	}
}

// TestSetOverrideCountStampsGauge_spec_25_13_4834 asserts the override
// gauge takes the supplied count.
func TestSetOverrideCountStampsGauge_spec_25_13_4834(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetOverrideCount(7)
	if got := gaugeValue(t, reg, "lenny_alerting_rule_overrides"); got != 7 {
		t.Errorf("overrides = %v, want 7", got)
	}
	m.SetOverrideCount(0)
	if got := gaugeValue(t, reg, "lenny_alerting_rule_overrides"); got != 0 {
		t.Errorf("overrides reset = %v, want 0", got)
	}
}

// TestObserveRuleEvalDurationRecordsHistogram_spec_25_13_4835 asserts
// the per-rule histogram receives observations under the supplied rule
// label.
func TestObserveRuleEvalDurationRecordsHistogram_spec_25_13_4835(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m, err := alertingmetrics.New(reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveRuleEvalDuration("WarmPoolExhausted", 12*time.Millisecond)
	m.ObserveRuleEvalDuration("WarmPoolExhausted", 50*time.Millisecond)
	body := familyText(t, reg, "lenny_alerting_rule_eval_duration_seconds")
	if !strings.Contains(body, `WarmPoolExhausted`) {
		t.Errorf("histogram body missing rule label: %s", body)
	}
	if count := histogramCount(t, reg, "lenny_alerting_rule_eval_duration_seconds", "rule", "WarmPoolExhausted"); count != 2 {
		t.Errorf("histogram count = %d, want 2", count)
	}
}

// TestNewRejectsDoubleRegistration_spec_25_13_4833 confirms double-
// registration on the same registerer is a wiring bug rather than a
// silent overwrite.
func TestNewRejectsDoubleRegistration_spec_25_13_4833(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	if _, err := alertingmetrics.New(reg); err != nil {
		t.Fatalf("first New: %v", err)
	}
	if _, err := alertingmetrics.New(reg); err == nil {
		t.Fatal("expected second New on same registry to error")
	}
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string, labelKV ...string) float64 {
	t.Helper()
	m := findMetric(t, reg, name, labelKV...)
	if m == nil {
		return 0
	}
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	t.Fatalf("metric %q is not a gauge", name)
	return 0
}

func histogramCount(t *testing.T, reg *prometheus.Registry, name string, labelKV ...string) uint64 {
	t.Helper()
	m := findMetric(t, reg, name, labelKV...)
	if m == nil {
		return 0
	}
	if h := m.GetHistogram(); h != nil {
		return h.GetSampleCount()
	}
	t.Fatalf("metric %q is not a histogram", name)
	return 0
}

func findMetric(t *testing.T, reg *prometheus.Registry, name string, labelKV ...string) *dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	wantLabels := map[string]string{}
	for i := 0; i+1 < len(labelKV); i += 2 {
		wantLabels[labelKV[i]] = labelKV[i+1]
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			match := true
			for k, v := range wantLabels {
				if labels[k] != v {
					match = false
					break
				}
			}
			if match {
				return m
			}
		}
	}
	t.Fatalf("metric %q with labels %v not found", name, wantLabels)
	return nil
}

func familyText(t *testing.T, reg *prometheus.Registry, name string) string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f.String()
		}
	}
	t.Fatalf("metric %q not found", name)
	return ""
}
