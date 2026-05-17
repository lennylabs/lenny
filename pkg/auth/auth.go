// SPDX-License-Identifier: MIT

// Package auth encodes the §10.2 gateway authentication primitives that
// do not depend on a live OIDC provider, KMS, or HTTP transport: the
// JWT `typ` enum, the RBAC role enum, the §10.2 `tenant_id` format
// validator, and the §10.2 tenant-claim extraction state machine.
//
// The gateway binary (a later phase) wraps these primitives with the
// signature-validation and KMS-signing layers; this package stays pure
// so it can be unit-tested without external dependencies.
package auth

import (
	"fmt"
	"regexp"
)

// TokenType is the §10.2 `typ` payload-claim enum. It discriminates
// the purpose of a gateway-issued JWT.
type TokenType string

const (
	TokenUserBearer        TokenType = "user_bearer"
	TokenSessionCapability TokenType = "session_capability"
	TokenA2ADelegation     TokenType = "a2a_delegation"
	TokenServiceToken      TokenType = "service_token"
)

// AllTokenTypes returns the closed enum from §10.2.
func AllTokenTypes() []TokenType {
	return []TokenType{TokenUserBearer, TokenSessionCapability, TokenA2ADelegation, TokenServiceToken}
}

// IsValid reports whether t is one of the four §10.2 token types.
func (t TokenType) IsValid() bool {
	for _, v := range AllTokenTypes() {
		if t == v {
			return true
		}
	}
	return false
}

// Role is the built-in RBAC role enum from §10.2 Authorization and RBAC.
type Role string

const (
	RolePlatformAdmin Role = "platform-admin"
	RoleTenantAdmin   Role = "tenant-admin"
	RoleTenantViewer  Role = "tenant-viewer"
	RoleBillingViewer Role = "billing-viewer"
	RoleUser          Role = "user"
)

// AllRoles returns the closed enum in §10.2 order.
func AllRoles() []Role {
	return []Role{RolePlatformAdmin, RoleTenantAdmin, RoleTenantViewer, RoleBillingViewer, RoleUser}
}

// IsValid reports whether r is one of the five §10.2 built-in roles.
func (r Role) IsValid() bool {
	for _, v := range AllRoles() {
		if r == v {
			return true
		}
	}
	return false
}

// IsTenantScoped reports whether the role's authority is scoped to a
// single tenant. Only platform-admin spans tenants.
func (r Role) IsTenantScoped() bool {
	return r != RolePlatformAdmin
}

// roleNamePattern is the §10.2 role-name format, shared by built-in
// and custom role names. All five built-in roles match it.
var roleNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// IsWellFormedRoleName reports whether s is a syntactically valid role
// name in the format shared by built-in and §10.2 custom roles. It
// does not verify that a custom role of that name exists; existence is
// checked where the tenant's custom-role registry is available.
func IsWellFormedRoleName(s string) bool {
	return roleNamePattern.MatchString(s)
}

// Permission is one operation category from the §10.2 RBAC permission
// matrix. The closed set below mirrors the matrix rows one to one; each
// constant's comment quotes the matrix row it represents. §10.2 custom
// roles are defined as a subset of these.
type Permission string

const (
	// PermManageOwnSessions — §10.2 "Create / cancel own sessions".
	PermManageOwnSessions Permission = "manage_own_sessions"
	// PermReadOwnSessions — §10.2 "Read own session history".
	PermReadOwnSessions Permission = "read_own_sessions"
	// PermManageOwnCredentials — §10.2 "Manage own credentials".
	PermManageOwnCredentials Permission = "manage_own_credentials"
	// PermReadTenantSessions — §10.2 "Read other users' sessions (same tenant)".
	PermReadTenantSessions Permission = "read_tenant_sessions"
	// PermManageRuntimes — §10.2 "Manage runtimes".
	PermManageRuntimes Permission = "manage_runtimes"
	// PermManagePools — §10.2 "Manage pools / scaling policies".
	PermManagePools Permission = "manage_pools"
	// PermManageCredentialPools — §10.2 "Manage credential pools".
	PermManageCredentialPools Permission = "manage_credential_pools"
	// PermViewUsage — §10.2 "View usage / metering".
	PermViewUsage Permission = "view_usage"
	// PermManageQuotas — §10.2 "Manage quotas".
	PermManageQuotas Permission = "manage_quotas"
	// PermManageUsers — §10.2 "Manage users / role assignments".
	PermManageUsers Permission = "manage_users"
	// PermManageLegalHolds — §10.2 "Set / release legal holds".
	PermManageLegalHolds Permission = "manage_legal_holds"
	// PermManageWebhooks — §10.2 "Configure callback URLs / webhooks".
	PermManageWebhooks Permission = "manage_webhooks"
	// PermManageRBACConfig — §10.2 "Configure tenant RBAC config".
	PermManageRBACConfig Permission = "manage_rbac_config"
	// PermManageEnvironments — §10.2 "Manage environments".
	PermManageEnvironments Permission = "manage_environments"
	// PermManageDelegationPolicies — §10.2 "Manage delegation policies".
	PermManageDelegationPolicies Permission = "manage_delegation_policies"
	// PermManageEgressProfiles — §10.2 "Manage egress profiles".
	PermManageEgressProfiles Permission = "manage_egress_profiles"
	// PermIssueBillingCorrections — §10.2 "Issue billing corrections (operator-initiated)".
	PermIssueBillingCorrections Permission = "issue_billing_corrections"
	// PermManagePlatformSettings — §10.2 "Manage platform-wide settings".
	PermManagePlatformSettings Permission = "manage_platform_settings"
	// PermAccessCrossTenantData — §10.2 "Access other tenants' data".
	PermAccessCrossTenantData Permission = "access_cross_tenant_data"
)

