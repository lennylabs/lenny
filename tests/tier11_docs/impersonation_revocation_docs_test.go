// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the reader-facing pages that describe
// platform-admin impersonation sessions: the bearer-tokens item of the
// security guide, the impersonation endpoints of the admin API reference,
// and the platform-tenant audit residency rows that point at the audit
// logging halt rule instead of restating it. No external infrastructure.

package tier11_docs_test

import "testing"

// TestReaderDocsDescribeImpersonationRevocation_spec_13_3 asserts that the
// security guide names the end endpoint as a way to revoke an impersonation
// bearer, that the admin API reference documents the start, list, and end
// endpoints with the 409 for an inactive session and the bearer that stays
// revoked when the ended audit write fails, and that the CMP-058 rows no
// longer state that the originating operation halts or succeeds once audit
// can commit.
//
// diagnosis: A failure means the security guide's bearer-tokens item omits
// the end endpoint, the admin API reference is missing an impersonation
// section or its 409 INVALID_STATE_TRANSITION or revoked-bearer statement,
// or a PlatformAuditResidencyViolation or PLATFORM_AUDIT_REGION_UNRESOLVABLE
// row carries the halt clause that the audit logging section now owns.
//
// spec: 13.3 (Platform-admin impersonation), 15.1 (POST, GET, and DELETE
// /v1/admin/impersonation rows), 11.7 (Platform-tenant audit event
// residency)
func TestReaderDocsDescribeImpersonationRevocation_spec_13_3(t *testing.T) {
	retiredHalt := []string{"originating operation halts", "once audit can commit"}
	runDocPageChecks(t, []docPageCheck{
		{
			page:     "operator-guide/security.md",
			row:      "3. **Bearer tokens**",
			contains: []string{"DELETE /v1/admin/impersonation/{id}"},
		},
		{
			page: "api/admin.md",
			contains: []string{
				"### POST /v1/admin/impersonation\n",
				"### GET /v1/admin/impersonation\n",
				"### DELETE /v1/admin/impersonation/{id}\n",
				"409 INVALID_STATE_TRANSITION",
				"bearer stays revoked",
			},
		},
		{
			page:       "operator-guide/observability.md",
			row:        "| `PlatformAuditResidencyViolation` |",
			notContain: retiredHalt,
		},
		{
			page:       "reference/metrics.md",
			row:        "| `PlatformAuditResidencyViolation` |",
			notContain: retiredHalt,
		},
		{
			page:       "reference/error-catalog.md",
			row:        "| `PLATFORM_AUDIT_REGION_UNRESOLVABLE` |",
			notContain: retiredHalt,
		},
	})
}
