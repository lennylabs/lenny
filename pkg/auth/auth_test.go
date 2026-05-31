// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAllTokenTypesIsExhaustive(t *testing.T) {
	if got := len(AllTokenTypes()); got != 4 {
		t.Errorf("AllTokenTypes() returned %d, want 4 per §10.2", got)
	}
	for _, tt := range AllTokenTypes() {
		if !tt.IsValid() {
			t.Errorf("AllTokenTypes() returned invalid value %q", tt)
		}
	}
	if TokenType("bogus").IsValid() {
		t.Errorf("unknown token type should be invalid")
	}
}

func TestAllRolesIsExhaustive(t *testing.T) {
	if got := len(AllRoles()); got != 5 {
		t.Errorf("AllRoles() returned %d, want 5 per §10.2", got)
	}
	for _, r := range AllRoles() {
		if !r.IsValid() {
			t.Errorf("AllRoles() returned invalid role %q", r)
		}
	}
}

func TestRoleIsTenantScoped(t *testing.T) {
	if RolePlatformAdmin.IsTenantScoped() {
		t.Errorf("platform-admin must NOT be tenant-scoped")
	}
	for _, r := range []Role{RoleTenantAdmin, RoleTenantViewer, RoleBillingViewer, RoleUser} {
		if !r.IsTenantScoped() {
			t.Errorf("%q must be tenant-scoped", r)
		}
	}
}

func TestAllPermissionsIsExhaustive(t *testing.T) {
	// §10.2's permission matrix has 19 operation rows, plus the §10.7
	// session:eval:write capability permission (F-10.7.4).
	if got := len(AllPermissions()); got != 20 {
		t.Errorf("AllPermissions() returned %d, want 20 (19 §10.2 matrix rows + §10.7 session:eval:write)", got)
	}
	seen := map[Permission]bool{}
	for _, p := range AllPermissions() {
		if !p.IsValid() {
			t.Errorf("AllPermissions() returned invalid permission %q", p)
		}
		if seen[p] {
			t.Errorf("AllPermissions() returned duplicate permission %q", p)
		}
		seen[p] = true
	}
}

func TestPermissionIsValidRejectsUnknown(t *testing.T) {
	if Permission("manage_everything").IsValid() {
		t.Error("an unrecognised permission must not validate")
	}
}

func TestTenantAdminPermissionsExcludesPlatformOnly(t *testing.T) {
	// §10.2: tenant-admin holds every operation except the three the
	// matrix reserves to platform-admin, plus the §10.7
	// session:eval:write capability permission (F-10.7.4).
	if got := len(TenantAdminPermissions()); got != 17 {
		t.Errorf("TenantAdminPermissions() returned %d, want 17", got)
	}
	platformOnly := []Permission{
		PermIssueBillingCorrections, PermManagePlatformSettings, PermAccessCrossTenantData,
	}
	for _, p := range platformOnly {
		if IsTenantAdminPermission(p) {
			t.Errorf("%q is platform-admin-only and must not be a tenant-admin permission", p)
		}
	}
	for _, p := range TenantAdminPermissions() {
		if !IsTenantAdminPermission(p) {
			t.Errorf("TenantAdminPermissions() includes %q but IsTenantAdminPermission rejects it", p)
		}
		if !p.IsValid() {
			t.Errorf("TenantAdminPermissions() returned invalid permission %q", p)
		}
	}
}

func permSet(ps []Permission) map[Permission]bool {
	m := map[Permission]bool{}
	for _, p := range ps {
		m[p] = true
	}
	return m
}

func samePermSet(a, b []Permission) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := permSet(a), permSet(b)
	if len(sa) != len(a) || len(sb) != len(b) {
		return false // a duplicate entry on either side
	}
	for p := range sa {
		if !sb[p] {
			return false
		}
	}
	return true
}

