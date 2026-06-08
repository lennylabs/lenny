// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"strings"
	"testing"
)

// TestSLODefinitionsBackEveryBurnRateAlert asserts the §16.5 burn-rate
// alerts are derived from the single SLODefinitions catalog: every SLO
// yields exactly a fast-window critical rule and a slow-window warning
// rule whose expressions embed the SLO's budget-normalised base ratio
// compared against the §16.5 line 640 operator-tunable multiplier scalars
// (scalar(lenny_slo_burn_rate_{fast,slow}_multiplier or vector(14|3))).
// This is the single-source invariant the §16.10 OpenSLO export depends
// on (slo.go).
func TestSLODefinitionsBackEveryBurnRateAlert(t *testing.T) {
	defs := SLODefinitions()
	alerts := burnRateAlerts()
	if got, want := len(alerts), len(defs)*2; got != want {
		t.Fatalf("burnRateAlerts count = %d, want %d (2 per SLO)", got, want)
	}
	byName := map[string]Rule{}
	for _, r := range alerts {
		byName[r.Name] = r
	}
	for _, d := range defs {
		fast, ok := byName[d.AlertName]
		if !ok {
			t.Errorf("SLO %q: no fast-window alert %q in catalog", d.Name, d.AlertName)
			continue
		}
		slow, ok := byName[d.AlertName+"Slow"]
		if !ok {
			t.Errorf("SLO %q: no slow-window alert %q in catalog", d.Name, d.AlertName+"Slow")
			continue
		}
		if want := fmt.Sprintf(`%s > %s`, d.BurnRateExpr, burnRateFastMultiplierThreshold); fast.Expr != want {
			t.Errorf("SLO %q fast expr = %q, want %q", d.Name, fast.Expr, want)
		}
		if want := fmt.Sprintf(`%s > %s`, d.BurnRateExpr, burnRateSlowMultiplierThreshold); slow.Expr != want {
			t.Errorf("SLO %q slow expr = %q, want %q", d.Name, slow.Expr, want)
		}
		if fast.Severity != SeverityCritical {
			t.Errorf("SLO %q fast severity = %q, want critical", d.Name, fast.Severity)
		}
		if slow.Severity != SeverityWarning {
			t.Errorf("SLO %q slow severity = %q, want warning", d.Name, slow.Severity)
		}
		if fast.SLO != d.Objective || slow.SLO != d.Objective {
			t.Errorf("SLO %q objective annotation mismatch: fast=%q slow=%q want=%q", d.Name, fast.SLO, slow.SLO, d.Objective)
		}
	}
}

// TestSLODefinitionsCoverSpec16_5Catalog confirms the canonical catalog
// names every SLO in the §16.5 burn-rate table (R-006). A missing SLO is
// an incomplete OpenSLO export and an unmonitored error budget.
//
// spec: §16.5 lines 627-638 (burn-rate alert table).
func TestSLODefinitionsCoverSpec16_5Catalog(t *testing.T) {
	want := []string{
		"SessionCreationSuccessRateBurnRate",
		"SessionCreationLatencyBurnRate",
		"SessionAvailabilityBurnRate",
		"GatewayAvailabilityBurnRate",
		"StartupLatencyBurnRate",
		"StartupLatencyGVisorBurnRate",
		"TTFTBurnRate",
		"CheckpointDurationBurnRate",
	}
	got := map[string]bool{}
	for _, d := range SLODefinitions() {
		got[d.AlertName] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("§16.5 SLO %q missing from SLODefinitions", name)
		}
	}
	if len(SLODefinitions()) != len(want) {
		t.Errorf("SLODefinitions has %d SLOs, want the %d §16.5 SLOs", len(SLODefinitions()), len(want))
	}
}

// TestSLODefinitionsValidateForOpenSLO exercises the per-SLO OpenSLO
// validation across the whole catalog, plus its rejection paths.
func TestSLODefinitionsValidateForOpenSLO(t *testing.T) {
	for _, d := range SLODefinitions() {
		if err := d.validateForOpenSLO(); err != nil {
			t.Errorf("SLO %q fails OpenSLO validation: %v", d.Name, err)
		}
		// The deployment-tier scaffolding (§16.10 line 734) must be
		// present on the good-or-bad query so RenderOpenSLO and the chart
		// can scope it to the deployment tier.
		q := d.SLI.Good
		if q == "" {
			q = d.SLI.Bad
		}
		if !strings.Contains(q, SLOTierPlaceholder) {
			t.Errorf("SLO %q SLI query has no deployment_tier placeholder: %q", d.Name, q)
		}
	}

	bad := []SLODefinition{
		{Name: "", Objective: "x", Target: 0.5, SLI: SLIRatio{Good: "g", Total: "t"}},
		{Name: "n", Objective: "", Target: 0.5, SLI: SLIRatio{Good: "g", Total: "t"}},
		{Name: "n", Objective: "x", Target: 0, SLI: SLIRatio{Good: "g", Total: "t"}},
		{Name: "n", Objective: "x", Target: 1.5, SLI: SLIRatio{Good: "g", Total: "t"}},
		{Name: "n", Objective: "x", Target: 0.5, SLI: SLIRatio{Total: ""}},
		{Name: "n", Objective: "x", Target: 0.5, SLI: SLIRatio{Good: "g", Bad: "b", Total: "t"}},
		{Name: "n", Objective: "x", Target: 0.5, SLI: SLIRatio{Total: "t"}}, // neither good nor bad
	}
	for i, d := range bad {
		if err := d.validateForOpenSLO(); err == nil {
			t.Errorf("malformed SLO #%d passed validation: %+v", i, d)
		}
	}
}
