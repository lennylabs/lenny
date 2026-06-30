// SPDX-License-Identifier: MIT

package policy_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// spec: §12.9 line 1048 — the gateway policy engine validates tenant
// classification at session creation. A recognized tier (including the
// empty default) is admitted.
func TestValidateTenantClassificationValid(t *testing.T) {
	for _, tier := range []string{"", "T3", "T4"} {
		if err := policy.ValidateTenantClassification(tenantstore.Tenant{ID: "acme", WorkspaceTier: tier}); err != nil {
			t.Errorf("tier %q: unexpected error %v", tier, err)
		}
	}
}

// A misconfigured tier (a stale value left over from a direct DB write)
// is rejected with a ClassificationError carrying the §15.1 details.
func TestValidateTenantClassificationInvalid(t *testing.T) {
	err := policy.ValidateTenantClassification(tenantstore.Tenant{ID: "acme", WorkspaceTier: "T2"})
	if err == nil {
		t.Fatal("expected a classification error for tier T2")
	}
	var ce *policy.ClassificationError
	if !errors.As(err, &ce) {
		t.Fatalf("error is not a *ClassificationError: %T", err)
	}
	if ce.TenantID != "acme" || ce.Tier != "T2" || ce.Reason != "invalid_workspace_tier" {
		t.Errorf("ClassificationError = %+v, want {acme T2 invalid_workspace_tier}", ce)
	}
}
