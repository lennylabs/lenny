// SPDX-License-Identifier: MIT

package rules

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// parsedDoc is the subset of an OpenSLO document the renderer tests
// assert on.
type parsedDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Service       string   `yaml:"service"`
		IndicatorRef  string   `yaml:"indicatorRef"`
		AlertPolicies []string `yaml:"alertPolicies"`
		Objectives    []struct {
			Target float64 `yaml:"target"`
		} `yaml:"objectives"`
		RatioMetric struct {
			Counter bool `yaml:"counter"`
		} `yaml:"ratioMetric"`
		Conditions []struct {
			Spec struct {
				Severity  string `yaml:"severity"`
				Condition struct {
					Threshold      int    `yaml:"threshold"`
					LookbackWindow string `yaml:"lookbackWindow"`
				} `yaml:"condition"`
			} `yaml:"spec"`
		} `yaml:"conditions"`
	} `yaml:"spec"`
}

func parseOpenSLO(t *testing.T, b []byte) []parsedDoc {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []parsedDoc
	for {
		var d parsedDoc
		err := dec.Decode(&d)
		if err != nil {
			break
		}
		docs = append(docs, d)
	}
	return docs
}

// TestRenderOpenSLOEmitsThreeDocsPerSLO verifies the §16.10 export emits
// an SLI, an SLO, and an AlertPolicy document for every §16.5 SLO, each a
// well-formed OpenSLO v1 document.
//
// spec: §16.10 lines 732-736.
func TestRenderOpenSLOEmitsThreeDocsPerSLO(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1")
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	docs := parseOpenSLO(t, out)
	defs := SLODefinitions()
	if got, want := len(docs), len(defs)*3; got != want {
		t.Fatalf("rendered %d documents, want %d (SLI+SLO+AlertPolicy per SLO)", got, want)
	}
	kinds := map[string]int{}
	for _, d := range docs {
		if d.APIVersion != openSLOAPIVersion {
			t.Errorf("document %q apiVersion = %q, want %q", d.Metadata.Name, d.APIVersion, openSLOAPIVersion)
		}
		if d.Metadata.Labels["deployment_tier"] != "tier1" {
			t.Errorf("document %q deployment_tier label = %q, want tier1", d.Metadata.Name, d.Metadata.Labels["deployment_tier"])
		}
		kinds[d.Kind]++
	}
	for _, k := range []string{"SLI", "SLO", "AlertPolicy"} {
		if kinds[k] != len(defs) {
			t.Errorf("rendered %d %s documents, want %d", kinds[k], k, len(defs))
		}
	}
}

// TestRenderOpenSLOObjectivesMatchCatalog confirms each SLO document's
// objective target equals the catalog target and that the SLO references
// its SLI and AlertPolicy by name (referential integrity for downstream
// OpenSLO tooling).
func TestRenderOpenSLOObjectivesMatchCatalog(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1")
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	docs := parseOpenSLO(t, out)
	targets := map[string]float64{}
	names := map[string]bool{}
	for _, d := range SLODefinitions() {
		targets[d.Name] = d.Target
	}
	for _, d := range docs {
		names[d.Metadata.Name] = true
	}
	for _, d := range docs {
		if d.Kind != "SLO" {
			continue
		}
		want, ok := targets[d.Metadata.Name]
		if !ok {
			t.Errorf("SLO document %q has no matching catalog entry", d.Metadata.Name)
			continue
		}
		if len(d.Spec.Objectives) != 1 || d.Spec.Objectives[0].Target != want {
			t.Errorf("SLO %q objective target mismatch: got %+v want %v", d.Metadata.Name, d.Spec.Objectives, want)
		}
		if d.Spec.Service != OpenSLOService {
			t.Errorf("SLO %q service = %q, want %q", d.Metadata.Name, d.Spec.Service, OpenSLOService)
		}
		if !names[d.Spec.IndicatorRef] {
			t.Errorf("SLO %q references SLI %q that is not rendered", d.Metadata.Name, d.Spec.IndicatorRef)
		}
		for _, ap := range d.Spec.AlertPolicies {
			if !names[ap] {
				t.Errorf("SLO %q references AlertPolicy %q that is not rendered", d.Metadata.Name, ap)
			}
		}
	}
}

