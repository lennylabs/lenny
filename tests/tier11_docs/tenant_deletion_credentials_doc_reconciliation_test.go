// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the reader pages that document
// tenant deletion's credential phase and data phase, the tenant tombstone,
// legal-hold escrow on force-delete, deleting the built-in `default` tenant,
// tenant ID extraction, the reserved `platform` tenant ID, the bootstrap
// seed's refusal of a deleting tenant, and the full_revoke session-listing
// failure.
//
// Before these pages were reconciled, the Phase 3 row named a lease-store
// method that does not exist and said nothing about bearer refusal, the
// tombstone sentence said every mutable field is nulled, the Legal Holds
// section said held resources survive tenant deletion, and the tenant ID
// extraction tables treated a deleting tenant's bearers as valid.
//
// Each check first confirms the spec still states the behavior the page
// describes, so a spec change that retires the behavior fails here with a
// "re-derive this check" message rather than passing against stale prose.
//
// spec: 12.8 (Compliance Interfaces), 10.2 (Authentication), 11.4 (User
// Invalidation), 17.6 (Packaging and Installation)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// storeMethodRE matches an internal store method name such as
// `TokenStore.DeleteByTenant` or `CredentialPoolStore.RevokeByTenant`. A
// reader-facing phase row describes outcomes and names none.
var storeMethodRE = regexp.MustCompile(`[A-Z][A-Za-z]*Store\.[A-Z][A-Za-z]*`)

