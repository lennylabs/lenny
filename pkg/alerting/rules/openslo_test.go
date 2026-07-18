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
		Service      string `yaml:"service"`
		IndicatorRef string `yaml:"indicatorRef"`
		// Target is spec.target on the AlertNotificationTarget document.
		Target        string `yaml:"target"`
		AlertPolicies []struct {
			AlertPolicyRef string `yaml:"alertPolicyRef"`
		} `yaml:"alertPolicies"`
		NotificationTargets []struct {
			TargetRef string `yaml:"targetRef"`
		} `yaml:"notificationTargets"`
		Objectives []struct {
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

// TestRenderOpenSLOEmitsFourDocsPerSLOPlusSharedTarget verifies the §16.10
// export emits an SLI, an SLO, and two single-condition AlertPolicy
// documents (fast + slow) for every §16.5 SLO, plus one shared
// AlertNotificationTarget document for the whole fragment. Each is a
// well-formed OpenSLO v1 document.
//
// spec: §16.10 lines 742-746.
func TestRenderOpenSLOEmitsFourDocsPerSLOPlusSharedTarget(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1", OpenSLODefaultNotificationTarget)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	docs := parseOpenSLO(t, out)
	defs := SLODefinitions()
	if got, want := len(docs), len(defs)*4+1; got != want {
		t.Fatalf("rendered %d documents, want %d (SLI+SLO+2 AlertPolicy per SLO, plus 1 shared AlertNotificationTarget)", got, want)
	}
	kinds := map[string]int{}
	for _, d := range docs {
		if d.APIVersion != openSLOAPIVersion {
			t.Errorf("document %q apiVersion = %q, want %q", d.Metadata.Name, d.APIVersion, openSLOAPIVersion)
		}
		// The shared notification target is fragment-scoped, not per-tier,
		// so it carries no deployment_tier label.
		if d.Kind != "AlertNotificationTarget" && d.Metadata.Labels["deployment_tier"] != "tier1" {
			t.Errorf("document %q deployment_tier label = %q, want tier1", d.Metadata.Name, d.Metadata.Labels["deployment_tier"])
		}
		kinds[d.Kind]++
	}
	if kinds["SLI"] != len(defs) {
		t.Errorf("rendered %d SLI documents, want %d", kinds["SLI"], len(defs))
	}
	if kinds["SLO"] != len(defs) {
		t.Errorf("rendered %d SLO documents, want %d", kinds["SLO"], len(defs))
	}
	if kinds["AlertPolicy"] != len(defs)*2 {
		t.Errorf("rendered %d AlertPolicy documents, want %d (fast+slow per SLO)", kinds["AlertPolicy"], len(defs)*2)
	}
	if kinds["AlertNotificationTarget"] != 1 {
		t.Errorf("rendered %d AlertNotificationTarget documents, want exactly 1 shared", kinds["AlertNotificationTarget"])
	}
}

// TestRenderOpenSLOSharedNotificationTarget asserts every AlertPolicy
// carries a present, non-empty notificationTargets whose single targetRef
// equals the shared target name, and that exactly one AlertNotificationTarget
// document with that metadata.name is rendered with a non-empty spec.target.
// OpenSLO v1 requires notificationTargets present and non-empty, and a
// dangling targetRef (no emitted target) is not self-contained.
//
// spec: §16.10 (required non-empty notificationTargets referencing an
// emitted AlertNotificationTarget).
func TestRenderOpenSLOSharedNotificationTarget(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1", OpenSLODefaultNotificationTarget)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	docs := parseOpenSLO(t, out)
	const wantTarget = "lenny-slo-notifications"
	var targets int
	for _, d := range docs {
		switch d.Kind {
		case "AlertPolicy":
			if len(d.Spec.NotificationTargets) != 1 {
				t.Errorf("AlertPolicy %q has %d notificationTargets, want exactly 1", d.Metadata.Name, len(d.Spec.NotificationTargets))
				continue
			}
			if got := d.Spec.NotificationTargets[0].TargetRef; got != wantTarget {
				t.Errorf("AlertPolicy %q targetRef = %q, want %q", d.Metadata.Name, got, wantTarget)
			}
		case "AlertNotificationTarget":
			targets++
			if d.Metadata.Name != wantTarget {
				t.Errorf("AlertNotificationTarget metadata.name = %q, want %q", d.Metadata.Name, wantTarget)
			}
			if d.Spec.Target == "" {
				t.Errorf("AlertNotificationTarget %q has empty spec.target", d.Metadata.Name)
			}
		}
	}
	if targets != 1 {
		t.Errorf("rendered %d AlertNotificationTarget documents, want exactly 1", targets)
	}
}

// TestRenderOpenSLOObjectivesMatchCatalog confirms each SLO document's
// objective target equals the catalog target and that the SLO references
// its SLI and AlertPolicy by name (referential integrity for downstream
// OpenSLO tooling).
func TestRenderOpenSLOObjectivesMatchCatalog(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1", OpenSLODefaultNotificationTarget)
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
		if len(d.Spec.AlertPolicies) != 2 {
			t.Errorf("SLO %q has %d alertPolicies, want 2 (fast+slow reference objects)", d.Metadata.Name, len(d.Spec.AlertPolicies))
		}
		for _, ap := range d.Spec.AlertPolicies {
			if ap.AlertPolicyRef == "" {
				t.Errorf("SLO %q has an alertPolicies entry with an empty alertPolicyRef (bare string, not a reference object)", d.Metadata.Name)
				continue
			}
			if !names[ap.AlertPolicyRef] {
				t.Errorf("SLO %q references AlertPolicy %q that is not rendered", d.Metadata.Name, ap.AlertPolicyRef)
			}
		}
	}
}

