// SPDX-License-Identifier: MIT

package evaluator_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// spec: §25.3 lines 443-451 — FiringAlerts returns the full Alert (rule +
// severity) for every firing rule so the health derivation can map a
// critical alert to unhealthy and a warning to degraded.
func TestFiringAlertsCarriesRuleAndSeverity_spec_25_3_443(t *testing.T) {
	critical := rules.Rule{
		Name:       "CritRule",
		Expr:       "crit_expr",
		Severity:   rules.SeverityCritical,
		Summary:    "critical test rule",
		RunbookURL: "https://example.test/runbooks/crit",
	}
	warn := testRule("WarnRule", "warn_expr", 0)
	fake := &fakeExpr{active: map[string]bool{"crit_expr": true, "warn_expr": true}}
	ev := evaluator.New([]rules.Rule{critical, warn}, fake, evaluator.Options{})

	// Before any tick nothing is firing.
	if got := ev.FiringAlerts(); len(got) != 0 {
		t.Fatalf("FiringAlerts before tick = %d, want 0", len(got))
	}

	ev.Tick(context.Background(), base) // both fire immediately (For=0)

	got := ev.FiringAlerts()
	if len(got) != 2 {
		t.Fatalf("FiringAlerts = %d alerts, want 2", len(got))
	}
	bySeverity := map[rules.Severity]evaluator.Alert{}
	for _, a := range got {
		if a.State != evaluator.StateFiring {
			t.Errorf("alert %q state = %q, want firing", a.Rule.Name, a.State)
		}
		bySeverity[a.Rule.Severity] = a
	}
	if bySeverity[rules.SeverityCritical].Rule.Name != "CritRule" {
		t.Errorf("critical firing alert = %q, want CritRule", bySeverity[rules.SeverityCritical].Rule.Name)
	}
	if bySeverity[rules.SeverityWarning].Rule.Name != "WarnRule" {
		t.Errorf("warning firing alert = %q, want WarnRule", bySeverity[rules.SeverityWarning].Rule.Name)
	}
}

// A rule still in StatePending (its For window has not elapsed) is not a
// firing alert. spec: §25.3 lines 443-451.
func TestFiringAlertsExcludesPending_spec_25_3_443(t *testing.T) {
	warn := testRule("Pending", "warn_expr", time.Hour)
	fake := &fakeExpr{active: map[string]bool{"warn_expr": true}}
	ev := evaluator.New([]rules.Rule{warn}, fake, evaluator.Options{})

	ev.Tick(context.Background(), base) // → pending (For=1h not elapsed)

	if got := ev.FiringAlerts(); len(got) != 0 {
		t.Fatalf("FiringAlerts with a pending rule = %d, want 0", len(got))
	}

	ev.Tick(context.Background(), base.Add(2*time.Hour)) // pending → firing
	if got := ev.FiringAlerts(); len(got) != 1 {
		t.Fatalf("FiringAlerts after For elapsed = %d, want 1", len(got))
	}
}
