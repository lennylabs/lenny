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
