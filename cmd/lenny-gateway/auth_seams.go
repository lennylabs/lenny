// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/introspection"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

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
// adapter. It consults the wired tenantstore so a bearer whose tenant
// claim names a tenant that is not registered (missing, `deleting`, or
// `deleted`) is rejected with TENANT_NOT_FOUND. applyTenantAuthOptions
// wires it in multi-tenant mode only. spec: §10.2, §12.8. F-10.2.1.
type bearerTenantRegistry struct {
	store tenantstore.Store
}

func (r bearerTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	return tenantRegistered(context.Background(), r.store, tenantID)
}

// tenantRegistered is the one registration reading both request-path
// registry adapters (bearerTenantRegistry and playgroundTenantRegistry)
// apply. A found row, `default` included, is registered when
// tenantstore.Tenant.Registered holds, so a `default` tenant in
// `deleting` or `deleted` is refused like any other. A missing row
// admits only `default`: the in-memory tenant store has no `default`
// row, and with Postgres migrations seed it, so the fallback never
// revives a deleted `default`, whose tombstone row remains. Any other
// store error is returned, which the auth middleware and the playground
// treat as a refusal, so an unreachable store fails closed for `default`
// as well. spec: §10.2, §12.8.
func tenantRegistered(ctx context.Context, store tenantstore.Store, tenantID string) (bool, error) {
	row, err := store.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return tenantID == auth.DefaultTenantID, nil
	}
	if err != nil {
		return false, fmt.Errorf("read tenant %s registration: %w", tenantID, err)
	}
	return row.Registered(), nil
}

// authFailureAuditAdapter bridges the §10.2 auth middleware to the
// §11.7 audit chain so every §4.2 tenant-claim rejection
// (TENANT_CLAIM_MISSING / TENANT_NOT_FOUND / TENANT_CLAIM_INVALID_FORMAT)
// produces an `auth_failure` audit row. A TENANT_NOT_FOUND rejection is
// written to the platform chain with the inferred tenant as its
// top-level target_tenant_id, following the §11.7 cross-tenant pattern,
// so a refused bearer never appends a row to the chain of a tenant that
// is missing or in deletion (after §12.8 Phase 4 that chain holds only
// gdpr.* rows). TENANT_CLAIM_INVALID_FORMAT lands on the inferred
// tenant's chain, and TENANT_CLAIM_MISSING, which carries no tenant,
// lands on the platform chain (admin.ChainAuditSink defaults the empty
// ActorTenantID to "platform"). spec: §10.2, §11.7, §12.8.
type authFailureAuditAdapter struct {
	sink admin.AuditSink
}

func (a authFailureAuditAdapter) EmitAuthFailure(ctx context.Context, ev authmw.AuthFailureEvent) {
	if a.sink == nil {
		return
	}
	event := admin.AuditEvent{
		Type:          authmw.AuthFailureEventType,
		ActorSubject:  ev.UserID,
		ActorTenantID: ev.TenantID,
		Detail: map[string]any{
			"reason":    ev.Reason,
			"tenant_id": ev.TenantID,
			"user_id":   ev.UserID,
			"jti":       ev.JTI,
		},
		At: ev.At,
	}
	if ev.Reason == "TENANT_NOT_FOUND" {
		event.ActorTenantID = ""
		event.TargetTenantID = ev.TenantID
	}
	a.sink.EmitAdminEvent(ctx, event)
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