func TestRolePermissionsMatchMatrix(t *testing.T) {
	// Each set is the §10.2 permission matrix read down the role's
	// column. A "read-only" cell for a manage category contributes no
	// permission; only the explicit session-read categories and the
	// view-usage category give a viewer role any permission.
	cases := []struct {
		role Role
		want []Permission
	}{
		{RolePlatformAdmin, AllPermissions()},
		{RoleTenantAdmin, TenantAdminPermissions()},
		{RoleTenantViewer, []Permission{
			PermReadOwnSessions, PermReadTenantSessions, PermViewUsage,
		}},
		{RoleBillingViewer, []Permission{PermViewUsage}},
		{RoleUser, []Permission{
			PermManageOwnSessions, PermReadOwnSessions, PermManageOwnCredentials,
			PermSessionEvalWrite,
		}},
	}
	for _, c := range cases {
		got := RolePermissions(c.role)
		if !samePermSet(got, c.want) {
			t.Errorf("RolePermissions(%q) = %v, want %v", c.role, got, c.want)
		}
		for _, p := range got {
			if !p.IsValid() {
				t.Errorf("RolePermissions(%q) returned invalid permission %q", c.role, p)
			}
		}
	}
}

func TestRolePermissionsUnknownRoleIsEmpty(t *testing.T) {
	// A tenant custom role has no built-in matrix row; its permissions
	// come from the custom-role registry, not from RolePermissions.
	if got := RolePermissions(Role("session-manager")); len(got) != 0 {
		t.Errorf("RolePermissions(custom role) = %v, want empty", got)
	}
}

func TestNonPlatformRolesAreBoundedByTenantAdmin(t *testing.T) {
	// §10.2: only platform-admin holds the three platform-only
	// permissions. Every other built-in role is within the tenant-admin
	// ceiling, the same ceiling a custom role may not exceed.
	for _, r := range []Role{RoleTenantAdmin, RoleTenantViewer, RoleBillingViewer, RoleUser} {
		for _, p := range RolePermissions(r) {
			if !IsTenantAdminPermission(p) {
				t.Errorf("role %q holds %q, which exceeds the tenant-admin ceiling", r, p)
			}
		}
	}
}

// TestSessionEvalWritePermission_spec_10_7_line_936 covers the §10.7
// session:eval:write capability permission: the session-owning roles
// hold it, the read-only viewer roles do not, and it is within the
// tenant-admin ceiling so a tenant may grant it to a custom external-
// scorer role. F-10.7.4.
func TestSessionEvalWritePermission_spec_10_7_line_936(t *testing.T) {
	if !PermSessionEvalWrite.IsValid() {
		t.Fatal("session:eval:write must be a recognised permission")
	}
	grant := []Role{RolePlatformAdmin, RoleTenantAdmin, RoleUser}
	for _, r := range grant {
		if !RolesGrant([]Role{r}, PermSessionEvalWrite) {
			t.Errorf("role %q must grant session:eval:write per §10.7 line 936", r)
		}
	}
	deny := []Role{RoleTenantViewer, RoleBillingViewer}
	for _, r := range deny {
		if RolesGrant([]Role{r}, PermSessionEvalWrite) {
			t.Errorf("read-only role %q must not grant session:eval:write", r)
		}
	}
	// Within the tenant-admin ceiling: a custom scorer role may include it.
	if !IsTenantAdminPermission(PermSessionEvalWrite) {
		t.Error("session:eval:write must be within the tenant-admin ceiling so a custom role can grant it")
	}
}

func TestRolesGrant(t *testing.T) {
	// A built-in role grants its matrix permissions.
	if !RolesGrant([]Role{RoleBillingViewer}, PermViewUsage) {
		t.Error("billing-viewer should grant view_usage")
	}
	if !RolesGrant([]Role{RoleTenantViewer}, PermViewUsage) {
		t.Error("tenant-viewer should grant view_usage")
	}
	if RolesGrant([]Role{RoleUser}, PermViewUsage) {
		t.Error("user must not grant view_usage")
	}
	// The grant holds when any role in the set grants it.
	if !RolesGrant([]Role{RoleUser, RoleTenantAdmin}, PermManageUsers) {
		t.Error("a set containing tenant-admin should grant manage_users")
	}
	// A custom role name has no built-in matrix row and is ignored.
	if RolesGrant([]Role{"session-manager"}, PermManageOwnSessions) {
		t.Error("a custom role name must not resolve through RolesGrant")
	}
	// The empty set grants nothing.
	if RolesGrant(nil, PermViewUsage) {
		t.Error("the empty role set must grant nothing")
	}
}

