// SPDX-License-Identifier: MIT

// Package auditscope is the §11.7 write-time tenant-scope guard over
// the audit hash chain. It is the defense-in-depth complement to the
// hash chain: the chain detects tampering after the fact, while this
// guard prevents a compromised caller from injecting a forged-tenant
// audit row at write time in the first place.
//
// §11.7 line 428 ("Write-time tenant validation") requires that the
// `tenant_id` on every audit row equal the authenticated caller's
// scope, derived from the JWT claim or the session context rather
// than from the caller-supplied payload. A mismatch is rejected at
// the write boundary with AUDIT_TENANT_SCOPE_MISMATCH and emits a
// `security.audit_write_rejected` event under the platform tenant.
//
// Validator wraps any audit-chain backend (the Postgres-backed
// pkg/gateway/auditstore.Store or the in-memory pkg/audit.ChainSet)
// and is inserted at the caller-driven write boundaries (the §11.7
// admin audit sink and the §4.8 policy-rejection sink). Gateway-
// internal writers that carry no authenticated principal on their
// context (background reconcilers, key-rotation observers) pass the
// guard unchanged, since they are not the forged-tenant vector the
// spec names.
//
// spec: §11.7 line 428.
package auditscope

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// platformTenant is the §11.7 platform-tenant chain id. Platform-
// scoped callers legitimately write cross-tenant (and to this chain),
// so they bypass the per-tenant scope match.
const platformTenant = "platform"

// CodeTenantScopeMismatch is the §11.7 line 428 error code surfaced
// when an audit write targets a tenant other than the authenticated
// caller's scope.
const CodeTenantScopeMismatch = "AUDIT_TENANT_SCOPE_MISMATCH"

// Chain is the audit hash-chain surface Validator guards. Both the
// Postgres-backed auditstore.Store and the in-memory ChainSet adapter
// returned by NewChainSetChain satisfy it, so the guard is backend-
// agnostic.
type Chain interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
	Rows(ctx context.Context, tenantID string) ([]audit.Row, error)
	Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error)
}

// TenantScopeError is returned by Validator.Append when the row's
// target tenant does not match the authenticated caller's scope. Its
// Code is the §11.7 line 428 AUDIT_TENANT_SCOPE_MISMATCH.
type TenantScopeError struct {
	// Attempted is the tenant the rejected row targeted.
	Attempted string
	// Authenticated is the caller's JWT/session scope the write was
	// validated against.
	Authenticated string
	// EventType is the audit event type that was rejected.
	EventType string
}

func (e *TenantScopeError) Error() string {
	return fmt.Sprintf("%s: audit write to tenant %q rejected for caller scoped to %q (event %q)",
		CodeTenantScopeMismatch, e.Attempted, e.Authenticated, e.EventType)
}

// Code returns the §11.7 error code so callers can map the rejection
// onto the wire envelope.
func (e *TenantScopeError) Code() string { return CodeTenantScopeMismatch }

// Validator wraps a Chain with the §11.7 write-time tenant-scope
// guard. Construct with New.
type Validator struct {
	inner Chain
	clock func() time.Time
}

// New returns a Validator guarding inner. clock overrides time.Now for
// the rejection event timestamp; pass nil in production.
func New(inner Chain, clock func() time.Time) *Validator {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Validator{inner: inner, clock: clock}
}

// Append validates the row's target tenant against the authenticated
// caller's scope on ctx, then delegates to the wrapped chain. A
// mismatch is rejected with a *TenantScopeError and records a
// `security.audit_write_rejected` row on the platform chain.
//
// The scope is derived from the request context's authenticated
// principal, never from the caller-supplied payload (§11.7 line 428).
// A write that carries no authenticated principal (a gateway-internal
// background writer) is allowed: it is not the forged-tenant vector
// the spec guards against.
func (v *Validator) Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if err := v.check(ctx, tenantID, eventType); err != nil {
		v.recordRejection(ctx, err, at)
		return audit.Row{}, err
	}
	return v.inner.Append(ctx, tenantID, eventType, payload, at)
}

// Rows delegates to the wrapped chain. Reads carry no write-time
// tenant-scope obligation; the §10.2 read-path RLS already scopes
// them.
func (v *Validator) Rows(ctx context.Context, tenantID string) ([]audit.Row, error) {
	return v.inner.Rows(ctx, tenantID)
}

// Verify delegates to the wrapped chain.
func (v *Validator) Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error) {
	return v.inner.Verify(ctx, tenantID)
}

// check returns a *TenantScopeError when the authenticated caller on
// ctx is scoped to a tenant other than tenantID, and nil otherwise.
func (v *Validator) check(ctx context.Context, tenantID, eventType string) error {
	p, ok := authmw.FromContext(ctx)
	if !ok {
		// No authenticated caller: a gateway-internal/system write.
		return nil
	}
	// Platform-scoped callers legitimately write cross-tenant and to
	// the platform chain.
	if p.TenantID == platformTenant || p.HasRole(auth.RolePlatformAdmin) {
		return nil
	}
	if tenantID == p.TenantID {
		return nil
	}
	return &TenantScopeError{Attempted: tenantID, Authenticated: p.TenantID, EventType: eventType}
}

// recordRejection commits a `security.audit_write_rejected` row to the
// platform chain. It writes through the wrapped chain directly so the
// rejection record itself is not re-validated (it legitimately lands
// on the platform tenant regardless of the rejected caller's scope).
// A failure to record the rejection is logged but not surfaced — the
// caller already has the originating *TenantScopeError.
func (v *Validator) recordRejection(ctx context.Context, scopeErr error, at time.Time) {
	se, ok := scopeErr.(*TenantScopeError)
	if !ok {
		return
	}
	if at.IsZero() {
		at = v.clock()
	}
	fields := map[string]any{
		"error_code":           CodeTenantScopeMismatch,
		"attempted_tenant_id":  se.Attempted,
		"authenticated_tenant": se.Authenticated,
		"rejected_event_type":  se.EventType,
		"caller_kind":          "service",
	}
	if p, ok := authmw.FromContext(ctx); ok {
		fields["actor_subject"] = p.Subject
		if p.CallerType != "" {
			fields["caller_kind"] = p.CallerType
		}
	}
	payload, _ := json.Marshal(fields)
	if _, err := v.inner.Append(ctx, platformTenant, obsaudit.EventSecurityAuditWriteRejected.String(), payload, at); err != nil {
		log.Printf("auditscope: failed to record %s rejection on platform chain: %v",
			CodeTenantScopeMismatch, err)
	}
}
