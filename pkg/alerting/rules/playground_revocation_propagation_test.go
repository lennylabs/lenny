// SPDX-License-Identifier: MIT

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

// playgroundPropagationMetric is the §27.8 histogram whose P99 the
// logout-propagation alert reads.
const playgroundPropagationMetric = "lenny_playground_session_revocation_propagation_seconds"

// playgroundPropagationSLOSeconds is the §27.3.1 logout propagation SLO:
// a revocation published on any replica must be visible to the
// per-request revocation check on every other replica within 500 ms at
// P99. §27.8 pins the histogram's P99 alert threshold to that SLO.
const playgroundPropagationSLOSeconds = 0.5

// TestPlaygroundRevocationPropagationAlertExists asserts the alert
// catalog carries a rule that evaluates the P99 of the §27.8
// lenny_playground_session_revocation_propagation_seconds histogram
// against the 500 ms logout propagation SLO, and that the rule points
// at an on-disk runbook so an operator paged by it has a remediation
// page.
//
// Without such a rule the §27.3.1 SLO is measured but never alerted on:
// a replica that stops observing revocations within the budget leaves a
// window in which a logged-out session's bearer is still honored on a
// peer replica, and nothing pages.
//
// spec: §27.8 (metrics table, lenny_playground_session_revocation_propagation_seconds:
// "P99 alert threshold is the 500 ms logout propagation SLO defined in
// §27.3.1"); §27.3.1 ("a revocation published on any replica MUST be
// visible to the per-request revocation check on every other replica
// within 500 ms at P99"); §17.7 (runbooks under docs/runbooks/).
func TestPlaygroundRevocationPropagationAlertExists_spec_27_8(t *testing.T) {
	t.Skip("the §16.5 alert tables the catalog transcribes carry no row for the §27.8 playground propagation histogram, so the alert's name, severity, sustain window, and runbook are undecided; reconciling §16.5 with §27.8 is a spec decision")

	var got Rule
	for _, r := range Catalog() {
		if strings.Contains(r.Expr, playgroundPropagationMetric) {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatalf("no alert in the catalog evaluates %s; the §27.8 P99 alert threshold has no rule", playgroundPropagationMetric)
	}
	if _, err := parser.ParseExpr(got.Expr); err != nil {
		t.Fatalf("alert %q expression does not parse: %v", got.Name, err)
	}
	for _, frag := range []string{
		"histogram_quantile(0.99",
		playgroundPropagationMetric + "_bucket",
	} {
		if !strings.Contains(got.Expr, frag) {
			t.Errorf("alert %q expression %q is missing %q; §27.8 pins the alert to the histogram's P99", got.Name, got.Expr, frag)
		}
	}
	if !thresholdIs(t, got.Expr, playgroundPropagationSLOSeconds) {
		t.Errorf("alert %q expression %q does not compare against the 500 ms §27.3.1 SLO", got.Name, got.Expr)
	}

	slug := got.RunbookSlug()
	if slug == "" {
		t.Fatalf("alert %q carries no runbook target", got.Name)
	}
	// docs/runbooks is three directories up from pkg/alerting/rules.
	path := filepath.Join("..", "..", "..", "docs", "runbooks", slug+".md")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("alert %q runbook %q resolves to missing file %s", got.Name, slug, path)
	}
}

// thresholdIs reports whether expr's top-level comparison right-hand
// side is the given scalar. It reads the parsed expression rather than
// matching on text so an equivalent formatting of the same threshold
// (0.5 vs 500e-3) still satisfies the assertion.
func thresholdIs(t *testing.T, expr string, want float64) bool {
	t.Helper()
	parsed, err := parser.ParseExpr(expr)
	if err != nil {
		return false
	}
	be, ok := parsed.(*parser.BinaryExpr)
	if !ok || !be.Op.IsComparisonOperator() {
		return false
	}
	num, ok := be.RHS.(*parser.NumberLiteral)
	if !ok {
		return false
	}
	return num.Val == want
}
