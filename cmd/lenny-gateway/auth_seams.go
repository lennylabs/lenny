// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/introspection"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this in single-tenant mode (where the §10.2 dev-header transport
// flips to MultiTenant=true to round-trip the tenant header).
// Multi-tenant production deployments use bearerTenantRegistry
// instead, which consults the real tenantstore.
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// userstorePlatformRoles adapts a userstore.Store into the §10.2 platform-managed role resolver consulted by the auth middleware.
// When a row carries a platform-managed assignment (RoleAssigned) — even
// one whose Roles slice is empty — its Roles fully replace the OIDC
// claim, so tenant-admins can downgrade a user with an over-broad OIDC
// claim by recording an explicit (possibly empty) assignment. A row with
// no assignment (the state left by `DELETE /v1/admin/tenants/{id}/users/
// {userId}/role`) or a missing row leaves the JWT claim authoritative.
// spec: §10.2, §15.1. F-10.2.3, F-15.1.3.
type userstorePlatformRoles struct {
	store userstore.Store
}

func (r userstorePlatformRoles) ResolveRoles(ctx context.Context, tenantID, subject string) ([]auth.Role, bool, error) {
	if r.store == nil {
		return nil, false, nil
	}
	row, err := r.store.Get(ctx, tenantID, subject)
	if errors.Is(err, userstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]auth.Role(nil), row.Roles...), row.RoleAssigned, nil
}

// tenantIntrospectionConfig resolves the §10.6 real-time
// group-check configuration from a tenant's stored identityProvider
// record, satisfying introspection.ConfigSource. A tenant that has not
// set introspectionEnabled yields a disabled Config, so the auth
// middleware keeps the JWT groups claim for it. F-10.6.8.
type tenantIntrospectionConfig struct {
	store tenantstore.Store
}

func (s tenantIntrospectionConfig) IntrospectionConfig(ctx context.Context, tenantID string) (introspection.Config, error) {
	if s.store == nil {
		return introspection.Config{}, nil
	}
	row, err := s.store.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return introspection.Config{}, nil
	}
	if err != nil {
		return introspection.Config{}, err
	}
	ip := row.RBACConfig.IdentityProvider
	return introspection.Config{
		Enabled:      ip.IntrospectionEnabled,
		Endpoint:     ip.IntrospectionEndpoint,
		ClientID:     ip.IntrospectionClientID,
		ClientSecret: ip.IntrospectionClientSecret,
		CacheTTL:     time.Duration(ip.IntrospectionCacheTTLSeconds) * time.Second,
	}, nil
}

// bearerTenantRegistry is the §10.2 multi-tenant bearer-chain
// adapter. It consults the wired tenantstore so a Bearer JWT whose
// `tenant_id` claim names a tenant that is not provisioned (or is
// soft-deleted) is rejected with TENANT_NOT_FOUND. The built-in
// `default` tenant is admitted unconditionally so the Embedded-Mode
// quickstart (which seeds the default row via the bootstrap Job) works
// even before the row is persisted; once the row exists, the active
// flag (IsActive) governs.
// spec: §10.2. F-10.2.1.
type bearerTenantRegistry struct {
	store tenantstore.Store
}

func (r bearerTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == auth.DefaultTenantID {
		return true, nil
	}
	row, err := r.store.Get(context.Background(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.IsActive(), nil
}

// authFailureAuditAdapter bridges the §10.2 auth middleware to the
// §11.7 audit chain so every §4.2 tenant-claim rejection
// (TENANT_CLAIM_MISSING / TENANT_NOT_FOUND / TENANT_CLAIM_INVALID_FORMAT)
// produces an `auth_failure` audit row. Rejections that infer a
// tenant id from the JWT claim or dev header land on that inferred
// tenant's chain; the TENANT_CLAIM_MISSING case (no claim presented)
// falls back to the platform chain.
type authFailureAuditAdapter struct {
	sink admin.AuditSink
}

func (a authFailureAuditAdapter) EmitAuthFailure(ctx context.Context, ev authmw.AuthFailureEvent) {
	if a.sink == nil {
		return
	}
	actorTenant := ev.TenantID
	if actorTenant == "" {
		// §4.2: when no tenant could be inferred, land the row on the
		// platform chain (admin.NewChainAuditSink defaults the empty
		// ActorTenantID to "platform").
		actorTenant = ""
	}
	a.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          authmw.AuthFailureEventType,
		ActorSubject:  ev.UserID,
		ActorTenantID: actorTenant,
		Detail: map[string]any{
			"reason":    ev.Reason,
			"tenant_id": ev.TenantID,
			"user_id":   ev.UserID,
			"jti":       ev.JTI,
		},
		At: ev.At,
	})
}

// jwksAdvertisesAsymmetric reports whether doc contains at least one
// asymmetric (`kty: RSA` or `kty: EC`) entry. The HMAC-only case
// produces only `kty: oct` entries with no `k` field, so the document
// advertises kid/alg metadata that a verifier cannot use to validate a
// signature. F-10.2.14 keys the §10.3 publication notice on this check
// so an operator who opts into --jwks-publish on top of the v1 HMAC
// signer is told that the JWKS document is metadata-only.
// spec: §10.2. F-10.2.14.
func jwksAdvertisesAsymmetric(doc jwt.JWKSet) bool {
	for _, k := range doc.Keys {
		if k.Kty != "" && k.Kty != "oct" {
			return true
		}
	}
	return false
}