// AllPermissions returns the closed §10.2 permission set in matrix
// order.
func AllPermissions() []Permission {
	return []Permission{
		PermManageOwnSessions, PermReadOwnSessions, PermManageOwnCredentials,
		PermReadTenantSessions, PermManageRuntimes, PermManagePools,
		PermManageCredentialPools, PermViewUsage, PermManageQuotas,
		PermManageUsers, PermManageLegalHolds, PermManageWebhooks,
		PermManageRBACConfig, PermManageEnvironments, PermManageDelegationPolicies,
		PermManageEgressProfiles, PermIssueBillingCorrections,
		PermManagePlatformSettings, PermAccessCrossTenantData,
	}
}

// IsValid reports whether p is a recognised §10.2 permission.
func (p Permission) IsValid() bool {
	for _, v := range AllPermissions() {
		if p == v {
			return true
		}
	}
	return false
}

// TenantAdminPermissions returns the permissions the §10.2 `tenant-admin`
// role holds — every operation category except the three the matrix
// reserves to `platform-admin` (issue billing corrections, manage
// platform-wide settings, access other tenants' data). A §10.2 custom
// role's permission set must be a subset of this.
func TenantAdminPermissions() []Permission {
	return RolePermissions(RoleTenantAdmin)
}

// IsTenantAdminPermission reports whether p is within the `tenant-admin`
// permission set — the §10.2 ceiling a custom role may not exceed.
func IsTenantAdminPermission(p Permission) bool {
	for _, v := range TenantAdminPermissions() {
		if p == v {
			return true
		}
	}
	return false
}

// RolePermissions returns the §10.2 permission-matrix row for a
// built-in role. The matrix grades each operation category as full
// access, read-only, or none; RolePermissions returns the permission
// for every category the role holds at the access level the
// permission denotes. The read-only cells for the manage categories
// (a `tenant-viewer` may read runtimes but not manage them) do not map
// to a permission, because the permission set models manage-level
// operations and the explicit session-read categories. Resource reads
// are gated separately.
//
// A non-built-in role name (a tenant custom role) has no matrix row;
// its permissions come from the tenant custom-role registry, so
// RolePermissions returns nil for it.
func RolePermissions(r Role) []Permission {
	switch r {
	case RolePlatformAdmin:
		return AllPermissions()
	case RoleTenantAdmin:
		return []Permission{
			PermManageOwnSessions, PermReadOwnSessions, PermManageOwnCredentials,
			PermReadTenantSessions, PermManageRuntimes, PermManagePools,
			PermManageCredentialPools, PermViewUsage, PermManageQuotas,
			PermManageUsers, PermManageLegalHolds, PermManageWebhooks,
			PermManageRBACConfig, PermManageEnvironments, PermManageDelegationPolicies,
			PermManageEgressProfiles,
		}
	case RoleTenantViewer:
		return []Permission{
			PermReadOwnSessions, PermReadTenantSessions, PermViewUsage,
		}
	case RoleBillingViewer:
		return []Permission{PermViewUsage}
	case RoleUser:
		return []Permission{
			PermManageOwnSessions, PermReadOwnSessions, PermManageOwnCredentials,
		}
	default:
		return nil
	}
}

// RolesGrant reports whether any built-in role in roles grants perm.
// A non-built-in role name (a tenant custom role) is ignored: its
// permissions live in the tenant custom-role registry, which this
// function does not consult. A caller that must honor custom roles
// resolves them against the registry and combines the result with
// RolesGrant.
func RolesGrant(roles []Role, perm Permission) bool {
	for _, r := range roles {
		for _, granted := range RolePermissions(r) {
			if granted == perm {
				return true
			}
		}
	}
	return false
}