// requireSpecPhrase fails the test when the spec file no longer carries
// phrase, which means the doc check below it must be re-derived.
func requireSpecPhrase(t *testing.T, root, specFile, phrase string) {
	t.Helper()
	if !strings.Contains(readDocPage(t, filepath.Join(root, "spec", specFile)), phrase) {
		t.Fatalf("spec/%s no longer states %q; re-derive this check", specFile, phrase)
	}
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle, Phase 3),
// 10.2 (Authentication, tenant_id extraction)
// diagnosis: the multi-tenancy Phase 3 row still names the nonexistent
// `CredentialPoolStore.RevokeByTenant` or another internal store method, or
// omits that a multi-tenant gateway refuses the tenant's bearers with
// TENANT_NOT_FOUND from this phase. An operator expects the tenant's users
// to keep API access until the tombstone.
func TestTenantDeletionPhaseThreeRowStatesBearerRefusalAndLeaseReturn(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"so in a multi-tenant deployment the gateway rejects every bearer token of the tenant with `403 TENANT_NOT_FOUND`")
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"returns every credential lease they hold through the lease step")

	page := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	row := tableRowByPrefix(t, page, readDocPage(t, filepath.Join(root, page)), "| 3. Credential revocation |")
	requireAllContain(t, page+" Phase 3 row", row, []string{
		"rejects every bearer token of the tenant with `403 TENANT_NOT_FOUND`",
		"connector tokens and registered credentials are deleted",
		"credential leases its sessions still hold are returned",
	})
	if m := storeMethodRE.FindString(row); m != "" {
		t.Errorf("%s: Phase 3 row names the internal store method %q", page, m)
	}
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle, Phase 4;
// Tenant record tombstone)
// diagnosis: the Phase 4 row still says billing events are deleted
// "respecting" the erasure policy without stating what `exempt` keeps, or the
// tombstone sentence still says every mutable field is nulled. An operator
// reading the page cannot tell that an exempt tenant's billing events survive
// deletion or which fields the tombstone keeps.
func TestTenantDeletionPhaseFourRowAndTombstoneStateWhatIsKept(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"a tenant whose `billingErasurePolicy` is `exempt` keeps its billing events until `billing.retentionDays` ages them out")
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"every mutable field nulled except those that a resumed or later phase, or a reader of the tombstone snapshot, still reads, such as `billingErasurePolicy`, `workspaceTier`, and `dataResidencyRegion`")

	page := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	body := readDocPage(t, filepath.Join(root, page))
	row := tableRowByPrefix(t, page, body, "| 4. Data deletion |")
	requireAllContain(t, page+" Phase 4 row", row, []string{
		"caches and artifacts",
		"tokens and credentials",
		"access rows, roles, and configuration",
		"A tenant whose `billingErasurePolicy` is `exempt` keeps its billing events until `billing.retentionDays` ages them out",
		"[Legal Holds](#legal-holds)",
	})
	requireNoneContain(t, page+" Phase 4 row", row, []string{"respecting `billingErasurePolicy`"})

	tombstone := lineContaining(body, "The tenant record itself is retained after Phase 6")
	if tombstone == "" {
		t.Fatalf("%s: no tombstone sentence", page)
	}
	requireAllContain(t, page+" tombstone sentence", tombstone, []string{
		"the billing erasure policy", "the workspace tier", "the data residency region",
	})
	requireNoneContain(t, page, body, []string{"all mutable fields nulled"})
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle, Phase 4;
// --acknowledge-hold-override admin action), 10.2 (Authentication)
// diagnosis: the Legal Holds section still says held resources are kept
// through tenant deletion, or omits that force-delete copies held evidence to
// the escrow bucket before Phase 4 deletes the originals, or the pages omit
// that a deleting tenant's own tenant-admins lose API access, or a Min Role
// cell of the lenny-ctl Legal Hold Management table lists `tenant-admin`
// without the `deleting` qualifier or applies that qualifier to
// `platform-admin` as well. An operator relies on the originals surviving,
// waits on a tenant-admin who can no longer release the holds, or concludes
// that no role can release them.
func TestLegalHoldDocsStateEscrowAndDeletingTenantAdminLockout(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"`DeleteByTenant` deletes the originals of resources that Phase 3.5 copied to the legal-hold escrow bucket.")

	mtPage := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	mt := readDocPage(t, filepath.Join(root, mtPage))
	holds := docHeadingSection(t, mtPage, mt, "## Legal Holds")
	requireAllContain(t, mtPage+" Legal Holds", holds, []string{
		"An active hold blocks Phase 4 of tenant deletion.",
		"the held evidence is copied to the legal-hold escrow bucket before Phase 4 deletes the originals",
	})
	requireNoneContain(t, mtPage+" Legal Holds", holds, []string{"prevents deletion of held resources"})

	const lockout = "A tenant-admin of a tenant in the `deleting` state cannot call the API. A platform-admin releases its holds or force-deletes it."
	// The roles sentence follows a fenced block whose shell comments read as
	// headings to docHeadingSection, so it is located by its own line.
	requireAllContain(t, mtPage+" legal-hold roles sentence",
		lineContaining(mt, "Both `platform-admin` and `tenant-admin` roles can manage legal holds."), []string{lockout})
	ctlPage := filepath.Join("docs", "operator-guide", "lenny-ctl.md")
	ctlHolds := docHeadingSection(t, ctlPage, readDocPage(t, filepath.Join(root, ctlPage)), "## Legal Hold Management")
	requireAllContain(t, ctlPage+" Legal Hold Management", ctlHolds, []string{lockout})
	requireLegalHoldRoleCellsQualified(t, ctlPage, ctlHolds)
}

