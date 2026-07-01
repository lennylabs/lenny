// SPDX-License-Identifier: MIT

package policy

import (
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// ClassificationError reports a §12.9 tenant data-classification
// misconfiguration the gateway policy engine detects at session
// creation. It carries the fields the §15.1 CLASSIFICATION_CONTROL_VIOLATION
// error catalog entry expects in `details`: the tenant, the offending
// tier, and a machine-readable reason. spec: §12.9 line 1048; §15.1 line
// 1078.
type ClassificationError struct {
	TenantID string
	Tier     string
	Reason   string
}

func (e *ClassificationError) Error() string {
	return fmt.Sprintf("tenant %q workspaceTier %q is not a recognized §12.9 data-classification tier (%s)",
		e.TenantID, e.Tier, e.Reason)
}

// ValidateTenantClassification implements the §12.9 line 1048
// requirement that "the gateway policy engine validates tenant
// classification configuration at session creation." It rejects a
// session whose tenant carries a workspaceTier outside the closed
// §12.9 tenant-settable enum (empty, T3, or T4). A stale or malformed
// value — left over from a direct database write or a pre-validation
// bootstrap — is otherwise treated by every downstream consumer as
// "not T4" (KMS probe skipped, SSE-KMS resolver falls back, the
// t4-node-isolation predicate skipped), silently deferring the
// classification violation to a runtime write that a happy-path session
// may never reach. Validating at admission turns that latent
// misconfiguration into a session-creation failure.
//
// A valid tier (including the empty default) returns nil. The reason
// `invalid_workspace_tier` extends the non-exhaustive §15.1 reason set
// (`kms_probe_failed`, `kms_unavailable`, `tier_store_mismatch`).
func ValidateTenantClassification(tenant tenantstore.Tenant) error {
	if tenantstore.ValidWorkspaceTier(tenant.WorkspaceTier) {
		return nil
	}
	return &ClassificationError{
		TenantID: tenant.ID,
		Tier:     tenant.WorkspaceTier,
		Reason:   "invalid_workspace_tier",
	}
}
