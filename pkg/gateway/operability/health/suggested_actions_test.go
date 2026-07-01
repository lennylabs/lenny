// SPDX-License-Identifier: MIT

package health_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// TestActionsForIssue_RankedVsSingular pins the §25.3 contract: the four
// capacity/throttling issues present the ordered suggestedActions array
// while every other known issue presents a single canonical
// suggestedAction. The catalog and conventions.UsesRankedActions must
// agree on which form each issue uses.
// spec: §25.3 lines 459-501, 484-487.
func TestActionsForIssue_RankedVsSingular_spec_25_3_484(t *testing.T) {
	ranked := []string{
		"WARM_POOL_EXHAUSTED", "WARM_POOL_LOW",
		"CREDENTIAL_POOL_EXHAUSTED", "CIRCUIT_BREAKER_OPEN",
	}
	for _, issue := range ranked {
		single, multi := health.ActionsForIssue(issue, "default-gvisor")
		if !conventions.UsesRankedActions(issue) {
			t.Errorf("%s: UsesRankedActions = false, want true", issue)
		}
		if single != nil {
			t.Errorf("%s: singular action %+v, want nil for a ranked issue", issue, single)
		}
		if len(multi) < 2 {
			t.Errorf("%s: %d ranked actions, want at least 2 alternatives", issue, len(multi))
		}
		// Ranked alternatives are ordered by descending confidence and
		// carry the Confidence/Risk fields the singular form omits.
		for i := 1; i < len(multi); i++ {
			if multi[i-1].Confidence < multi[i].Confidence {
				t.Errorf("%s: actions not ordered by descending confidence: %v", issue, multi)
			}
		}
		if multi[0].Confidence == 0 || multi[0].Risk == "" {
			t.Errorf("%s: primary ranked action missing confidence/risk: %+v", issue, multi[0])
		}
	}

	singular := map[string]string{
		"POSTGRES_UNREACHABLE":         "RUN_POSTGRES_FAILOVER",
		"REDIS_UNREACHABLE":            "INVESTIGATE_REDIS",
		"MINIO_UNREACHABLE":            "INVESTIGATE_OBJECT_STORE",
		"CERT_EXPIRY_IMMINENT":         "RENEW_CERTIFICATE",
		"AUDIT_SIEM_DELIVERY_DEGRADED": "VERIFY_SIEM_DELIVERY",
	}
	for issue, wantAction := range singular {
		single, multi := health.ActionsForIssue(issue, "")
		if conventions.UsesRankedActions(issue) {
			t.Errorf("%s: UsesRankedActions = true, want false", issue)
		}
		if len(multi) != 0 {
			t.Errorf("%s: ranked actions %v, want the singular form", issue, multi)
		}
		if single == nil {
			t.Fatalf("%s: nil singular action", issue)
		}
		if single.Action != wantAction {
			t.Errorf("%s: action %q, want %q", issue, single.Action, wantAction)
		}
		// The singular form omits confidence and risk (the action is the
		// canonical correct response).
		if single.Confidence != 0 || single.Risk != "" {
			t.Errorf("%s: singular action carries confidence/risk: %+v", issue, single)
		}
		// The runbook is sourced from the §25.7 Path B table.
		if single.Runbook != health.RunbookForIssue(issue) {
			t.Errorf("%s: runbook %q, want %q from the issueRunbooks table",
				issue, single.Runbook, health.RunbookForIssue(issue))
		}
	}
}

// TestActionsForIssue_PrimaryRunbookMatchesPathB guards against drift
// between the catalog's primary action and the §25.7 issueRunbooks
// table for the ranked issues.
// spec: §25.7 line 3234.
func TestActionsForIssue_PrimaryRunbookMatchesPathB_spec_25_7_3234(t *testing.T) {
	for _, issue := range []string{
		"WARM_POOL_EXHAUSTED", "WARM_POOL_LOW",
		"CREDENTIAL_POOL_EXHAUSTED", "CIRCUIT_BREAKER_OPEN",
	} {
		_, multi := health.ActionsForIssue(issue, "p1")
		if multi[0].Runbook != health.RunbookForIssue(issue) {
			t.Errorf("%s: primary runbook %q, want %q", issue, multi[0].Runbook, health.RunbookForIssue(issue))
		}
	}
}

// TestActionsForIssue_TargetSubstitution covers the endpoint
// templating: a named target lands in the path, and an empty target
// leaves a legible placeholder rather than an empty path segment.
// spec: §25.3 lines 459-501.
func TestActionsForIssue_TargetSubstitution(t *testing.T) {
	_, named := health.ActionsForIssue("WARM_POOL_EXHAUSTED", "default-gvisor")
	if named[0].Endpoint != "PUT /v1/admin/pools/default-gvisor/warm-count" {
		t.Errorf("endpoint with target = %q", named[0].Endpoint)
	}
	_, blank := health.ActionsForIssue("WARM_POOL_EXHAUSTED", "")
	if blank[0].Endpoint != "PUT /v1/admin/pools/{pool}/warm-count" {
		t.Errorf("endpoint without target = %q, want a {pool} placeholder", blank[0].Endpoint)
	}
}

// TestActionsForIssue_Unknown returns no hint for an unrecognized issue
// so the §25.3 response simply omits the remediation fields.
func TestActionsForIssue_Unknown(t *testing.T) {
	single, multi := health.ActionsForIssue("NOT_A_REAL_ISSUE", "x")
	if single != nil || len(multi) != 0 {
		t.Errorf("unknown issue returned %v / %v, want (nil, nil)", single, multi)
	}
}