// requireLegalHoldRoleCellsQualified checks that every command row of the
// Legal Hold Management table limits its `tenant-admin` entry in the Min Role
// column to a tenant that is not `deleting`, and leaves `platform-admin`
// unrestricted. A reader scanning the role column alone must not see an
// unqualified `tenant-admin`, and must not read the qualifier as covering
// `platform-admin`, which is the role that releases a deleting tenant's holds.
func requireLegalHoldRoleCellsQualified(t *testing.T, page, section string) {
	t.Helper()
	const qualified = "`platform-admin` / `tenant-admin` (tenant-admin: not while its tenant is `deleting`)"
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `lenny-ctl admin legal-holds") {
			continue
		}
		rows++
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		role := strings.TrimSpace(cells[len(cells)-1])
		if role != qualified {
			t.Errorf("%s Legal Hold Management: Min Role cell %q, want %q", page, role, qualified)
		}
		if legalHoldQualifierCoversPlatformAdmin(role) {
			t.Errorf("%s Legal Hold Management: Min Role cell %q restricts platform-admin on a deleting tenant", page, role)
		}
	}
	if rows == 0 {
		t.Errorf("%s Legal Hold Management: no `lenny-ctl admin legal-holds` command rows found", page)
	}
}

// legalHoldQualifierCoversPlatformAdmin reports whether a Min Role cell
// carries a `deleting` parenthetical that is not scoped to tenant-admin. A
// parenthetical placed after both roles without naming tenant-admin reads as
// restricting both of them.
func legalHoldQualifierCoversPlatformAdmin(role string) bool {
	open := strings.Index(role, "(")
	if open < 0 || !strings.Contains(role[open:], "`deleting`") {
		return false
	}
	return !strings.HasPrefix(role[open+1:], "tenant-admin:")
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle), 17.6
// (Packaging and Installation)
// diagnosis: the multi-tenancy page no longer tells an operator how to delete
// the built-in `default` tenant safely, or names a Helm value or install
// heading that does not exist. An operator deletes `default` and loses the
// initial admin token at Phase 3, or the bootstrap Job fails on every upgrade.
func TestDefaultTenantDeletionStepsMatchSpecAndChart(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"In a single-tenant deployment ([Section 10.2](10_gateway-internals.md#102-authentication)), the gateway refuses a request to delete or force-delete the built-in `default` tenant with `409 INVALID_STATE_TRANSITION`")
	requireSpecPhrase(t, root, "12_storage-architecture.md",
		"the gateway refuses that user's admin token from Phase 3 onward")

	page := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	sec := docHeadingSection(t, page, readDocPage(t, filepath.Join(root, page)), "#### Deleting the default tenant")
	requireAllContain(t, page+" Deleting the default tenant", sec, []string{
		"`gateway.adminToken.tenant`",
		"`--admin-token-tenant`",
		"refuses the user's admin token from Phase 3 onward",
		"rotate the initial admin token",
		"(installation.md#initial-admin-credential)",
		"`bootstrap.defaultGithubVcsPool`",
		"A single-tenant deployment refuses a request to delete or force-delete `default` with `409 INVALID_STATE_TRANSITION`",
	})

	// The page names chart values and an install heading; each must exist.
	values := readDocPage(t, filepath.Join(root, "charts", "lenny", "values.yaml"))
	if !regexp.MustCompile(`(?m)^  adminToken:\n(?:    .*\n)*?    tenant: `).MatchString(values) {
		t.Errorf("charts/lenny/values.yaml: gateway.adminToken.tenant is gone; %s names it", page)
	}
	if !regexp.MustCompile(`(?m)^  defaultGithubVcsPool:$`).MatchString(values) {
		t.Errorf("charts/lenny/values.yaml: bootstrap.defaultGithubVcsPool is gone; %s names it", page)
	}
	install := filepath.Join("docs", "operator-guide", "installation.md")
	docHeadingSection(t, install, readDocPage(t, filepath.Join(root, install)), "### Initial Admin Credential")
}

// spec: 10.2 (Authentication, tenant_id extraction)
// diagnosis: a tenant ID extraction table still says the gateway reads the
// configured claim from every token, or its TENANT_NOT_FOUND row omits that a
// deleting or deleted tenant's bearers are refused. A client or operator
// treats the 403 after tenant deletion as a misconfiguration.
func TestTenantIDExtractionTablesNameIssuedTokensAndDeletingTenants(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "10_gateway-internals.md",
		"from this claim in an OIDC ID token and from the `tenant_id` claim in a gateway-issued token")
	requireSpecPhrase(t, root, "10_gateway-internals.md",
		"A tenant whose `TenantState` is `deleting` or `deleted` is not registered")

	secPage := filepath.Join("docs", "operator-guide", "security.md")
	sec := readDocPage(t, filepath.Join(root, secPage))
	requireAllContain(t, secPage+" multi-tenant row",
		tableRowByPrefix(t, secPage, sec, "| Multi-tenant mode"),
		[]string{"from a validated OIDC ID token", "`tenant_id` from a gateway-issued token"})
	requireAllContain(t, secPage+" TENANT_NOT_FOUND row", lineContaining(sec, "`TENANT_NOT_FOUND`"),
		[]string{"`deleting` or `deleted` state"})

	authPage := filepath.Join("docs", "client-guide", "authentication.md")
	tc := docHeadingSection(t, authPage, readDocPage(t, filepath.Join(root, authPage)), "### Tenant Context")
	requireAllContain(t, authPage+" Tenant Context", tc, []string{
		"`auth.tenantIdClaim`",
		"the `tenant_id` claim in a token the gateway issued",
	})
	requireAllContain(t, authPage+" TENANT_NOT_FOUND row", lineContaining(tc, "`TENANT_NOT_FOUND`"),
		[]string{"`deleting` or `deleted` state"})

	mtPage := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	mt := readDocPage(t, filepath.Join(root, mtPage))
	requireAllContain(t, mtPage+" Multi-Tenant Mode step 2", lineContaining(mt, "`403 TENANT_NOT_FOUND`"),
		[]string{"names a tenant that has no record or whose record is in the `deleting` or `deleted` state"})
	requireNoneContain(t, mtPage, mt, []string{"no active record"})
}

// spec: 10.2 (Authentication, tenant_id format validation), 12.8
// (Compliance Interfaces, Tenant deletion lifecycle), 11.4 (User
// Invalidation)
// diagnosis: the admin API reference omits the RESERVED_IDENTIFIER refusal
// of the tenant ID `platform`, the single-tenant INVALID_STATE_TRANSITION
// refusal of deleting `default`, or the full_revoke 500 on a session-listing
// failure. A client cannot recognize the refusal or misreads the 500 as
// "nothing happened" when the user's tokens were already revoked.
func TestAdminReferenceTenantAndInvalidateEntriesNameRefusals(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "10_gateway-internals.md",
		"`POST /v1/admin/tenants` rejects it with `422 RESERVED_IDENTIFIER`")
	// The listing-failure status is the handler's: the spec names no status
	// for it, so the check reads the code that answers it.
	users := readDocPage(t, filepath.Join(root, "pkg", "gateway", "externalapi", "admin", "users.go"))
	if !strings.Contains(users, `"INTERNAL_ERROR",
			"session listing failed: "+rep.listingError, fields)`) {
		t.Fatalf("users.go no longer answers a full_revoke listing failure with INTERNAL_ERROR and the counts; re-derive this check")
	}

	page := filepath.Join("docs", "api", "admin.md")
	body := readDocPage(t, filepath.Join(root, page))
	requireAllContain(t, page+" create tenant",
		docHeadingSection(t, page, body, "### POST /v1/admin/tenants"),
		[]string{"The tenant ID `platform` is reserved", "`422 RESERVED_IDENTIFIER`"})
	requireAllContain(t, page+" delete tenant",
		docHeadingSection(t, page, body, "### DELETE /v1/admin/tenants/{id}"),
		[]string{"single-tenant deployment", "`default`", "`409 INVALID_STATE_TRANSITION`"})
	requireAllContain(t, page+" invalidate user",
		docHeadingSection(t, page, body, "### POST /v1/admin/users/{userId}/invalidate"),
		[]string{
			"In `full_revoke` mode, when the user's sessions cannot be listed",
			"revokes the user's tokens and dismisses the user's pending elicitations",
			"`500 INTERNAL_ERROR` with the per-effect counts in the error details",
		})
}

// spec: 17.6 (Packaging and Installation, Upsert semantics)
// diagnosis: an Upsert Semantics table omits the row for a tenant entry that
// names the reserved `platform` ID or a deleting or deleted tenant. An
// operator expects the seed to recreate or update the tenant and is surprised
// by the bootstrap Job's error exit.
func TestBootstrapUpsertTablesNameReservedAndDeletingTenantRow(t *testing.T) {
	root := repoRoot(t)
	requireSpecPhrase(t, root, "17_deployment-topology.md",
		"Tenant entry whose `id` is the reserved `platform`")
	for _, page := range []string{
		filepath.Join("docs", "operator-guide", "installation.md"),
		filepath.Join("docs", "operator-guide", "lenny-ctl.md"),
	} {
		row := tableRowByPrefix(t, page, readDocPage(t, filepath.Join(root, page)), "| Tenant entry whose `id` is the reserved `platform`")
		requireAllContain(t, page+" upsert row", row, []string{
			"`deleting` or `deleted` state",
			"no tenant is created or changed",
			"exit code 2, or 1 when no entry succeeds",
		})
	}
}
