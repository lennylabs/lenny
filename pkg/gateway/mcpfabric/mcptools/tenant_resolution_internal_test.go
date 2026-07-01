// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// TestCallerTenantIDPrefersPrincipal pins §9.2 / §16.1 / §15.2 line
// 1335: when the request carries an authenticated principal, the
// MCP tool surface scopes its store lookups, audit emissions, and
// metric labels to the principal's tenant — not the Register-time
// fallback. A multi-tenant deployment must not commingle these.
// F-9.2.13 / F-15.2.15.
func TestCallerTenantIDPrefersPrincipal_spec_9_2(t *testing.T) {
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		TenantID: "acme", Subject: "user_alice",
	})
	if got := callerTenantID(ctx, "default"); got != "acme" {
		t.Errorf("callerTenantID = %q, want acme (principal wins over fallback)", got)
	}
}

// TestCallerTenantIDFallsBackWhenUnauthenticated pins the
// tests / dev-headers path: a request with no principal context
// resolves to the binary's configured fallback so the v1 tool surface
// stays usable in minimal deployments. spec: §9.2 / §16.1;
// F-9.2.13 / F-15.2.15.
func TestCallerTenantIDFallsBackWhenUnauthenticated_spec_9_2(t *testing.T) {
	if got := callerTenantID(context.Background(), "acme"); got != "acme" {
		t.Errorf("callerTenantID = %q, want acme (no principal → fallback)", got)
	}
}

// TestCallerTenantIDFallsBackWhenPrincipalHasNoTenant pins the
// degenerate case where the principal carries an empty tenant id
// (a malformed token or a pre-tenant-claim deployment). The helper
// must still fall back to the configured default; an empty tenant
// would collapse into an unbounded scan on the session store.
// spec: §9.2 / §16.1; F-9.2.13 / F-15.2.15.
func TestCallerTenantIDFallsBackWhenPrincipalHasNoTenant_spec_9_2(t *testing.T) {
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: "user_alice",
	})
	if got := callerTenantID(ctx, "acme"); got != "acme" {
		t.Errorf("callerTenantID = %q, want acme (empty principal tenant → fallback)", got)
	}
}

// TestCallerTenantIDDefaultsToDefaultWhenBothEmpty pins the absolute
// floor: an empty principal tenant AND an empty configured fallback
// resolve to "default" so the store layer is never asked to scan an
// unbounded tenant. spec: §9.2 / §16.1; F-9.2.13 / F-15.2.15.
func TestCallerTenantIDDefaultsToDefaultWhenBothEmpty_spec_9_2(t *testing.T) {
	if got := callerTenantID(context.Background(), ""); got != "default" {
		t.Errorf("callerTenantID = %q, want default (no principal, no fallback)", got)
	}
}

// TestCallerTenantIDPrincipalOverridesNonEmptyFallback pins the
// production posture: even when the Register-time fallback is set
// to a non-empty value (the binary's --tenant-id flag), the
// per-request principal still wins. A multi-tenant deployment scopes
// every elicitation budget, store lookup, audit emission, and
// tamper metric to the authenticated caller's tenant — not the
// binary-wide default. spec: §9.2 / §16.1; F-9.2.13 / F-15.2.15.
func TestCallerTenantIDPrincipalOverridesNonEmptyFallback_spec_9_2(t *testing.T) {
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		TenantID: "globex", Subject: "user_alice",
	})
	if got := callerTenantID(ctx, "default"); got != "globex" {
		t.Errorf("callerTenantID = %q, want globex (principal beats binary default)", got)
	}
}