// TestRenderOpenSLOBurnRateMatchesAlerts confirms the §16.5 multi-window
// burn rate is preserved across two single-condition AlertPolicy documents:
// each SLO renders a <name>-burnrate-fast policy carrying exactly the
// 1h/14x critical condition and a <name>-burnrate-slow policy carrying
// exactly the 6h/3x warning condition. OpenSLO v1 caps an AlertPolicy at
// one condition, so a two-condition policy or a dropped window is a defect.
//
// spec: §16.10 (one condition per AlertPolicy), §16.5 line 627 (multi-window
// burn rate preserved across two policies).
func TestRenderOpenSLOBurnRateMatchesAlerts(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1", OpenSLODefaultNotificationTarget)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	defs := SLODefinitions()
	fastByName := map[string]bool{}
	slowByName := map[string]bool{}
	for _, d := range defs {
		fastByName[d.Name+"-burnrate-fast"] = true
		slowByName[d.Name+"-burnrate-slow"] = true
	}
	var fastSeen, slowSeen int
	for _, d := range parseOpenSLO(t, out) {
		if d.Kind != "AlertPolicy" {
			continue
		}
		if len(d.Spec.Conditions) != 1 {
			t.Errorf("AlertPolicy %q has %d conditions, want exactly 1 (OpenSLO v1 cap)", d.Metadata.Name, len(d.Spec.Conditions))
			continue
		}
		cond := d.Spec.Conditions[0].Spec
		switch {
		case fastByName[d.Metadata.Name]:
			fastSeen++
			if cond.Condition.Threshold != burnRateFastMultiplier || cond.Condition.LookbackWindow != "1h" || cond.Severity != "critical" {
				t.Errorf("AlertPolicy %q condition = %+v, want 14x/1h/critical", d.Metadata.Name, cond)
			}
		case slowByName[d.Metadata.Name]:
			slowSeen++
			if cond.Condition.Threshold != burnRateSlowMultiplier || cond.Condition.LookbackWindow != "6h" || cond.Severity != "warning" {
				t.Errorf("AlertPolicy %q condition = %+v, want 3x/6h/warning", d.Metadata.Name, cond)
			}
		default:
			t.Errorf("AlertPolicy %q is neither a -burnrate-fast nor a -burnrate-slow policy", d.Metadata.Name)
		}
	}
	if fastSeen != len(defs) || slowSeen != len(defs) {
		t.Errorf("saw %d fast and %d slow burn-rate policies, want %d each", fastSeen, slowSeen, len(defs))
	}
}

// TestRenderOpenSLOReferencesCanonicalMetrics asserts the rendered
// queries reference the canonical §16.5 metric names (R-002).
func TestRenderOpenSLOReferencesCanonicalMetrics(t *testing.T) {
	out, err := RenderOpenSLO(OpenSLOService, "tier1", OpenSLODefaultNotificationTarget)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	s := string(out)
	for _, metric := range []string{
		// SessionCreationSuccessRate + GatewayAvailability derive from
		// the gateway HTTP request counter; SessionCreationLatency from
		// the gateway request-duration histogram; CheckpointDuration from
		// the checkpoint-duration histogram; SessionAvailability from the
		// session-unavailability ratio gauge. F-16.5.3.
		"lenny_gateway_requests_total",
		"lenny_gateway_request_duration_seconds_bucket",
		"lenny_session_unavailability_ratio",
		"lenny_session_startup_duration_seconds_bucket",
		"lenny_session_time_to_first_token_seconds_bucket",
		"lenny_checkpoint_duration_seconds_bucket",
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
		out, err := RenderOpenSLO(OpenSLOService, tier, OpenSLODefaultNotificationTarget)
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
	out, err := RenderOpenSLO(OpenSLOService, SLOTierPlaceholder, SLONotificationTargetPlaceholder)
	if err != nil {
		t.Fatalf("RenderOpenSLO: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, SLOTierPlaceholder) {
		t.Error("placeholder-tier render dropped the tier placeholder the chart substitutes")
	}
	if !strings.Contains(s, SLONotificationTargetPlaceholder) {
		t.Error("placeholder render dropped the notification-target placeholder the chart substitutes")
	}
}

// TestRenderOpenSLORejectsBadInput exercises the input guards.
func TestRenderOpenSLORejectsBadInput(t *testing.T) {
	if _, err := RenderOpenSLO("", "tier1", OpenSLODefaultNotificationTarget); err == nil {
		t.Error("empty service did not error")
	}
	if _, err := RenderOpenSLO(OpenSLOService, "", OpenSLODefaultNotificationTarget); err == nil {
		t.Error("empty tier did not error")
	}
	if _, err := RenderOpenSLO(OpenSLOService, "tier1", ""); err == nil {
		t.Error("empty notification target did not error")
	}
}