// TestRenderOpenSLOBurnRateMatchesAlerts confirms every AlertPolicy
// carries the §16.5 multi-window burn-rate conditions (1h/14x critical,
// 6h/3x warning), identical to the burn-rate alert windows.
//
// spec: §16.5 line 627.
func TestRenderOpenSLOBurnRateMatchesAlerts(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1")
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	for _, d := range parseOpenSLO(t, out) {
		if d.Kind != "AlertPolicy" {
			continue
		}
		if len(d.Spec.Conditions) != 2 {
			t.Errorf("AlertPolicy %q has %d conditions, want 2 (fast+slow)", d.Metadata.Name, len(d.Spec.Conditions))
			continue
		}
		fast := d.Spec.Conditions[0].Spec
		slow := d.Spec.Conditions[1].Spec
		if fast.Condition.Threshold != burnRateFastMultiplier || fast.Condition.LookbackWindow != "1h" || fast.Severity != "critical" {
			t.Errorf("AlertPolicy %q fast condition = %+v, want 14x/1h/critical", d.Metadata.Name, fast)
		}
		if slow.Condition.Threshold != burnRateSlowMultiplier || slow.Condition.LookbackWindow != "6h" || slow.Severity != "warning" {
			t.Errorf("AlertPolicy %q slow condition = %+v, want 3x/6h/warning", d.Metadata.Name, slow)
		}
	}
}

// TestRenderOpenSLOReferencesCanonicalMetrics asserts the rendered
// queries reference the canonical §16.5 metric names (R-002).
func TestRenderOpenSLOReferencesCanonicalMetrics(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1")
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	s := string(out)
	for _, metric := range []string{
		"lenny_session_creation_error_ratio",
		"lenny_session_creation_duration_seconds_bucket",
		"lenny_session_unavailability_ratio",
		"lenny_gateway_unavailability_ratio",
		"lenny_session_startup_duration_seconds_bucket",
		"lenny_session_time_to_first_token_seconds_bucket",
		"lenny_checkpoint_duration_slow_ratio",
	} {
		if !strings.Contains(s, metric) {
			t.Errorf("rendered OpenSLO does not reference canonical metric %q", metric)
		}
	}
}

// TestRenderOpenSLOTierSubstitution confirms the tier argument replaces
// the placeholder in both labels and queries, leaving no placeholder
// behind (R-003).
func TestRenderOpenSLOTierSubstitution(t *testing.T) {
	for _, tier := range []string{"tier1", "tier2", "tier3"} {
		out, err := RenderOpenSLO(OpenSLOService, tier)
		if err != nil {
			t.Fatalf("RenderOpenSLO(%q): %v", tier, err)
		}
		s := string(out)
		if strings.Contains(s, SLOTierPlaceholder) {
			t.Errorf("tier %q: placeholder %q not substituted", tier, SLOTierPlaceholder)
		}
		if !strings.Contains(s, `deployment_tier: `+tier) {
			t.Errorf("tier %q: no deployment_tier label", tier)
		}
		if !strings.Contains(s, `deployment_tier="`+tier+`"`) {
			t.Errorf("tier %q: no scoped query matcher", tier)
		}
	}
}

// TestRenderOpenSLODefaultPlaceholderRoundTrips confirms rendering with
// the placeholder tier (the chart-fragment form) leaves the placeholder
// intact for Helm to substitute.
func TestRenderOpenSLODefaultPlaceholderRoundTrips(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, SLOTierPlaceholder)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	if !strings.Contains(string(out), SLOTierPlaceholder) {
		t.Error("placeholder-tier render dropped the placeholder the chart substitutes")
	}
}

// TestRenderOpenSLORejectsBadInput exercises the input guards.
func TestRenderOpenSLORejectsBadInput(t *testing.T) {
	if _, err := RenderOpenSLO("", "tier1"); err == nil {
		t.Error("empty service did not error")
	}
	if _, err := RenderOpenSLO(OpenSLOService, ""); err == nil {
		t.Error("empty tier did not error")
	}
}