// tenantIDPattern is the regex from §10.2: `^[a-zA-Z0-9_-]{1,128}$`.
// Enforced at every boundary that ingests a tenant identifier.
var tenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// ValidateTenantID reports whether s satisfies the §10.2 format
// constraint. Returns nil on success and a *TenantIDFormatError
// otherwise. The constraint exists so tenant_id values are safe in DDL
// identifiers, MinIO path prefixes, Redis keys, and log fields.
func ValidateTenantID(s string) error {
	if !tenantIDPattern.MatchString(s) {
		return &TenantIDFormatError{Value: s}
	}
	return nil
}

// TenantIDFormatError captures a §10.2 tenant_id pattern mismatch.
type TenantIDFormatError struct{ Value string }

func (e *TenantIDFormatError) Error() string {
	return fmt.Sprintf("auth: tenant_id %q does not match pattern ^[a-zA-Z0-9_-]{1,128}$ (§10.2)", e.Value)
}

// TenantRegistry is the contract the extractor uses to confirm a
// claim-derived tenant_id corresponds to an actually-provisioned
// tenant. Production wires this to a Postgres-backed lookup; tests can
// supply a fake implementation.
type TenantRegistry interface {
	// IsRegistered reports whether tenantID is a known tenant on the
	// platform. The returned error covers transport-level failures
	// (Postgres unavailable, network) so callers can distinguish
	// "tenant absent" from "lookup failed".
	IsRegistered(tenantID string) (bool, error)
}

// ExtractRequest carries the inputs to ExtractTenant.
type ExtractRequest struct {
	// MultiTenant reflects the auth.multiTenant Helm value. When
	// false, the claim is ignored and the built-in default tenant is
	// returned per §10.2.
	MultiTenant bool

	// Claim is the value of the OIDC ID-token claim configured by
	// auth.tenantIdClaim (default `tenant_id`). The empty string
	// means the claim is absent from the token.
	Claim string

	// Registry is the lookup used to confirm a non-default tenant is
	// provisioned. Required in multi-tenant mode; ignored otherwise.
	Registry TenantRegistry
}

// ExtractResult is the outcome of a successful extraction.
type ExtractResult struct {
	// TenantID is the resolved tenant identifier. In single-tenant
	// mode this is always DefaultTenantID; in multi-tenant mode it
	// is the validated claim value.
	TenantID string

	// FromDefault is true when the result fell through to the
	// built-in default tenant (single-tenant mode). Useful for log
	// fields that distinguish "explicit default" from "fall-through".
	FromDefault bool
}

// DefaultTenantID is the built-in tenant ID used in single-tenant
// deployments per §10.2.
const DefaultTenantID = "default"

// ExtractTenant applies the §10.2 tenant-claim extraction table:
//
//	single-tenant         → DefaultTenantID, regardless of the claim
//	multi-tenant, missing → ErrTenantClaimMissing (HTTP 401)
//	multi-tenant, malformed → *TenantIDFormatError (HTTP 401, §10.2)
//	multi-tenant, unregistered → ErrTenantNotFound (HTTP 403)
//	multi-tenant, registered → ExtractResult{TenantID: claim}
//
// Caller is responsible for mapping the returned error to the
// corresponding HTTP envelope (TENANT_CLAIM_MISSING,
// TENANT_CLAIM_INVALID_FORMAT, TENANT_NOT_FOUND).
func ExtractTenant(r ExtractRequest) (ExtractResult, error) {
	if !r.MultiTenant {
		return ExtractResult{TenantID: DefaultTenantID, FromDefault: true}, nil
	}
	if r.Claim == "" {
		return ExtractResult{}, ErrTenantClaimMissing
	}
	if err := ValidateTenantID(r.Claim); err != nil {
		return ExtractResult{}, err
	}
	if r.Registry == nil {
		return ExtractResult{}, fmt.Errorf("auth: ExtractTenant in multi-tenant mode requires a Registry")
	}
	ok, err := r.Registry.IsRegistered(r.Claim)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("auth: tenant lookup: %w", err)
	}
	if !ok {
		return ExtractResult{}, ErrTenantNotFound
	}
	return ExtractResult{TenantID: r.Claim}, nil
}

// Sentinel errors for the §10.2 rejection categories. The gateway
// maps each to its spec-mandated HTTP envelope:
//
//	ErrTenantClaimMissing → 401 TENANT_CLAIM_MISSING
//	*TenantIDFormatError  → 401 TENANT_CLAIM_INVALID_FORMAT
//	ErrTenantNotFound     → 403 TENANT_NOT_FOUND
var (
	ErrTenantClaimMissing = fmt.Errorf("auth: tenant claim is missing or empty")
	ErrTenantNotFound     = fmt.Errorf("auth: tenant is not registered on this platform")
)
