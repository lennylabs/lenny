// SPDX-License-Identifier: MIT

package rules

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func renderCatalog(t *testing.T, name string) prometheusRule {
	t.Helper()
	out, err := RenderPrometheusRule(name, Catalog())
	if err != nil {
		t.Fatalf("RenderPrometheusRule: %v", err)
	}
	var doc prometheusRule
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal rendered manifest: %v\n%s", err, out)
	}
	return doc
}

func TestRenderPrometheusRuleStructure(t *testing.T) {
	doc := renderCatalog(t, "lenny-alerts")
	if doc.APIVersion != "monitoring.coreos.com/v1" || doc.Kind != "PrometheusRule" {
		t.Errorf("apiVersion/kind = %q/%q", doc.APIVersion, doc.Kind)
	}
	if doc.Metadata.Name != "lenny-alerts" {
		t.Errorf("metadata.name = %q, want lenny-alerts", doc.Metadata.Name)
	}
	if len(doc.Spec.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(doc.Spec.Groups))
	}
	rendered := map[string]bool{}
	for _, r := range doc.Spec.Groups[0].Rules {
		rendered[r.Alert] = true
	}
	for _, r := range Catalog() {
		if !rendered[r.Name] {
			t.Errorf("rendered manifest is missing catalogue rule %q", r.Name)
		}
	}
}

func TestRenderPrometheusRuleFields(t *testing.T) {
	doc := renderCatalog(t, "lenny-alerts")
	var got *promAlertRule
	for i := range doc.Spec.Groups[0].Rules {
		if doc.Spec.Groups[0].Rules[i].Alert == "WarmPoolExhausted" {
			got = &doc.Spec.Groups[0].Rules[i]
		}
	}
	if got == nil {
		t.Fatal("WarmPoolExhausted was not rendered")
	}
	if got.Expr == "" {
		t.Error("expr is empty")
	}
	if got.For != "1m" {
		t.Errorf("for = %q, want 1m", got.For)
	}
	if got.Labels["severity"] != "critical" {
		t.Errorf("severity label = %q, want critical", got.Labels["severity"])
	}
	if got.Annotations["summary"] == "" {
		t.Error("summary annotation is empty")
	}
	if got.Annotations["runbook_url"] == "" {
		t.Error("runbook_url annotation is empty for a critical alert")
	}
}

func TestRenderPrometheusRuleOmitsZeroFor(t *testing.T) {
	doc := renderCatalog(t, "lenny-alerts")
	for _, r := range doc.Spec.Groups[0].Rules {
		if r.Alert == "CertExpiryImminent" && r.For != "" {
			t.Errorf("CertExpiryImminent rendered for=%q, want it omitted (no sustain window)", r.For)
		}
	}
}

func TestRenderPrometheusRuleRejectsInvalidCatalogue(t *testing.T) {
	bad := []Rule{{Name: "Bad", Expr: ")(", Severity: SeverityWarning, Summary: "unparseable"}}
	if _, err := RenderPrometheusRule("x", bad); err == nil {
		t.Error("rendering a catalogue with an invalid rule must fail")
	}
}

func TestRenderPrometheusRuleRejectsEmptyName(t *testing.T) {
	if _, err := RenderPrometheusRule("", Catalog()); err == nil {
		t.Error("an empty PrometheusRule name must be rejected")
	}
}

func TestPrometheusDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{60 * time.Second, "1m"},
		{120 * time.Second, "2m"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "90s"},
		{0, "0s"},
		{1 * time.Hour, "1h"},
		{6 * time.Hour, "6h"},
		{72 * time.Hour, "72h"},
		{5 * time.Minute, "5m"},
	}
	for _, tc := range cases {
		if got := prometheusDuration(tc.d); got != tc.want {
			t.Errorf("prometheusDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestRenderPrometheusRuleEmitsSLOAnnotation verifies the burn-rate
// rules carry their §16.5 SLO as the "slo" annotation.
func TestRenderPrometheusRuleEmitsSLOAnnotation(t *testing.T) {
	doc := renderCatalog(t, "lenny-alerts")
	var got *promAlertRule
	for i := range doc.Spec.Groups[0].Rules {
		if doc.Spec.Groups[0].Rules[i].Alert == "SessionCreationLatencyBurnRate" {
			got = &doc.Spec.Groups[0].Rules[i]
		}
	}
	if got == nil {
		t.Fatal("SessionCreationLatencyBurnRate was not rendered")
	}
	if got.Annotations["slo"] == "" {
		t.Error("a burn-rate rule must carry an slo annotation")
	}
	if got.For != "1h" {
		t.Errorf("burn-rate fast window for = %q, want 1h", got.For)
	}
}

// TestRenderPrometheusRuleRendersFullCatalog verifies the rendered
// manifest carries every §16.5 catalog rule with a parseable expr and
// the required severity label.
func TestRenderPrometheusRuleRendersFullCatalog(t *testing.T) {
	doc := renderCatalog(t, "lenny-alerts")
	rendered := doc.Spec.Groups[0].Rules
	if len(rendered) != len(Catalog()) {
		t.Fatalf("rendered %d rules, catalog has %d", len(rendered), len(Catalog()))
	}
	for _, r := range rendered {
		if r.Expr == "" {
			t.Errorf("rendered rule %q has an empty expr", r.Alert)
		}
		if r.Labels["severity"] == "" {
			t.Errorf("rendered rule %q has no severity label", r.Alert)
		}
	}
}
