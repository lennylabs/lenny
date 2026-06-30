// SPDX-License-Identifier: MIT

// Tier-11 documentation check: the F-SH-1 OpsClockSkewExceeded alert
// resolves to its on-disk runbook. This is a focused regression that
// pins the specific alert-to-runbook mapping the §16.5 / §25.4 clock-skew
// alert introduces, on top of the catalog-wide cross-checks in
// runbooks_test.go (TestAlertCatalogRunbookSlugsResolveToDocs,
// TestRunbookTriggersResolveToAlertCatalog). No external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// TestOpsClockSkewExceededResolvesToRunbook_spec_16_5 asserts the
// OpsClockSkewExceeded warning alert in the §16.5 catalog carries a
// runbook_url whose slug resolves to docs/runbooks/ops-clock-skew.md, and
// that the runbook's front-matter trigger names the alert back. Pre-fix
// the alert did not exist, so the slug could not resolve. F-SH-1.
//
// diagnosis: A failure means the OpsClockSkewExceeded alert (the §25.4
// Postgres-Redis >10s skew alert) points at a missing or misnamed runbook
// file, so an operator following the runbook_url annotation reaches a
// broken link. Either the alert's RunbookURL slug or the on-disk runbook
// filename drifted.
//
// spec: 16.5 (OpsClockSkewExceeded warning-alert row), 25.4 (Postgres-Redis
// skew monitoring and >10s alert, line 2280), 17.7 (runbooks in
// docs/runbooks/)
func TestOpsClockSkewExceededResolvesToRunbook_spec_16_5(t *testing.T) {
	const (
		alertName = "OpsClockSkewExceeded"
		wantSlug  = "ops-clock-skew"
	)
	var got rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == alertName {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatalf("%s missing from the §16.5 alert catalog", alertName)
	}
	if slug := got.RunbookSlug(); slug != wantSlug {
		t.Fatalf("%s runbook slug = %q, want %q", alertName, slug, wantSlug)
	}

	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runbooks", wantSlug+".md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s runbook does not resolve to %s: %v", alertName, path, err)
	}
	// The runbook's front matter must name the alert back so the reverse
	// trigger→alert cross-check (TestRunbookTriggersResolveToAlertCatalog)
	// holds and the agent-consumable /v1/admin/runbooks index maps the
	// alert to this runbook.
	if !strings.Contains(string(body), "alert: "+alertName) {
		t.Errorf("%s front matter does not declare a trigger for %q", path, alertName)
	}
}
