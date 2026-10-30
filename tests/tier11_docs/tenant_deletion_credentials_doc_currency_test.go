// SPDX-License-Identifier: MIT

// Tier-11 currency checks for the reader pages that document tenant-deletion
// credential revocation, the tenant registry's refusal of a deleting tenant,
// and the tenant create and delete refusals.
//
// The operator guide once described the credential-revocation phase as a
// tenant-wide pool revocation (`RevokeByTenant`), a method the tree does not
// have, and described the configuration erasure as "all mutable fields
// nulled", which overstates what the eraser does. The pages now state that
// the phase refuses the tenant's bearers with `TENANT_NOT_FOUND`, that the
// security page's `TENANT_NOT_FOUND` row covers a tenant in the `deleting`
// state, and that the admin reference names `RESERVED_IDENTIFIER` for the
// reserved tenant ID and `INVALID_STATE_TRANSITION` for deleting the
// built-in tenant of a single-tenant deployment.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks. It
// asserts a documentation contract rather than a platform behavior, so it
// carries no specification-section credit; tests/tier0_static registers it
// in documentConsistencyGates.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// docSection returns the lines of page from the heading line that starts with
// heading up to the next heading of the same or a higher level.
func docSection(page, heading string) string {
	lines := strings.Split(page, "\n")
	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	var out []string
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			lv := len(l) - len(strings.TrimLeft(l, "#"))
			if in && lv <= level {
				break
			}
			if strings.HasPrefix(l, heading) {
				in = true
			}
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// diagnosis: the multi-tenancy page names a pool revocation method the tree
// does not have, overstates the configuration erasure, or no longer tells the
// operator that the credential-revocation phase refuses the tenant's bearers
// with TENANT_NOT_FOUND.
func TestMultiTenancyCredentialRevocationRowIsCurrent(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "operator-guide", "multi-tenancy.md"))
	for _, stale := range []string{"RevokeByTenant", "all mutable fields nulled"} {
		if strings.Contains(page, stale) {
			t.Errorf("multi-tenancy.md still says %q", stale)
		}
	}
	row := ""
	for _, l := range strings.Split(page, "\n") {
		if strings.HasPrefix(l, "| 3. ") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("multi-tenancy.md has no credential-revocation row in its deletion phase table")
	}
	if !strings.Contains(row, "TENANT_NOT_FOUND") {
		t.Errorf("the credential-revocation row does not name TENANT_NOT_FOUND: %s", row)
	}
}

// diagnosis: the security page's TENANT_NOT_FOUND row describes only an
// unknown tenant, so an operator reading it does not learn that a deleting
// tenant's bearers are refused.
func TestSecurityTenantNotFoundRowNamesDeletingState(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "operator-guide", "security.md"))
	found := false
	for _, l := range strings.Split(page, "\n") {
		if strings.HasPrefix(l, "|") && strings.Contains(l, "TENANT_NOT_FOUND") {
			found = true
			if !strings.Contains(l, "`deleting`") {
				t.Errorf("the TENANT_NOT_FOUND row does not name the deleting state: %s", l)
			}
		}
	}
	if !found {
		t.Fatal("security.md has no TENANT_NOT_FOUND table row")
	}
}

// diagnosis: the admin reference omits the refusal of the reserved tenant ID
// on create or the refusal to delete the built-in tenant of a single-tenant
// deployment, so a client meets error codes the reference does not document.
func TestAdminTenantCreateAndDeleteRefusalsAreDocumented(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "api", "admin.md"))
	cases := []struct{ heading, code string }{
		{"### POST /v1/admin/tenants", "RESERVED_IDENTIFIER"},
		{"### DELETE /v1/admin/tenants/{id}", "INVALID_STATE_TRANSITION"},
	}
	for _, c := range cases {
		sec := docSection(page, c.heading)
		if sec == "" {
			t.Errorf("admin.md has no %q entry", c.heading)
			continue
		}
		if !strings.Contains(sec, c.code) {
			t.Errorf("admin.md %q does not name %s", c.heading, c.code)
		}
	}
}