func TestValidateTenantIDAcceptsCanonicalValues(t *testing.T) {
	cases := []string{
		"acme",
		"acme-corp",
		"acme_corp",
		"tenant_42",
		"A",
		"a1b2c3",
		strings.Repeat("a", 128), // 128-char max length per §10.2
	}
	for _, s := range cases {
		if err := ValidateTenantID(s); err != nil {
			t.Errorf("ValidateTenantID(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateTenantIDRejectsBadValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"space", "acme corp"},
		{"slash", "acme/corp"},
		{"dot", "acme.corp"},
		{"unicode", "açme"},
		{"129 chars", strings.Repeat("a", 129)},
		{"dollar", "acme$"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTenantID(c.value)
			if err == nil {
				t.Fatalf("ValidateTenantID(%q) returned nil, want error", c.value)
			}
			var fe *TenantIDFormatError
			if !errors.As(err, &fe) {
				t.Errorf("expected *TenantIDFormatError, got %T", err)
			}
		})
	}
}

// Single-tenant deployments ignore the claim and return the default.
func TestExtractTenantSingleTenantReturnsDefault(t *testing.T) {
	for _, claim := range []string{"", "acme", "anything"} {
		got, err := ExtractTenant(ExtractRequest{
			MultiTenant: false,
			Claim:       claim,
		})
		if err != nil {
			t.Errorf("single-tenant Extract should not error, got %v", err)
		}
		if got.TenantID != DefaultTenantID {
			t.Errorf("single-tenant TenantID: want %q, got %q", DefaultTenantID, got.TenantID)
		}
		if !got.FromDefault {
			t.Errorf("single-tenant FromDefault must be true")
		}
	}
}

func TestExtractTenantMultiTenantClaimMissing(t *testing.T) {
	_, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "",
		Registry:    &fakeRegistry{},
	})
	if !errors.Is(err, ErrTenantClaimMissing) {
		t.Errorf("expected ErrTenantClaimMissing, got %v", err)
	}
}

func TestExtractTenantMultiTenantClaimBadFormat(t *testing.T) {
	_, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "bad/value",
		Registry:    &fakeRegistry{registered: map[string]bool{"bad/value": true}},
	})
	var fe *TenantIDFormatError
	if !errors.As(err, &fe) {
		t.Errorf("expected *TenantIDFormatError, got %v", err)
	}
}

func TestExtractTenantMultiTenantUnregistered(t *testing.T) {
	_, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "acme",
		Registry:    &fakeRegistry{registered: map[string]bool{"globex": true}},
	})
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestExtractTenantMultiTenantRegistered(t *testing.T) {
	got, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "acme",
		Registry:    &fakeRegistry{registered: map[string]bool{"acme": true}},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID: want acme, got %q", got.TenantID)
	}
	if got.FromDefault {
		t.Errorf("FromDefault must be false for non-default tenant")
	}
}

func TestExtractTenantMultiTenantRegistryError(t *testing.T) {
	registry := &fakeRegistry{err: fmt.Errorf("postgres down")}
	_, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "acme",
		Registry:    registry,
	})
	if err == nil {
		t.Fatalf("expected transport-level lookup error")
	}
	// Should NOT collapse into ErrTenantNotFound — the caller needs to
	// distinguish lookup failure from absent tenant for runbook routing.
	if errors.Is(err, ErrTenantNotFound) {
		t.Errorf("transport error must not be reported as TENANT_NOT_FOUND, got %v", err)
	}
}

func TestExtractTenantMultiTenantMissingRegistryIsProgramError(t *testing.T) {
	_, err := ExtractTenant(ExtractRequest{
		MultiTenant: true,
		Claim:       "acme",
		Registry:    nil,
	})
	if err == nil {
		t.Errorf("nil Registry in multi-tenant mode must error")
	}
}

// fakeRegistry implements TenantRegistry for tests.
type fakeRegistry struct {
	registered map[string]bool
	err        error
}

func (f *fakeRegistry) IsRegistered(id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.registered[id], nil
}
